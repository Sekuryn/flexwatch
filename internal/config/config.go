// Package config charge la configuration depuis l'environnement uniquement.
// Aucun fichier de config, aucun secret en dur : le binaire est le même en
// local et sur AWS, seules les variables changent (cf. PLAN.md phase 6).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// MinPollInterval est un plancher volontairement conservateur : l'API
// Communauto sert une réponse mise en cache ~5 s, poller plus vite ne donne
// aucune information neuve et ressemble à un abus côté serveur.
const MinPollInterval = 10 * time.Second

// DefaultAPIBaseURL est l'endpoint public (sans authentification) de
// disponibilité des véhicules en free-floating (Flex).
const DefaultAPIBaseURL = "https://restapifrontoffice.reservauto.net/api/v2"

// CityIDMontreal est l'identifiant de ville utilisé par l'API.
const CityIDMontreal = 59

// DefaultTelegramBaseURL est l'API Telegram publique.
const DefaultTelegramBaseURL = "https://api.telegram.org"

// Config est la configuration complète de l'application.
type Config struct {
	CityID       int
	Center       Center
	RadiusKM     float64
	PollInterval time.Duration
	HTTPTimeout  time.Duration
	// RealertAfter est le délai pendant lequel une voiture déjà signalée ne
	// sera pas re-signalée après avoir disparu (anti-flapping).
	RealertAfter time.Duration
	UserAgent    string
	APIBaseURL   string
	MetricsAddr  string
	LogLevel     slog.Level

	TelegramToken  string
	TelegramChatID string
	// TelegramAPIBaseURL n'existe que pour pointer vers un faux serveur lors
	// des tests de bout en bout (cf. test/e2e). En production on ne la définit
	// pas. Elle n'ouvre pas de risque nouveau : quiconque peut écrire dans
	// l'environnement du process y lit déjà TELEGRAM_TOKEN.
	TelegramAPIBaseURL string

	StoreEnabled bool
	DatabaseURL  string
}

// Center est le centre du géofence.
type Center struct {
	Lat float64
	Lon float64
}

// TelegramEnabled indique si le sink Telegram doit être branché.
func (c Config) TelegramEnabled() bool {
	return c.TelegramToken != "" && c.TelegramChatID != ""
}

// Redacted renvoie une vue loggable de la config : les secrets n'y figurent
// jamais, seulement leur présence.
func (c Config) Redacted() []any {
	return []any{
		"city_id", c.CityID,
		"center_lat", c.Center.Lat,
		"center_lon", c.Center.Lon,
		"radius_km", c.RadiusKM,
		"poll_interval", c.PollInterval.String(),
		"http_timeout", c.HTTPTimeout.String(),
		"realert_after", c.RealertAfter.String(),
		"api_base_url", c.APIBaseURL,
		"metrics_addr", c.MetricsAddr,
		"telegram_enabled", c.TelegramEnabled(),
		"telegram_api_base_url", c.TelegramAPIBaseURL,
		"store_enabled", c.StoreEnabled,
	}
}

