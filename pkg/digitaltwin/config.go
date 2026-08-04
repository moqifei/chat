package digitaltwin

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/openimsdk/chat/pkg/botstruct"
	"github.com/openimsdk/chat/pkg/common/imwebhook"
	"github.com/openimsdk/protocol/constant"
)

const (
	EnvEnabled   = "OPENIM_DIGITAL_TWIN_ENABLED"
	EnvUserIDs   = "OPENIM_DIGITAL_TWIN_USER_IDS"
	EnvReplyText = "OPENIM_DIGITAL_TWIN_REPLY_TEXT"
	EnvPrompt    = "OPENIM_DIGITAL_TWIN_PROMPT"

	ExtType       = "digital_twin"
	ExtTypeField  = "openim_ext_type"
	UserConfigKey = "openim_digital_twin"
	TraceKey      = "openim_digital_twin_trace"

	defaultReplyText = "I am away right now. My digital twin has received your message and I will check it later."

	TriggerModeImmediate     = "immediate"
	TriggerModeManual        = "manual"
	TriggerModeUnreadTimeout = "unread_timeout"
)

type Config struct {
	Enabled              bool
	UserIDs              map[string]struct{}
	AllowAll             bool
	ReplyText            string
	Prompt               string
	ReplyCooldownSeconds int64
	ReplySchedule        ReplySchedule
	AllowedSenderUserIDs map[string]struct{}
	BlockedSenderUserIDs map[string]struct{}
	TriggerMode          string
	UnreadTimeoutSeconds int64
	// Knowledge base configuration for AI-enhanced replies
	KnowledgeBase *KnowledgeBaseConfig
}

type UserConfig struct {
	Enabled              bool          `json:"enabled"`
	ReplyText            string        `json:"replyText,omitempty"`
	Prompt               string        `json:"prompt,omitempty"`
	ReplyCooldownSeconds int64         `json:"replyCooldownSeconds,omitempty"`
	ReplySchedule        ReplySchedule `json:"replySchedule,omitempty"`
	AllowedSenderUserIDs []string      `json:"allowedSenderUserIDs,omitempty"`
	BlockedSenderUserIDs []string      `json:"blockedSenderUserIDs,omitempty"`
	TriggerMode          string        `json:"triggerMode,omitempty"`
	UnreadTimeoutSeconds int64         `json:"unreadTimeoutSeconds,omitempty"`
	KnowledgeBase        *KnowledgeBaseConfig `json:"knowledgeBase,omitempty"`
	Version              int           `json:"version"`
	UpdatedAt            int64         `json:"updatedAt"`
}

type ReplySchedule struct {
	Enabled     bool   `json:"enabled"`
	StartMinute int    `json:"startMinute"`
	EndMinute   int    `json:"endMinute"`
	Timezone    string `json:"timezone,omitempty"`
}

// KnowledgeBaseConfig holds knowledge base enhancement settings for digital twin.
// When enabled, the AI reply generation can reference documents from configured
// Arkon wiki spaces via semantic search.
type KnowledgeBaseConfig struct {
	Enabled             bool     `json:"enabled"`
	SpaceIDs            []string `json:"spaceIds,omitempty"`
	AnswerStrategy      string   `json:"answerStrategy,omitempty"`       // auto_search | knowledge_only | no_fabricate
	CitationStyle       string   `json:"citationStyle,omitempty"`        // always_show | knowledge_only
	PermissionStrategy  string   `json:"permissionStrategy,omitempty"`   // authorized_only | summary_on_no_access
	SensitiveNoAutoReply bool    `json:"sensitiveNoAutoReply"`
	// Arkon knowledge base API base URL (e.g. http://localhost:8478). Injected by
	// the chat server from OPENIM_KB_BASE_URL; the client does not set this.
	BaseURL string `json:"baseURL,omitempty"`
}

