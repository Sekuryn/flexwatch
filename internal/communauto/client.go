// Package communauto est un client lecture seule de l'API publique Reservauto.
//
// Seul l'endpoint de disponibilité free-floating (Flex) est implémenté, et il
// ne prend aucune authentification. Aucune fonction de ce package ne réserve,
// ne bloque ni ne modifie quoi que ce soit : c'est un choix d'architecture,
// pas un oubli (cf. PLAN.md phase 8).
package communauto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sekuryn/flexwatch/internal/geo"
)

// maxBodyBytes borne la lecture de la réponse : un serveur amont compromis ou
// en vrac ne doit pas pouvoir faire exploser la mémoire du process.
const maxBodyBytes = 8 << 20 // 8 MiB

// Vehicle est un véhicule Flex disponible, normalisé.
type Vehicle struct {
	// ID est l'identifiant interne (`vehicleId`). Il ne sert qu'à dédupliquer
	// et à détecter les apparitions.
	ID string
	// Number est le numéro peint sur la voiture (`vehicleNb`) : c'est LUI que
	// l'utilisateur cherche dans la rue. L'API ne renvoie pas de plaque.
	Number string
	// Model est vide en pratique : l'API ne renvoie que des identifiants
	// numériques de type/carrosserie, non documentés. On préfère ne rien
	// afficher plutôt que d'inventer une correspondance.
	Model    string
	Position geo.Point
	// EnergyLevel est un pourcentage, ou -1 si l'API ne le fournit pas.
	// Observé : seuls les véhicules à propulsion 2 (électriques, 3 sur 42 au
	// relevé) renseignent ce champ ; les autres ont `null`.
	EnergyLevel int
	// PropulsionTypeID est `vehiclePropulsionTypeId` brut (0 = absent).
	// Corrélation observée, non documentée : 1 = essence, 2 = électrique.
	PropulsionTypeID int
}

// MapsURL renvoie un lien cliquable vers la position du véhicule.
func (v Vehicle) MapsURL() string {
	return fmt.Sprintf("https://www.google.com/maps/search/?api=1&query=%.6f,%.6f",
		v.Position.Lat, v.Position.Lon)
}

// Label est une description courte, utilisable en notification.
func (v Vehicle) Label() string {
	// On mène avec le numéro visible sur la voiture ; l'identifiant interne
	// n'est affiché que s'il n'y a rien de mieux.
	head := "#" + v.ID
	if v.Number != "" {
		head = "Flex " + v.Number
	}

	parts := []string{head}
	if v.Model != "" {
		parts = append(parts, v.Model)
	}
	if v.EnergyLevel >= 0 {
		parts = append(parts, strconv.Itoa(v.EnergyLevel)+"%")
	}
	return strings.Join(parts, " ")
}

// Availability est la réponse normalisée de l'endpoint de disponibilité.
type Availability struct {
	// TotalNbVehicles est le total annoncé par l'API pour la bbox demandée.
	TotalNbVehicles int
	Vehicles        []Vehicle
	// Skipped compte les véhicules que l'on n'a pas su décoder (position ou
	// identifiant manquant). Non nul de façon durable = le schéma a bougé.
	Skipped int
}

// HTTPError décrit une réponse non-2xx de l'API.
type HTTPError struct {
	StatusCode int
	RetryAfter time.Duration
	Snippet    string
}

func (e *HTTPError) Error() string {
	if e.Snippet == "" {
		return fmt.Sprintf("api communauto: status %d", e.StatusCode)
	}
	return fmt.Sprintf("api communauto: status %d: %s", e.StatusCode, e.Snippet)
}

// Throttled indique que l'on doit ralentir (429 ou 5xx).
func (e *HTTPError) Throttled() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// ErrSchemaDrift est renvoyée quand la réponse ne contient aucun tableau de
// véhicules exploitable : le contrat d'API a probablement changé.
var ErrSchemaDrift = errors.New("api communauto: schema inattendu (aucune liste de vehicules trouvee)")

// Client interroge l'API Reservauto.
type Client struct {
	httpClient *http.Client
	baseURL    string
	userAgent  string
}

// NewClient construit un client. Le timeout s'applique à la requête entière.
func NewClient(baseURL, userAgent string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        4,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     90 * time.Second,
				ForceAttemptHTTP2:   true,
			},
		},
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: userAgent,
	}
}

// FreeFloatingAvailability renvoie les véhicules Flex disponibles dans la
// bounding box. L'API ne sait pas filtrer par rayon : le filtrage circulaire
// se fait côté appelant (cf. geo.DistanceKM).
func (c *Client) FreeFloatingAvailability(ctx context.Context, cityID int, box geo.BBox) (*Availability, error) {
	endpoint := c.baseURL + "/Vehicle/FreeFloatingAvailability"

	q := url.Values{}
	q.Set("CityId", strconv.Itoa(cityID))
	q.Set("MaxLatitude", formatCoord(box.MaxLat))
	q.Set("MinLatitude", formatCoord(box.MinLat))
	q.Set("MaxLongitude", formatCoord(box.MaxLon))
	q.Set("MinLongitude", formatCoord(box.MinLon))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("construction requete: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// On s'identifie honnêtement : pas d'usurpation de navigateur.
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("appel api: %w", err)
	}
	defer func() {
		// Drain borné puis fermeture, pour réutiliser la connexion.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("lecture reponse: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Snippet:    snippet(body, 256),
		}
	}

	return parseAvailability(body)
}

func formatCoord(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func snippet(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
