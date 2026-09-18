package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() sur env vide doit reussir: %v", err)
	}
	if cfg.CityID != CityIDMontreal {
		t.Errorf("CityID = %d, attendu %d", cfg.CityID, CityIDMontreal)
	}
	if cfg.PollInterval < MinPollInterval {
		t.Errorf("PollInterval par defaut (%s) sous le plancher", cfg.PollInterval)
	}
	if cfg.TelegramEnabled() {
		t.Error("Telegram ne doit pas etre actif sans token/chat id")
	}
}

func TestLoadRejectsTooFastPolling(t *testing.T) {
	t.Setenv("FLEX_POLL_INTERVAL", "2s")

	_, err := Load()
	if err == nil {
		t.Fatal("un intervalle de 2s doit etre refuse")
	}
	if !strings.Contains(err.Error(), "FLEX_POLL_INTERVAL") {
		t.Errorf("l'erreur doit nommer la variable fautive, got %v", err)
	}
}

func TestLoadAggregatesErrors(t *testing.T) {
	t.Setenv("FLEX_RADIUS_KM", "0")
	t.Setenv("FLEX_CENTER_LAT", "120")
	t.Setenv("TELEGRAM_TOKEN", "abc")

	_, err := Load()
	if err == nil {
		t.Fatal("config invalide acceptee")
	}
	for _, want := range []string{"FLEX_RADIUS_KM", "FLEX_CENTER_LAT", "TELEGRAM_CHAT_ID"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur aggregee incomplete, %q manquant dans: %v", want, err)
		}
	}
}

func TestRedactedNeverLeaksSecrets(t *testing.T) {
	cfg := Config{TelegramToken: "123456:SUPER-SECRET", TelegramChatID: "42", DatabaseURL: "postgres://u:p@h/db"}
	for _, v := range cfg.Redacted() {
		if s, ok := v.(string); ok {
			if strings.Contains(s, "SUPER-SECRET") || strings.Contains(s, "p@h") {
				t.Fatalf("Redacted() fuit un secret: %q", s)
			}
		}
	}
}

// La règle est stricte en sortie de boucle locale : c'est ce qui permet
// d'autoriser http://127.0.0.1 pour les tests sans affaiblir la production.
func TestBaseURLScheme(t *testing.T) {
	tests := []struct {
		raw     string
		wantErr bool
	}{
		{"https://restapifrontoffice.reservauto.net/api/v2", false},
		{"https://api.telegram.org", false},
		{"http://127.0.0.1:18080/api/v2", false},
		{"http://localhost:18080", false},
		{"http://evil.example.com", true},
		{"http://169.254.169.254", true},
		{"ftp://example.com", true},
		{"pas-une-url", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			err := validateBaseURL("TEST_URL", tt.raw)
			if tt.wantErr && err == nil {
				t.Errorf("%q devrait etre refuse", tt.raw)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("%q devrait etre accepte: %v", tt.raw, err)
			}
		})
	}
}

func TestLoadRejectsPlainHTTPToInternet(t *testing.T) {
	t.Setenv("FLEX_API_BASE_URL", "http://restapifrontoffice.reservauto.net/api/v2")

	_, err := Load()
	if err == nil {
		t.Fatal("http vers Internet doit etre refuse")
	}
	if !strings.Contains(err.Error(), "FLEX_API_BASE_URL") {
		t.Errorf("l'erreur doit nommer la variable: %v", err)
	}
}

func TestLoadDefaultsTelegramBaseURL(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.TelegramAPIBaseURL != DefaultTelegramBaseURL {
		t.Errorf("TelegramAPIBaseURL = %q, attendu %q", cfg.TelegramAPIBaseURL, DefaultTelegramBaseURL)
	}
}

func TestLoadCustomValues(t *testing.T) {
	t.Setenv("FLEX_POLL_INTERVAL", "30s")
	t.Setenv("FLEX_HTTP_TIMEOUT", "5s")
	t.Setenv("FLEX_RADIUS_KM", "2.5")
	t.Setenv("FLEX_LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.PollInterval != 30*time.Second || cfg.RadiusKM != 2.5 {
		t.Errorf("valeurs mal lues: %+v", cfg)
	}
	if cfg.LogLevel.String() != "DEBUG" {
		t.Errorf("LogLevel = %s, attendu DEBUG", cfg.LogLevel)
	}
}
