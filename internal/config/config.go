package config

import "os"

type Config struct {
	ListenAddr      string
	AdminListenAddr string
	DataDir         string
	DBPath          string
	BaseURL         string
}

func Load() Config {
	c := Config{
		ListenAddr:      ":8080",
		AdminListenAddr: "127.0.0.1:9090",
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
	return c
}
