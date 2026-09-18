package communauto

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Sekuryn/flexwatch/internal/geo"
)

// L'API Reservauto n'est pas documentée publiquement et sa casse de champs a
// déjà varié selon les versions. Plutôt que de coder en dur un schéma exact
// qui casserait silencieusement (0 voiture détectée pour toujours), on décode
// de façon tolérante sur une liste de clés candidates, et on échoue *fort*
// (ErrSchemaDrift) si rien ne correspond. Adapter la liste ici si le contrat
// bouge : c'est le seul endroit à toucher.
var (
	vehiclesKeys = []string{"vehicles", "vehicleslist", "vehiclelist", "freefloatingvehicles", "availablevehicles", "cars", "carlist"}
	totalKeys    = []string{"totalnbvehicles", "totalnbvehicule", "total", "count", "nbvehicles"}
	idKeys       = []string{"vehicleid", "carid", "id", "carno"}
	// L'API ne renvoie pas de plaque : `vehicleNb` est le numero peint sur la
	// voiture. Les autres cles sont conservees par tolerance.
	numberKeys = []string{"vehiclenb", "carplate", "plate", "carnumber", "licenseplate", "licenceplate", "platenumber"}
	modelKeys  = []string{"model", "carmodel", "modelname", "carbrand", "brandmodel"}
	// `energyLevelPercentage` est la cle reelle (null sur les vehicules a
	// essence).
	energyKeys     = []string{"energylevelpercentage", "energylevel", "energy", "fuellevel", "batterylevel", "stateofcharge", "soc"}
	propulsionKeys = []string{"vehiclepropulsiontypeid", "propulsiontypeid"}
	latKeys        = []string{"latitude", "lat"}
	lonKeys        = []string{"longitude", "lon", "lng", "long"}
	// `vehicleLocation` est la cle reelle de la position imbriquee.
	nestedPosKey = []string{"vehiclelocation", "position", "location", "coordinates", "geoposition", "geo"}
	nameKeys     = []string{"name", "modelname", "label", "description", "value"}
)

// rawObject est un objet JSON dont les clés ont été mises en minuscules, ce
// qui rend la recherche insensible à la casse.
type rawObject map[string]json.RawMessage

func normalizeObject(b []byte) (rawObject, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("json invalide: %w", err)
	}
	out := make(rawObject, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out, nil
}

func (o rawObject) lookup(keys ...string) (json.RawMessage, bool) {
	for _, k := range keys {
		if v, ok := o[k]; ok && len(v) > 0 && string(v) != "null" {
			return v, true
		}
	}
	return nil, false
}

func (o rawObject) keyNames() string {
	names := make([]string, 0, len(o))
	for k := range o {
		names = append(names, k)
	}
	return strings.Join(names, ",")
}

// parseAvailability normalise la réponse brute de l'API.
func parseAvailability(body []byte) (*Availability, error) {
	root, err := normalizeObject(body)
	if err != nil {
		return nil, err
	}

	raw, ok := root.lookup(vehiclesKeys...)
	if !ok {
		// Dernier recours : le premier tableau trouvé dans l'objet racine.
		raw, ok = firstArray(root)
	}
	if !ok {
		return nil, fmt.Errorf("%w (cles vues: %s)", ErrSchemaDrift, root.keyNames())
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("liste de vehicules illisible: %w", err)
	}

	out := &Availability{Vehicles: make([]Vehicle, 0, len(items))}
	for _, item := range items {
		v, err := decodeVehicle(item)
		if err != nil {
			out.Skipped++
			continue
		}
		out.Vehicles = append(out.Vehicles, v)
	}

	// Une liste non vide dont *aucun* élément n'est décodable signifie que la
	// forme des véhicules a changé : on préfère une erreur bruyante à un
	// détecteur qui ne détecte plus rien.
	if len(items) > 0 && len(out.Vehicles) == 0 {
		return nil, fmt.Errorf("%w (%d vehicules recus, 0 decodable)", ErrSchemaDrift, len(items))
	}

	if v, ok := root.lookup(totalKeys...); ok {
		if n, err := decodeInt(v); err == nil {
			out.TotalNbVehicles = n
		}
	}
	if out.TotalNbVehicles == 0 {
		out.TotalNbVehicles = len(out.Vehicles)
	}

	return out, nil
}

