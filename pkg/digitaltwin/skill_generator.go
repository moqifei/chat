package digitaltwin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvSkillGeneratorURL       = "OPENIM_DIGITAL_TWIN_SKILL_GENERATOR_URL"
	orangeDigitalTwinSkillGen  = "/api/v1/digital-twin/skills/generate"
	orangeDigitalTwinSkillList = "/api/v1/digital-twin/skills/list"
	orangeDigitalTwinSkillDel  = "/api/v1/digital-twin/skills/delete"
)

type SkillGeneratorRequest struct {
	OwnerUserID string `json:"ownerUserID"`
	SkillName   string `json:"skillName"`
	Description string `json:"description"`
	OperationID string `json:"operationID,omitempty"`
}

type SkillGeneratorResponse struct {
	OwnerUserID  string         `json:"ownerUserID"`
	SkillName    string         `json:"skillName"`
	SkillPath    string         `json:"skillPath"`
	SkillContent string         `json:"skillContent,omitempty"`
	Source       string         `json:"source,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type SkillListRequest struct {
	OwnerUserID string `json:"ownerUserID"`
	OperationID string `json:"operationID,omitempty"`
}

type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SkillPath   string `json:"skillPath"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
}

type SkillListResponse struct {
	OwnerUserID string         `json:"ownerUserID"`
	Skills      []SkillSummary `json:"skills"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type SkillDeleteRequest struct {
	OwnerUserID string `json:"ownerUserID"`
	SkillName   string `json:"skillName"`
	OperationID string `json:"operationID,omitempty"`
}

type SkillDeleteResponse struct {
	OwnerUserID string `json:"ownerUserID"`
	SkillName   string `json:"skillName"`
	Deleted     bool   `json:"deleted"`
	SkillPath   string `json:"skillPath"`
}

func LoadSkillGeneratorConfigFromEnv() GeneratorConfig {
	cfg := GeneratorConfig{
		URL:     normalizeSkillGeneratorURL(firstNonEmpty(os.Getenv(EnvSkillGeneratorURL), os.Getenv(EnvGeneratorURL))),
		Token:   strings.TrimSpace(firstNonEmpty(os.Getenv(EnvGeneratorToken), os.Getenv(EnvOrangeTwinToken))),
		Timeout: defaultGeneratorTimeout,
	}
	if raw := strings.TrimSpace(os.Getenv(EnvGeneratorTimeoutSec)); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			cfg.Timeout = time.Duration(seconds) * time.Second
		}
	}
	return cfg
}

func LoadSkillListConfigFromEnv() GeneratorConfig {
	cfg := LoadSkillGeneratorConfigFromEnv()
	cfg.URL = normalizeSkillURL(firstNonEmpty(os.Getenv(EnvSkillGeneratorURL), os.Getenv(EnvGeneratorURL)), orangeDigitalTwinSkillList)
	return cfg
}

func LoadSkillDeleteConfigFromEnv() GeneratorConfig {
	cfg := LoadSkillGeneratorConfigFromEnv()
	cfg.URL = normalizeSkillURL(firstNonEmpty(os.Getenv(EnvSkillGeneratorURL), os.Getenv(EnvGeneratorURL)), orangeDigitalTwinSkillDel)
	return cfg
}

func CallHTTPSkillGenerator(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req SkillGeneratorRequest) (SkillGeneratorResponse, error) {
	var genResp SkillGeneratorResponse
	if err := callHTTPSkillEndpoint(ctx, client, genCfg, req, &genResp); err != nil {
		return SkillGeneratorResponse{}, err
	}
	return genResp, nil
}

func CallHTTPSkillList(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req SkillListRequest) (SkillListResponse, error) {
	var resp SkillListResponse
	if err := callHTTPSkillEndpoint(ctx, client, genCfg, req, &resp); err != nil {
		return SkillListResponse{}, err
	}
	return resp, nil
}

func CallHTTPSkillDelete(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req SkillDeleteRequest) (SkillDeleteResponse, error) {
	var resp SkillDeleteResponse
	if err := callHTTPSkillEndpoint(ctx, client, genCfg, req, &resp); err != nil {
		return SkillDeleteResponse{}, err
	}
	return resp, nil
}

func callHTTPSkillEndpoint(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req any, out any) error {
	timeout := genCfg.Timeout
	if timeout <= 0 {
		timeout = defaultGeneratorTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, genCfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set(generatorResponseContentTypeHeader, "application/json")
	if genCfg.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+genCfg.Token)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, maxGeneratorResponsePreviewBytes))
		return errGeneratorStatus(resp.StatusCode, string(preview))
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxGeneratorResponsePreviewBytes*64))
	return decoder.Decode(out)
}

func normalizeSkillGeneratorURL(raw string) string {
	return normalizeSkillURL(raw, orangeDigitalTwinSkillGen)
}

func normalizeSkillURL(raw string, targetPath string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch path {
	case targetPath:
		return raw
	case "":
		parsed.Path = targetPath
	case "/api/v1", orangeDigitalTwinPath, orangeDigitalTwinSkillGen, orangeDigitalTwinSkillList, orangeDigitalTwinSkillDel:
		parsed.Path = targetPath
	default:
		return raw
	}
	return parsed.String()
}

func NormalizeSkillName(raw string) string {
	var builder strings.Builder
	previousSeparator := false
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			previousSeparator = false
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r + ('a' - 'A'))
			previousSeparator = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			previousSeparator = false
		case r == '-' || r == '_' || r == ' ' || r == '\t' || r == '\n':
			if builder.Len() > 0 && !previousSeparator {
				builder.WriteByte('-')
				previousSeparator = true
			}
		}
	}
	return strings.Trim(builder.String(), "-_")
}
