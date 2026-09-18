package communauto

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sekuryn/flexwatch/internal/geo"
)

// testdata/freefloating_montreal.json est une VRAIE réponse de l'API, capturée
// le 2026-09-17 sur CityId=59 et réduite à deux véhicules. C'est le test qui
// protège du mode de panne le plus probable du projet : le décodeur qui ne
// correspond plus au contrat réel.
func TestFreeFloatingAvailabilityDecodesRealResponse(t *testing.T) {
	body, err := os.ReadFile("testdata/freefloating_montreal.json")
	if err != nil {
		t.Fatalf("lecture du fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	av, err := NewClient(srv.URL, "flexwatch-test/1.0", 2*time.Second).
		FreeFloatingAvailability(context.Background(), 59, geo.BBox{})
	if err != nil {
		t.Fatalf("FreeFloatingAvailability() = %v", err)
	}
	if av.Skipped != 0 {
		t.Fatalf("%d vehicules ignores sur une reponse reelle", av.Skipped)
	}
	if len(av.Vehicles) != 2 {
		t.Fatalf("%d vehicules decodes, attendu 2", len(av.Vehicles))
	}

	// Véhicule à essence : pas de niveau d'énergie (null côté API).
	gas := av.Vehicles[0]
	want := Vehicle{
		ID:               "10428",
		Number:           "8129",
		Position:         geo.Point{Lat: 45.4721155462088, Lon: -73.587243162777},
		EnergyLevel:      -1,
		PropulsionTypeID: 1,
	}
	if gas != want {
		t.Errorf("vehicule essence = %+v\nattendu           %+v", gas, want)
	}
	if got := gas.Label(); got != "Flex 8129" {
		t.Errorf("Label() = %q, attendu %q", got, "Flex 8129")
	}

	// Véhicule électrique : celui-là renseigne energyLevelPercentage.
	electric := av.Vehicles[1]
	if electric.PropulsionTypeID != 2 {
		t.Errorf("PropulsionTypeID = %d, attendu 2 (electrique)", electric.PropulsionTypeID)
	}
	if electric.EnergyLevel < 0 || electric.EnergyLevel > 100 {
		t.Errorf("EnergyLevel = %d, attendu un pourcentage", electric.EnergyLevel)
	}
	if !strings.Contains(electric.Label(), "%") {
		t.Errorf("Label() = %q, devrait montrer la charge", electric.Label())
	}

	if av.TotalNbVehicles != 3 {
		t.Errorf("TotalNbVehicles = %d, attendu 3 (valeur du fixture)", av.TotalNbVehicles)
	}
}

// Formes historiques ou hypothétiques : le décodeur doit rester tolérant, pour
// qu'un simple changement de casse ou d'imbrication côté API ne rende pas le
// bot aveugle du jour au lendemain.
const bodyLegacyKeys = `{
  "totalNbVehicles": 2,
  "cachingInfo": {"cachingDurationInSec": 5},
  "vehicles": [
    {"carId": 90210, "carPlate": "FLEX01", "energyLevel": 74,
     "model": {"name": "Toyota Prius C"}, "position": {"latitude": 45.5100, "longitude": -73.5700}},
    {"carId": 90211, "carPlate": "FLEX02", "energyLevel": 31,
     "model": {"name": "Kia Rio"}, "position": {"latitude": 45.5300, "longitude": -73.6100}}
  ]
}`

const bodyFlatPosition = `{
  "TotalNbVehicles": 1,
  "Vehicles": [
    {"Id": "abc-123", "Plate": "FLEX09", "Model": "Nissan Micra",
     "Latitude": 45.52, "Longitude": -73.58, "EnergyLevel": 100}
  ]
}`

func TestFreeFloatingAvailabilityToleratesSchemaVariants(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantCount int
		wantFirst Vehicle
	}{
		{
			name:      "cles historiques, position imbriquee",
			body:      bodyLegacyKeys,
			wantCount: 2,
			wantFirst: Vehicle{ID: "90210", Number: "FLEX01", Model: "Toyota Prius C",
				Position: geo.Point{Lat: 45.51, Lon: -73.57}, EnergyLevel: 74},
		},
		{
			name:      "casse differente, position a plat",
			body:      bodyFlatPosition,
			wantCount: 1,
			wantFirst: Vehicle{ID: "abc-123", Number: "FLEX09", Model: "Nissan Micra",
				Position: geo.Point{Lat: 45.52, Lon: -73.58}, EnergyLevel: 100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery, gotUA string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				gotUA = r.Header.Get("User-Agent")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "flexwatch-test/1.0", 2*time.Second)
			box := geo.BBoxAround(geo.Point{Lat: 45.5017, Lon: -73.5673}, 3)

			av, err := c.FreeFloatingAvailability(context.Background(), 59, box)
			if err != nil {
				t.Fatalf("FreeFloatingAvailability() = %v", err)
			}
			if len(av.Vehicles) != tt.wantCount {
				t.Fatalf("%d vehicules, attendu %d (skipped=%d)", len(av.Vehicles), tt.wantCount, av.Skipped)
			}
			if av.Vehicles[0] != tt.wantFirst {
				t.Errorf("vehicule[0] = %+v, attendu %+v", av.Vehicles[0], tt.wantFirst)
			}
			if gotUA != "flexwatch-test/1.0" {
				t.Errorf("User-Agent = %q, on doit s'identifier honnetement", gotUA)
			}
			for _, want := range []string{"CityId=59", "MaxLatitude=", "MinLatitude=", "MaxLongitude=", "MinLongitude="} {
				if !strings.Contains(gotQuery, want) {
					t.Errorf("query %q ne contient pas %q", gotQuery, want)
				}
			}
		})
	}
}

