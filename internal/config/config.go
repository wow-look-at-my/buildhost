package config

import (
	"os"
	"strings"
)

type Config struct {
	ListenAddr      string
	AdminListenAddr string
	DataDir         string
	DBPath          string
	BaseURL         string
	OIDCIssuers     []string
}

func Load() Config {
	c := Config{
		ListenAddr:      ":8080",
		AdminListenAddr: ":9090",
		DataDir:         "./data",
		DBPath:          "./data/buildhost.db",
		BaseURL:         "http://localhost:8080",
	}
	if v := os.Getenv("BUILDHOST_LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("BUILDHOST_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("BUILDHOST_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("BUILDHOST_ADMIN_LISTEN_ADDR"); v != "" {
		c.AdminListenAddr = v
	}
	if v := os.Getenv("BUILDHOST_BASE_URL"); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv("BUILDHOST_OIDC_ISSUERS"); v != "" {
		for _, iss := range strings.Split(v, ",") {
			if iss = strings.TrimSpace(iss); iss != "" {
				c.OIDCIssuers = append(c.OIDCIssuers, iss)
			}
		}
	}
	return c
}
