package digitaltwin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	commonconstant "github.com/openimsdk/chat/pkg/common/constant"
	"github.com/openimsdk/chat/pkg/common/imwebhook"
	"github.com/openimsdk/protocol/constant"
)

func TestShouldReply(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		UserIDs:   map[string]struct{}{"user_b": {}},
		ReplyText: "ok",
	}
	req := imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{
			SendID:      "user_a",
			ContentType: constant.Text,
		},
		RecvID: "user_b",
	}
	if decision := ShouldReply(cfg, req); !decision.Handled {
		t.Fatalf("expected handled, got reason %q", decision.Reason)
	}

	req.RecvID = "user_c"
	if decision := ShouldReply(cfg, req); decision.Handled || decision.Reason != "receiver_not_enabled" {
		t.Fatalf("expected receiver_not_enabled, got %+v", decision)
	}

	req.RecvID = "bot_1"
	if decision := ShouldReply(cfg, req); decision.Handled || decision.Reason != "agent_message" {
		t.Fatalf("expected agent_message, got %+v", decision)
	}

	req.RecvID = "user_b"
	req.SenderPlatformID = commonconstant.AgentPlatformID
	if decision := ShouldReply(cfg, req); decision.Handled || decision.Reason != "agent_platform_message" {
		t.Fatalf("expected agent_platform_message, got %+v", decision)
	}
	req.SenderPlatformID = 0

	cfg = Config{
		Enabled:              true,
		UserIDs:              map[string]struct{}{"user_b": {}},
		ReplyText:            "ok",
		BlockedSenderUserIDs: map[string]struct{}{"user_a": {}},
	}
	if decision := ShouldReply(cfg, req); decision.Handled || decision.Reason != "sender_blocked" {
		t.Fatalf("expected sender_blocked, got %+v", decision)
	}

	cfg = Config{
		Enabled:              true,
		UserIDs:              map[string]struct{}{"user_b": {}},
		ReplyText:            "ok",
		AllowedSenderUserIDs: map[string]struct{}{"user_c": {}},
	}
	if decision := ShouldReply(cfg, req); decision.Handled || decision.Reason != "sender_not_allowed" {
		t.Fatalf("expected sender_not_allowed, got %+v", decision)
	}

	cfg.AllowedSenderUserIDs["user_a"] = struct{}{}
	if decision := ShouldReply(cfg, req); !decision.Handled {
		t.Fatalf("expected allow-listed sender to be handled, got %+v", decision)
	}
}

func TestIsDigitalTwinEx(t *testing.T) {
	if !IsDigitalTwinEx(`{"openim_ext_type":"digital_twin"}`) {
		t.Fatal("expected digital twin ex to be detected")
	}
	if IsDigitalTwinEx(`{"openim_ext_type":"normal"}`) {
		t.Fatal("expected normal ex to be ignored")
	}
	if IsDigitalTwinEx(`not-json`) {
		t.Fatal("expected invalid json to be ignored")
	}
}

func TestMergeUserConfigToExPreservesExistingFields(t *testing.T) {
	ex, err := MergeUserConfigToEx(`{"legacy":"keep"}`, UserConfig{
		Enabled:   true,
		ReplyText: "hello",
		Prompt:    "reply like me",
	}, time.UnixMilli(123))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(ex), &root); err != nil {
		t.Fatal(err)
	}
	if root["legacy"] != "keep" {
		t.Fatalf("expected legacy field to be preserved, got %v", root["legacy"])
	}
	cfg, ok, err := ParseUserConfigFromEx(ex)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !cfg.Enabled || cfg.ReplyText != "hello" || cfg.Prompt != "reply like me" || cfg.Version != 1 || cfg.UpdatedAt != 123 {
		t.Fatalf("unexpected config: ok=%v cfg=%+v", ok, cfg)
	}
}

func TestMergeUserConfigToExRejectsInvalidExistingEx(t *testing.T) {
	if _, err := MergeUserConfigToEx(`legacy`, UserConfig{Enabled: true}, time.UnixMilli(123)); err == nil {
		t.Fatal("expected invalid existing ex to be rejected")
	}
}

func TestLoadConfigFromUserEx(t *testing.T) {
	ex, err := MergeUserConfigToEx("", UserConfig{Enabled: true, ReplyText: "from user", Prompt: "from prompt"}, time.UnixMilli(123))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok, err := LoadConfigFromUserEx("user_b", ex)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !cfg.Enabled || cfg.ReplyText != "from user" || cfg.Prompt != "from prompt" {
		t.Fatalf("unexpected config: ok=%v cfg=%+v", ok, cfg)
	}
	req := imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{
			SendID:      "user_a",
			ContentType: constant.Text,
		},
		RecvID: "user_b",
	}
	if decision := ShouldReply(cfg, req); !decision.Handled {
		t.Fatalf("expected handled, got %+v", decision)
	}
}

