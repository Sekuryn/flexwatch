// Package metrics expose les métriques de l'application au format texte
// Prometheus (exposition format 0.0.4).
//
// Pourquoi à la main plutôt qu'avec client_golang : le binaire n'a ainsi
// AUCUNE dépendance externe. Un SBOM à zéro dépendance tierce, c'est zéro CVE
// transitive à trier et un argument supply-chain direct (cf. PLAN.md phase 3).
// Le format est stable et documenté ; ce qu'on en utilise ici (counter, gauge,
// histogram) tient en 150 lignes testables.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const namespace = "flexwatch"

// Counter est un compteur monotone croissant.
type Counter struct{ v atomic.Uint64 }

// Inc ajoute 1.
func (c *Counter) Inc() { c.v.Add(1) }

// Add ajoute n (ignoré si n < 0 : un counter ne décroît jamais).
func (c *Counter) Add(n int) {
	if n > 0 {
		c.v.Add(uint64(n))
	}
}

// Value renvoie la valeur courante.
func (c *Counter) Value() uint64 { return c.v.Load() }

// Gauge est une valeur instantanée qui peut monter et descendre.
type Gauge struct{ bits atomic.Uint64 }

// Set fixe la valeur.
func (g *Gauge) Set(v float64) { g.bits.Store(math.Float64bits(v)) }

// SetTime enregistre un instant en secondes Unix (convention Prometheus).
func (g *Gauge) SetTime(t time.Time) { g.Set(float64(t.UnixNano()) / 1e9) }

// Value renvoie la valeur courante.
func (g *Gauge) Value() float64 { return math.Float64frombits(g.bits.Load()) }

// Histogram est un histogramme cumulatif à buckets fixes.
type Histogram struct {
	mu     sync.Mutex
	bounds []float64
	counts []uint64
	sum    float64
	total  uint64
}

// NewHistogram crée un histogramme. bounds doit être trié croissant.
func NewHistogram(bounds ...float64) *Histogram {
	b := make([]float64, len(bounds))
	copy(b, bounds)
	sort.Float64s(b)
	return &Histogram{bounds: b, counts: make([]uint64, len(b))}
}

// Observe enregistre une mesure.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += v
	h.total++
	for i, ub := range h.bounds {
		if v <= ub {
			h.counts[i]++
		}
	}
}

// counterVec est un compteur étiqueté par une seule dimension (p.ex. le sink
// de notification). Volontairement limité : un seul label suffit ici.
type counterVec struct {
	mu       sync.RWMutex
	label    string
	children map[string]*Counter
}

func newCounterVec(label string) *counterVec {
	return &counterVec{label: label, children: make(map[string]*Counter)}
}

// With renvoie le compteur pour cette valeur de label, en le créant au besoin.
func (cv *counterVec) With(value string) *Counter {
	cv.mu.RLock()
	c, ok := cv.children[value]
	cv.mu.RUnlock()
	if ok {
		return c
	}

	cv.mu.Lock()
	defer cv.mu.Unlock()
	// Double vérification : un autre appelant a pu créer le compteur entre le
	// RUnlock et le Lock. La variable est nommée différemment pour ne pas
	// masquer celle de la lecture optimiste (govet/shadow).
	if existing, ok := cv.children[value]; ok {
		return existing
	}
	c = &Counter{}
	cv.children[value] = c
	return c
}

// Metrics regroupe toutes les séries exposées par flexwatch.
type Metrics struct {
	version string

	pollDuration *Histogram

	// polls porte le label result=success|error.
	polls *counterVec
	// pollErrors porte le label kind=http|network|schema.
	pollErrors *counterVec
	// notifications porte le label outcome=<sink>_ok|<sink>_error.
	notifications *counterVec
	// storeWrites porte le label result=success|error.
	storeWrites *counterVec

	newAppearances  Counter
	disappearances  Counter
	vehiclesSkipped Counter

	vehiclesInCity  Gauge
	vehiclesInFence Gauge
	lastPollAt      Gauge
	lastSuccessAt   Gauge
}

// New construit le jeu de métriques.
func New(version string) *Metrics {
	return &Metrics{
		version: version,
		// Buckets adaptés à un appel HTTP externe : de 50 ms à 10 s.
		pollDuration:  NewHistogram(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10),
		polls:         newCounterVec("result"),
		pollErrors:    newCounterVec("kind"),
		notifications: newCounterVec("outcome"),
		storeWrites:   newCounterVec("result"),
	}
}

// ObservePollSuccess enregistre un poll réussi.
func (m *Metrics) ObservePollSuccess(at time.Time, d time.Duration, inCity, inFence, skipped int) {
	m.polls.With("success").Inc()
	m.pollDuration.Observe(d.Seconds())
	m.vehiclesInCity.Set(float64(inCity))
	m.vehiclesInFence.Set(float64(inFence))
	m.vehiclesSkipped.Add(skipped)
	m.lastPollAt.SetTime(at)
	m.lastSuccessAt.SetTime(at)
}

// ObservePollError enregistre un poll en échec. kind sert à distinguer une
// panne réseau d'une dérive de schéma dans les alertes.
func (m *Metrics) ObservePollError(at time.Time, d time.Duration, kind string) {
	m.polls.With("error").Inc()
	m.pollErrors.With(kind).Inc()
	m.pollDuration.Observe(d.Seconds())
	m.lastPollAt.SetTime(at)
}

// ObserveDiff enregistre les apparitions et disparitions détectées.
func (m *Metrics) ObserveDiff(newCount, goneCount int) {
	m.newAppearances.Add(newCount)
	m.disappearances.Add(goneCount)
}

