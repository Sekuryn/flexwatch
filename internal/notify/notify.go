// Package notify envoie les alertes. C'est la frontière « notify-first » du
// projet : on informe un humain, on ne réserve jamais rien à sa place. Aucun
// sink n'a le droit d'appeler une API Communauto authentifiée.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Fougere/flexwatch/internal/communauto"
	"github.com/Fougere/flexwatch/internal/geo"
)

// Notification est un événement à annoncer.
type Notification struct {
	At       time.Time
	New      []communauto.Vehicle
	Gone     []string
	Present  int
	Center   geo.Point
	RadiusKM float64
}

// Notifier est un canal de sortie (console, Telegram, ...).
type Notifier interface {
	// Name identifie le sink dans les logs et les métriques.
	Name() string
	// Notify envoie la notification. L'implémentation doit respecter ctx.
	Notify(ctx context.Context, n Notification) error
}

// Text rend la notification en texte brut, lisible en console comme dans un
// message Telegram.
func (n Notification) Text() string {
	var b strings.Builder

	plural := ""
	if len(n.New) > 1 {
		plural = "s"
	}
	fmt.Fprintf(&b, "%d Flex disponible%s dans %.1f km de %.4f,%.4f\n",
		len(n.New), plural, n.RadiusKM, n.Center.Lat, n.Center.Lon)

	for _, v := range n.New {
		dist := geo.DistanceKM(n.Center, v.Position)
		fmt.Fprintf(&b, "- %s a %.0f m -> %s\n", v.Label(), dist*1000, v.MapsURL())
	}

	fmt.Fprintf(&b, "(%d dans le rayon, %s)\n", n.Present, n.At.Format(time.RFC3339))
	return b.String()
}

// Console écrit les notifications sur un io.Writer (stdout par défaut).
// C'est le sink toujours actif : même sans Telegram, le bot est utilisable.
type Console struct {
	mu sync.Mutex
	w  io.Writer
}

// NewConsole construit le sink console.
func NewConsole(w io.Writer) *Console { return &Console{w: w} }

// Name implémente Notifier.
func (c *Console) Name() string { return "console" }

// Notify implémente Notifier.
func (c *Console) Notify(_ context.Context, n Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := io.WriteString(c.w, n.Text())
	return err
}

// Multi diffuse vers plusieurs sinks. Un sink en panne ne doit jamais empêcher
// les autres d'être servis : les erreurs sont collectées, pas propagées tout
// de suite.
type Multi struct {
	Sinks []Notifier
	// OnResult, si fourni, est appelé après chaque sink (pour les métriques).
	OnResult func(sink string, err error)
}

// Name implémente Notifier.
func (m *Multi) Name() string { return "multi" }

// Notify implémente Notifier.
func (m *Multi) Notify(ctx context.Context, n Notification) error {
	var errs []error
	for _, s := range m.Sinks {
		err := s.Notify(ctx, n)
		if m.OnResult != nil {
			m.OnResult(s.Name(), err)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
		}
	}
	return errors.Join(errs...)
}
