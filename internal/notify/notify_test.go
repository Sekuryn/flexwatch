package notify

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sekuryn/flexwatch/internal/communauto"
	"github.com/Sekuryn/flexwatch/internal/geo"
)

var sample = Notification{
	At:       time.Date(2026, 9, 17, 18, 30, 0, 0, time.UTC),
	Center:   geo.Point{Lat: 45.5017, Lon: -73.5673},
	RadiusKM: 1.5,
	Present:  2,
	New: []communauto.Vehicle{
		{ID: "90210", Number: "8129", Model: "Prius C", EnergyLevel: 74,
			Position: geo.Point{Lat: 45.5050, Lon: -73.5700}},
	},
}

func TestNotificationText(t *testing.T) {
	got := sample.Text()
	for _, want := range []string{"1 Flex disponible", "Flex 8129", "74%", "google.com/maps", "2026-09-17"} {
		if !strings.Contains(got, want) {
			t.Errorf("texte de notification sans %q:\n%s", want, got)
		}
	}
}

func TestConsoleNotify(t *testing.T) {
	var buf bytes.Buffer
	c := NewConsole(&buf)

	if err := c.Notify(context.Background(), sample); err != nil {
		t.Fatalf("Notify() = %v", err)
	}
	if !strings.Contains(buf.String(), "Flex 8129") {
		t.Errorf("sortie console inattendue: %q", buf.String())
	}
	if c.Name() != "console" {
		t.Errorf("Name() = %q", c.Name())
	}
}

type failingSink struct{ name string }

func (f failingSink) Name() string { return f.name }
func (f failingSink) Notify(context.Context, Notification) error {
	return errors.New("boom")
}

// Un sink cassé ne doit pas empêcher les autres de recevoir l'alerte.
func TestMultiIsolatesFailures(t *testing.T) {
	var buf bytes.Buffer
	results := map[string]bool{}

	m := &Multi{
		Sinks: []Notifier{failingSink{name: "telegram"}, NewConsole(&buf)},
		OnResult: func(sink string, err error) {
			results[sink] = err == nil
		},
	}

	err := m.Notify(context.Background(), sample)
	if err == nil {
		t.Error("l'erreur du sink en panne doit etre remontee")
	}
	if !strings.Contains(err.Error(), "telegram") {
		t.Errorf("l'erreur doit nommer le sink fautif: %v", err)
	}
	if !strings.Contains(buf.String(), "Flex 8129") {
		t.Error("la console n'a pas recu la notification malgre l'echec du premier sink")
	}
	if results["telegram"] || !results["console"] {
		t.Errorf("OnResult mal appele: %v", results)
	}
}

func TestTelegramSendsPlainTextPayload(t *testing.T) {
	const token = "123456:AA-SUPER-SECRET-TOKEN"
	var gotPath, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := readAll(r)
		gotBody = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	tg := NewTelegram(token, "42", srv.URL, 2*time.Second)
	if err := tg.Notify(context.Background(), sample); err != nil {
		t.Fatalf("Notify() = %v", err)
	}
	if gotPath != "/bot"+token+"/sendMessage" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{`"chat_id":"42"`, "Flex 8129", `"disable_web_page_preview":true`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("payload sans %q: %s", want, gotBody)
		}
	}
	if strings.Contains(gotBody, "parse_mode") {
		t.Error("pas de parse_mode attendu: le texte brut evite toute injection de markup")
	}
}

// Test de sécurité : le token ne doit jamais fuiter dans une erreur, sinon il
// finit dans les logs, puis dans Loki, puis dans un dashboard partagé.
func TestTelegramErrorsNeverLeakToken(t *testing.T) {
	const token = "123456:AA-SUPER-SECRET-TOKEN"

	t.Run("refus applicatif", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"bot was blocked by the user"}`))
		}))
		defer srv.Close()

		err := NewTelegram(token, "42", srv.URL, 2*time.Second).Notify(context.Background(), sample)
		assertNoToken(t, err, token)
	})

	t.Run("panne transport", func(t *testing.T) {
		// Port fermé : provoque une erreur de transport contenant l'URL.
		err := NewTelegram(token, "42", "http://127.0.0.1:1", 500*time.Millisecond).
			Notify(context.Background(), sample)
		assertNoToken(t, err, token)
	})

	t.Run("reponse illisible", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html>nope</html>`))
		}))
		defer srv.Close()

		err := NewTelegram(token, "42", srv.URL, 2*time.Second).Notify(context.Background(), sample)
		assertNoToken(t, err, token)
	})
}

func assertNoToken(t *testing.T, err error, token string) {
	t.Helper()
	if err == nil {
		t.Fatal("erreur attendue")
	}
	msg := err.Error()
	if strings.Contains(msg, token) || strings.Contains(msg, "SUPER-SECRET") {
		t.Fatalf("FUITE DE SECRET dans l'erreur: %q", msg)
	}
}

func TestTelegramTruncatesLongMessages(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAll(r)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	big := sample
	for i := 0; i < 200; i++ {
		big.New = append(big.New, communauto.Vehicle{
			ID: strings.Repeat("9", 8), Number: "6106", Model: "Modele tres tres long",
			EnergyLevel: 50, Position: geo.Point{Lat: 45.5, Lon: -73.6},
		})
	}

	if err := NewTelegram("t", "42", srv.URL, 2*time.Second).Notify(context.Background(), big); err != nil {
		t.Fatalf("Notify() = %v", err)
	}
	if len(gotBody) > telegramMaxMessage+512 {
		t.Errorf("payload non tronque: %d octets", len(gotBody))
	}
	if !strings.Contains(gotBody, "tronque") {
		t.Error("le message tronque doit le dire")
	}
}

func readAll(r *http.Request) (string, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.String(), err
}
