package watch

import (
	"testing"
	"time"

	"github.com/Fougere/flexwatch/internal/communauto"
	"github.com/Fougere/flexwatch/internal/geo"
)

func car(id string) communauto.Vehicle {
	return communauto.Vehicle{ID: id, Position: geo.Point{Lat: 45.51, Lon: -73.57}, EnergyLevel: -1}
}

func ids(vs []communauto.Vehicle) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDetectorFirstRunDoesNotAlert(t *testing.T) {
	d := NewDetector(10 * time.Minute)
	t0 := time.Now()

	diff := d.Update(t0, []communauto.Vehicle{car("a"), car("b")})
	if !diff.FirstRun {
		t.Error("le premier poll doit etre marque FirstRun")
	}
	if diff.HasAlerts() {
		t.Errorf("aucune alerte au premier poll, got %v", ids(diff.New))
	}
	if diff.Present != 2 {
		t.Errorf("Present = %d, attendu 2", diff.Present)
	}
}

func TestDetectorDetectsNewAndGone(t *testing.T) {
	d := NewDetector(10 * time.Minute)
	t0 := time.Now()
	d.Update(t0, []communauto.Vehicle{car("a")})

	// "b" arrive, "a" reste.
	diff := d.Update(t0.Add(20*time.Second), []communauto.Vehicle{car("a"), car("b")})
	if !equal(ids(diff.New), []string{"b"}) {
		t.Errorf("New = %v, attendu [b]", ids(diff.New))
	}
	if len(diff.Gone) != 0 {
		t.Errorf("Gone = %v, attendu vide", diff.Gone)
	}

	// "a" part, "b" reste : pas de re-alerte sur "b".
	diff = d.Update(t0.Add(40*time.Second), []communauto.Vehicle{car("b")})
	if diff.HasAlerts() {
		t.Errorf("New = %v, un vehicule qui reste ne doit pas re-alerter", ids(diff.New))
	}
	if !equal(diff.Gone, []string{"a"}) {
		t.Errorf("Gone = %v, attendu [a]", diff.Gone)
	}
}

// Cas reel : cache serveur 5 s + derive GPS en bord de rayon font clignoter
// une voiture. On ne doit pas notifier a chaque clignotement.
func TestDetectorDoesNotSpamOnFlapping(t *testing.T) {
	d := NewDetector(10 * time.Minute)
	t0 := time.Now()
	d.Update(t0, nil)

	diff := d.Update(t0.Add(20*time.Second), []communauto.Vehicle{car("a")})
	if !equal(ids(diff.New), []string{"a"}) {
		t.Fatalf("premiere apparition non detectee: %v", ids(diff.New))
	}

	for i := 2; i < 12; i++ {
		at := t0.Add(time.Duration(i*20) * time.Second)
		var fleet []communauto.Vehicle
		if i%2 == 0 {
			fleet = []communauto.Vehicle{car("a")}
		}
		if diff := d.Update(at, fleet); diff.HasAlerts() {
			t.Fatalf("re-alerte prematuree au poll %d: %v", i, ids(diff.New))
		}
	}
}

func TestDetectorRealertsAfterCooldown(t *testing.T) {
	const cooldown = 10 * time.Minute
	d := NewDetector(cooldown)
	t0 := time.Now()

	d.Update(t0, nil)
	d.Update(t0.Add(20*time.Second), []communauto.Vehicle{car("a")})
	d.Update(t0.Add(40*time.Second), nil) // "a" quitte le rayon

	diff := d.Update(t0.Add(40*time.Second+cooldown), []communauto.Vehicle{car("a")})
	if !equal(ids(diff.New), []string{"a"}) {
		t.Errorf("apres %s d'absence, une reapparition doit re-alerter, got %v", cooldown, ids(diff.New))
	}
}

func TestDetectorNeverRealertsWhenCooldownZero(t *testing.T) {
	d := NewDetector(0)
	t0 := time.Now()

	d.Update(t0, nil)
	d.Update(t0.Add(20*time.Second), []communauto.Vehicle{car("a")})
	d.Update(t0.Add(40*time.Second), nil)

	diff := d.Update(t0.Add(72*time.Hour), []communauto.Vehicle{car("a")})
	if diff.HasAlerts() {
		t.Errorf("avec realertAfter=0 on ne re-alerte jamais, got %v", ids(diff.New))
	}
}

func TestDetectorDeduplicatesAndSorts(t *testing.T) {
	d := NewDetector(time.Minute)
	t0 := time.Now()
	d.Update(t0, nil)

	diff := d.Update(t0.Add(20*time.Second), []communauto.Vehicle{car("c"), car("a"), car("a"), {ID: ""}})
	if !equal(ids(diff.New), []string{"a", "c"}) {
		t.Errorf("New = %v, attendu [a c] (dedup + tri, id vide ignore)", ids(diff.New))
	}
	if diff.Present != 2 {
		t.Errorf("Present = %d, attendu 2", diff.Present)
	}
}

func TestDetectorPrunesMemory(t *testing.T) {
	d := NewDetector(time.Minute)
	t0 := time.Now()
	d.Update(t0, []communauto.Vehicle{car("a")})
	d.Update(t0.Add(time.Second), nil)
	d.Update(t0.Add(10*time.Minute), nil)

	if len(d.lastAlerted) != 0 {
		t.Errorf("lastAlerted devrait etre purge, contient %d entrees", len(d.lastAlerted))
	}
}