func firstArray(o rawObject) (json.RawMessage, bool) {
	for _, v := range o {
		if t := strings.TrimLeft(string(v), " \t\r\n"); strings.HasPrefix(t, "[") {
			return v, true
		}
	}
	return nil, false
}

func decodeVehicle(item json.RawMessage) (Vehicle, error) {
	obj, err := normalizeObject(item)
	if err != nil {
		return Vehicle{}, err
	}

	v := Vehicle{EnergyLevel: -1}

	rawID, ok := obj.lookup(idKeys...)
	if !ok {
		return Vehicle{}, fmt.Errorf("vehicule sans identifiant (cles: %s)", obj.keyNames())
	}
	if v.ID, err = decodeString(rawID); err != nil || v.ID == "" {
		return Vehicle{}, fmt.Errorf("identifiant illisible: %s", string(rawID))
	}

	v.Position, err = decodePosition(obj)
	if err != nil {
		return Vehicle{}, err
	}

	if raw, ok := obj.lookup(numberKeys...); ok {
		v.Number, _ = decodeString(raw)
	}
	if raw, ok := obj.lookup(modelKeys...); ok {
		v.Model = decodeLabel(raw)
	}
	if raw, ok := obj.lookup(energyKeys...); ok {
		if n, err := decodeInt(raw); err == nil && n >= 0 && n <= 100 {
			v.EnergyLevel = n
		}
	}
	if raw, ok := obj.lookup(propulsionKeys...); ok {
		if n, err := decodeInt(raw); err == nil && n > 0 {
			v.PropulsionTypeID = n
		}
	}

	return v, nil
}

func decodePosition(obj rawObject) (geo.Point, error) {
	if lat, latOK := obj.lookup(latKeys...); latOK {
		if lon, lonOK := obj.lookup(lonKeys...); lonOK {
			return buildPoint(lat, lon)
		}
	}

	// Position imbriquée : {"position": {"latitude": ..., "longitude": ...}}
	if raw, ok := obj.lookup(nestedPosKey...); ok {
		nested, err := normalizeObject(raw)
		if err == nil {
			lat, latOK := nested.lookup(latKeys...)
			lon, lonOK := nested.lookup(lonKeys...)
			if latOK && lonOK {
				return buildPoint(lat, lon)
			}
		}
	}

	return geo.Point{}, fmt.Errorf("vehicule sans position (cles: %s)", obj.keyNames())
}

func buildPoint(rawLat, rawLon json.RawMessage) (geo.Point, error) {
	lat, err := decodeFloat(rawLat)
	if err != nil {
		return geo.Point{}, fmt.Errorf("latitude illisible: %w", err)
	}
	lon, err := decodeFloat(rawLon)
	if err != nil {
		return geo.Point{}, fmt.Errorf("longitude illisible: %w", err)
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return geo.Point{}, fmt.Errorf("position hors bornes: %v,%v", lat, lon)
	}
	// (0,0) est presque toujours un placeholder, jamais une voiture Flex.
	if lat == 0 && lon == 0 {
		return geo.Point{}, fmt.Errorf("position nulle (0,0) ignoree")
	}
	return geo.Point{Lat: lat, Lon: lon}, nil
}

// decodeString accepte une chaîne ou un nombre (les identifiants voyagent
// tantôt en int, tantôt en string selon les endpoints).
func decodeString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s), nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), nil
	}
	return "", fmt.Errorf("ni chaine ni nombre: %s", string(raw))
}

func decodeFloat(raw json.RawMessage) (float64, error) {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strconv.ParseFloat(strings.TrimSpace(s), 64)
	}
	return 0, fmt.Errorf("nombre attendu, recu %s", string(raw))
}

func decodeInt(raw json.RawMessage) (int, error) {
	f, err := decodeFloat(raw)
	if err != nil {
		return 0, err
	}
	return int(f), nil
}

// decodeLabel aplatit soit une chaîne, soit un objet {"name": "..."}.
func decodeLabel(raw json.RawMessage) string {
	if s, err := decodeString(raw); err == nil {
		return s
	}
	if obj, err := normalizeObject(raw); err == nil {
		if v, ok := obj.lookup(nameKeys...); ok {
			if s, err := decodeString(v); err == nil {
				return s
			}
		}
	}
	return ""
}
