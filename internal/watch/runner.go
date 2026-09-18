package watch

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/Fougere/flexwatch/internal/communauto"
	"github.com/Fougere/flexwatch/internal/geo"
	"github.com/Fougere/flexwatch/internal/metrics"
	"github.com/Fougere/flexwatch/internal/notify"
	"github.com/Fougere/flexwatch/internal/store"
)

// maxBackoff plafonne le recul en cas de panne : au-delà, on ne recule plus,
// on reste à un poll toutes les 5 minutes pour repartir vite au rétablissement.
const maxBackoff = 5 * time.Minute

// maxBackoffExponent borne le calcul 2^n. À 20, le délai théorique dépasse
// déjà largement maxBackoff (2^20 x 20 s ≈ 242 jours) : la seule raison de
// cette borne est d'éviter un débordement d'int64.
const maxBackoffExponent = 20

// notifyTimeout borne l'envoi des notifications pour ne pas bloquer la boucle.
const notifyTimeout = 15 * time.Second

// Params est la configuration de la boucle de surveillance.
type Params struct {
	CityID       int
	Center       geo.Point
	RadiusKM     float64
	PollInterval time.Duration
	RealertAfter time.Duration
	Persist      bool
}

// Result est le résultat d'un poll.
type Result struct {
	// TotalInCity est le total annoncé par l'API pour la bounding box.
	TotalInCity int
	// InBBox est le nombre de véhicules renvoyés avant filtrage circulaire.
	InBBox int
	// InFence sont les véhicules réellement dans le rayon.
	InFence []communauto.Vehicle
	// Skipped compte les véhicules indécodables.
	Skipped int
	Diff    Diff
}

// Runner orchestre poll -> filtrage -> détection -> notification.
type Runner struct {
	params   Params
	client   *communauto.Client
	det      *Detector
	notifier notify.Notifier
	store    store.Store
	metrics  *metrics.Metrics
	log      *slog.Logger
	box      geo.BBox
}

// NewRunner assemble la boucle. Un store nil est remplacé par store.Noop.
func NewRunner(
	params Params,
	client *communauto.Client,
	notifier notify.Notifier,
	st store.Store,
	m *metrics.Metrics,
	log *slog.Logger,
) *Runner {
	if st == nil {
		st = store.Noop{}
	}
	return &Runner{
		params:   params,
		client:   client,
		det:      NewDetector(params.RealertAfter),
		notifier: notifier,
		store:    st,
		metrics:  m,
		log:      log,
		// La bbox ne dépend que de la config : on la calcule une fois.
		box: geo.BBoxAround(params.Center, params.RadiusKM),
	}
}

// BBox renvoie la bounding box interrogée (utile pour les logs de démarrage).
func (r *Runner) BBox() geo.BBox { return r.box }

// Run boucle jusqu'à annulation du contexte. Le retour est nil sur un arrêt
// demandé : un SIGTERM n'est pas une erreur.
func (r *Runner) Run(ctx context.Context) error {
	r.log.Info("surveillance demarree",
		"city_id", r.params.CityID,
		"center_lat", r.params.Center.Lat,
		"center_lon", r.params.Center.Lon,
		"radius_km", r.params.RadiusKM,
		"poll_interval", r.params.PollInterval.String(),
		"bbox_min_lat", r.box.MinLat, "bbox_max_lat", r.box.MaxLat,
		"bbox_min_lon", r.box.MinLon, "bbox_max_lon", r.box.MaxLon,
	)

	failures := 0
	for {
		res, err := r.Poll(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			// Erreur causée par l'arrêt : on sort sans bruit. Renvoyer nil
			// alors que err est non nul est l'intention même — un SIGTERM
			// n'est pas un échec — d'où l'exception explicite au linter.
			r.log.Info("arret demande, boucle terminee")
			return nil //nolint:nilerr // un arret demande n'est pas une erreur
		case err != nil:
			failures++
			r.log.Error("poll en echec",
				"error", err.Error(),
				"kind", errorKind(err),
				"consecutive_failures", failures,
			)
		default:
			if failures > 0 {
				r.log.Info("api de nouveau joignable", "after_failures", failures)
			}
			failures = 0
			r.report(ctx, res)
		}

		delay := r.nextDelay(failures, err)
		if failures > 0 {
			r.log.Warn("backoff avant nouvelle tentative", "delay", delay.String())
		}

		select {
		case <-ctx.Done():
			r.log.Info("arret demande, boucle terminee")
			return nil
		case <-time.After(delay):
		}
	}
}

