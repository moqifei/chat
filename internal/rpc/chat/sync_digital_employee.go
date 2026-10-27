package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openimsdk/chat/pkg/common/config"
	"github.com/openimsdk/chat/pkg/common/constant"
	"github.com/openimsdk/chat/pkg/common/db/dbutil"
	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/digitaltwin"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/log"
)

const (
	digitalEmployeeListPath = "/digital-employees/employeelist"
	digitalEmployeeBindPath = "/digital-employees/external-bind"
)

type digitalEmployee struct {
	EmployeeCode string `json:"employeeCode"`
	EmployeeName string `json:"employeeName"`
	Status       string `json:"status"`
}

type digitalEmployeeSnapshotResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		List []digitalEmployee `json:"list"`
	} `json:"data"`
}

type digitalEmployeeSnapshotPlan struct {
	Active   []digitalEmployee
	Inactive []digitalEmployee
}

func planDigitalEmployeeSnapshot(employees []digitalEmployee) (digitalEmployeeSnapshotPlan, error) {
	plan := digitalEmployeeSnapshotPlan{}
	seen := make(map[string]struct{}, len(employees))
	for _, employee := range employees {
		code := strings.TrimSpace(employee.EmployeeCode)
		name := strings.TrimSpace(employee.EmployeeName)
		if code == "" || name == "" {
			return digitalEmployeeSnapshotPlan{}, fmt.Errorf("employeeCode and employeeName are required")
		}
		if _, ok := seen[code]; ok {
			return digitalEmployeeSnapshotPlan{}, fmt.Errorf("duplicate employeeCode: %s", code)
		}
		seen[code] = struct{}{}
		employee.EmployeeCode = code
		employee.EmployeeName = name
		switch employee.Status {
		case "1":
			plan.Active = append(plan.Active, employee)
		case "2":
			plan.Inactive = append(plan.Inactive, employee)
		default:
			return digitalEmployeeSnapshotPlan{}, fmt.Errorf("invalid status for %s: %q", code, employee.Status)
		}
	}
	return plan, nil
}

func digitalEmployeeConfigDefaults(cfg config.DigitalEmployee) config.DigitalEmployee {
	if cfg.SysID == "" {
		cfg.SysID = "openIM"
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if cfg.DepartmentID == "" {
		cfg.DepartmentID = "OU=数字员工,OU=ZXBXUsers,DC=qa,DC=bx"
	}
	if cfg.DepartmentName == "" {
		cfg.DepartmentName = "数字员工"
	}
	if cfg.DefaultEmail == "" {
		cfg.DefaultEmail = "12345678901@aiehr.com"
	}
	if cfg.DefaultPhone == "" {
		cfg.DefaultPhone = "12345678901"
	}
	return cfg
}

func fetchDigitalEmployeeSnapshot(ctx context.Context, client *http.Client, cfg config.DigitalEmployee) ([]digitalEmployee, error) {
	cfg = digitalEmployeeConfigDefaults(cfg)
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("digital employee baseURL is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(struct {
		SysID string `json:"sysId"`
	}{SysID: cfg.SysID})
	if err != nil {
		return nil, err
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	url := strings.TrimRight(cfg.BaseURL, "/") + digitalEmployeeListPath
	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("digital employee list returned HTTP %d", resp.StatusCode)
	}
	var result digitalEmployeeSnapshotResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("digital employee list failed: code=%d message=%s", result.Code, result.Message)
	}
	return result.Data.List, nil
}

type digitalEmployeeBindItem struct {
	EmployeeCode       string `json:"employeeCode"`
	EmployeeName       string `json:"employeeName"`
	OpenIMEmployeeCode string `json:"openimEmployeeCode"`
}