type UserConfigPatch struct {
	Enabled              *bool                  `json:"enabled"`
	ReplyText            *string                `json:"replyText"`
	Prompt               *string                `json:"prompt"`
	ReplyCooldownSeconds *int64                 `json:"replyCooldownSeconds"`
	ReplySchedule        *ReplySchedule         `json:"replySchedule"`
	AllowedSenderUserIDs *[]string              `json:"allowedSenderUserIDs"`
	BlockedSenderUserIDs *[]string              `json:"blockedSenderUserIDs"`
	TriggerMode          *string                `json:"triggerMode"`
	UnreadTimeoutSeconds *int64                 `json:"unreadTimeoutSeconds"`
	KnowledgeBase        *KnowledgeBaseConfig  `json:"knowledgeBase"`
}

type Decision struct {
	Handled bool   `json:"handled"`
	Reason  string `json:"reason,omitempty"`
}

type ReplyExt struct {
	OpenIMExtType      string           `json:"openim_ext_type"`
	Version            int              `json:"version"`
	OwnerUserID        string           `json:"ownerUserID"`
	TriggerSendID      string           `json:"triggerSendID"`
	TriggerServerMsgID string           `json:"triggerServerMsgID,omitempty"`
	TriggerClientMsgID string           `json:"triggerClientMsgID,omitempty"`
	GeneratedBy        string           `json:"generatedBy"`
	ReplySource        string           `json:"replySource"`
	ReplyText          string           `json:"replyText,omitempty"`
	GeneratorError     string           `json:"generatorError,omitempty"`
	CreatedAt          int64            `json:"createdAt"`
	Trace              *ReplyTrace      `json:"openim_digital_twin_trace,omitempty"`
	Citations          []map[string]any `json:"citations,omitempty"`
}

