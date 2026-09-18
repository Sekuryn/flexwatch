package geo

import (
	"math"
	"testing"
)

var (
	montrealDowntown = Point{Lat: 45.5017, Lon: -73.5673}
	montrealMileEnd  = Point{Lat: 45.5250, Lon: -73.6000}
	paris            = Point{Lat: 48.8566, Lon: 2.3522}
)

func TestDistanceKM(t *testing.T) {
	tests := []struct {
		name   string
		a, b   Point
		wantKM float64
		tolKM  float64
	}{
		{"identique", montrealDowntown, montrealDowntown, 0, 1e-9},
		{"centre-ville vers Mile End", montrealDowntown, montrealMileEnd, 3.68, 0.05},
		{"Montreal vers Paris", montrealDowntown, paris, 5510, 15},
		{"symetrique", montrealMileEnd, montrealDowntown, 3.68, 0.05},
		{"un degre de latitude", Point{0, 0}, Point{1, 0}, 111.19, 0.05},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DistanceKM(tt.a, tt.b)
			if math.Abs(got-tt.wantKM) > tt.tolKM {
				t.Errorf("DistanceKM() = %.4f km, attendu %.4f +/- %.4f", got, tt.wantKM, tt.tolKM)
			}
		})
	}
}

// TestBBoxAroundContainsCircle vérifie l'invariant qui rend l'algorithme
// correct : tout point du cercle doit être dans la bbox, sinon on manquerait
// des voitures côté API avant même le filtrage haversine.
func TestBBoxAroundContainsCircle(t *testing.T) {
	const radiusKM = 2.0
	box := BBoxAround(montrealDowntown, radiusKM)

	for deg := 0; deg < 360; deg++ {
		bearing := float64(deg) * math.Pi / 180
		// Projection approximative d'un point sur le cercle.
		lat := montrealDowntown.Lat + (radiusKM/kmPerDegreeLat)*math.Cos(bearing)
		lon := montrealDowntown.Lon + (radiusKM/(kmPerDegreeLat*math.Cos(radians(montrealDowntown.Lat))))*math.Sin(bearing)
		if !box.Contains(Point{Lat: lat, Lon: lon}) {
			t.Fatalf("bbox %+v ne contient pas le point de bearing %d (%.5f, %.5f)", box, deg, lat, lon)
		}
	}
}

func TestBBoxAroundDegenerate(t *testing.T) {
	box := BBoxAround(montrealDowntown, 0)
	if !box.Contains(montrealDowntown) {
		t.Errorf("une bbox de rayon 0 doit contenir son centre, got %+v", box)
	}

	// Rayon négatif : traité comme 0, pas de NaN qui se propagerait dans l'URL.
	box = BBoxAround(montrealDowntown, -5)
	if math.IsNaN(box.MinLat) || math.IsNaN(box.MaxLon) {
		t.Errorf("rayon negatif produit des NaN: %+v", box)
	}

	// Pôle : la plage de longitude doit rester bornée.
	box = BBoxAround(Point{Lat: 90, Lon: 0}, 10)
	if math.IsInf(box.MinLon, 0) || box.MinLon < -180 || box.MaxLon > 180 {
		t.Errorf("longitudes hors bornes au pole: %+v", box)
	}
}