func (o *chatSvr) bindDigitalEmployees(ctx context.Context, cfg config.DigitalEmployee, items []digitalEmployeeBindItem) error {
	if len(items) == 0 {
		return nil
	}
	body, err := json.Marshal(struct {
		EmployeeList []digitalEmployeeBindItem `json:"employeeList"`
	}{EmployeeList: items})
	if err != nil {
		return err
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	url := strings.TrimRight(cfg.BaseURL, "/") + digitalEmployeeBindPath
	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("digital employee bind returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("digital employee bind failed: code=%d message=%s", result.Code, result.Message)
	}
	return nil
}

func (o *chatSvr) startDigitalEmployeeSyncScheduler(ctx context.Context) {
	cfg := digitalEmployeeConfigDefaults(o.DigitalEmployeeConfig)
	if !cfg.Enable || strings.TrimSpace(cfg.BaseURL) == "" {
		log.ZInfo(ctx, "digital employee sync is disabled, skipping scheduler")
		return
	}
	log.ZInfo(ctx, "digital employee sync scheduler starting", "cron", cfg.Cron)
	go func() {
		time.Sleep(10 * time.Second)
		if err := o.runDigitalEmployeeSync(syncBackgroundCtx()); err != nil {
			log.ZError(syncBackgroundCtx(), "digital employee initial sync failed", err)
		}
	}()
	go func() {
		for {
			next := nextScheduleTime(cfg.Cron)
			delay := time.Until(next)
			log.ZInfo(syncBackgroundCtx(), "digital employee sync next scheduled", "next", next.Format(time.RFC3339), "delay", delay)
			time.Sleep(delay)
			if err := o.runDigitalEmployeeSync(syncBackgroundCtx()); err != nil {
				log.ZError(syncBackgroundCtx(), "digital employee scheduled sync failed", err)
			}
		}
	}()
}

func (o *chatSvr) runDigitalEmployeeSync(ctx context.Context) error {
	o.digitalEmployeeSyncMu.Lock()
	defer o.digitalEmployeeSyncMu.Unlock()

	cfg := digitalEmployeeConfigDefaults(o.DigitalEmployeeConfig)
	if !cfg.Enable {
		return nil
	}
	employees, err := fetchDigitalEmployeeSnapshot(ctx, http.DefaultClient, cfg)
	if err != nil {
		log.ZError(ctx, "digital employee sync: failed to fetch snapshot", err)
		return err
	}
	plan, err := planDigitalEmployeeSnapshot(employees)
	if err != nil {
		log.ZError(ctx, "digital employee sync: invalid snapshot", err)
		return err
	}

	now := time.Now()
	department := &chatdb.Department{
		DepartmentID: cfg.DepartmentID,
		Name:         cfg.DepartmentName,
		ParentID:     "",
		Level:        0,
		SyncTime:     now,
		CreateTime:   now,
	}
	if err := o.Database.GetDepartmentInterface().UpsertMany(ctx, []*chatdb.Department{department}); err != nil {
		return err
	}
	oldMembers, err := o.Database.GetDepartmentMemberInterface().FindByDepartmentIDs(ctx, []string{cfg.DepartmentID})
	if err != nil {
		return err
	}

	activeCodes := make(map[string]struct{}, len(plan.Active))
	activeMembers := make([]*chatdb.DepartmentMember, 0, len(plan.Active))
	bindItems := make([]digitalEmployeeBindItem, 0, len(plan.Active))
	for _, employee := range plan.Active {
		activeCodes[employee.EmployeeCode] = struct{}{}
		userID, err := o.ensureDigitalEmployeeUser(ctx, cfg, employee)
		if err != nil {
			return err
		}
		if err := o.updateDigitalEmployeeAttribute(ctx, cfg, userID, employee); err != nil {
			return err
		}
		member := &chatdb.DepartmentMember{
			DepartmentID: cfg.DepartmentID,
			UserID:       userID,
			Username:     employee.EmployeeCode,
			DisplayName:  employee.EmployeeName,
			Nickname:     employee.EmployeeName,
			Email:        cfg.DefaultEmail,
			Position:     "数字员工",
			Phone:        cfg.DefaultPhone,
			IsPrimary:    true,
			SyncTime:     now,
			CreateTime:   now,
		}
		activeMembers = append(activeMembers, member)
		bindItems = append(bindItems, digitalEmployeeBindItem{
			EmployeeCode:       employee.EmployeeCode,
			EmployeeName:       employee.EmployeeName,
			OpenIMEmployeeCode: userID,
		})
		if err := o.syncDigitalEmployeeToIM(ctx, member); err != nil {
			return err
		}
		if err := o.setDigitalTwinEnabled(ctx, userID, true); err != nil {
			return err
		}
	}

	for _, member := range oldMembers {
		if member.UserID == "" {
			continue
		}
		if _, ok := activeCodes[member.Username]; ok {
			continue
		}
		if err := o.setDigitalTwinEnabled(ctx, member.UserID, false); err != nil {
			return err
		}
	}

	if err := o.Database.GetDepartmentMemberInterface().DeleteByDepartmentID(ctx, cfg.DepartmentID); err != nil {
		return err
	}
	if len(activeMembers) > 0 {
		if err := o.Database.GetDepartmentMemberInterface().Create(ctx, activeMembers); err != nil {
			return err
		}
	}
	if err := o.Database.GetDepartmentInterface().UpdateMemberCount(ctx, cfg.DepartmentID, len(activeMembers)); err != nil {
		return err
	}
	if err := o.bindDigitalEmployees(ctx, cfg, bindItems); err != nil {
		log.ZError(ctx, "digital employee sync: external bind failed", err)
		return err
	}
	log.ZInfo(ctx, "digital employee sync: completed", "active", len(activeMembers), "inactive", len(plan.Inactive))
	return nil
}

func (o *chatSvr) ensureDigitalEmployeeUser(ctx context.Context, cfg config.DigitalEmployee, employee digitalEmployee) (string, error) {
	credentialAccount := BuildCredentialDigitalEmployee(employee.EmployeeCode)
	credential, err := o.Database.TakeCredentialByAccount(ctx, credentialAccount)
	if err == nil {
		return credential.UserID, nil
	}
	if !dbutil.IsDBNotFound(err) {
		return "", err
	}
	userID := "de_" + o.genUserID()
	for i := 0; i < 20; i++ {
		if _, checkErr := o.Database.GetUser(ctx, userID); dbutil.IsDBNotFound(checkErr) {
			break
		} else if checkErr == nil {
			userID = "de_" + o.genUserID()
		} else {
			return "", checkErr
		}
	}
	now := time.Now()
	register := &chatdb.Register{UserID: userID, AccountType: constant.Account, Mode: constant.UserMode, CreateTime: now}
	account := &chatdb.Account{UserID: userID, CreateTime: now, ChangeTime: now}
	attribute := &chatdb.Attribute{
		UserID: userID, Account: employee.EmployeeCode, Email: cfg.DefaultEmail,
		PhoneNumber: cfg.DefaultPhone, Nickname: employee.EmployeeName,
		Gender: constant.GenderUnknown, AllowVibration: constant.DefaultAllowVibration,
		AllowBeep: constant.DefaultAllowBeep, AllowAddFriend: constant.DefaultAllowAddFriend,
		RegisterType: constant.DigitalEmployeeRegister, CreateTime: now, ChangeTime: now,
	}
	credentials := []*chatdb.Credential{{
		UserID: userID, Account: credentialAccount, Type: constant.CredentialDigitalEmployee,
		AllowChange: false,
	}}
	if err := o.Database.RegisterUser(ctx, register, account, attribute, credentials); err != nil {
		return "", err
	}
	return userID, nil
}

func (o *chatSvr) updateDigitalEmployeeAttribute(ctx context.Context, cfg config.DigitalEmployee, userID string, employee digitalEmployee) error {
	return o.Database.UpdateUseInfo(ctx, userID, map[string]any{
		"account": employee.EmployeeCode, "email": cfg.DefaultEmail, "phone_number": cfg.DefaultPhone,
		"nickname": employee.EmployeeName, "register_type": constant.DigitalEmployeeRegister,
		"change_time": time.Now(),
	}, nil, nil)
}

func (o *chatSvr) syncDigitalEmployeeToIM(ctx context.Context, member *chatdb.DepartmentMember) error {
	token, err := o.IMCaller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return err
	}
	imCtx := mctx.WithApiToken(ctx, token)
	ex := adUserEx(member.Username, member.Email, member.Phone, member.Position)
	imInfo, err := o.IMCaller.GetUserInfo(imCtx, member.UserID)
	if err != nil {
		return o.IMCaller.RegisterUser(imCtx, []*sdkws.UserInfo{{UserID: member.UserID, Nickname: member.Nickname, Ex: ex}})
	}
	ex, err = mergeDigitalEmployeeEx(imInfo.Ex, member)
	if err != nil {
		return err
	}
	if imInfo.Nickname != member.Nickname || imInfo.Ex != ex {
		return o.IMCaller.UpdateUserInfo(imCtx, member.UserID, member.Nickname, imInfo.FaceURL, ex)
	}
	return nil
}