func TestUserConfigStorePersistsConfig(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "digital_twin_configs.json")
	t.Setenv(EnvConfigStorePath, storePath)

	cfg := UserConfig{
		Enabled:   true,
		ReplyText: "from store",
		Prompt:    "store prompt",
		Version:   1,
		UpdatedAt: 123,
	}
	if err := SaveUserConfigToStore("user_b", cfg, time.UnixMilli(456)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatal(err)
	}

	got, ok, err := LoadUserConfigFromStore("user_b")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !got.Enabled || got.ReplyText != "from store" || got.Prompt != "store prompt" {
		t.Fatalf("unexpected config from store: ok=%v cfg=%+v", ok, got)
	}
	if got.UpdatedAt != 123 {
		t.Fatalf("expected user config updatedAt to be preserved, got %d", got.UpdatedAt)
	}

	dtCfg := ConfigFromUserConfig("user_b", got)
	if !dtCfg.Enabled || dtCfg.ReplyText != "from store" || dtCfg.Prompt != "store prompt" {
		t.Fatalf("unexpected digital twin config %+v", dtCfg)
	}
}

func TestApplyUserConfigPatch(t *testing.T) {
	replyText := " new fallback "
	prompt := " use short sentences "
	cooldownSeconds := int64(90)
	schedule := ReplySchedule{Enabled: true, StartMinute: -10, EndMinute: 1500, Timezone: " Asia/Shanghai "}
	allowedSenderUserIDs := []string{" user_a ", "user_b", "user_a", ""}
	blockedSenderUserIDs := []string{" user_x ", "user_y", "user_x"}
	triggerMode := TriggerModeManual
	unreadTimeoutSeconds := int64(5)
	cfg := ApplyUserConfigPatch(UserConfig{
		Enabled:   true,
		ReplyText: "old fallback",
		Prompt:    "old prompt",
	}, UserConfigPatch{
		ReplyText:            &replyText,
		Prompt:               &prompt,
		ReplyCooldownSeconds: &cooldownSeconds,
		ReplySchedule:        &schedule,
		AllowedSenderUserIDs: &allowedSenderUserIDs,
		BlockedSenderUserIDs: &blockedSenderUserIDs,
		TriggerMode:          &triggerMode,
		UnreadTimeoutSeconds: &unreadTimeoutSeconds,
	})
	if !cfg.Enabled || cfg.ReplyText != "new fallback" || cfg.Prompt != "use short sentences" || cfg.ReplyCooldownSeconds != 90 {
		t.Fatalf("unexpected patched config %+v", cfg)
	}
	if !cfg.ReplySchedule.Enabled || cfg.ReplySchedule.StartMinute != 0 || cfg.ReplySchedule.EndMinute != 1439 || cfg.ReplySchedule.Timezone != "Asia/Shanghai" {
		t.Fatalf("unexpected schedule %+v", cfg.ReplySchedule)
	}
	if len(cfg.AllowedSenderUserIDs) != 2 || cfg.AllowedSenderUserIDs[0] != "user_a" || cfg.AllowedSenderUserIDs[1] != "user_b" {
		t.Fatalf("unexpected allowed senders %+v", cfg.AllowedSenderUserIDs)
	}
	if len(cfg.BlockedSenderUserIDs) != 2 || cfg.BlockedSenderUserIDs[0] != "user_x" || cfg.BlockedSenderUserIDs[1] != "user_y" {
		t.Fatalf("unexpected blocked senders %+v", cfg.BlockedSenderUserIDs)
	}
	if cfg.TriggerMode != TriggerModeManual || cfg.UnreadTimeoutSeconds != 30 {
		t.Fatalf("unexpected trigger config %+v", cfg)
	}
	enabled := false
	cfg = ApplyUserConfigPatch(cfg, UserConfigPatch{Enabled: &enabled})
	if cfg.Enabled || cfg.ReplyText != "new fallback" || cfg.Prompt != "use short sentences" || cfg.ReplyCooldownSeconds != 90 {
		t.Fatalf("unexpected disabled config %+v", cfg)
	}
	tooLargeCooldown := int64(90000)
	cfg = ApplyUserConfigPatch(cfg, UserConfigPatch{ReplyCooldownSeconds: &tooLargeCooldown})
	if cfg.ReplyCooldownSeconds != 86400 {
		t.Fatalf("expected cooldown to be capped, got %+v", cfg)
	}
}

func TestBuildReplyExWithTrace(t *testing.T) {
	ex, err := BuildReplyExWithSourceTraceAndText(imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{
			SendID:      "user_a",
			ServerMsgID: "server_msg",
			ClientMsgID: "client_msg",
		},
		RecvID: "user_b",
	}, time.UnixMilli(123), ReplySourceHTTPGenerator, &ReplyTrace{
		Source:         "orange_dispatcher",
		ProtocolSource: "openclaw_channel_tool",
		FinalizeSource: "openclaw_channel_tool",
		Metadata: map[string]any{
			"agentId":       "openim1",
			"workspacePath": "/tmp/sandboxes/openim1/openim/digital_twin/user_b",
		},
	}, "hello from twin")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(ex), &root); err != nil {
		t.Fatal(err)
	}
	trace, ok := root[TraceKey].(map[string]any)
	if !ok {
		t.Fatalf("expected trace in ex, got %v", root[TraceKey])
	}
	if trace["protocolSource"] != "openclaw_channel_tool" || trace["finalizeSource"] != "openclaw_channel_tool" {
		t.Fatalf("unexpected trace %+v", trace)
	}
	metadata, ok := trace["metadata"].(map[string]any)
	if !ok || metadata["agentId"] != "openim1" {
		t.Fatalf("unexpected metadata %+v", trace["metadata"])
	}
	if root["replyText"] != "hello from twin" {
		t.Fatalf("expected replyText in ex, got %v", root["replyText"])
	}
}
