package bot

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openimsdk/chat/pkg/botstruct"
	"github.com/openimsdk/chat/pkg/common/imwebhook"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/digitaltwin"
	"github.com/openimsdk/tools/apiresp"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
)

func (o *Api) afterSendSingleMsgDigitalTwin(c *gin.Context, req imwebhook.CallbackAfterSendSingleMsgReq) {
	cfg, source, ok := o.loadDigitalTwinConfig(c, req.RecvID)
	if !ok {
		apiresp.GinSuccess(c, struct {
			digitaltwin.Decision
			ConfigSource string                  `json:"configSource"`
			ReplySource  string                  `json:"replySource,omitempty"`
			Trace        *digitaltwin.ReplyTrace `json:"trace,omitempty"`
			SendID       string                  `json:"sendID,omitempty"`
			RecvID       string                  `json:"recvID,omitempty"`
		}{
			Decision:     digitaltwin.Decision{Reason: "config_load_failed"},
			ConfigSource: source,
			SendID:       req.RecvID,
			RecvID:       req.SendID,
		})
		return
	}
	decision := digitaltwin.ShouldReply(cfg, req)
	log.ZInfo(c, "digital twin after send decision",
		"entry", "bot_api",
		"callbackCommand", req.CallbackCommand,
		"operationID", req.OperationID,
		"sendID", req.SendID,
		"recvID", req.RecvID,
		"senderPlatformID", req.SenderPlatformID,
		"sessionType", req.SessionType,
		"msgFrom", req.MsgFrom,
		"contentType", req.ContentType,
		"senderIsAgentByPlatform", botstruct.IsAgentPlatformID(req.SenderPlatformID),
		"sendIsAgentByPrefix", botstruct.IsAgentUserID(req.SendID),
		"recvIsAgentByPrefix", botstruct.IsAgentUserID(req.RecvID),
		"isDigitalTwinEx", digitaltwin.IsDigitalTwinEx(req.Ex),
		"configSource", source,
		"configEnabled", cfg.Enabled,
		"configAllowAll", cfg.AllowAll,
		"triggerMode", cfg.TriggerMode,
		"decisionHandled", decision.Handled,
		"decisionReason", decision.Reason,
	)
	resp := struct {
		digitaltwin.Decision
		ConfigSource string                  `json:"configSource"`
		ReplySource  string                  `json:"replySource,omitempty"`
		Trace        *digitaltwin.ReplyTrace `json:"trace,omitempty"`
		SendID       string                  `json:"sendID,omitempty"`
		RecvID       string                  `json:"recvID,omitempty"`
	}{
		Decision:     decision,
		ConfigSource: source,
		SendID:       req.RecvID,
		RecvID:       req.SendID,
	}
	if !decision.Handled {
		apiresp.GinSuccess(c, resp)
		return
	}
	detachedCtx := context.WithoutCancel(c)
	now := time.Now()
	key, ok := c.GetQuery(botstruct.Key)
	if !ok {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("missing key in query").Wrap())
		return
	}
	if cfg.TriggerMode == digitaltwin.TriggerModeUnreadTimeout {
		scheduleResult := digitaltwin.ScheduleUnreadTimeoutReply(detachedCtx, req, cfg, key, func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
			return o.executeUnreadTimeoutDigitalTwin(ctx, key, req)
		})
		resp.Decision = digitaltwin.Decision{Reason: "trigger_mode_unread_timeout_schedule_failed"}
		if scheduleResult.Scheduled {
			resp.Decision = digitaltwin.Decision{Reason: "trigger_mode_unread_timeout_scheduled"}
			if scheduleResult.Replaced {
				resp.Decision = digitaltwin.Decision{Reason: "trigger_mode_unread_timeout_rescheduled"}
			}
		}
		apiresp.GinSuccess(c, resp)
		return
	}
	triggerModeDecision := digitaltwin.ShouldSkipByTriggerMode(cfg)
	if !triggerModeDecision.Handled {
		resp.Decision = triggerModeDecision
		apiresp.GinSuccess(c, resp)
		return
	}
	scheduleDecision := digitaltwin.ShouldSkipBySchedule(cfg, now)
	if !scheduleDecision.Handled {
		resp.Decision = scheduleDecision
		apiresp.GinSuccess(c, resp)
		return
	}
	cooldownDecision, err := digitaltwin.ShouldSkipByCooldown(detachedCtx, cfg, req, now)
	if err != nil {
		log.ZWarn(detachedCtx, "digital twin cooldown check failed", err,
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
		)
	} else if !cooldownDecision.Handled {
		resp.Decision = cooldownDecision
		apiresp.GinSuccess(c, resp)
		return
	}
	replyPlan, err := digitaltwin.SendReply(detachedCtx, o.imApiCaller, key, req, cfg, source, now)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp.ReplySource = replyPlan.Source
	resp.Trace = replyPlan.Trace
	apiresp.GinSuccess(c, resp)
}