type ReplyTrace struct {
	Source         string         `json:"source,omitempty"`
	ProtocolSource string         `json:"protocolSource,omitempty"`
	FinalizeSource string         `json:"finalizeSource,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

func LoadConfigFromEnv() Config {
	cfg := Config{
		Enabled:   parseBool(os.Getenv(EnvEnabled)),
		UserIDs:   make(map[string]struct{}),
		ReplyText: strings.TrimSpace(os.Getenv(EnvReplyText)),
		Prompt:    strings.TrimSpace(os.Getenv(EnvPrompt)),
	}
	if cfg.ReplyText == "" {
		cfg.ReplyText = defaultReplyText
	}
	for _, userID := range strings.Split(os.Getenv(EnvUserIDs), ",") {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if userID == "*" {
			cfg.AllowAll = true
			continue
		}
		cfg.UserIDs[userID] = struct{}{}
	}
	return cfg
}

func LoadConfigFromUserEx(userID string, ex string) (Config, bool, error) {
	userCfg, ok, err := ParseUserConfigFromEx(ex)
	if err != nil || !ok {
		return Config{}, ok, err
	}
	return ConfigFromUserConfig(userID, userCfg), true, nil
}

func ConfigFromUserConfig(userID string, userCfg UserConfig) Config {
	cfg := Config{
		Enabled:              userCfg.Enabled,
		UserIDs:              map[string]struct{}{userID: {}},
		ReplyText:            strings.TrimSpace(userCfg.ReplyText),
		Prompt:               strings.TrimSpace(userCfg.Prompt),
		ReplyCooldownSeconds: normalizeReplyCooldownSeconds(userCfg.ReplyCooldownSeconds),
		ReplySchedule:        normalizeReplySchedule(userCfg.ReplySchedule),
		AllowedSenderUserIDs: userIDSet(normalizeUserIDList(userCfg.AllowedSenderUserIDs)),
		BlockedSenderUserIDs: userIDSet(normalizeUserIDList(userCfg.BlockedSenderUserIDs)),
		TriggerMode:          normalizeTriggerMode(userCfg.TriggerMode),
		UnreadTimeoutSeconds: normalizeUnreadTimeoutSeconds(userCfg.UnreadTimeoutSeconds),
		KnowledgeBase:        normalizeKnowledgeBase(userCfg.KnowledgeBase),
	}
	if cfg.ReplyText == "" {
		cfg.ReplyText = defaultReplyText
	}
	return cfg
}

func ApplyUserConfigPatch(cfg UserConfig, patch UserConfigPatch) UserConfig {
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.ReplyText != nil {
		cfg.ReplyText = strings.TrimSpace(*patch.ReplyText)
	}
	if patch.Prompt != nil {
		cfg.Prompt = strings.TrimSpace(*patch.Prompt)
	}
	if patch.ReplyCooldownSeconds != nil {
		cfg.ReplyCooldownSeconds = normalizeReplyCooldownSeconds(*patch.ReplyCooldownSeconds)
	}
	if patch.ReplySchedule != nil {
		cfg.ReplySchedule = normalizeReplySchedule(*patch.ReplySchedule)
	}
	if patch.AllowedSenderUserIDs != nil {
		cfg.AllowedSenderUserIDs = normalizeUserIDList(*patch.AllowedSenderUserIDs)
	}
	if patch.BlockedSenderUserIDs != nil {
		cfg.BlockedSenderUserIDs = normalizeUserIDList(*patch.BlockedSenderUserIDs)
	}
	if patch.TriggerMode != nil {
		cfg.TriggerMode = normalizeTriggerMode(*patch.TriggerMode)
	}
	if patch.UnreadTimeoutSeconds != nil {
		cfg.UnreadTimeoutSeconds = normalizeUnreadTimeoutSeconds(*patch.UnreadTimeoutSeconds)
	}
	if patch.KnowledgeBase != nil {
		cfg.KnowledgeBase = patch.KnowledgeBase
	}
	return cfg
}

func ShouldReply(cfg Config, req imwebhook.CallbackAfterSendSingleMsgReq) Decision {
	if !cfg.Enabled {
		return Decision{Reason: "disabled"}
	}
	if req.ContentType != constant.Text {
		return Decision{Reason: "unsupported_content_type"}
	}
	if req.SendID == "" || req.RecvID == "" {
		return Decision{Reason: "missing_user_id"}
	}
	if req.SendID == req.RecvID {
		return Decision{Reason: "same_sender_receiver"}
	}
	if botstruct.IsAgentPlatformID(req.SenderPlatformID) {
		return Decision{Reason: "agent_platform_message"}
	}
	if botstruct.IsAgentUserID(req.SendID) || botstruct.IsAgentUserID(req.RecvID) {
		return Decision{Reason: "agent_message"}
	}
	if IsDigitalTwinEx(req.Ex) {
		return Decision{Reason: "digital_twin_message"}
	}
	if !cfg.AllowAll {
		if _, ok := cfg.UserIDs[req.RecvID]; !ok {
			return Decision{Reason: "receiver_not_enabled"}
		}
	}
	if _, ok := cfg.BlockedSenderUserIDs[req.SendID]; ok {
		return Decision{Reason: "sender_blocked"}
	}
	if len(cfg.AllowedSenderUserIDs) > 0 {
		if _, ok := cfg.AllowedSenderUserIDs[req.SendID]; !ok {
			return Decision{Reason: "sender_not_allowed"}
		}
	}
	return Decision{Handled: true}
}

func ParseUserConfigFromEx(ex string) (UserConfig, bool, error) {
	root, ok, err := parseExRoot(ex)
	if err != nil || !ok {
		return UserConfig{}, false, err
	}
	raw, ok := root[UserConfigKey]
	if !ok {
		return UserConfig{}, false, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return UserConfig{}, false, err
	}
	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return UserConfig{}, false, err
	}
	return cfg, true, nil
}

func MergeUserConfigToEx(ex string, cfg UserConfig, now time.Time) (string, error) {
	root, _, err := parseExRoot(ex)
	if err != nil {
		return "", err
	}
	if root == nil {
		root = make(map[string]any)
	}
	cfg.Version = 1
	cfg.UpdatedAt = now.UnixMilli()
	root[UserConfigKey] = cfg
	data, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func BuildReplyEx(req imwebhook.CallbackAfterSendSingleMsgReq, now time.Time) (string, error) {
	return BuildReplyExWithSource(req, now, ReplySourceStatic)
}

func BuildReplyExWithSource(req imwebhook.CallbackAfterSendSingleMsgReq, now time.Time, replySource string) (string, error) {
	return BuildReplyExWithSourceAndTrace(req, now, replySource, nil)
}

func BuildReplyExWithSourceAndTrace(req imwebhook.CallbackAfterSendSingleMsgReq, now time.Time, replySource string, trace *ReplyTrace) (string, error) {
	return BuildReplyExWithSourceTraceAndText(req, now, replySource, trace, "")
}

func BuildReplyExWithSourceTraceAndText(req imwebhook.CallbackAfterSendSingleMsgReq, now time.Time, replySource string, trace *ReplyTrace, replyText string) (string, error) {
	return BuildReplyExWithSourceTraceTextAndError(req, now, replySource, trace, replyText, "", nil)
}

func BuildReplyExWithSourceTraceTextAndError(req imwebhook.CallbackAfterSendSingleMsgReq, now time.Time, replySource string, trace *ReplyTrace, replyText string, generatorError string, citations []map[string]any) (string, error) {
	if strings.TrimSpace(replySource) == "" {
		replySource = ReplySourceStatic
	}
	data, err := json.Marshal(ReplyExt{
		OpenIMExtType:      ExtType,
		Version:            1,
		OwnerUserID:        req.RecvID,
		TriggerSendID:      req.SendID,
		TriggerServerMsgID: req.ServerMsgID,
		TriggerClientMsgID: req.ClientMsgID,
		GeneratedBy:        "chat.digitaltwin.mvp",
		ReplySource:        replySource,
		ReplyText:          strings.TrimSpace(replyText),
		GeneratorError:     strings.TrimSpace(generatorError),
		CreatedAt:          now.UnixMilli(),
		Trace:              trace,
		Citations:          citations,
	})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseExRoot(ex string) (map[string]any, bool, error) {
	ex = strings.TrimSpace(ex)
	if ex == "" {
		return nil, false, nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(ex), &root); err != nil {
		return nil, false, err
	}
	if root == nil {
		root = make(map[string]any)
	}
	return root, true, nil
}

func IsDigitalTwinEx(ex string) bool {
	ex = strings.TrimSpace(ex)
	if ex == "" {
		return false
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(ex), &data); err != nil {
		return false
	}
	val, ok := data[ExtTypeField].(string)
	return ok && val == ExtType
}

func parseBool(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "t", "true", "y", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

func normalizeReplyCooldownSeconds(seconds int64) int64 {
	if seconds < 0 {
		return 0
	}
	if seconds > 86400 {
		return 86400
	}
	return seconds
}

func normalizeReplySchedule(schedule ReplySchedule) ReplySchedule {
	if schedule.StartMinute < 0 {
		schedule.StartMinute = 0
	}
	if schedule.StartMinute > 1439 {
		schedule.StartMinute = 1439
	}
	if schedule.EndMinute < 0 {
		schedule.EndMinute = 0
	}
	if schedule.EndMinute > 1439 {
		schedule.EndMinute = 1439
	}
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	return schedule
}

func normalizeUserIDList(userIDs []string) []string {
	seen := make(map[string]struct{}, len(userIDs))
	result := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		result = append(result, userID)
		if len(result) >= 100 {
			break
		}
	}
	return result
}

func userIDSet(userIDs []string) map[string]struct{} {
	if len(userIDs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		set[userID] = struct{}{}
	}
	return set
}

// EnvKbBaseURL points the chat server at the Arkon knowledge base (or local mock)
// API. It is forwarded to orange's digital-twin generator so the
// knowledge_base_search tool can reach the right endpoint.
const EnvKbBaseURL = "OPENIM_KB_BASE_URL"

// normalizeKnowledgeBase ensures the base URL is populated from the environment
// when absent, so orange always has a concrete Arkon endpoint to query. Falls
// back to the local mock server when the env var is unset (local dev).
func normalizeKnowledgeBase(kb *KnowledgeBaseConfig) *KnowledgeBaseConfig {
	if kb == nil {
		return nil
	}
	if kb.BaseURL == "" {
		kb.BaseURL = strings.TrimSpace(os.Getenv(EnvKbBaseURL))
	}
	if kb.BaseURL == "" {
		kb.BaseURL = "http://localhost:8478"
	}
	return kb
}
