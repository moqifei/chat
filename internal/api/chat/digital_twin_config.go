package chat

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/digitaltwin"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/apiresp"
	"github.com/openimsdk/tools/errs"
)

type digitalTwinConfigReq struct {
	Enabled              *bool                      `json:"enabled"`
	ReplyText            *string                    `json:"replyText"`
	Prompt               *string                    `json:"prompt"`
	ReplyCooldownSeconds *int64                     `json:"replyCooldownSeconds"`
	ReplySchedule        *digitaltwin.ReplySchedule `json:"replySchedule"`
	AllowedSenderUserIDs *[]string                  `json:"allowedSenderUserIDs"`
	BlockedSenderUserIDs *[]string                  `json:"blockedSenderUserIDs"`
	TriggerMode          *string                    `json:"triggerMode"`
	UnreadTimeoutSeconds *int64                     `json:"unreadTimeoutSeconds"`
}

type digitalTwinConfigResp struct {
	UserID string                 `json:"userID"`
	Config digitaltwin.UserConfig `json:"config"`
}

type digitalTwinReplyListReq struct {
	Limit           int64  `json:"limit"`
	ReviewStatus    string `json:"reviewStatus"`
	BeforeCreatedAt int64  `json:"beforeCreatedAt"`
	SenderUserID    string `json:"senderUserID"`
}

type digitalTwinReplyListResp struct {
	UserID     string                         `json:"userID"`
	Records    []digitaltwin.ReplyRecord      `json:"records"`
	HasMore    bool                           `json:"hasMore"`
	NextCursor int64                          `json:"nextCursor,omitempty"`
	Summary    digitaltwin.ReplyRecordSummary `json:"summary"`
}

type digitalTwinReplyReviewReq struct {
	OperationID string `json:"operationID"`
	Status      string `json:"status"`
	Note        string `json:"note"`
}

type digitalTwinReplyReviewResp struct {
	UserID      string `json:"userID"`
	OperationID string `json:"operationID"`
	Status      string `json:"status"`
}

type digitalTwinUnreadTimeoutSummaryResp struct {
	UserID  string                               `json:"userID"`
	Summary digitaltwin.UnreadTimeoutTaskSummary `json:"summary"`
}

type digitalTwinOverviewResp struct {
	UserID               string                               `json:"userID"`
	Config               digitaltwin.UserConfig               `json:"config"`
	ReplySummary         digitaltwin.ReplyRecordSummary       `json:"replySummary"`
	UnreadTimeoutSummary digitaltwin.UnreadTimeoutTaskSummary `json:"unreadTimeoutSummary"`
	LatestReplies        []digitaltwin.ReplyRecord            `json:"latestReplies"`
}

type digitalTwinSkillGenerateReq struct {
	SkillName   string `json:"skillName"`
	Description string `json:"description"`
}

