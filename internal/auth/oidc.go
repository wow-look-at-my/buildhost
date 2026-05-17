package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type OIDCConfig struct {
	Issuer   string
	Audience string
}

type jwks struct {
	Keys []json.RawMessage `json:"keys"`
}

func FetchJWKS(issuer string) (*jwks, error) {
	url := issuer + "/.well-known/jwks"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var keys jwks
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	return &keys, nil
}
