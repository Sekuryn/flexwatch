package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExpositionFormat(t *testing.T) {
	m := New("1.2.3")
	now := time.Now()

	m.ObservePollSuccess(now, 120*time.Millisecond, 412, 3, 0)
	m.ObservePollError(now, 2*time.Second, "http")
	m.ObserveDiff(2, 1)
	m.ObserveNotification("telegram", nil)
	m.ObserveNotification("telegram", errors.New("boom"))
	m.ObserveStoreWrite(nil)

	var sb strings.Builder
	m.WriteExposition(&sb)
	out := sb.String()

	wants := []string{
		`flexwatch_build_info{version="1.2.3"`,
		`flexwatch_polls_total{result="success"} 1`,
		`flexwatch_polls_total{result="error"} 1`,
		`flexwatch_poll_errors_total{kind="http"} 1`,
		`flexwatch_vehicles_in_city 412`,
		`flexwatch_vehicles_in_fence 3`,
		`flexwatch_new_appearances_total 2`,
		`flexwatch_disappearances_total 1`,
		`flexwatch_notifications_total{outcome="telegram_ok"} 1`,
		`flexwatch_notifications_total{outcome="telegram_error"} 1`,
		`flexwatch_store_writes_total{result="success"} 1`,
		`flexwatch_poll_duration_seconds_bucket{le="0.25"} 1`,
		`flexwatch_poll_duration_seconds_bucket{le="+Inf"} 2`,
		`flexwatch_poll_duration_seconds_count 2`,
		`# TYPE flexwatch_polls_total counter`,
		`# TYPE flexwatch_poll_duration_seconds histogram`,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("exposition sans %q\n--- sortie ---\n%s", want, out)
		}
	}

	// Aucune série ne doit sortir avec une valeur non finie : Prometheus
	// refuserait le scrape entier.
	for _, bad := range []string{"NaN", "-Inf"} {
		if strings.Contains(out, bad) {
			t.Errorf("valeur non finie exposee (%s):\n%s", bad, out)
		}
	}

	// Chaque métrique doit avoir son HELP et son TYPE.
	help := strings.Count(out, "# HELP ")
	typ := strings.Count(out, "# TYPE ")
	if help != typ || help == 0 {
		t.Errorf("%d HELP pour %d TYPE", help, typ)
	}
}

func TestEscapeDoesNotDoubleEscape(t *testing.T) {
	m := New(`1.0"beta\x`)

	var sb strings.Builder
	m.WriteExposition(&sb)

	want := `version="1.0\"beta\\x"`
	if !strings.Contains(sb.String(), want) {
		t.Errorf("echappement incorrect, attendu %q dans:\n%s", want, sb.String())
	}
}

func TestHistogramCumulative(t *testing.T) {
	h := NewHistogram(1, 2, 5)
	for _, v := range []float64{0.5, 1.5, 3, 10} {
		h.Observe(v)
	}

	var sb strings.Builder
	writeHistogram(&sb, "test_seconds", "help", h)
	out := sb.String()

	// Buckets cumulatifs : le<=1 -> 1, le<=2 -> 2, le<=5 -> 3, +Inf -> 4.
	for _, want := range []string{
		`test_seconds_bucket{le="1"} 1`,
		`test_seconds_bucket{le="2"} 2`,
		`test_seconds_bucket{le="5"} 3`,
		`test_seconds_bucket{le="+Inf"} 4`,
		`test_seconds_count 4`,
		`test_seconds_sum 15`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("histogramme sans %q:\n%s", want, out)
		}
	}
}

func TestHandlerRejectsNonGet(t *testing.T) {
	m := New("test")
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/metrics", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d, attendu 405", rec.Code)
	}
}

func TestReadyzReflectsStaleness(t *testing.T) {
	m := New("test")
	srv := NewServer(":0", m, m.StaleAfter(time.Minute))

	// Avant tout poll réussi : pas prêt.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz avant le premier poll = %d, attendu 503", rec.Code)
	}

	// Liveness reste verte : ce n'est pas au process de mourir si l'API amont
	// est en panne.
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, attendu 200", rec.Code)
	}

	// Après un poll réussi : prêt.
	m.ObservePollSuccess(time.Now(), time.Second, 10, 1, 0)
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/readyz apres un poll reussi = %d, attendu 200", rec.Code)
	}

	// Poll réussi trop vieux : plus prêt.
	m.ObservePollSuccess(time.Now().Add(-10*time.Minute), time.Second, 10, 1, 0)
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz avec un poll vieux de 10 min = %d, attendu 503", rec.Code)
	}
}
