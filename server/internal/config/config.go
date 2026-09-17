// Package config loads server configuration from environment variables.
//
// Every third-party API key is intentionally left empty by default. Fill them
// in via the environment (see .env.example) before running the analysis
// pipeline against real providers.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr       string
	DataDir    string
	SessionTTL time.Duration

	// Pinterest scraping.
	PinterestBaseURL string
	ImportPinLimit   int
	ImportWorkers    int

	// Analysis pipeline.
	GeminiAPIKey string
	GeminiModel  string
	// GeminiFallbackModels are tried in order when the primary model is
	// overloaded or unavailable to the key.
	GeminiFallbackModels []string
	ImgbbAPIKey          string

	// LensProvider selects the reverse image search backend:
	// "serpapi", "scrapingdog" or "searchapi".
	LensProvider      string
	SerpAPIKey        string
	ScrapingdogAPIKey string
	SearchAPIKey      string

	// DemoAnalysis swaps the paid providers for deterministic placeholder
	// output so the app can be exercised without API keys.
	DemoAnalysis bool
}

func Load() Config {
	return Config{
		Addr:       env("FITKIT_ADDR", ":8080"),
		DataDir:    env("FITKIT_DATA_DIR", "./data"),
		SessionTTL: time.Duration(envInt("FITKIT_SESSION_TTL_DAYS", 60)) * 24 * time.Hour,

		PinterestBaseURL: env("FITKIT_PINTEREST_BASE_URL", "https://www.pinterest.com"),
		ImportPinLimit:   envInt("FITKIT_IMPORT_PIN_LIMIT", 250),
		ImportWorkers:    envInt("FITKIT_IMPORT_WORKERS", 2),

		GeminiAPIKey:         env("GEMINI_API_KEY", ""),
		GeminiModel:          env("GEMINI_MODEL", "gemini-3.6-flash"),
		GeminiFallbackModels: envList("GEMINI_FALLBACK_MODELS", "gemini-3.5-flash,gemini-2.5-flash"),
		ImgbbAPIKey:          env("IMGBB_API_KEY", ""),

		LensProvider:      env("LENS_PROVIDER", "searchapi"),
		SerpAPIKey:        env("SERPAPI_API_KEY", ""),
		ScrapingdogAPIKey: env("SCRAPINGDOG_API_KEY", ""),
		SearchAPIKey:      env("SEARCHAPI_API_KEY", ""),

		DemoAnalysis: envBool("FITKIT_DEMO_ANALYSIS", false),
	}
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// envList reads a comma-separated list, trimming blanks.
func envList(key, fallback string) []string {
	var out []string
	for _, part := range strings.Split(env(key, fallback), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}
