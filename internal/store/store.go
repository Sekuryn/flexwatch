// Package store persiste les snapshots de disponibilité, pour un historique
// et une heatmap ultérieure (cf. PLAN.md phase 7).
//
// La persistance est OPTIONNELLE et compilée séparément : le binaire par
// défaut n'embarque aucun driver base de données, donc aucune dépendance
// tierce. `go build -tags postgres` active l'implémentation pgx.
// Moins de code dans l'image par défaut = moins de surface d'attaque et un
// SBOM plus court à auditer.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/Sekuryn/flexwatch/internal/communauto"
	"github.com/Sekuryn/flexwatch/internal/geo"
)

// ErrNotCompiled est renvoyée quand la persistance est demandée alors que le
// binaire a été construit sans le tag `postgres`.
var ErrNotCompiled = errors.New("store: support postgres non compile (rebuild avec -tags postgres)")

// Snapshot est l'état observé à un instant donné.
type Snapshot struct {
	ObservedAt  time.Time
	CityID      int
	Center      geo.Point
	RadiusKM    float64
	TotalInCity int
	InFence     []communauto.Vehicle
}

// Store persiste les snapshots.
type Store interface {
	SaveSnapshot(ctx context.Context, s Snapshot) error
	Close() error
}

// Noop est l'implémentation par défaut : elle ne persiste rien. Elle évite
// des `if store != nil` partout dans la boucle de polling.
type Noop struct{}

// SaveSnapshot implémente Store.
func (Noop) SaveSnapshot(context.Context, Snapshot) error { return nil }

// Close implémente Store.
func (Noop) Close() error { return nil }