func mergeDigitalEmployeeEx(existing string, member *chatdb.DepartmentMember) (string, error) {
	data := make(map[string]any)
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &data); err != nil {
			return "", fmt.Errorf("invalid existing IM user ex: %w", err)
		}
	}
	data["username"] = member.Username
	data["email"] = member.Email
	data["phone"] = member.Phone
	data["position"] = member.Position
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (o *chatSvr) setDigitalTwinEnabled(ctx context.Context, userID string, enabled bool) error {
	token, err := o.IMCaller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return err
	}
	imCtx := mctx.WithApiToken(ctx, token)
	user, err := o.IMCaller.GetUserInfo(imCtx, userID)
	if err != nil {
		return err
	}
	cfg, ok, err := digitaltwin.ParseUserConfigFromEx(user.Ex)
	if err != nil {
		return err
	}
	if !ok {
		cfg, ok, err = digitaltwin.LoadUserConfigFromStore(userID)
		if err != nil {
			return err
		}
		if !ok {
			cfg = digitaltwin.UserConfig{Version: 1}
		}
	}
	cfg = digitaltwin.ApplyUserConfigPatch(cfg, digitaltwin.UserConfigPatch{Enabled: &enabled})
	now := time.Now()
	cfg.Version = 1
	cfg.UpdatedAt = now.UnixMilli()
	ex, err := digitaltwin.MergeUserConfigToEx(user.Ex, cfg, now)
	if err != nil {
		return err
	}
	if err := o.IMCaller.UpdateUserEx(imCtx, userID, ex); err != nil {
		return err
	}
	return digitaltwin.SaveUserConfigToStore(userID, cfg, now)
}
