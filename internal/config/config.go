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
	OIDCIssuers []string
	OIDCOrgs    []string
	OIDCEvents  []string
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
	if v := os.Getenv("BUILDHOST_OIDC_ORGS"); v != "" {
		for _, org := range strings.Split(v, ",") {
			if org = strings.TrimSpace(org); org != "" {
				c.OIDCOrgs = append(c.OIDCOrgs, org)
			}
		}
	}
	if v := os.Getenv("BUILDHOST_OIDC_EVENTS"); v != "" {
		for _, ev := range strings.Split(v, ",") {
			if ev = strings.TrimSpace(ev); ev != "" {
				c.OIDCEvents = append(c.OIDCEvents, ev)
			}
		}
	}
	if len(c.OIDCEvents) == 0 {
		c.OIDCEvents = []string{"push"}
	}
	return c
}
