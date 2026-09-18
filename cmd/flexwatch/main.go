// Commande flexwatch : surveille les véhicules Communauto Flex dans un
// géofence et notifie les nouvelles apparitions.
//
// LECTURE SEULE PAR CONSTRUCTION. Le binaire n'appelle qu'un endpoint public
// et non authentifié, et ne contient aucun code de réservation ou de blocage
// de véhicule. Le mode automatique est décrit, non implémenté (PLAN.md, phase 8).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Sekuryn/flexwatch/internal/communauto"
	"github.com/Sekuryn/flexwatch/internal/config"
	"github.com/Sekuryn/flexwatch/internal/geo"
	"github.com/Sekuryn/flexwatch/internal/metrics"
	"github.com/Sekuryn/flexwatch/internal/notify"
	"github.com/Sekuryn/flexwatch/internal/store"
	"github.com/Sekuryn/flexwatch/internal/watch"
)

// version est injectée au build : -ldflags "-X main.version=$(git describe)".
var version = "dev"

// shutdownGrace est le temps laissé au serveur d'admin pour finir ses requêtes.
const shutdownGrace = 5 * time.Second

func main() {
	var (
		once        = flag.Bool("once", false, "effectuer un seul poll, afficher le resultat et sortir")
		showVersion = flag.Bool("version", false, "afficher la version et sortir")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("flexwatch %s (%s)\n", version, buildRevision())
		return
	}

	if err := run(*once); err != nil {
		// Le logger n'est peut-être pas encore prêt (erreur de config) :
		// stderr est le canal fiable ici.
		fmt.Fprintf(os.Stderr, "flexwatch: %v\n", err)
		os.Exit(1)
	}
}

func run(once bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration invalide:\n%w", err)
	}

	// Logs structurés JSON sur stdout : directement exploitables par Promtail
	// puis Loki, sans parsing fragile (cf. PLAN.md phase 7).
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	log.Info("flexwatch demarre", append([]any{"version", version}, cfg.Redacted()...)...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := metrics.New(version)
	client := communauto.NewClient(cfg.APIBaseURL, cfg.UserAgent, cfg.HTTPTimeout)

	st, closeStore, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeStore()

	notifier := buildNotifier(cfg, m, log)

	runner := watch.NewRunner(watch.Params{
		CityID:       cfg.CityID,
		Center:       geo.Point{Lat: cfg.Center.Lat, Lon: cfg.Center.Lon},
		RadiusKM:     cfg.RadiusKM,
		PollInterval: cfg.PollInterval,
		RealertAfter: cfg.RealertAfter,
		Persist:      cfg.StoreEnabled,
	}, client, notifier, st, m, log)

	if once {
		return runOnce(ctx, runner, cfg)
	}

	srv, serverErrs := startAdminServer(cfg, m, log)
	defer func() {
		if err := metrics.Shutdown(srv, shutdownGrace); err != nil {
			log.Error("arret du serveur d'admin en echec", "error", err.Error())
		}
	}()

	// Si le port d'admin est déjà pris, on veut le savoir tout de suite : un
	// bot sans /metrics est un bot qu'on ne peut pas surveiller.
	select {
	case err := <-serverErrs:
		return fmt.Errorf("serveur d'admin: %w", err)
	case <-time.After(150 * time.Millisecond):
	}

	return runner.Run(ctx)
}

// runOnce exécute un unique poll et affiche un résumé lisible. C'est le mode
// à utiliser pour vérifier à la main que le contrat de l'API n'a pas bougé.
func runOnce(ctx context.Context, runner *watch.Runner, cfg config.Config) error {
	box := runner.BBox()
	fmt.Printf("bbox interrogee: lat [%.6f, %.6f] lon [%.6f, %.6f]\n",
		box.MinLat, box.MaxLat, box.MinLon, box.MaxLon)

	res, err := runner.Poll(ctx)
	if err != nil {
		return fmt.Errorf("poll: %w", err)
	}

	fmt.Printf("total annonce par l'API : %d\n", res.TotalInCity)
	fmt.Printf("recus dans la bbox      : %d\n", res.InBBox)
	fmt.Printf("dans le rayon de %.1f km : %d\n", cfg.RadiusKM, len(res.InFence))
	if res.Skipped > 0 {
		fmt.Printf("indecodables            : %d  <-- le schema de l'API a peut-etre change\n", res.Skipped)
	}

	center := geo.Point{Lat: cfg.Center.Lat, Lon: cfg.Center.Lon}
	for _, v := range res.InFence {
		fmt.Printf("  %-28s %5.0f m  %s\n",
			v.Label(), geo.DistanceKM(center, v.Position)*1000, v.MapsURL())
	}
	return nil
}

// buildNotifier assemble les sinks. La console est toujours branchée : sans
// Telegram, le bot reste utilisable et testable.
func buildNotifier(cfg config.Config, m *metrics.Metrics, log *slog.Logger) notify.Notifier {
	sinks := []notify.Notifier{notify.NewConsole(os.Stdout)}

	if cfg.TelegramEnabled() {
		sinks = append(sinks, notify.NewTelegram(
			cfg.TelegramToken, cfg.TelegramChatID, cfg.TelegramAPIBaseURL, cfg.HTTPTimeout))
		log.Info("sink telegram actif",
			"chat_id", cfg.TelegramChatID,
			"api_base_url", cfg.TelegramAPIBaseURL)
	} else {
		log.Info("sink telegram inactif (TELEGRAM_TOKEN/TELEGRAM_CHAT_ID absents)")
	}

	return &notify.Multi{
		Sinks: sinks,
		OnResult: func(sink string, err error) {
			m.ObserveNotification(sink, err)
		},
	}
}

// openStore ouvre la persistance si elle est demandée. Le binaire par défaut
// est compilé sans driver SQL : on l'explique clairement plutôt que d'échouer
// sur un message obscur.
func openStore(ctx context.Context, cfg config.Config, log *slog.Logger) (store.Store, func(), error) {
	if !cfg.StoreEnabled {
		return store.Noop{}, func() {}, nil
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		if errors.Is(err, store.ErrNotCompiled) {
			return nil, nil, fmt.Errorf(
				"FLEX_STORE_ENABLED=true mais ce binaire est compile sans postgres: "+
					"utiliser `make build-postgres`, ou laisser FLEX_STORE_ENABLED=false (%w)", err)
		}
		return nil, nil, fmt.Errorf("ouverture du store: %w", err)
	}

	log.Info("persistance des snapshots active")
	return st, func() {
		if err := st.Close(); err != nil {
			log.Error("fermeture du store en echec", "error", err.Error())
		}
	}, nil
}

// startAdminServer lance /metrics, /healthz et /readyz dans une goroutine.
func startAdminServer(cfg config.Config, m *metrics.Metrics, log *slog.Logger) (*http.Server, <-chan error) {
	// Pas de poll réussi depuis 5 intervalles = pas prêt. Le seuil est relatif
	// à l'intervalle pour rester juste quel que soit le réglage.
	srv := metrics.NewServer(cfg.MetricsAddr, m, m.StaleAfter(5*cfg.PollInterval))

	errs := make(chan error, 1)
	go func() {
		log.Info("serveur d'admin en ecoute", "addr", cfg.MetricsAddr,
			"endpoints", "/metrics /healthz /readyz")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serveur d'admin arrete", "error", err.Error())
			errs <- err
		}
	}()
	return srv, errs
}

// buildRevision renvoie le commit git intégré par le toolchain Go, utile pour
// relier un binaire en production à une ligne de code.
func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "revision inconnue"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "revision inconnue"
}
