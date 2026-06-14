package auth

import (
	"encoding/base64"
	"net/http"
	"strings"
)

func ExtractToken(r *http.Request) string {
	if t := extractBearer(r); t != "" {
		return t
	}
	if t := extractBasicAuth(r); t != "" {
		return t
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return ""
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return h[7:]
	}
	return ""
}

func extractBasicAuth(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Basic ") {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(h[6:])
	if err != nil {
		return ""
	}
	_, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return ""
	}
	return password
}
