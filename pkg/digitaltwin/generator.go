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

	"github.com/openimsdk/chat/pkg/botstruct"
	"github.com/openimsdk/chat/pkg/common/imwebhook"
	"github.com/openimsdk/tools/log"
)

const (
	EnvGeneratorURL        = "OPENIM_DIGITAL_TWIN_GENERATOR_URL"
	EnvGeneratorToken      = "OPENIM_DIGITAL_TWIN_GENERATOR_TOKEN"
	EnvGeneratorTimeoutSec = "OPENIM_DIGITAL_TWIN_GENERATOR_TIMEOUT_SEC"
	EnvOrangeTwinToken     = "ORANGE_DIGITAL_TWIN_TOKEN"
	orangeDigitalTwinPath  = "/api/v1/digital-twin/reply"

	ReplySourceStatic                  = "static"
	ReplySourceHTTPGenerator           = "http_generator"
	ReplySourceHTTPGeneratorFallback   = "http_generator_fallback"
	// 数字分身回复走 Orange 的同步 agent loop（可能多轮工具调用），
	// 复杂查询会跑 30~60s 以上，默认超时必须足够大，否则 chat 侧 HTTP 超时
	// 取消请求并触发 `context deadline exceeded` 兜底。
	defaultGeneratorTimeout            = 120 * time.Second
	maxGeneratorResponsePreviewBytes   = 512
	// 技能相关接口（列表/任务状态）可能返回较大响应体（如完整 SKILL.md 内容），
	// 允许读取到 8MB，避免被 LimitReader 截断导致 JSON 解码 unexpected EOF。
	maxSkillResponseBytes              = 8 * 1024 * 1024
	// 分身回复文本本身也可能很长（如查询结果、长段落），放宽到 8MB，
	// 避免被 LimitReader 截断导致 JSON 解码 unexpected EOF。
	maxGeneratorReplyBytes             = 8 * 1024 * 1024
	generatorResponseContentTypeHeader = "Content-Type"
)

type GeneratorConfig struct {
	URL     string
	Token   string
	Timeout time.Duration
}

type GeneratorRequest struct {
	OwnerUserID       string                  `json:"ownerUserID"`
	SenderUserID      string                  `json:"senderUserID"`
	MessageContent    string                  `json:"messageContent"`
	FallbackReplyText string                  `json:"fallbackReplyText"`
	Prompt            string                  `json:"prompt,omitempty"`
	ServerMsgID       string                  `json:"serverMsgID,omitempty"`
	ClientMsgID       string                  `json:"clientMsgID,omitempty"`
	OperationID       string                  `json:"operationID,omitempty"`
	KnowledgeBase     *KnowledgeBaseConfig   `json:"knowledgeBase,omitempty"`
}

