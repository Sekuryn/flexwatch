//go:build postgres

// Ce fichier n'est compilé qu'avec `-tags postgres`. Il ajoute la seule
// dépendance tierce du projet :
//
//	go get github.com/jackc/pgx/v5@v5.7.2
//
// Le tag et la version épinglée sont volontaires : le build par défaut reste
// à zéro dépendance, et l'ajout de pgx est un choix explicite et traçable.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Sekuryn/flexwatch/internal/geo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schema est idempotent : appliqué à chaque démarrage. Pour un vrai service on
// sortirait ça dans des migrations versionnées (cf. PLAN.md phase 7).
const schema = `
CREATE TABLE IF NOT EXISTS vehicle_snapshot (
    id            BIGSERIAL PRIMARY KEY,
    observed_at   TIMESTAMPTZ NOT NULL,
    city_id       INTEGER     NOT NULL,
    center_lat    DOUBLE PRECISION NOT NULL,
    center_lon    DOUBLE PRECISION NOT NULL,
    radius_km     DOUBLE PRECISION NOT NULL,
    total_in_city INTEGER     NOT NULL,
    nb_in_fence   INTEGER     NOT NULL
);

CREATE TABLE IF NOT EXISTS vehicle_position (
    snapshot_id  BIGINT NOT NULL REFERENCES vehicle_snapshot(id) ON DELETE CASCADE,
    vehicle_id   TEXT   NOT NULL,
    vehicle_nb   TEXT,
    model        TEXT,
    energy_level INTEGER,
    latitude     DOUBLE PRECISION NOT NULL,
    longitude    DOUBLE PRECISION NOT NULL,
    distance_m   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_snapshot_observed_at ON vehicle_snapshot (observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_position_snapshot ON vehicle_position (snapshot_id);
CREATE INDEX IF NOT EXISTS idx_position_vehicle ON vehicle_position (vehicle_id);
`

// Postgres persiste les snapshots dans PostgreSQL via pgx.
type Postgres struct {
	pool *pgxpool.Pool
}

// Open ouvre le pool, vérifie la connexion et applique le schéma.
func Open(ctx context.Context, dsn string) (Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// On n'inclut jamais le DSN : il contient le mot de passe.
		return nil, fmt.Errorf("store: DSN invalide")
	}
	// Un bot qui poll toutes les 20 s n'a pas besoin de 20 connexions.
	cfg.MaxConns = 4
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: ouverture du pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: base injoignable: %w", err)
	}

	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: application du schema: %w", err)
	}

	return &Postgres{pool: pool}, nil
}

// SaveSnapshot écrit un snapshot et ses positions dans une seule transaction.
func (p *Postgres) SaveSnapshot(ctx context.Context, s Snapshot) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() {
		// Rollback est un no-op si la transaction est déjà committée.
		_ = tx.Rollback(ctx)
	}()

	var snapshotID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO vehicle_snapshot
			(observed_at, city_id, center_lat, center_lon, radius_km, total_in_city, nb_in_fence)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		s.ObservedAt, s.CityID, s.Center.Lat, s.Center.Lon, s.RadiusKM,
		s.TotalInCity, len(s.InFence),
	).Scan(&snapshotID)
	if err != nil {
		return fmt.Errorf("store: insert snapshot: %w", err)
	}

	if len(s.InFence) > 0 {
		rows := make([][]any, 0, len(s.InFence))
		for _, v := range s.InFence {
			distanceM := int(geo.DistanceKM(s.Center, v.Position) * 1000)
			rows = append(rows, []any{
				snapshotID, v.ID, nullable(v.Number), nullable(v.Model),
				nullableEnergy(v.EnergyLevel), v.Position.Lat, v.Position.Lon, distanceM,
			})
		}
		_, err = tx.CopyFrom(ctx,
			pgx.Identifier{"vehicle_position"},
			[]string{"snapshot_id", "vehicle_id", "vehicle_nb", "model", "energy_level", "latitude", "longitude", "distance_m"},
			pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("store: copy positions: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Close ferme le pool.
func (p *Postgres) Close() error {
	p.pool.Close()
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableEnergy(level int) any {
	if level < 0 {
		return nil
	}
	return level
}