// Load lit la configuration depuis l'environnement et la valide. Les erreurs
// sont agrégées pour qu'un déploiement mal configuré les montre toutes d'un
// coup au lieu d'échouer une variable à la fois.
func Load() (Config, error) {
	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	cfg := Config{
		APIBaseURL: envString("FLEX_API_BASE_URL", DefaultAPIBaseURL),
		UserAgent: envString("FLEX_USER_AGENT",
			"flexwatch/1.0 (+https://github.com/Fougere/flexwatch; portfolio DevSecOps, read-only)"),
		MetricsAddr: envString("FLEX_METRICS_ADDR", ":2112"),

		TelegramToken:      strings.TrimSpace(os.Getenv("TELEGRAM_TOKEN")),
		TelegramChatID:     strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		TelegramAPIBaseURL: envString("TELEGRAM_API_BASE_URL", DefaultTelegramBaseURL),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
	}

	var err error
	if cfg.CityID, err = envInt("FLEX_CITY_ID", CityIDMontreal); err != nil {
		collect(err)
	}
	if cfg.Center.Lat, err = envFloat("FLEX_CENTER_LAT", 45.5017); err != nil {
		collect(err)
	}
	if cfg.Center.Lon, err = envFloat("FLEX_CENTER_LON", -73.5673); err != nil {
		collect(err)
	}
	if cfg.RadiusKM, err = envFloat("FLEX_RADIUS_KM", 1.5); err != nil {
		collect(err)
	}
	if cfg.PollInterval, err = envDuration("FLEX_POLL_INTERVAL", 20*time.Second); err != nil {
		collect(err)
	}
	if cfg.HTTPTimeout, err = envDuration("FLEX_HTTP_TIMEOUT", 10*time.Second); err != nil {
		collect(err)
	}
	if cfg.RealertAfter, err = envDuration("FLEX_REALERT_AFTER", 10*time.Minute); err != nil {
		collect(err)
	}
	if cfg.LogLevel, err = envLogLevel("FLEX_LOG_LEVEL", slog.LevelInfo); err != nil {
		collect(err)
	}
	if cfg.StoreEnabled, err = envBool("FLEX_STORE_ENABLED", false); err != nil {
		collect(err)
	}

	collect(cfg.validate())

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error

	if c.Center.Lat < -90 || c.Center.Lat > 90 {
		errs = append(errs, fmt.Errorf("FLEX_CENTER_LAT hors bornes: %v", c.Center.Lat))
	}
	if c.Center.Lon < -180 || c.Center.Lon > 180 {
		errs = append(errs, fmt.Errorf("FLEX_CENTER_LON hors bornes: %v", c.Center.Lon))
	}
	if c.RadiusKM <= 0 || c.RadiusKM > 50 {
		errs = append(errs, fmt.Errorf("FLEX_RADIUS_KM doit etre dans ]0, 50]: %v", c.RadiusKM))
	}
	if c.PollInterval < MinPollInterval {
		errs = append(errs, fmt.Errorf(
			"FLEX_POLL_INTERVAL=%s est sous le plancher de %s (cache serveur ~5s, polling respectueux)",
			c.PollInterval, MinPollInterval))
	}
	if c.HTTPTimeout <= 0 || c.HTTPTimeout > c.PollInterval {
		errs = append(errs, fmt.Errorf(
			"FLEX_HTTP_TIMEOUT=%s doit etre > 0 et <= FLEX_POLL_INTERVAL (%s)",
			c.HTTPTimeout, c.PollInterval))
	}
	if c.RealertAfter < 0 {
		errs = append(errs, fmt.Errorf("FLEX_REALERT_AFTER ne peut pas etre negatif: %s", c.RealertAfter))
	}
	if c.UserAgent == "" {
		errs = append(errs, errors.New("FLEX_USER_AGENT ne peut pas etre vide: on s'identifie honnetement"))
	}
	if err := validateBaseURL("FLEX_API_BASE_URL", c.APIBaseURL); err != nil {
		errs = append(errs, err)
	}
	if err := validateBaseURL("TELEGRAM_API_BASE_URL", c.TelegramAPIBaseURL); err != nil {
		errs = append(errs, err)
	}
	// Un demi-secret est presque toujours une erreur de déploiement.
	if (c.TelegramToken == "") != (c.TelegramChatID == "") {
		errs = append(errs, errors.New("TELEGRAM_TOKEN et TELEGRAM_CHAT_ID doivent etre fournis ensemble"))
	}
	if c.StoreEnabled && c.DatabaseURL == "" {
		errs = append(errs, errors.New("FLEX_STORE_ENABLED=true exige DATABASE_URL"))
	}

	return errors.Join(errs...)
}

// validateBaseURL impose https, avec UNE exception : http est toléré vers la
// boucle locale, ce qui permet les tests de bout en bout contre un faux serveur
// (cf. test/e2e) sans jamais affaiblir le chemin de production. Un token ou des
// positions ne partiront donc jamais en clair sur un réseau.
func validateBaseURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: URL invalide %q", name, raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%s: URL sans hote %q", name, raw)
	}

	switch u.Scheme {
	case "https":
		return nil
	case "http":
		switch u.Hostname() {
		case "127.0.0.1", "localhost", "::1":
			return nil
		}
		return fmt.Errorf("%s: http interdit vers %q (https exige hors boucle locale)", name, u.Hostname())
	default:
		return fmt.Errorf("%s: schema %q non supporte", name, u.Scheme)
	}
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: entier invalide %q", key, raw)
	}
	return v, nil
}

func envFloat(key string, def float64) (float64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("%s: nombre invalide %q", key, raw)
	}
	return v, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: duree invalide %q (attendu p.ex. 20s, 2m)", key, raw)
	}
	return v, nil
}

func envBool(key string, def bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s: booleen invalide %q", key, raw)
	}
	return v, nil
}

func envLogLevel(key string, def slog.Level) (slog.Level, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def, nil
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.TrimSpace(raw))); err != nil {
		return 0, fmt.Errorf("%s: niveau invalide %q (debug|info|warn|error)", key, raw)
	}
	return lvl, nil
}
