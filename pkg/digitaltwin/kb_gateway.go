package digitaltwin

import (
	"context"
	"net/http"
	"os"
)

// Knowledge base endpoints exposed by Orange.
//
// The client-facing knowledge APIs (space list / wiki index / semantic search)
// are routed through Orange instead of hitting the knowledge base directly, so
// every knowledge call shares one path:
//
//	IM client -> chat -> orange -> gateway -> knowledge base
//
// Orange injects the gateway token (reusing the `openclaw-channel`
// `http_token_injector` config) and the gateway exchanges it for a knowledge
// base token. This keeps the LLM-driven `knowledge_base_search` tool and the
// client-driven directory tree on exactly the same authenticated route.
const (
	orangeKBSpacesPath = "/api/v1/kb/spaces"
	orangeKBIndexPath  = "/api/v1/kb/index"
	orangeKBSearchPath = "/api/v1/kb/search"
)

// loadKBConfig builds the Orange call config for a knowledge base endpoint.
// Returns an empty URL when Orange is not configured, letting callers fall back
// to a direct knowledge base call.
func loadKBConfig(targetPath string) GeneratorConfig {
	cfg := LoadGeneratorConfigFromEnv()
	cfg.URL = normalizeSkillURL(
		firstNonEmpty(os.Getenv(EnvSkillGeneratorURL), os.Getenv(EnvGeneratorURL)),
		targetPath,
	)
	return cfg
}

// LoadKBSpacesConfigFromEnv targets `POST /api/v1/kb/spaces` on Orange.
func LoadKBSpacesConfigFromEnv() GeneratorConfig { return loadKBConfig(orangeKBSpacesPath) }

// LoadKBIndexConfigFromEnv targets `POST /api/v1/kb/index` on Orange.
func LoadKBIndexConfigFromEnv() GeneratorConfig { return loadKBConfig(orangeKBIndexPath) }

// LoadKBSearchConfigFromEnv targets `POST /api/v1/kb/search` on Orange.
func LoadKBSearchConfigFromEnv() GeneratorConfig { return loadKBConfig(orangeKBSearchPath) }

// CallOrangeKB posts a JSON request to an Orange knowledge base endpoint and
// decodes the response into `out`. Authentication reuses the digital-twin
// bearer token (`ORANGE_DIGITAL_TWIN_TOKEN`).
func CallOrangeKB(ctx context.Context, client *http.Client, cfg GeneratorConfig, req any, out any) error {
	return callHTTPSkillEndpoint(ctx, client, cfg, req, out)
}
