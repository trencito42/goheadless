package config

import (
	"fmt"
	"os"
)

// Config holds process configuration sourced from environment variables.
type Config struct {
	HTTPAddr    string
	DatabaseURL string
	RedisURL    string // optional; empty disables Redis-backed caching.
	SessionTTL  string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    getEnv("CMS_HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("CMS_DATABASE_URL"),
		RedisURL:    os.Getenv("CMS_REDIS_URL"),
		SessionTTL:  getEnv("CMS_SESSION_TTL", "720h"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("CMS_DATABASE_URL is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