// ObserveNotification enregistre le résultat d'un envoi par un sink.
func (m *Metrics) ObserveNotification(sink string, err error) {
	outcome := sink + "_ok"
	if err != nil {
		outcome = sink + "_error"
	}
	m.notifications.With(outcome).Inc()
}

// ObserveStoreWrite enregistre une écriture de snapshot en base.
func (m *Metrics) ObserveStoreWrite(err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.storeWrites.With(result).Inc()
}

// WriteExposition écrit l'exposition Prometheus.
//
// Volontairement pas nommée WriteTo : `go vet` (stdmethods) signale à juste
// titre qu'une méthode WriteTo(io.Writer) doit renvoyer (int64, error) pour
// satisfaire io.WriterTo, sinon un appelant qui passe ce type à io.Copy aurait
// une surprise silencieuse.
func (m *Metrics) WriteExposition(w io.Writer) {
	var b strings.Builder

	writeBuildInfo(&b, m.version)

	writeCounterVec(&b, "polls_total",
		"Nombre de polls de l'API Communauto par resultat.", m.polls)
	writeCounterVec(&b, "poll_errors_total",
		"Erreurs de poll par nature (http, network, schema).", m.pollErrors)
	writeHistogram(&b, "poll_duration_seconds",
		"Latence d'un poll de l'API Communauto, en secondes.", m.pollDuration)

	writeGauge(&b, "vehicles_in_city",
		"Nombre de vehicules annonces par l'API pour la bounding box.", m.vehiclesInCity.Value())
	writeGauge(&b, "vehicles_in_fence",
		"Nombre de vehicules dans le geofence circulaire.", m.vehiclesInFence.Value())
	writeGauge(&b, "last_poll_timestamp_seconds",
		"Horodatage du dernier poll, reussi ou non.", m.lastPollAt.Value())
	writeGauge(&b, "last_success_timestamp_seconds",
		"Horodatage du dernier poll reussi (base de l'alerte de staleness).", m.lastSuccessAt.Value())

	writeCounter(&b, "new_appearances_total",
		"Vehicules nouvellement apparus dans le geofence.", m.newAppearances.Value())
	writeCounter(&b, "disappearances_total",
		"Vehicules ayant quitte le geofence.", m.disappearances.Value())
	writeCounter(&b, "vehicles_undecodable_total",
		"Vehicules ignores faute de position ou d'identifiant (derive de schema).", m.vehiclesSkipped.Value())

	writeCounterVec(&b, "notifications_total",
		"Notifications envoyees par sink et resultat.", m.notifications)
	writeCounterVec(&b, "store_writes_total",
		"Ecritures de snapshots en base par resultat.", m.storeWrites)

	_, _ = io.WriteString(w, b.String())
}

// Handler renvoie le handler /metrics.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.WriteExposition(w)
	})
}

func writeBuildInfo(b *strings.Builder, version string) {
	name := namespace + "_build_info"
	fmt.Fprintf(b, "# HELP %s Version du binaire et du toolchain Go.\n", name)
	fmt.Fprintf(b, "# TYPE %s gauge\n", name)
	fmt.Fprintf(b, "%s{version=\"%s\",go_version=\"%s\"} 1\n", name, escape(version), escape(runtime.Version()))
}

func writeCounter(b *strings.Builder, name, help string, v uint64) {
	full := namespace + "_" + name
	fmt.Fprintf(b, "# HELP %s %s\n", full, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", full)
	fmt.Fprintf(b, "%s %d\n", full, v)
}

func writeGauge(b *strings.Builder, name, help string, v float64) {
	full := namespace + "_" + name
	fmt.Fprintf(b, "# HELP %s %s\n", full, help)
	fmt.Fprintf(b, "# TYPE %s gauge\n", full)
	fmt.Fprintf(b, "%s %s\n", full, formatFloat(v))
}

func writeCounterVec(b *strings.Builder, name, help string, cv *counterVec) {
	full := namespace + "_" + name
	fmt.Fprintf(b, "# HELP %s %s\n", full, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", full)

	cv.mu.RLock()
	keys := make([]string, 0, len(cv.children))
	for k := range cv.children {
		keys = append(keys, k)
	}
	values := make(map[string]uint64, len(keys))
	for _, k := range keys {
		values[k] = cv.children[k].Value()
	}
	label := cv.label
	cv.mu.RUnlock()

	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%s{%s=\"%s\"} %d\n", full, label, escape(k), values[k])
	}
}

func writeHistogram(b *strings.Builder, name, help string, h *Histogram) {
	full := namespace + "_" + name
	fmt.Fprintf(b, "# HELP %s %s\n", full, help)
	fmt.Fprintf(b, "# TYPE %s histogram\n", full)

	h.mu.Lock()
	bounds := make([]float64, len(h.bounds))
	copy(bounds, h.bounds)
	counts := make([]uint64, len(h.counts))
	copy(counts, h.counts)
	sum, total := h.sum, h.total
	h.mu.Unlock()

	for i, ub := range bounds {
		fmt.Fprintf(b, "%s_bucket{le=\"%s\"} %d\n", full, formatFloat(ub), counts[i])
	}
	fmt.Fprintf(b, "%s_bucket{le=\"+Inf\"} %d\n", full, total)
	fmt.Fprintf(b, "%s_sum %s\n", full, formatFloat(sum))
	fmt.Fprintf(b, "%s_count %d\n", full, total)
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// escape échappe une valeur de label (backslash, guillemet, saut de ligne).
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return strings.ReplaceAll(s, "\n", `\n`)
}