type GeneratorResponse struct {
	ReplyText      string         `json:"replyText"`
	Content        string         `json:"content"`
	Source         string         `json:"source,omitempty"`
	ProtocolSource string         `json:"protocolSource,omitempty"`
	FinalizeSource string         `json:"finalizeSource,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Citations      []map[string]any `json:"citations,omitempty"`
}

type ReplyPlan struct {
	Content        string
	Source         string
	Trace          *ReplyTrace
	GeneratorError string
	Citations      []map[string]any
}

func LoadGeneratorConfigFromEnv() GeneratorConfig {
	// [标准化部署] 优先从 chat/config/digital_twin.yml 读取, 不再依赖 docker-compose 注入。
	if f := DigitalTwinFile(); f != nil {
		url := normalizeGeneratorURL(strings.TrimSpace(f.Generator.URL))
		if url != "" {
			timeout := defaultGeneratorTimeout
			if f.Generator.TimeoutSec > 0 {
				timeout = time.Duration(f.Generator.TimeoutSec) * time.Second
			}
			return GeneratorConfig{
				URL:     url,
				Token:   strings.TrimSpace(f.Generator.Token),
				Timeout: timeout,
			}
		}
	}
	// 兜底: 兼容旧的环境变量注入方式
	cfg := GeneratorConfig{
		URL:     normalizeGeneratorURL(os.Getenv(EnvGeneratorURL)),
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

func BuildReplyPlan(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq, cfg Config) ReplyPlan {
	genCfg := LoadGeneratorConfigFromEnv()
	if genCfg.URL == "" {
		return ReplyPlan{Content: cfg.ReplyText, Source: ReplySourceStatic}
	}
	genResp, err := CallHTTPGeneratorResponse(ctx, http.DefaultClient, genCfg, req, cfg)
	reply := genResp.Text()
	if err != nil || strings.TrimSpace(reply) == "" {
		errText := generatorErrorText(err, reply)
		log.ZWarn(ctx, "digital twin generator fallback", nil,
			"url", genCfg.URL,
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"error", errText,
		)
		return ReplyPlan{
			Content:        cfg.ReplyText,
			Source:         ReplySourceHTTPGeneratorFallback,
			Trace:          fallbackTrace(genResp.Trace(), errText),
			GeneratorError: errText,
		}
	}
	return ReplyPlan{
		Content:   reply,
		Source:    ReplySourceHTTPGenerator,
		Trace:     genResp.Trace(),
		Citations: genResp.Citations,
	}
}

func CallHTTPGenerator(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req imwebhook.CallbackAfterSendSingleMsgReq, cfg Config) (string, error) {
	genResp, err := CallHTTPGeneratorResponse(ctx, client, genCfg, req, cfg)
	if err != nil {
		return "", err
	}
	return genResp.Text(), nil
}

func CallHTTPGeneratorResponse(ctx context.Context, client *http.Client, genCfg GeneratorConfig, req imwebhook.CallbackAfterSendSingleMsgReq, cfg Config) (GeneratorResponse, error) {
	timeout := genCfg.Timeout
	if timeout <= 0 {
		timeout = defaultGeneratorTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	body, err := json.Marshal(GeneratorRequest{
		OwnerUserID:       req.RecvID,
		SenderUserID:      req.SendID,
		MessageContent:    ExtractTextContent(req.Content),
		FallbackReplyText: cfg.ReplyText,
		Prompt:            cfg.Prompt,
		ServerMsgID:       req.ServerMsgID,
		ClientMsgID:       req.ClientMsgID,
		OperationID:       req.OperationID,
		KnowledgeBase:     cfg.KnowledgeBase,
	})
	if err != nil {
		return GeneratorResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, genCfg.URL, bytes.NewReader(body))
	if err != nil {
		return GeneratorResponse{}, err
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
		return GeneratorResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, maxGeneratorResponsePreviewBytes))
		return GeneratorResponse{}, errGeneratorStatus(resp.StatusCode, string(preview))
	}
	var genResp GeneratorResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxGeneratorReplyBytes))
	if err := decoder.Decode(&genResp); err != nil {
		return GeneratorResponse{}, err
	}
	return genResp, nil
}

func (r GeneratorResponse) Text() string {
	if text := strings.TrimSpace(r.ReplyText); text != "" {
		return text
	}
	return strings.TrimSpace(r.Content)
}

func (r GeneratorResponse) Trace() *ReplyTrace {
	if r.Source == "" && r.ProtocolSource == "" && r.FinalizeSource == "" && len(r.Metadata) == 0 {
		return nil
	}
	return &ReplyTrace{
		Source:         strings.TrimSpace(r.Source),
		ProtocolSource: strings.TrimSpace(r.ProtocolSource),
		FinalizeSource: strings.TrimSpace(r.FinalizeSource),
		Metadata:       r.Metadata,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeGeneratorURL(raw string) string {
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
	case orangeDigitalTwinPath:
		return raw
	case "":
		parsed.Path = orangeDigitalTwinPath
	case "/api/v1":
		parsed.Path = "/api/v1/digital-twin/reply"
	default:
		return raw
	}
	return parsed.String()
}

func generatorErrorText(err error, reply string) string {
	if err != nil {
		return err.Error()
	}
	if strings.TrimSpace(reply) == "" {
		return "digital twin generator returned empty reply"
	}
	return ""
}

func fallbackTrace(trace *ReplyTrace, errText string) *ReplyTrace {
	if trace == nil {
		trace = &ReplyTrace{}
	}
	if trace.Metadata == nil {
		trace.Metadata = make(map[string]any)
	}
	trace.Metadata["generatorError"] = errText
	trace.Metadata["fallback"] = true
	return trace
}

func ExtractTextContent(content string) string {
	var elem botstruct.TextElem
	if err := json.Unmarshal([]byte(content), &elem); err == nil && strings.TrimSpace(elem.Content) != "" {
		return elem.Content
	}
	return strings.TrimSpace(content)
}

type generatorStatusError struct {
	status int
	body   string
}

func (e generatorStatusError) Error() string {
	if strings.TrimSpace(e.body) == "" {
		return "digital twin generator returned status " + strconv.Itoa(e.status)
	}
	return "digital twin generator returned status " + strconv.Itoa(e.status) + ": " + strings.TrimSpace(e.body)
}

func errGeneratorStatus(status int, body string) error {
	return generatorStatusError{status: status, body: body}
}
