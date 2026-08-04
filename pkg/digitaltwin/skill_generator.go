package digitaltwin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openimsdk/tools/log"
)

const (
	EnvSkillGeneratorURL       = "OPENIM_DIGITAL_TWIN_SKILL_GENERATOR_URL"
	EnvSkillPlazaURL           = "OPENIM_DIGITAL_TWIN_SKILL_PLAZA_URL"
	orangeDigitalTwinSkillGen  = "/api/v1/digital-twin/skills/generate"
	orangeDigitalTwinSkillTask = "/api/v1/digital-twin/skills/tasks/"
	orangeDigitalTwinSkillList = "/api/v1/digital-twin/skills/list"
	orangeDigitalTwinSkillDel  = "/api/v1/digital-twin/skills/delete"
	orangeDigitalTwinSkillGet  = "/api/v1/digital-twin/skills/get"
	orangeDigitalTwinStats     = "/api/v1/digital-twin/stats"
	plazaGetAllSkillPath       = "/api/get_all_skill"
	PlazaDownloadSkillPath     = "/api/download_skill"
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
	Content     string `json:"content,omitempty"`
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

type SkillGetRequest struct {
	OwnerUserID string `json:"ownerUserID"`
	SkillName   string `json:"skillName"`
	OperationID string `json:"operationID,omitempty"`
}

type SkillGetResponse struct {
	OwnerUserID string `json:"ownerUserID"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SkillPath   string `json:"skillPath"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
	Content     string `json:"content"`
}

type SkillOwnerStat struct {
	OwnerUserID   string   `json:"ownerUserID"`
	WorkspacePath string   `json:"workspacePath"`
	SkillCount    int      `json:"skillCount"`
	SkillNames    []string `json:"skillNames"`
	HasMemory     bool     `json:"hasMemory"`
	LastActiveAt  int64    `json:"lastActiveAt,omitempty"`
}

type SkillStatsResponse struct {
	TotalOwners            int              `json:"totalOwners"`
	TotalSkills            int              `json:"totalSkills"`
	AverageSkillsPerOwner  float64          `json:"averageSkillsPerOwner"`
	Owners                 []SkillOwnerStat `json:"owners"`
	TopOwners              []SkillOwnerStat `json:"topOwners"`
}

// --- Async skill-generation task types ---

type SkillGenerateAcceptResponse struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type SkillGenerateTaskStatus string

const (
	SkillGenerateTaskPending   SkillGenerateTaskStatus = "pending"
	SkillGenerateTaskRunning   SkillGenerateTaskStatus = "running"
	SkillGenerateTaskCompleted SkillGenerateTaskStatus = "completed"
	SkillGenerateTaskFailed    SkillGenerateTaskStatus = "failed"
)

type SkillGenerateTask struct {
	ID           string                 `json:"id"`
	Status       SkillGenerateTaskStatus `json:"status"`
	OwnerUserID  string                 `json:"owner_user_id"`
	SkillName    string                 `json:"skill_name"`
	SkillPath    string                 `json:"skill_path,omitempty"`
	SkillContent string                 `json:"skill_content,omitempty"`
	Source       string                 `json:"source,omitempty"`
	Error        string                 `json:"error,omitempty"`
	CreatedAt    string                 `json:"created_at"`
	CompletedAt  string                 `json:"completed_at,omitempty"`
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

func LoadSkillTaskStatusConfigFromEnv(baseURL string) GeneratorConfig {
	cfg := LoadSkillGeneratorConfigFromEnv()
	parsed, err := url.Parse(baseURL)
	if err == nil && parsed.Host != "" {
		cfg.URL = parsed.Scheme + "://" + parsed.Host + orangeDigitalTwinSkillTask
	} else {
		cfg.URL = ""
	}
	return cfg
}

// CallHTTPSkillGenerateSubmit submits a skill generation request and returns
// the 202 accepted response containing a task_id for async polling.
func CallHTTPSkillGenerateSubmit(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req SkillGeneratorRequest) (SkillGenerateAcceptResponse, error) {
	timeout := genCfg.Timeout
	if timeout <= 0 {
		timeout = defaultGeneratorTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		return SkillGenerateAcceptResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, genCfg.URL, bytes.NewReader(body))
	if err != nil {
		return SkillGenerateAcceptResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if genCfg.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+genCfg.Token)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return SkillGenerateAcceptResponse{}, err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxGeneratorResponsePreviewBytes*64))
	var acceptResp SkillGenerateAcceptResponse
	if err := decoder.Decode(&acceptResp); err != nil {
		return SkillGenerateAcceptResponse{}, err
	}
	return acceptResp, nil
}