// Poll exécute un cycle complet : requête, filtrage circulaire, diff,
// persistance optionnelle. Il ne notifie pas (c'est report qui le fait), ce
// qui le rend utilisable tel quel par le mode -once.
func (r *Runner) Poll(ctx context.Context) (Result, error) {
	start := time.Now()

	av, err := r.client.FreeFloatingAvailability(ctx, r.params.CityID, r.box)
	if err != nil {
		r.metrics.ObservePollError(start, time.Since(start), errorKind(err))
		return Result{}, err
	}

	// L'API filtre par rectangle ; on resserre au vrai cercle ici.
	inFence := make([]communauto.Vehicle, 0, len(av.Vehicles))
	for _, v := range av.Vehicles {
		if geo.DistanceKM(r.params.Center, v.Position) <= r.params.RadiusKM {
			inFence = append(inFence, v)
		}
	}

	now := time.Now()
	res := Result{
		TotalInCity: av.TotalNbVehicles,
		InBBox:      len(av.Vehicles),
		InFence:     inFence,
		Skipped:     av.Skipped,
		Diff:        r.det.Update(now, inFence),
	}

	r.metrics.ObservePollSuccess(start, time.Since(start), av.TotalNbVehicles, len(inFence), av.Skipped)
	r.metrics.ObserveDiff(len(res.Diff.New), len(res.Diff.Gone))

	if av.Skipped > 0 {
		r.log.Warn("vehicules indecodables ignores",
			"count", av.Skipped,
			"hint", "le schema de l'API a peut-etre change (voir internal/communauto/decode.go)")
	}

	r.log.Debug("poll termine",
		"total_in_city", av.TotalNbVehicles,
		"in_bbox", len(av.Vehicles),
		"in_fence", len(inFence),
		"new", len(res.Diff.New),
		"gone", len(res.Diff.Gone),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	r.persist(ctx, now, res)
	return res, nil
}

// report journalise et notifie ce qui vient d'être détecté.
func (r *Runner) report(ctx context.Context, res Result) {
	if res.Diff.FirstRun {
		r.log.Info("etat initial enregistre (aucune notification)",
			"in_fence", res.Diff.Present)
		return
	}

	for _, id := range res.Diff.Gone {
		r.log.Info("vehicule sorti du rayon", "vehicle_id", id)
	}

	if !res.Diff.HasAlerts() {
		return
	}

	for _, v := range res.Diff.New {
		r.log.Info("nouveau vehicule dans le rayon",
			"vehicle_id", v.ID,
			"vehicle_nb", v.Number,
			"model", v.Model,
			"energy_level", v.EnergyLevel,
			"distance_m", int(geo.DistanceKM(r.params.Center, v.Position)*1000),
		)
	}

	notifyCtx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()

	err := r.notifier.Notify(notifyCtx, notify.Notification{
		At:       time.Now(),
		New:      res.Diff.New,
		Gone:     res.Diff.Gone,
		Present:  res.Diff.Present,
		Center:   r.params.Center,
		RadiusKM: r.params.RadiusKM,
	})
	if err != nil {
		// Une notification perdue ne doit pas tuer le bot : on log et on
		// continue. L'alerte Prometheus sur notifications_total{outcome=~".*_error"}
		// prend le relais (cf. PLAN.md phase 7).
		r.log.Error("notification en echec", "error", err.Error())
	}
}

func (r *Runner) persist(ctx context.Context, now time.Time, res Result) {
	if !r.params.Persist {
		return
	}
	err := r.store.SaveSnapshot(ctx, store.Snapshot{
		ObservedAt:  now,
		CityID:      r.params.CityID,
		Center:      r.params.Center,
		RadiusKM:    r.params.RadiusKM,
		TotalInCity: res.TotalInCity,
		InFence:     res.InFence,
	})
	r.metrics.ObserveStoreWrite(err)
	if err != nil {
		r.log.Error("persistance du snapshot en echec", "error", err.Error())
	}
}

// nextDelay calcule l'attente avant le prochain poll : intervalle nominal
// jitteré en régime normal, backoff exponentiel plafonné sinon.
//
// Le jitter n'est pas cosmétique : il évite que plusieurs instances (ou
// plusieurs redémarrages) tombent en phase et tapent l'API en rafale.
func (r *Runner) nextDelay(failures int, lastErr error) time.Duration {
	if failures == 0 {
		return jitter(r.params.PollInterval)
	}

	// Un Retry-After explicite de l'API prime sur notre propre calcul.
	var httpErr *communauto.HTTPError
	if errors.As(lastErr, &httpErr) && httpErr.RetryAfter > 0 {
		return min(httpErr.RetryAfter, maxBackoff)
	}

	// L'exposant est borné : 2^63 * un intervalle déborderait int64 et
	// produirait une durée NÉGATIVE, donc une attente nulle et un martèlement
	// de l'API pile au moment où elle est en panne. Une panne de quelques
	// heures suffit à atteindre ces valeurs.
	exp := min(failures-1, maxBackoffExponent)
	delay := time.Duration(float64(r.params.PollInterval) * math.Pow(2, float64(exp)))

	// Le jitter est appliqué AVANT le plafonnement final : sans ce second
	// min(), ses +10 % pourraient dépasser maxBackoff, qui doit rester une
	// garantie dure et pas une indication.
	return min(jitter(min(delay, maxBackoff)), maxBackoff)
}

// jitter applique +/- 10 % à une durée.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	spread := 0.9 + 0.2*rand.Float64() //nolint:gosec // jitter, pas de la crypto
	return time.Duration(float64(d) * spread)
}

// errorKind classe une erreur pour les métriques et les alertes : une dérive
// de schéma ne se traite pas comme une coupure réseau.
func errorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, communauto.ErrSchemaDrift):
		return "schema"
	}
	var httpErr *communauto.HTTPError
	if errors.As(err, &httpErr) {
		return "http"
	}
	return "network"
}