func TestFreeFloatingAvailabilitySkipsBrokenVehicles(t *testing.T) {
	body := `{"totalNbVehicles": 3, "vehicles": [
	  {"vehicleId": 1, "vehicleLocation": {"latitude": 45.5, "longitude": -73.5}},
	  {"vehicleId": 2},
	  {"vehicleId": 3, "vehicleLocation": {"latitude": 0, "longitude": 0}}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	av, err := NewClient(srv.URL, "t", time.Second).
		FreeFloatingAvailability(context.Background(), 59, geo.BBox{})
	if err != nil {
		t.Fatalf("erreur inattendue: %v", err)
	}
	if len(av.Vehicles) != 1 || av.Skipped != 2 {
		t.Errorf("attendu 1 vehicule / 2 ignores, got %d / %d", len(av.Vehicles), av.Skipped)
	}
}

func TestFreeFloatingAvailabilityDetectsSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"totalNbVehicles": 0, "cachingInfo": {}}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "t", time.Second).
		FreeFloatingAvailability(context.Background(), 59, geo.BBox{})
	if !errors.Is(err, ErrSchemaDrift) {
		t.Fatalf("attendu ErrSchemaDrift, got %v", err)
	}
}

// Le cas vicieux : l'API répond 200 avec des véhicules, mais aucun n'est
// décodable. Un décodeur laxiste renverrait « 0 voiture » pour toujours ; on
// veut une erreur bruyante et une alerte.
func TestFreeFloatingAvailabilityFailsLoudlyWhenNothingDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"totalNbVehicles": 2, "vehicles": [
		  {"machinChose": 1, "ouListe": {"x": 45.5, "y": -73.5}},
		  {"machinChose": 2, "ouListe": {"x": 45.6, "y": -73.6}}
		]}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "t", time.Second).
		FreeFloatingAvailability(context.Background(), 59, geo.BBox{})
	if !errors.Is(err, ErrSchemaDrift) {
		t.Fatalf("attendu ErrSchemaDrift, got %v", err)
	}
}

func TestFreeFloatingAvailabilityHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"slow down"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "t", time.Second).
		FreeFloatingAvailability(context.Background(), 59, geo.BBox{})

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("attendu *HTTPError, got %T (%v)", err, err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests || !httpErr.Throttled() {
		t.Errorf("429 doit etre throttled: %+v", httpErr)
	}
	if httpErr.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %s, attendu 30s", httpErr.RetryAfter)
	}
}

func TestFreeFloatingAvailabilityRespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewClient(srv.URL, "t", 5*time.Second).
		FreeFloatingAvailability(ctx, 59, geo.BBox{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("attendu context.Canceled, got %v", err)
	}
}
