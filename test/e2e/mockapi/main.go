// Commande mockapi : faux serveur pour le test de bout en bout local.
//
// Il joue DEUX rôles à la fois :
//   - l'API Communauto (/api/v2/Vehicle/FreeFloatingAvailability), avec un
//     scénario déterministe piloté par le NUMÉRO d'appel — et non par
//     l'horloge, pour que le test soit reproductible ;
//   - l'API Telegram (/bot<token>/sendMessage), qui enregistre les messages
//     reçus au lieu de les envoyer.
//
// Le point /_recorded permet au script de test d'inspecter ce qui a été
// notifié. Ce binaire est un outil de test : il n'est jamais déployé.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Positions choisies autour du centre-ville de Montréal (45.5017, -73.5673),
// le centre que run.sh passe au bot :
//
//	proche1  ~370 m du centre  -> dans un rayon de 1 km
//	proche2  ~280 m du centre  -> dans un rayon de 1 km
//	loin     ~4,3 km du centre -> renvoyé par l'API, mais DOIT être filtré

type vehicle struct {
	VehicleID               int      `json:"vehicleId"`
	VehicleNb               int      `json:"vehicleNb"`
	CityID                  int      `json:"cityId"`
	VehiclePropulsionTypeID int      `json:"vehiclePropulsionTypeId"`
	VehicleLocation         location `json:"vehicleLocation"`
	EnergyLevelPercentage   *int     `json:"energyLevelPercentage"`
	SatisfiesFilters        bool     `json:"satisfiesFilters"`
}

type location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type availability struct {
	TotalNbVehicles int         `json:"totalNbVehicles"`
	CachingInfo     cachingInfo `json:"cachingInfo"`
	Vehicles        []vehicle   `json:"vehicles"`
}

type cachingInfo struct {
	CachingDurationInSec int  `json:"cachingDurationInSec"`
	ServedFromCache      bool `json:"servedFromCache"`
}

func charge(p int) *int { return &p }

var (
	proche1 = vehicle{
		VehicleID: 10428, VehicleNb: 8129, CityID: 59, VehiclePropulsionTypeID: 1,
		VehicleLocation:  location{Latitude: 45.5050, Longitude: -73.5673},
		SatisfiesFilters: true,
	}
	proche2 = vehicle{
		VehicleID: 7959, VehicleNb: 6106, CityID: 59, VehiclePropulsionTypeID: 2,
		VehicleLocation:       location{Latitude: 45.5000, Longitude: -73.5700},
		EnergyLevelPercentage: charge(82),
		SatisfiesFilters:      true,
	}
	loin = vehicle{
		VehicleID: 11111, VehicleNb: 9999, CityID: 59, VehiclePropulsionTypeID: 1,
		VehicleLocation:  location{Latitude: 45.5400, Longitude: -73.5673},
		SatisfiesFilters: true,
	}
)

// scenario décrit la flotte renvoyée au n-ième appel (index 0 = 1er appel).
// Le dernier état est répété ensuite.
var scenario = [][]vehicle{
	{proche1, loin},          // 1. état initial      -> AUCUNE notification
	{proche1, proche2, loin}, // 2. proche2 apparaît  -> 1 notification
	{proche1, proche2, loin}, // 3. état stable       -> AUCUNE notification
	{proche1, loin},          // 4. proche2 s'en va   -> 1 disparition
	{proche1, loin},          // 5. stable
}

type server struct {
	mu        sync.Mutex
	calls     int
	telegrams []recordedMessage
}

type recordedMessage struct {
	Path     string    `json:"path"`
	ChatID   string    `json:"chat_id"`
	Text     string    `json:"text"`
	Received time.Time `json:"received"`
}

func (s *server) availability(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	step := s.calls
	s.calls++
	s.mu.Unlock()

	fleet := scenario[min(step, len(scenario)-1)]

	// On ne journalise QUE des valeurs converties en nombres, jamais la chaîne
	// brute de la requête : une entrée client peut contenir des sauts de ligne
	// et forger de fausses lignes de log (injection de logs, gosec G706).
	// Effet de bord utile : ça vérifie au passage que le bot envoie bien les
	// paramètres attendus.
	q := r.URL.Query()
	cityID, _ := strconv.Atoi(q.Get("CityId"))
	minLat, _ := strconv.ParseFloat(q.Get("MinLatitude"), 64)
	maxLat, _ := strconv.ParseFloat(q.Get("MaxLatitude"), 64)
	log.Printf("appel %d -> %d vehicules (CityId=%d, bbox lat %.4f..%.4f)",
		step+1, len(fleet), cityID, minLat, maxLat)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(availability{
		TotalNbVehicles: len(fleet),
		CachingInfo:     cachingInfo{CachingDurationInSec: 5, ServedFromCache: false},
		Vehicles:        fleet,
	})
}

func (s *server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"ok":false,"description":"json invalide"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.telegrams = append(s.telegrams, recordedMessage{
		Path: r.URL.Path, ChatID: payload.ChatID, Text: payload.Text, Received: time.Now(),
	})
	count := len(s.telegrams)
	s.mu.Unlock()

	// Meme regle que dans le handler de disponibilite : on ne journalise que
	// des valeurs converties en nombres. `payload.ChatID` vient du corps de
	// la requete et pourrait contenir des sauts de ligne (injection de logs).
	chatID, _ := strconv.Atoi(payload.ChatID)
	log.Printf("telegram #%d recu pour le chat %d (%d octets de texte)", count, chatID, len(payload.Text))

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
}

func (s *server) recorded(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := struct {
		Calls     int               `json:"api_calls"`
		Telegrams []recordedMessage `json:"telegrams"`
	}{Calls: s.calls, Telegrams: s.telegrams}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "adresse d'ecoute")
	flag.Parse()

	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/Vehicle/FreeFloatingAvailability", s.availability)
	// Telegram met le token dans le chemin : on accepte n'importe quel bot.
	mux.HandleFunc("/bot", s.sendMessage)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) > 4 && r.URL.Path[:4] == "/bot" {
			s.sendMessage(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/_recorded", s.recorded)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Printf("mockapi en ecoute sur http://%s\n", *addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
