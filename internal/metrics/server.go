package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ReadyFunc renvoie nil si l'application est prête à servir, une erreur
// explicative sinon.
type ReadyFunc func() error

// NewServer construit le serveur d'administration : /metrics pour Prometheus,
// /healthz (le process vit) et /readyz (le dernier poll est frais).
//
// Séparer liveness et readiness évite qu'un k8s redémarre en boucle un pod
// dont c'est l'API amont qui est en panne : /healthz reste vert, /readyz non.
func NewServer(addr string, m *Metrics, ready ReadyFunc) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if ready != nil {
			if err := ready(); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				// L'écriture peut échouer si le client a raccroché : il n'y a
				// rien de plus à faire, mais on ne masque pas l'intention.
				_, _ = fmt.Fprintf(w, "not ready: %v\n", err)
				return
			}
		}
		_, _ = w.Write([]byte("ready\n"))
	})

	return &http.Server{
		Addr:    addr,
		Handler: mux,
		// ReadHeaderTimeout borne les connexions lentes (Slowloris) : sans
		// lui, gosec le signale à juste titre.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// StaleAfter construit une ReadyFunc qui échoue si aucun poll n'a réussi
// depuis maxAge.
func (m *Metrics) StaleAfter(maxAge time.Duration) ReadyFunc {
	return func() error {
		ts := m.lastSuccessAt.Value()
		if ts == 0 {
			return fmt.Errorf("aucun poll reussi depuis le demarrage")
		}
		age := time.Since(time.Unix(0, int64(ts*1e9)))
		if age > maxAge {
			return fmt.Errorf("dernier poll reussi il y a %s (seuil %s)", age.Truncate(time.Second), maxAge)
		}
		return nil
	}
}

// Shutdown arrête proprement le serveur avec un délai de grâce.
func Shutdown(srv *http.Server, grace time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	return srv.Shutdown(ctx)
}
