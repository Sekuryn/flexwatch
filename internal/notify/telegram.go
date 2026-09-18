package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// telegramMaxMessage est la limite dure de l'API Telegram.
const telegramMaxMessage = 4096

// Telegram envoie les alertes dans un chat Telegram.
//
// Le token ne transite qu'ici, dans l'URL, et n'apparaît JAMAIS dans une
// erreur ni dans un log : c'est pour ça que les erreurs ci-dessous sont
// reconstruites à la main au lieu d'emballer l'erreur de net/http, qui
// contiendrait l'URL complète — donc le token (cf. PLAN.md phase 6).
type Telegram struct {
	token  string
	chatID string
	apiURL string
	client *http.Client
}

// NewTelegram construit le sink Telegram. apiBase vide vaut api.telegram.org.
func NewTelegram(token, chatID, apiBase string, timeout time.Duration) *Telegram {
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}
	return &Telegram{
		token:  token,
		chatID: chatID,
		apiURL: strings.TrimRight(apiBase, "/"),
		client: &http.Client{Timeout: timeout},
	}
}

// Name implémente Notifier.
func (t *Telegram) Name() string { return "telegram" }

type telegramRequest struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
}

// Notify implémente Notifier.
func (t *Telegram) Notify(ctx context.Context, n Notification) error {
	text := n.Text()
	if len(text) > telegramMaxMessage {
		// Couper sur une frontière d'octet est suffisant ici (texte ASCII),
		// mais on garde une marge pour le suffixe.
		text = text[:telegramMaxMessage-20] + "\n... (tronque)"
	}

	// Pas de parse_mode : en texte brut, aucune plaque ni aucun modèle de
	// voiture ne peut casser le rendu ou injecter du markup.
	payload, err := json.Marshal(telegramRequest{
		ChatID:                t.chatID,
		Text:                  text,
		DisableWebPagePreview: true,
	})
	if err != nil {
		return fmt.Errorf("encodage payload: %w", err)
	}

	endpoint := t.apiURL + "/bot" + t.token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("construction requete telegram")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// On ne rend PAS err tel quel : il contient l'URL, donc le token.
		return fmt.Errorf("appel telegram: %s", classifyTransportError(err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("lecture reponse telegram: status %d", resp.StatusCode)
	}

	var tr telegramResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("reponse telegram illisible: status %d", resp.StatusCode)
	}
	if !tr.OK {
		return fmt.Errorf("telegram a refuse le message: status %d, code %d: %s",
			resp.StatusCode, tr.ErrorCode, tr.Description)
	}
	return nil
}

// classifyTransportError réduit une erreur de transport à une catégorie sans
// données sensibles.
func classifyTransportError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context canceled"):
		return "annule"
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "Client.Timeout"):
		return "timeout"
	case strings.Contains(msg, "no such host"):
		return "resolution dns impossible"
	case strings.Contains(msg, "connection refused"):
		return "connexion refusee"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls"):
		return "erreur tls"
	default:
		return "erreur reseau"
	}
}
