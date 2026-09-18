//go:build !postgres

package store

import "context"

// Open renvoie toujours ErrNotCompiled dans le build par défaut : la
// persistance n'est pas embarquée. Voir store_postgres.go.
func Open(_ context.Context, _ string) (Store, error) {
	return nil, ErrNotCompiled
}
