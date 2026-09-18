package watch

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sekuryn/flexwatch/internal/communauto"
	"github.com/Sekuryn/flexwatch/internal/geo"
	"github.com/Sekuryn/flexwatch/internal/metrics"
	"github.com/Sekuryn/flexwatch/internal/notify"
)

type captureNotifier struct {
	mu    sync.Mutex
	calls int
	last  notify.Notification
}

func (c *captureNotifier) Name() string { return "capture" }

func (c *captureNotifier) Notify(_ context.Context, n notify.Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.last = n
	return nil
}

func (c *captureNotifier) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *captureNotifier) lastNotification() notify.Notification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// fakeAPI sert un corps de réponse configurable, sans jamais bloquer : un
// handler bloqué ferait pendre httptest.Server.Close().
type fakeAPI struct {
	mu   sync.Mutex
	body string
	hits atomic.Int64
}

func (f *fakeAPI) set(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = body
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.hits.Add(1)
	f.mu.Lock()
	body := f.body
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// Centre du géofence et deux voitures : une à ~370 m, une à ~4,3 km. Avec un
// rayon de 1 km, seule la première compte — c'est tout l'intérêt du filtrage
// haversine appliqué après la requête par bounding box.
const (
	centerLat = 45.5017
	centerLon = -73.5673
)

const bodyNear = `{"totalNbVehicles":2,"vehicles":[
  {"vehicleId":1,"vehicleNb":"NEAR","vehicleLocation":{"latitude":45.5050,"longitude":-73.5673}},
  {"vehicleId":2,"vehicleNb":"FAR","vehicleLocation":{"latitude":45.5400,"longitude":-73.5673}}
]}`

const bodyNearPlusOne = `{"totalNbVehicles":3,"vehicles":[
  {"vehicleId":1,"vehicleNb":"NEAR","vehicleLocation":{"latitude":45.5050,"longitude":-73.5673}},
  {"vehicleId":2,"vehicleNb":"FAR","vehicleLocation":{"latitude":45.5400,"longitude":-73.5673}},
  {"vehicleId":3,"vehicleNb":"NEW","vehicleLocation":{"latitude":45.5000,"longitude":-73.5700}}
]}`

func newTestRunner(t *testing.T) (*Runner, *captureNotifier, *fakeAPI) {
	t.Helper()

	api := &fakeAPI{body: bodyNear}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	notifier := &captureNotifier{}
	r := NewRunner(
		Params{
			CityID:       59,
			Center:       geo.Point{Lat: centerLat, Lon: centerLon},
			RadiusKM:     1,
			PollInterval: 20 * time.Second,
			RealertAfter: 10 * time.Minute,
		},
		communauto.NewClient(srv.URL, "flexwatch-test/1.0", 2*time.Second),
		notifier,
		nil, // remplacé par store.Noop
		metrics.New("test"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	return r, notifier, api
}

func TestRunnerFiltersToCircleAndDetectsNew(t *testing.T) {
	r, notifier, api := newTestRunner(t)
	ctx := context.Background()

	// Premier poll : état initial, aucune notification.
	res, err := r.Poll(ctx)
	if err != nil {
		t.Fatalf("premier Poll() = %v", err)
	}
	if res.InBBox != 2 {
		t.Errorf("InBBox = %d, attendu 2", res.InBBox)
	}
	if len(res.InFence) != 1 || res.InFence[0].Number != "NEAR" {
		t.Fatalf("filtrage circulaire incorrect: %+v", res.InFence)
	}
	r.report(ctx, res)
	if notifier.count() != 0 {
		t.Error("aucune notification attendue au premier poll")
	}

	// Deuxième poll : une voiture de plus dans le rayon.
	api.set(bodyNearPlusOne)
	res, err = r.Poll(ctx)
	if err != nil {
		t.Fatalf("second Poll() = %v", err)
	}
	if len(res.Diff.New) != 1 || res.Diff.New[0].Number != "NEW" {
		t.Fatalf("Diff.New = %+v, attendu la seule voiture NEW", res.Diff.New)
	}
	r.report(ctx, res)
	if notifier.count() != 1 {
		t.Fatalf("notifications = %d, attendu 1", notifier.count())
	}
	if n := notifier.lastNotification(); n.Present != 2 || n.RadiusKM != 1 {
		t.Errorf("notification mal remplie: %+v", n)
	}

	// Troisième poll identique : rien de neuf, donc rien à annoncer.
	res, err = r.Poll(ctx)
	if err != nil {
		t.Fatalf("troisieme Poll() = %v", err)
	}
	r.report(ctx, res)
	if notifier.count() != 1 {
		t.Errorf("notifications = %d, un etat stable ne doit pas re-notifier", notifier.count())
	}
}

func TestRunnerNextDelayBacksOff(t *testing.T) {
	r, _, _ := newTestRunner(t)

	if d := r.nextDelay(0, nil); d < 18*time.Second || d > 22*time.Second {
		t.Errorf("regime normal: delay = %s, attendu ~20s jittere", d)
	}

	first := r.nextDelay(1, errNetwork{})
	fourth := r.nextDelay(4, errNetwork{})
	if fourth <= first {
		t.Errorf("le backoff doit croitre: %s puis %s", first, fourth)
	}

	// Le plafond est une garantie dure : le jitter ne doit pas le dépasser, et
	// un compteur d'échecs très élevé (panne de plusieurs heures) ne doit pas
	// faire déborder le calcul 2^n en une durée négative — ce qui reviendrait à
	// marteler l'API au pire moment.
	for _, failures := range []int{20, 64, 1000, 1 << 20} {
		d := r.nextDelay(failures, errNetwork{})
		if d <= 0 {
			t.Errorf("nextDelay(%d) = %s : delai nul ou negatif (debordement)", failures, d)
		}
		if d > maxBackoff {
			t.Errorf("nextDelay(%d) = %s > plafond %s", failures, d, maxBackoff)
		}
	}
}

// Un Retry-After explicite de l'API doit primer sur notre propre backoff.
func TestRunnerHonorsRetryAfter(t *testing.T) {
	r, _, _ := newTestRunner(t)

	err := &communauto.HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 90 * time.Second}
	if d := r.nextDelay(1, err); d != 90*time.Second {
		t.Errorf("delay = %s, attendu 90s (Retry-After)", d)
	}

	// Mais il reste plafonné : un serveur ne nous endort pas une heure.
	err.RetryAfter = time.Hour
	if d := r.nextDelay(1, err); d != maxBackoff {
		t.Errorf("delay = %s, attendu le plafond %s", d, maxBackoff)
	}
}

func TestErrorKind(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, "none"},
		{errNetwork{}, "network"},
		{&communauto.HTTPError{StatusCode: 500}, "http"},
		{communauto.ErrSchemaDrift, "schema"},
	}
	for _, tt := range tests {
		if got := errorKind(tt.err); got != tt.want {
			t.Errorf("errorKind(%v) = %q, attendu %q", tt.err, got, tt.want)
		}
	}
}

// Un arrêt demandé (SIGINT/SIGTERM -> contexte annulé) doit sortir de la
// boucle sans erreur.
func TestRunnerRunStopsOnContextCancel(t *testing.T) {
	r, _, api := newTestRunner(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	// On laisse au moins un poll se produire, puis on coupe.
	deadline := time.Now().Add(2 * time.Second)
	for api.hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() = %v, un arret demande n'est pas une erreur", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() ne s'est pas arrete apres annulation du contexte")
	}
}

type errNetwork struct{}

func (errNetwork) Error() string { return "dial tcp: connection refused" }
