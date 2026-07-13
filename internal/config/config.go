// Package config loads 12-factor UBIQO_* environment configuration.
// Every secret-bearing variable also accepts a UBIQO_X_FILE variant
// (docker-secrets style).
package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL string // UBIQO_DATABASE_URL (or _FILE)
	DataDir     string // UBIQO_DATA_DIR — bare git repos + spool live here
	Listen      string // UBIQO_LISTEN, default :8383
	PublicURL   string // UBIQO_PUBLIC_URL — canonical external URL (required unless --local)
	LogLevel    string // UBIQO_LOG_LEVEL: debug|info|warn|error
	Local       bool   // set by `serve --local`: relaxes PublicURL requirement
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key + "_FILE"); ok && v != "" {
		b, err := os.ReadFile(v)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL: env("UBIQO_DATABASE_URL", ""),
		DataDir:     env("UBIQO_DATA_DIR", "/var/lib/ubiqo"),
		Listen:      env("UBIQO_LISTEN", ":8383"),
		PublicURL:   strings.TrimRight(env("UBIQO_PUBLIC_URL", ""), "/"),
		LogLevel:    env("UBIQO_LOG_LEVEL", "info"),
	}
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("UBIQO_DATABASE_URL is required (e.g. postgres://ubiqo@localhost:5432/ubiqo)")
	}
	return c, nil
}

// Validate applies serve-time rules (fail fast at boot, log redacted config).
func (c *Config) Validate() error {
	if !c.Local && c.PublicURL == "" {
		return fmt.Errorf("UBIQO_PUBLIC_URL is required for non-local serving (or run `ubiqo serve --local` for a localhost eval)")
	}
	return nil
}

// Redacted returns a loggable view with credentials masked.
func (c *Config) Redacted() map[string]string {
	db := c.DatabaseURL
	if i := strings.Index(db, "@"); i > 0 {
		if j := strings.Index(db, "://"); j > 0 && j+3 < i {
			db = db[:j+3] + "***" + db[i:]
		}
	}
	return map[string]string{
		"database_url": db, "data_dir": c.DataDir, "listen": c.Listen,
		"public_url": c.PublicURL, "log_level": c.LogLevel,
	}
}
