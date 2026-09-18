// Package geo contient les primitives géographiques du détecteur : distance
// haversine et calcul de la bounding box envoyée à l'API Communauto.
package geo

import "math"

// earthRadiusKM est le rayon moyen de la Terre (IUGG mean radius).
const earthRadiusKM = 6371.0088

// kmPerDegreeLat est la longueur d'un degré de latitude, en km. La valeur est
// quasi constante ; la longitude, elle, se contracte avec cos(latitude).
const kmPerDegreeLat = 111.045

// Point est une position géographique en degrés décimaux.
type Point struct {
	Lat float64
	Lon float64
}

// BBox est une bounding box en degrés décimaux. L'API Communauto
// (FreeFloatingAvailability) ne sait filtrer que par rectangle, pas par rayon :
// on requête donc la bbox circonscrite puis on filtre au vrai cercle côté
// client avec DistanceKM.
type BBox struct {
	MinLat float64
	MaxLat float64
	MinLon float64
	MaxLon float64
}

// DistanceKM renvoie la distance orthodromique entre a et b, en kilomètres.
func DistanceKM(a, b Point) float64 {
	lat1 := radians(a.Lat)
	lat2 := radians(b.Lat)
	dLat := lat2 - lat1
	dLon := radians(b.Lon - a.Lon)

	sinLat := math.Sin(dLat / 2)
	sinLon := math.Sin(dLon / 2)
	h := sinLat*sinLat + math.Cos(lat1)*math.Cos(lat2)*sinLon*sinLon

	// math.Asin(math.Sqrt(h)) est plus stable que atan2 pour les petites
	// distances, qui sont notre cas d'usage (quelques km).
	return 2 * earthRadiusKM * math.Asin(math.Sqrt(math.Min(1, h)))
}

// BBoxAround renvoie une bounding box qui contient entièrement le cercle de
// centre c et de rayon radiusKM. Elle est volontairement un peu plus grande que
// le cercle : le surplus est éliminé ensuite par DistanceKM.
func BBoxAround(c Point, radiusKM float64) BBox {
	if radiusKM < 0 {
		radiusKM = 0
	}

	dLat := radiusKM / kmPerDegreeLat

	// Près des pôles cos(lat) tend vers 0 et le delta de longitude explose :
	// on retombe alors sur la plage complète plutôt que sur +Inf.
	cosLat := math.Cos(radians(c.Lat))
	dLon := 180.0
	if cosLat > 1e-9 {
		dLon = math.Min(180.0, radiusKM/(kmPerDegreeLat*cosLat))
	}

	return BBox{
		MinLat: clamp(c.Lat-dLat, -90, 90),
		MaxLat: clamp(c.Lat+dLat, -90, 90),
		MinLon: clamp(c.Lon-dLon, -180, 180),
		MaxLon: clamp(c.Lon+dLon, -180, 180),
	}
}

// Contains indique si p est dans la bbox (bornes incluses).
func (b BBox) Contains(p Point) bool {
	return p.Lat >= b.MinLat && p.Lat <= b.MaxLat &&
		p.Lon >= b.MinLon && p.Lon <= b.MaxLon
}

func radians(deg float64) float64 { return deg * math.Pi / 180 }

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