// CallHTTPSkillGenerateTaskStatus polls the async skill-generation task.
func CallHTTPSkillGenerateTaskStatus(ctx context.Context, client *http.Client, genCfg GeneratorConfig, taskID string) (SkillGenerateTask, error) {
	taskURL := strings.TrimRight(genCfg.URL, "/") + "/" + taskID
	timeout := genCfg.Timeout
	if timeout <= 0 {
		timeout = defaultGeneratorTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, taskURL, nil)
	if err != nil {
		return SkillGenerateTask{}, err
	}
	if genCfg.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+genCfg.Token)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return SkillGenerateTask{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, maxGeneratorResponsePreviewBytes))
		return SkillGenerateTask{}, errGeneratorStatus(resp.StatusCode, string(preview))
	}
	var task SkillGenerateTask
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxSkillResponseBytes))
	if err := decoder.Decode(&task); err != nil {
		return SkillGenerateTask{}, err
	}
	return task, nil
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
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxSkillResponseBytes))
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
	case "/api/v1", orangeDigitalTwinPath, orangeDigitalTwinSkillGen, orangeDigitalTwinSkillList,
		orangeDigitalTwinSkillDel, orangeKBSpacesPath, orangeKBIndexPath, orangeKBSearchPath:
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

// -------------------------------------------------------------------
// SKILL Plaza (企业技能广场) — external marketplace integration
// -------------------------------------------------------------------

// PlazaSkillItem represents a single skill entry returned by the plaza API.
// The outer JSON is a map[string]PlazaSkillItem keyed by skill name.
type PlazaSkillItem struct {
	Author     string `json:"author"`
	Description string `json:"description"`
	Downloads   int    `json:"downloads"`
	SkillType   string `json:"skill_type"`
	ThumbsUps   int    `json:"thumbs_ups"`
	URL         string `json:"url"`
}

// PlazaSkillListResponse is the raw response from GET /api/get_all_skill.
type PlazaSkillListResponse map[string]PlazaSkillItem

// PlazaDownloadRequest is the POST body for downloading a skill zip.
type PlazaDownloadRequest struct {
	SkillName string `json:"skill_name"`
}

// LoadSkillPlazaConfigFromEnv reads the SKILL plaza base URL from env.
func LoadSkillPlazaConfigFromEnv() GeneratorConfig {
	raw := os.Getenv(EnvSkillPlazaURL)
	if raw == "" {
		return GeneratorConfig{}
	}
	return GeneratorConfig{
		URL:     strings.TrimRight(raw, "/"),
		Token:   "",
		Timeout: 30 * time.Second,
	}
}

// CallHTTPPlazaSkillList fetches the full skill catalog from the plaza.
func CallHTTPPlazaSkillList(ctx context.Context, client *http.Client, genCfg GeneratorConfig) (PlazaSkillListResponse, error) {
	timeout := genCfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, genCfg.URL+plazaGetAllSkillPath, nil)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, maxGeneratorResponsePreviewBytes))
		return nil, fmt.Errorf("plaza list status %d: %s", resp.StatusCode, string(preview))
	}
	var result PlazaSkillListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// CallHTTPPlazaDownload downloads a skill zip from the plaza and returns raw bytes.
func CallHTTPPlazaDownload(ctx context.Context, client *http.Client, genCfg GeneratorConfig, skillName string) ([]byte, error) {
	timeout := genCfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second // download may be slower
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	bodyBytes, err := json.Marshal(PlazaDownloadRequest{SkillName: skillName})
	if err != nil {
		return nil, err
	}
	downloadURL := genCfg.URL + PlazaDownloadSkillPath
	log.ZInfo(ctx, "[plaza-download] POST",
		"url", downloadURL, "skillName", skillName)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, downloadURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.ZError(ctx, "[plaza-download] HTTP request FAILED", err,
			"url", downloadURL, "skillName", skillName)
		return nil, err
	}
	defer resp.Body.Close()
	log.ZInfo(ctx, "[plaza-download] response received",
		"status", resp.StatusCode,
		"contentType", resp.Header.Get("Content-Type"),
		"skillName", skillName)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, maxGeneratorResponsePreviewBytes))
		return nil, fmt.Errorf("plaza download status %d for skill '%s': %s", resp.StatusCode, skillName, string(preview))
	}
	// Check if response is an error JSON (some plazas return JSON on error)
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("plaza error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("unexpected json response from plaza download")
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func LoadSkillGetConfigFromEnv() GeneratorConfig {
	cfg := LoadSkillGeneratorConfigFromEnv()
	cfg.URL = normalizeSkillURL(firstNonEmpty(os.Getenv(EnvSkillGeneratorURL), os.Getenv(EnvGeneratorURL)), orangeDigitalTwinSkillGet)
	return cfg
}

func CallHTTPSkillGet(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req SkillGetRequest) (SkillGetResponse, error) {
	var resp SkillGetResponse
	if err := callHTTPSkillEndpoint(ctx, client, genCfg, req, &resp); err != nil {
		return SkillGetResponse{}, err
	}
	return resp, nil
}

func LoadSkillStatsConfigFromEnv() GeneratorConfig {
	cfg := LoadSkillGeneratorConfigFromEnv()
	cfg.URL = normalizeSkillURL(firstNonEmpty(os.Getenv(EnvSkillGeneratorURL), os.Getenv(EnvGeneratorURL)), orangeDigitalTwinStats)
	return cfg
}

func CallHTTPSkillStats(ctx context.Context, client *http.Client, genCfg GeneratorConfig) (SkillStatsResponse, error) {
	var resp SkillStatsResponse
	// 统计接口不需要请求体，orange 侧仅校验鉴权头。
	if err := callHTTPSkillEndpoint(ctx, client, genCfg, nil, &resp); err != nil {
		return SkillStatsResponse{}, err
	}
	return resp, nil
}