type digitalTwinSkillGenerateResp struct {
	UserID    string         `json:"userID"`
	SkillName string         `json:"skillName"`
	SkillPath string         `json:"skillPath"`
	Source    string         `json:"source,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type digitalTwinSkillListResp struct {
	UserID string                     `json:"userID"`
	Skills []digitaltwin.SkillSummary `json:"skills"`
}

type digitalTwinSkillDeleteReq struct {
	SkillName string `json:"skillName"`
}

type digitalTwinSkillDeleteResp struct {
	UserID    string `json:"userID"`
	SkillName string `json:"skillName"`
	Deleted   bool   `json:"deleted"`
	SkillPath string `json:"skillPath"`
}

func (o *Api) GetDigitalTwinConfig(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	user, err := o.getIMUserInfoWithAdminToken(c, userID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	cfg, ok, err := digitaltwin.ParseUserConfigFromEx(user.Ex)
	if err != nil {
		apiresp.GinError(c, errs.ErrArgs.WrapMsg("invalid user ex: "+err.Error()))
		return
	}
	if !ok {
		cfg, ok, err = digitaltwin.LoadUserConfigFromStore(userID)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if !ok {
			cfg = digitaltwin.UserConfig{Version: 1}
		}
	}
	apiresp.GinSuccess(c, digitalTwinConfigResp{
		UserID: userID,
		Config: cfg,
	})
}

func (o *Api) UpdateDigitalTwinConfig(c *gin.Context) {
	var req digitalTwinConfigReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	userID := mctx.GetOpUserID(c)

	user, err := o.getIMUserInfoWithAdminToken(c, userID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	cfg, ok, err := digitaltwin.ParseUserConfigFromEx(user.Ex)
	if err != nil {
		apiresp.GinError(c, errs.ErrArgs.WrapMsg("invalid user ex: "+err.Error()))
		return
	}
	if !ok {
		cfg, ok, err = digitaltwin.LoadUserConfigFromStore(userID)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if !ok {
			cfg = digitaltwin.UserConfig{Version: 1}
		}
	}
	now := time.Now()
	cfg = digitaltwin.ApplyUserConfigPatch(cfg, digitaltwin.UserConfigPatch{
		Enabled:              req.Enabled,
		ReplyText:            req.ReplyText,
		Prompt:               req.Prompt,
		ReplyCooldownSeconds: req.ReplyCooldownSeconds,
		ReplySchedule:        req.ReplySchedule,
		AllowedSenderUserIDs: req.AllowedSenderUserIDs,
		BlockedSenderUserIDs: req.BlockedSenderUserIDs,
		TriggerMode:          req.TriggerMode,
		UnreadTimeoutSeconds: req.UnreadTimeoutSeconds,
	})
	cfg.Version = 1
	cfg.UpdatedAt = now.UnixMilli()
	ex, err := digitaltwin.MergeUserConfigToEx(user.Ex, cfg, now)
	if err != nil {
		apiresp.GinError(c, errs.ErrArgs.WrapMsg("invalid user ex: "+err.Error()))
		return
	}

	token, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	ctx := mctx.WithApiToken(c, token)
	if err := o.imApiCaller.UpdateUserEx(ctx, userID, ex); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := digitaltwin.SaveUserConfigToStore(userID, cfg, now); err != nil {
		apiresp.GinError(c, err)
		return
	}

	cfg, _, err = digitaltwin.ParseUserConfigFromEx(ex)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	apiresp.GinSuccess(c, digitalTwinConfigResp{
		UserID: userID,
		Config: cfg,
	})
}

func (o *Api) ListDigitalTwinReplies(c *gin.Context) {
	var req digitalTwinReplyListReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	userID := mctx.GetOpUserID(c)
	page, err := digitaltwin.ListReplyRecords(c, userID, req.Limit, req.ReviewStatus, req.BeforeCreatedAt, req.SenderUserID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, digitalTwinReplyListResp{
		UserID:     userID,
		Records:    page.Records,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
		Summary:    page.Summary,
	})
}

func (o *Api) ReviewDigitalTwinReply(c *gin.Context) {
	var req digitalTwinReplyReviewReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	status := digitaltwin.NormalizeReviewStatus(req.Status)
	if req.OperationID == "" || status == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("invalid operationID or status").Wrap())
		return
	}
	userID := mctx.GetOpUserID(c)
	if err := digitaltwin.ReviewReplyRecord(c, userID, req.OperationID, status, req.Note, time.Now()); err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, digitalTwinReplyReviewResp{
		UserID:      userID,
		OperationID: req.OperationID,
		Status:      status,
	})
}

func (o *Api) GetDigitalTwinUnreadTimeoutSummary(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	summary, err := digitaltwin.CountUnreadTimeoutTasks(c, userID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, digitalTwinUnreadTimeoutSummaryResp{
		UserID:  userID,
		Summary: summary,
	})
}

func (o *Api) GetDigitalTwinOverview(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	user, err := o.getIMUserInfoWithAdminToken(c, userID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	cfg, ok, err := digitaltwin.ParseUserConfigFromEx(user.Ex)
	if err != nil {
		apiresp.GinError(c, errs.ErrArgs.WrapMsg("invalid user ex: "+err.Error()))
		return
	}
	if !ok {
		cfg, ok, err = digitaltwin.LoadUserConfigFromStore(userID)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if !ok {
			cfg = digitaltwin.UserConfig{Version: 1}
		}
	}

	replyPage, err := digitaltwin.ListReplyRecords(c, userID, 3, "", 0, "")
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	unreadSummary, err := digitaltwin.CountUnreadTimeoutTasks(c, userID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	apiresp.GinSuccess(c, digitalTwinOverviewResp{
		UserID:               userID,
		Config:               cfg,
		ReplySummary:         replyPage.Summary,
		UnreadTimeoutSummary: unreadSummary,
		LatestReplies:        replyPage.Records,
	})
}

func (o *Api) GenerateDigitalTwinSkill(c *gin.Context) {
	var req digitalTwinSkillGenerateReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	skillName := digitaltwin.NormalizeSkillName(req.SkillName)
	description := strings.TrimSpace(req.Description)
	if skillName == "" || len(skillName) > 64 || description == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("invalid skillName or description").Wrap())
		return
	}
	userID := mctx.GetOpUserID(c)
	genCfg := digitaltwin.LoadSkillGeneratorConfigFromEnv()
	if genCfg.URL == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("digital twin skill generator url is not configured").Wrap())
		return
	}
	genResp, err := digitaltwin.CallHTTPSkillGenerator(c, http.DefaultClient, genCfg, digitaltwin.SkillGeneratorRequest{
		OwnerUserID: userID,
		SkillName:   skillName,
		Description: description,
		OperationID: c.GetHeader("operationID"),
	})
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	if genResp.SkillName == "" {
		genResp.SkillName = skillName
	}
	apiresp.GinSuccess(c, digitalTwinSkillGenerateResp{
		UserID:    userID,
		SkillName: genResp.SkillName,
		SkillPath: genResp.SkillPath,
		Source:    genResp.Source,
		Metadata:  genResp.Metadata,
	})
}

func (o *Api) ListDigitalTwinSkills(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	genCfg := digitaltwin.LoadSkillListConfigFromEnv()
	if genCfg.URL == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("digital twin skill generator url is not configured").Wrap())
		return
	}
	genResp, err := digitaltwin.CallHTTPSkillList(c, http.DefaultClient, genCfg, digitaltwin.SkillListRequest{
		OwnerUserID: userID,
		OperationID: c.GetHeader("operationID"),
	})
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	apiresp.GinSuccess(c, digitalTwinSkillListResp{
		UserID: userID,
		Skills: genResp.Skills,
	})
}

func (o *Api) DeleteDigitalTwinSkill(c *gin.Context) {
	var req digitalTwinSkillDeleteReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	skillName := digitaltwin.NormalizeSkillName(req.SkillName)
	if skillName == "" || skillName == "builtin" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("invalid skillName").Wrap())
		return
	}
	userID := mctx.GetOpUserID(c)
	genCfg := digitaltwin.LoadSkillDeleteConfigFromEnv()
	if genCfg.URL == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("digital twin skill generator url is not configured").Wrap())
		return
	}
	genResp, err := digitaltwin.CallHTTPSkillDelete(c, http.DefaultClient, genCfg, digitaltwin.SkillDeleteRequest{
		OwnerUserID: userID,
		SkillName:   skillName,
		OperationID: c.GetHeader("operationID"),
	})
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	apiresp.GinSuccess(c, digitalTwinSkillDeleteResp{
		UserID:    userID,
		SkillName: genResp.SkillName,
		Deleted:   genResp.Deleted,
		SkillPath: genResp.SkillPath,
	})
}

func (o *Api) getIMUserInfoWithAdminToken(c *gin.Context, userID string) (*sdkws.UserInfo, error) {
	token, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		return nil, err
	}
	user, err := o.imApiCaller.GetUserInfo(mctx.WithApiToken(c, token), userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}