func (o *Api) executeUnreadTimeoutDigitalTwin(ctx context.Context, key string, req imwebhook.CallbackAfterSendSingleMsgReq) error {
	cfg, source, ok := o.loadDigitalTwinConfig(ctx, req.RecvID)
	if !ok {
		return nil
	}
	if cfg.TriggerMode != digitaltwin.TriggerModeUnreadTimeout {
		log.ZInfo(ctx, "digital twin unread timeout skipped by trigger mode",
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
			"triggerMode", cfg.TriggerMode,
		)
		return nil
	}
	decision := digitaltwin.ShouldReply(cfg, req)
	if !decision.Handled {
		log.ZInfo(ctx, "digital twin unread timeout skipped by base decision",
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
			"reason", decision.Reason,
		)
		return nil
	}
	log.ZInfo(ctx, "digital twin unread timeout base decision passed",
		"entry", "bot_api",
		"ownerUserID", req.RecvID,
		"senderUserID", req.SendID,
		"operationID", req.OperationID,
		"senderPlatformID", req.SenderPlatformID,
		"sessionType", req.SessionType,
		"msgFrom", req.MsgFrom,
		"senderIsAgentByPlatform", botstruct.IsAgentPlatformID(req.SenderPlatformID),
		"sendIsAgentByPrefix", botstruct.IsAgentUserID(req.SendID),
		"recvIsAgentByPrefix", botstruct.IsAgentUserID(req.RecvID),
		"configSource", source,
		"triggerMode", cfg.TriggerMode,
	)
	now := time.Now()
	scheduleDecision := digitaltwin.ShouldSkipBySchedule(cfg, now)
	if !scheduleDecision.Handled {
		log.ZInfo(ctx, "digital twin unread timeout skipped by schedule",
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
			"reason", scheduleDecision.Reason,
		)
		return nil
	}
	cooldownDecision, err := digitaltwin.ShouldSkipByCooldown(ctx, cfg, req, now)
	if err != nil {
		log.ZWarn(ctx, "digital twin unread timeout cooldown check failed", err,
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
		)
	} else if !cooldownDecision.Handled {
		log.ZInfo(ctx, "digital twin unread timeout skipped by cooldown",
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
			"reason", cooldownDecision.Reason,
		)
		return nil
	}
	_, err = digitaltwin.SendReply(ctx, o.imApiCaller, key, req, cfg, source, now)
	return err
}

func (o *Api) loadDigitalTwinConfig(c context.Context, userID string) (digitaltwin.Config, string, bool) {
	envCfg := digitaltwin.LoadConfigFromEnv()
	token, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		return envCfg, "env", true
	}
	user, err := o.imApiCaller.GetUserInfo(mctx.WithApiToken(c, token), userID)
	if err != nil {
		return envCfg, "env", true
	}
	cfg, ok, err := digitaltwin.LoadConfigFromUserEx(userID, user.Ex)
	if err != nil {
		return envCfg, "env_user_ex_invalid", true
	}
	if ok {
		return cfg, "user_ex", true
	}
	storeCfg, ok, storeSource, err := digitaltwin.LoadUserConfigFromStoreWithSource(userID)
	if err != nil {
		return envCfg, storeSource + "_invalid", true
	}
	if ok {
		return digitaltwin.ConfigFromUserConfig(userID, storeCfg), storeSource, true
	}
	return envCfg, "env", true
}
