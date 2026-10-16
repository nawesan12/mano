// Package google renueva el acceso a las APIs de Google con el refresh token del dueño.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Auth guarda el access token y lo renueva un minuto antes de que venza.
type Auth struct {
	ClientID, ClientSecret, RefreshToken string
	TokenURL                             string // configurable para tests
	HC                                   *http.Client

	mu      sync.Mutex
	access  string
	expires time.Time
}

func NewAuth(clientID, secret, refresh string) *Auth {
	return &Auth{ClientID: clientID, ClientSecret: secret, RefreshToken: refresh, TokenURL: "https://oauth2.googleapis.com/token",
		HC: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Auth) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.access != "" && time.Now().Before(a.expires) {
		return a.access, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {a.RefreshToken}, "client_id": {a.ClientID}, "client_secret": {a.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := a.HC.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error_description"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode >= 300 || out.AccessToken == "" {
		return "", fmt.Errorf("no pude renovar el acceso a Google: %d %s", res.StatusCode, out.Error)
	}
	a.access = out.AccessToken
	a.expires = time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second)
	return a.access, nil
}
