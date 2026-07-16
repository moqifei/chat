package chat

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/digitaltwin"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/apiresp"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
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

// Async skill-generation accept response (202)
type digitalTwinSkillGenerateAcceptResp struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Skill generation task status query response
type digitalTwinSkillTaskStatusResp struct {
	ID           string                 `json:"id"`
	Status       string                 `json:"status"`
	OwnerUserID  string                 `json:"owner_user_id"`
	SkillName    string                 `json:"skill_name"`
	SkillPath    string                 `json:"skill_path,omitempty"`
	SkillContent string                 `json:"skill_content,omitempty"`
	Source       string                 `json:"source,omitempty"`
	Error        string                 `json:"error,omitempty"`
	CreatedAt    string                 `json:"created_at"`
	CompletedAt  string                 `json:"completed_at,omitempty"`
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

type digitalTwinSkillGetReq struct {
	SkillName string `json:"skillName"`
}

type digitalTwinSkillGetResp struct {
	UserID      string `json:"userID"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SkillPath   string `json:"skillPath"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
	Content     string `json:"content"`
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
	// Submit async request — Orange returns 202 with task_id immediately.
	acceptResp, err := digitaltwin.CallHTTPSkillGenerateSubmit(c, http.DefaultClient, genCfg, digitaltwin.SkillGeneratorRequest{
		OwnerUserID: userID,
		SkillName:   skillName,
		Description: description,
		OperationID: c.GetHeader("operationID"),
	})
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	apiresp.GinSuccess(c, digitalTwinSkillGenerateAcceptResp{
		TaskID:  acceptResp.TaskID,
		Status:  acceptResp.Status,
		Message: acceptResp.Message,
	})
}

func (o *Api) GetDigitalTwinSkillGenerateTaskStatus(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("task_id is required").Wrap())
		return
	}
	genCfg := digitaltwin.LoadSkillGeneratorConfigFromEnv()
	if genCfg.URL == "" {
		log.ZWarn(c, "digital twin skill generator url is not configured", nil, "taskID", taskID)
		apiresp.GinError(c, errs.ErrArgs.WithDetail("digital twin skill generator url is not configured").Wrap())
		return
	}
	taskCfg := digitaltwin.LoadSkillTaskStatusConfigFromEnv(genCfg.URL)
	if taskCfg.URL == "" {
		log.ZWarn(c, "digital twin skill task status url is empty", nil, "taskID", taskID, "genURL", genCfg.URL)
		apiresp.GinError(c, errs.ErrArgs.WithDetail("digital twin skill generator url is not configured").Wrap())
		return
	}
	log.ZInfo(c, "digital twin skill task status query", "taskID", taskID, "url", taskCfg.URL+taskID)
	task, err := digitaltwin.CallHTTPSkillGenerateTaskStatus(c, http.DefaultClient, taskCfg, taskID)
	if err != nil {
		log.ZError(c, "digital twin skill task status query failed", err, "taskID", taskID, "url", taskCfg.URL+taskID)
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	apiresp.GinSuccess(c, digitalTwinSkillTaskStatusResp{
		ID:           task.ID,
		Status:       string(task.Status),
		OwnerUserID:  task.OwnerUserID,
		SkillName:    task.SkillName,
		SkillPath:    task.SkillPath,
		SkillContent: task.SkillContent,
		Source:       task.Source,
		Error:        task.Error,
		CreatedAt:    task.CreatedAt,
		CompletedAt:  task.CompletedAt,
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

func (o *Api) GetDigitalTwinSkill(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	var req digitalTwinSkillGetReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	skillName := digitaltwin.NormalizeSkillName(req.SkillName)
	if skillName == "" || skillName == "builtin" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("invalid skillName").Wrap())
		return
	}
	genCfg := digitaltwin.LoadSkillGetConfigFromEnv()
	if genCfg.URL == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("digital twin skill generator url is not configured").Wrap())
		return
	}
	genResp, err := digitaltwin.CallHTTPSkillGet(c, http.DefaultClient, genCfg, digitaltwin.SkillGetRequest{
		OwnerUserID: userID,
		SkillName:   skillName,
		OperationID: c.GetHeader("operationID"),
	})
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	apiresp.GinSuccess(c, digitalTwinSkillGetResp{
		UserID:      userID,
		Name:        genResp.Name,
		Description: genResp.Description,
		SkillPath:   genResp.SkillPath,
		UpdatedAt:   genResp.UpdatedAt,
		Content:     genResp.Content,
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

// -------------------------------------------------------------------
// SKILL Plaza (企业技能广场) — list + download & install
// -------------------------------------------------------------------

type plazaSkillListResp struct {
	Skills []plazaSkillItemResp `json:"skills"`
}

type plazaSkillItemResp struct {
	Name        string `json:"name"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Downloads   int    `json:"downloads"`
	SkillType   string `json:"skill_type"`
	ThumbsUps   int    `json:"thumbs_ups"`
}

type plazaInstallReq struct {
	SkillName string `json:"skillName"`
}

type plazaInstallResp struct {
	UserID   string `json:"userID"`
	SkillName string `json:"skillName"`
	Installed bool  `json:"installed"`
	Message  string `json:"message,omitempty"`
}

// ListPlazaSkills proxies GET /api/get_all_skill to the external SKILL plaza.
// Returns a flat array of skills for frontend consumption.
func (o *Api) ListPlazaSkills(c *gin.Context) {
	plazaCfg := digitaltwin.LoadSkillPlazaConfigFromEnv()
	if plazaCfg.URL == "" {
		apiresp.GinError(c, errs.ErrArgs.WithDetail("skill plaza url not configured (set OPENIM_DIGITAL_TWIN_SKILL_PLAZA_URL)").Wrap())
		return
	}
	catalog, err := digitaltwin.CallHTTPPlazaSkillList(c, http.DefaultClient, plazaCfg)
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	items := make([]plazaSkillItemResp, 0, len(catalog))
	for name, item := range catalog {
		items = append(items, plazaSkillItemResp{
			Name:        name,
			Author:      item.Author,
			Description: item.Description,
			Downloads:   item.Downloads,
			SkillType:   item.SkillType,
			ThumbsUps:   item.ThumbsUps,
		})
	}
	apiresp.GinSuccess(c, plazaSkillListResp{Skills: items})
}

// DownloadAndInstallPlazaSkill downloads a skill zip from the plaza and
// installs it via Orange's admin upload API so it lands in the digital
// twin's personal skills directory.
func (o *Api) DownloadAndInstallPlazaSkill(c *gin.Context) {
	var req plazaInstallReq
	if err := c.BindJSON(&req); err != nil {
		log.ZError(c, "[plaza-install] bind request failed", err)
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	skillName := digitaltwin.NormalizeSkillName(req.SkillName)
	if skillName == "" {
		log.ZError(c, "[plaza-install] invalid skillName", nil, "raw", req.SkillName)
		apiresp.GinError(c, errs.ErrArgs.WithDetail("invalid skillName").Wrap())
		return
	}
	userID := mctx.GetOpUserID(c)
	log.ZInfo(c, "[plaza-install] START",
		"userID", userID, "skillName", skillName)

	// 1. Download zip from plaza
	plazaCfg := digitaltwin.LoadSkillPlazaConfigFromEnv()
	if plazaCfg.URL == "" {
		log.ZError(c, "[plaza-install] plaza URL not configured", nil)
		apiresp.GinError(c, errs.ErrArgs.WithDetail("skill plaza url not configured").Wrap())
		return
	}
	downloadURL := plazaCfg.URL + digitaltwin.PlazaDownloadSkillPath
	log.ZInfo(c, "[plaza-install] downloading zip from plaza",
		"downloadURL", downloadURL, "skillName", skillName)
	rawZip, err := digitaltwin.CallHTTPPlazaDownload(c, http.DefaultClient, plazaCfg, skillName)
	if err != nil {
		log.ZError(c, "[plaza-install] download from plaza failed", err,
			"downloadURL", downloadURL, "skillName", skillName)
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(fmt.Sprintf("download from plaza failed: %v", err)).Wrap())
		return
	}
	log.ZInfo(c, "[plaza-install] downloaded zip bytes",
		"sizeBytes", len(rawZip), "skillName", skillName)
	if len(rawZip) == 0 {
		log.ZError(c, "[plaza-install] empty zip from plaza", nil,
			"downloadURL", downloadURL, "skillName", skillName)
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail("empty zip from plaza").Wrap())
		return
	}

	// 2. Normalize the plaza zip into a single <skillName>/ directory so that
	//    *both* plaza packaging layouts are compatible:
	//      - subdir layout: <any-name>/SKILL.md (+ extra files)  -> strip the top dir
	//      - flat  layout:  SKILL.md (+ extra files) at the root  -> keep as-is
	//    Either way the result installs all files into the digital twin's
	//    skills/<skillName> directory under the expected skill name.
	zipBytes, err := normalizePlazaSkillZip(skillName, rawZip)
	if err != nil {
		log.ZError(c, "[plaza-install] normalize zip failed", err,
			"skillName", skillName)
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(fmt.Sprintf("normalize plaza zip failed: %v", err)).Wrap())
		return
	}
	log.ZInfo(c, "[plaza-install] normalized zip",
		"normalizedSizeBytes", len(zipBytes), "skillName", skillName)

	// 3. Upload zip to Orange's skill upload API
	genCfg := digitaltwin.LoadSkillGeneratorConfigFromEnv()
	if genCfg.URL == "" {
		log.ZError(c, "[plaza-install] orange URL not configured", nil,
			"userID", userID, "skillName", skillName)
		apiresp.GinError(c, errs.ErrArgs.WithDetail("orange skill generator url not configured").Wrap())
		return
	}
	// Derive Orange base URL from generator URL (strip path suffix)
	orangeBaseURL := genCfg.URL
	if idx := strings.Index(orangeBaseURL, "/api/v1"); idx >= 0 {
		orangeBaseURL = orangeBaseURL[:idx]
	}
	// Install into the owner's digital-twin sandbox workspace
	// (.../digital_twin/<ownerUserID>/skills), not the shared agent workspace.
	uploadURL := fmt.Sprintf("%s/api/v1/digital-twin/skills/install", orangeBaseURL)
	log.ZInfo(c, "[plaza-install] uploading zip to orange digital-twin sandbox",
		"uploadURL", uploadURL, "ownerUserID", userID, "skillName", skillName,
		"zipSizeBytes", len(zipBytes), "hasToken", genCfg.Token != "")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("ownerUserID", userID); err != nil {
		log.ZError(c, "[plaza-install] write ownerUserID field failed", err,
			"ownerUserID", userID, "skillName", skillName)
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail("write multipart ownerUserID field failed").Wrap())
		return
	}
	part, _ := writer.CreateFormFile("file", skillName+".zip")
	part.Write(zipBytes)
	writer.Close()

	httpReq, err := http.NewRequestWithContext(c, http.MethodPost, uploadURL, body)
	if err != nil {
		log.ZError(c, "[plaza-install] create upload request failed", err,
			"uploadURL", uploadURL)
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(err.Error()).Wrap())
		return
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	if genCfg.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+genCfg.Token)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		log.ZError(c, "[plaza-install] HTTP upload to orange FAILED", err,
			"uploadURL", uploadURL, "ownerUserID", userID)
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(fmt.Sprintf("upload to orange failed: %v", err)).Wrap())
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.ZInfo(c, "[plaza-install] orange upload response",
		"status", resp.StatusCode, "bodyLen", len(respBody),
		"bodyPreview", string(respBody)[:min(500, len(respBody))])

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		log.ZError(c, "[plaza-install] orange returned non-2xx status", nil,
			"status", resp.StatusCode, "body", string(respBody))
		apiresp.GinError(c, errs.ErrInternalServer.WithDetail(
			fmt.Sprintf("orange upload status %d: %s", resp.StatusCode, string(respBody))).Wrap())
		return
	}

	log.ZDebug(c, "plaza skill installed via orange",
		"userID", userID, "skillName", skillName, "status", resp.StatusCode)

	apiresp.GinSuccess(c, plazaInstallResp{
		UserID:    userID,
		SkillName: skillName,
		Installed: true,
		Message:   "skill installed successfully",
	})
}

// normalizePlazaSkillZip rewrites a skill zip downloaded from the plaza into a
// single top-level directory named after the skill. This makes both plaza zip
// layouts compatible with Orange's admin upload endpoint:
//   - subdir layout: <any-name>/SKILL.md (+ extra files) -> the common top dir is stripped
//   - flat  layout:  SKILL.md (+ extra files) at the archive root -> kept as-is
//
// In either case every entry ends up under "<skillName>/", so Orange installs
// all files into the digital twin's skills/<skillName> directory and the skill
// id always equals the plaza skill name.
func normalizePlazaSkillZip(skillName string, raw []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}

	stripPrefix, hasCommon := commonTopDir(reader.File)

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(f.Name, "/")
		if hasCommon && strings.HasPrefix(rel, stripPrefix) {
			rel = strings.TrimPrefix(rel, stripPrefix)
		}
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		target := skillName + "/" + rel

		rc, err := f.Open()
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		w, err := writer.Create(target)
		if err != nil {
			_ = rc.Close()
			_ = writer.Close()
			return nil, err
		}
		if _, err := io.Copy(w, rc); err != nil {
			_ = rc.Close()
			_ = writer.Close()
			return nil, err
		}
		_ = rc.Close()
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// commonTopDir returns the single common top-level directory that contains all
// files in the zip, if exactly one such directory exists and no files sit at
// the archive root. This distinguishes the subdir layout from the flat layout.
func commonTopDir(files []*zip.File) (string, bool) {
	topDirs := map[string]struct{}{}
	rootFile := false
	for _, f := range files {
		name := strings.TrimPrefix(f.Name, "/")
		if name == "" {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 1 {
			if !f.FileInfo().IsDir() {
				rootFile = true
			}
		} else {
			topDirs[parts[0]] = struct{}{}
		}
	}
	if rootFile || len(topDirs) != 1 {
		return "", false
	}
	for dir := range topDirs {
		return dir + "/", true
	}
	return "", false
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
