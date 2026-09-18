// Package watch contient la boucle de polling et la détection d'apparitions.
package watch

import (
	"sort"
	"time"

	"github.com/Sekuryn/flexwatch/internal/communauto"
)

// Diff est le résultat de la comparaison entre deux polls successifs.
type Diff struct {
	// New sont les véhicules à signaler : nouvellement présents dans le
	// géofence et pas déjà annoncés récemment.
	New []communauto.Vehicle
	// Gone sont les identifiants présents au poll précédent et plus maintenant.
	Gone []string
	// Present est le nombre de véhicules actuellement dans le géofence.
	Present int
	// FirstRun est vrai au tout premier poll : l'état initial n'est pas une
	// série d'apparitions, on ne veut pas notifier 12 voitures au démarrage.
	FirstRun bool
}

// HasAlerts indique s'il y a matière à notifier.
func (d Diff) HasAlerts() bool { return len(d.New) > 0 }

// Detector garde l'état entre deux polls et décide ce qui est nouveau.
//
// Deux mémoires distinctes :
//   - present : qui était là au dernier poll, pour détecter les disparitions ;
//   - lastAlerted : quand un véhicule a été signalé pour la dernière fois,
//     pour ne pas ré-alerter en boucle. Le cache serveur (~5 s) et la dérive
//     GPS font disparaître puis réapparaître une voiture en bord de rayon :
//     sans ce garde-fou, le bot spammerait.
type Detector struct {
	realertAfter time.Duration
	present      map[string]struct{}
	lastAlerted  map[string]time.Time
	started      bool
}

// NewDetector crée un détecteur. Un realertAfter <= 0 signifie : ne jamais
// ré-alerter pour un véhicule déjà vu.
func NewDetector(realertAfter time.Duration) *Detector {
	return &Detector{
		realertAfter: realertAfter,
		present:      make(map[string]struct{}),
		lastAlerted:  make(map[string]time.Time),
	}
}

// Update intègre le résultat d'un poll (déjà filtré au vrai rayon) et renvoie
// les changements. Les doublons d'identifiant sont dédupliqués.
func (d *Detector) Update(now time.Time, inFence []communauto.Vehicle) Diff {
	diff := Diff{FirstRun: !d.started}
	current := make(map[string]struct{}, len(inFence))

	for _, v := range inFence {
		if v.ID == "" {
			continue
		}
		if _, dup := current[v.ID]; dup {
			continue
		}
		current[v.ID] = struct{}{}

		if d.shouldAlert(now, v.ID) {
			diff.New = append(diff.New, v)
			d.lastAlerted[v.ID] = now
		}
	}

	for id := range d.present {
		if _, still := current[id]; !still {
			diff.Gone = append(diff.Gone, id)
		}
	}

	// Ordre stable : des notifications et des logs reproductibles sont plus
	// faciles à tester et à relire.
	sort.Slice(diff.New, func(i, j int) bool { return diff.New[i].ID < diff.New[j].ID })
	sort.Strings(diff.Gone)

	diff.Present = len(current)
	d.present = current
	d.started = true

	// Au premier poll on enregistre l'état mais on ne notifie pas.
	if diff.FirstRun {
		diff.New = nil
	}

	d.prune(now)
	return diff
}

func (d *Detector) shouldAlert(now time.Time, id string) bool {
	// Déjà présent au poll précédent : ce n'est pas une apparition.
	if _, was := d.present[id]; was {
		return false
	}
	last, seen := d.lastAlerted[id]
	if !seen {
		return true
	}
	if d.realertAfter <= 0 {
		return false
	}
	return now.Sub(last) >= d.realertAfter
}

// prune borne la mémoire : un process qui tourne des mois ne doit pas
// accumuler indéfiniment les identifiants de toute la flotte.
func (d *Detector) prune(now time.Time) {
	if d.realertAfter <= 0 {
		return
	}
	ttl := 2 * d.realertAfter
	for id, last := range d.lastAlerted {
		if _, still := d.present[id]; still {
			continue
		}
		if now.Sub(last) > ttl {
			delete(d.lastAlerted, id)
		}
	}
}
