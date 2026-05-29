package chat

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	constantpb "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/utils/datautil"

	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/mcontext"

	"github.com/openimsdk/chat/pkg/common/constant"
	"github.com/openimsdk/chat/pkg/common/db/dbutil"
	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/eerrs"
	"github.com/openimsdk/chat/pkg/protocol/chat"
)

type verifyType int

const (
	phone verifyType = iota
	mail
)

func (o *chatSvr) verifyCodeJoin(areaCode, phoneNumber string) string {
	return areaCode + " " + phoneNumber
}

func (o *chatSvr) SendVerifyCode(ctx context.Context, req *chat.SendVerifyCodeReq) (*chat.SendVerifyCodeResp, error) {
	switch int(req.UsedFor) {
	case constant.VerificationCodeForRegister:
		if err := o.Admin.CheckRegister(ctx, req.Ip); err != nil {
			return nil, err
		}
		if req.Email == "" {
			if req.AreaCode == "" || req.PhoneNumber == "" {
				return nil, errs.ErrArgs.WrapMsg("area code or phone number is empty")
			}
			if !strings.HasPrefix(req.AreaCode, "+") {
				req.AreaCode = "+" + req.AreaCode
			}
			if _, err := strconv.ParseUint(req.AreaCode[1:], 10, 64); err != nil {
				return nil, errs.ErrArgs.WrapMsg("area code must be number")
			}
			if _, err := strconv.ParseUint(req.PhoneNumber, 10, 64); err != nil {
				return nil, errs.ErrArgs.WrapMsg("phone number must be number")
			}
		} else {
			if err := chat.EmailCheck(req.Email); err != nil {
				return nil, errs.ErrArgs.WrapMsg("email must be right")
			}
		}
		conf, err := o.Admin.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		if val := conf[constant.NeedInvitationCodeRegisterConfigKey]; datautil.Contain(strings.ToLower(val), "1", "true", "yes") {
			if req.InvitationCode == "" {
				return nil, errs.ErrArgs.WrapMsg("invitation code is empty")
			}
			if err := o.Admin.CheckInvitationCode(ctx, req.InvitationCode); err != nil {
				return nil, err
			}
		}
	case constant.VerificationCodeForLogin, constant.VerificationCodeForResetPassword:
		if req.Email == "" {
			_, err := o.Database.TakeAttributeByPhone(ctx, req.AreaCode, req.PhoneNumber)
			if dbutil.IsDBNotFound(err) {
				return nil, eerrs.ErrAccountNotFound.WrapMsg("phone unregistered")
			} else if err != nil {
				return nil, err
			}
		} else {
			_, err := o.Database.TakeAttributeByEmail(ctx, req.Email)
			if dbutil.IsDBNotFound(err) {
				return nil, eerrs.ErrAccountNotFound.WrapMsg("email unregistered")
			} else if err != nil {
				return nil, err
			}
		}

	default:
		return nil, errs.ErrArgs.WrapMsg("used unknown")
	}
	if o.SMS == nil && o.Mail == nil {
		return &chat.SendVerifyCodeResp{}, nil // super code
	}
	if req.Email != "" {
		switch o.conf.Mail.Use {
		case constant.VerifySuperCode:
			return &chat.SendVerifyCodeResp{}, nil // super code
		case constant.VerifyMail:
		default:
			return nil, errs.ErrInternalServer.WrapMsg("email verification code is not enabled")
		}
	}

	if req.AreaCode != "" {
		switch o.conf.Phone.Use {
		case constant.VerifySuperCode:
			return &chat.SendVerifyCodeResp{}, nil // super code
		case constant.VerifyALi:
		default:
			return nil, errs.ErrInternalServer.WrapMsg("phone verification code is not enabled")
		}
	}

	isEmail := req.Email != ""
	var (
		code     = o.genVerifyCode()
		account  string
		sendCode func() error
	)
	if isEmail {
		sendCode = func() error {
			return o.Mail.SendMail(ctx, req.Email, code)
		}
		account = req.Email
	} else {
		sendCode = func() error {
			return o.SMS.SendCode(ctx, req.AreaCode, req.PhoneNumber, code)
		}
		account = o.verifyCodeJoin(req.AreaCode, req.PhoneNumber)
	}
	now := time.Now()
	count, err := o.Database.CountVerifyCodeRange(ctx, account, now.Add(-o.Code.UintTime), now)
	if err != nil {
		return nil, err
	}
	if o.Code.MaxCount < int(count) {
		return nil, eerrs.ErrVerifyCodeSendFrequently.Wrap()
	}
	platformName := constantpb.PlatformIDToName(int(req.Platform))
	if platformName == "" {
		platformName = fmt.Sprintf("platform:%d", req.Platform)
	}
	vc := &chatdb.VerifyCode{
		Account:    account,
		Code:       code,
		Platform:   platformName,
		Duration:   uint(o.Code.ValidTime / time.Second),
		Count:      0,
		Used:       false,
		CreateTime: now,
	}
	if err := o.Database.AddVerifyCode(ctx, vc, sendCode); err != nil {
		return nil, err
	}
	log.ZDebug(ctx, "send code success", "account", account, "code", code, "platform", platformName)
	return &chat.SendVerifyCodeResp{}, nil
}

func (o *chatSvr) verifyCode(ctx context.Context, account string, verifyCode string, type_ verifyType) (string, error) {
	if verifyCode == "" {
		return "", errs.ErrArgs.WrapMsg("verify code is empty")
	}
	switch type_ {
	case phone:
		switch o.conf.Phone.Use {
		case constant.VerifySuperCode:
			if o.Code.SuperCode != verifyCode {
				return "", eerrs.ErrVerifyCodeNotMatch.Wrap()
			}
			return "", nil
		case constant.VerifyALi:
		default:
			return "", errs.ErrInternalServer.WrapMsg("phone verification code is not enabled", "use", o.conf.Phone.Use)
		}
	case mail:
		switch o.conf.Mail.Use {
		case constant.VerifySuperCode:
			if o.Code.SuperCode != verifyCode {
				return "", eerrs.ErrVerifyCodeNotMatch.Wrap()
			}
			return "", nil
		case constant.VerifyMail:
		default:
			return "", errs.ErrInternalServer.WrapMsg("email verification code is not enabled")
		}
	}

	last, err := o.Database.TakeLastVerifyCode(ctx, account)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			return "", eerrs.ErrVerifyCodeExpired.Wrap()
		}
		return "", err
	}
	if last.CreateTime.Unix()+int64(last.Duration) < time.Now().Unix() {
		return last.ID, eerrs.ErrVerifyCodeExpired.Wrap()
	}
	if last.Used {
		return last.ID, eerrs.ErrVerifyCodeUsed.Wrap()
	}
	if n := o.Code.ValidCount; n > 0 {
		if last.Count >= n {
			return last.ID, eerrs.ErrVerifyCodeMaxCount.Wrap()
		}
		if last.Code != verifyCode {
			if err := o.Database.UpdateVerifyCodeIncrCount(ctx, last.ID); err != nil {
				return last.ID, err
			}
		}
	}
	if last.Code != verifyCode {
		return last.ID, eerrs.ErrVerifyCodeNotMatch.Wrap()
	}
	return last.ID, nil
}

func (o *chatSvr) VerifyCode(ctx context.Context, req *chat.VerifyCodeReq) (*chat.VerifyCodeResp, error) {
	var account string
	if req.PhoneNumber != "" {
		account = o.verifyCodeJoin(req.AreaCode, req.PhoneNumber)
		if _, err := o.verifyCode(ctx, account, req.VerifyCode, phone); err != nil {
			return nil, err
		}
	} else {
		account = req.Email
		if _, err := o.verifyCode(ctx, account, req.VerifyCode, mail); err != nil {
			return nil, err
		}
	}

	return &chat.VerifyCodeResp{}, nil
}

func (o *chatSvr) genUserID() string {
	const l = 10
	data := make([]byte, l)
	rand.Read(data)
	chars := []byte("0123456789")
	for i := 0; i < len(data); i++ {
		if i == 0 {
			data[i] = chars[1:][data[i]%9]
		} else {
			data[i] = chars[data[i]%10]
		}
	}
	return string(data)
}

func (o *chatSvr) genVerifyCode() string {
	data := make([]byte, o.Code.Len)
	rand.Read(data)
	chars := []byte("0123456789")
	for i := 0; i < len(data); i++ {
		data[i] = chars[data[i]%10]
	}
	return string(data)
}

func (o *chatSvr) RegisterUser(ctx context.Context, req *chat.RegisterUserReq) (*chat.RegisterUserResp, error) {
	isAdmin, err := o.Admin.CheckNilOrAdmin(ctx)
	ctx = o.WithAdminUser(ctx)
	if err != nil {
		return nil, err
	}
	if err = o.checkRegisterInfo(ctx, req.User, isAdmin); err != nil {
		return nil, err
	}
	var usedInvitationCode bool
	if !isAdmin {
		if !o.AllowRegister {
			return nil, errs.ErrNoPermission.WrapMsg("register user is disabled")
		}
		if req.User.UserID != "" {
			return nil, errs.ErrNoPermission.WrapMsg("only admin can set user id")
		}
		if err := o.Admin.CheckRegister(ctx, req.Ip); err != nil {
			return nil, err
		}
		conf, err := o.Admin.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		if val := conf[constant.NeedInvitationCodeRegisterConfigKey]; datautil.Contain(strings.ToLower(val), "1", "true", "yes") {
			usedInvitationCode = true
			if req.InvitationCode == "" {
				return nil, errs.ErrArgs.WrapMsg("invitation code is empty")
			}
			if err := o.Admin.CheckInvitationCode(ctx, req.InvitationCode); err != nil {
				return nil, err
			}
		}
		if req.User.Email == "" {
			if _, err := o.verifyCode(ctx, o.verifyCodeJoin(req.User.AreaCode, req.User.PhoneNumber), req.VerifyCode, phone); err != nil {
				return nil, err
			}
		} else {
			if _, err := o.verifyCode(ctx, req.User.Email, req.VerifyCode, mail); err != nil {
				return nil, err
			}
		}
	}
	if req.User.UserID == "" {
		for i := 0; i < 20; i++ {
			userID := o.genUserID()
			_, err := o.Database.GetUser(ctx, userID)
			if err == nil {
				continue
			} else if dbutil.IsDBNotFound(err) {
				req.User.UserID = userID
				break
			} else {
				return nil, err
			}
		}
		if req.User.UserID == "" {
			return nil, errs.ErrInternalServer.WrapMsg("gen user id failed")
		}
	} else {
		_, err := o.Database.GetUser(ctx, req.User.UserID)
		if err == nil {
			return nil, errs.ErrArgs.WrapMsg("appoint user id already register")
		} else if !dbutil.IsDBNotFound(err) {
			return nil, err
		}
	}
	var (
		credentials  []*chatdb.Credential
		registerType int32
	)

	if req.User.PhoneNumber != "" {
		registerType = constant.PhoneRegister
		credentials = append(credentials, &chatdb.Credential{
			UserID:      req.User.UserID,
			Account:     BuildCredentialPhone(req.User.AreaCode, req.User.PhoneNumber),
			Type:        constant.CredentialPhone,
			AllowChange: true,
		})
	}

	if req.User.Account != "" {
		credentials = append(credentials, &chatdb.Credential{
			UserID:      req.User.UserID,
			Account:     req.User.Account,
			Type:        constant.CredentialAccount,
			AllowChange: true,
		})
		registerType = constant.AccountRegister
	}

	if req.User.Email != "" {
		registerType = constant.EmailRegister
		credentials = append(credentials, &chatdb.Credential{
			UserID:      req.User.UserID,
			Account:     req.User.Email,
			Type:        constant.CredentialEmail,
			AllowChange: true,
		})
	}
	register := &chatdb.Register{
		UserID:      req.User.UserID,
		DeviceID:    req.DeviceID,
		IP:          req.Ip,
		Platform:    constantpb.PlatformID2Name[int(req.Platform)],
		AccountType: "",
		Mode:        constant.UserMode,
		CreateTime:  time.Now(),
	}
	account := &chatdb.Account{
		UserID:         req.User.UserID,
		Password:       req.User.Password,
		OperatorUserID: mcontext.GetOpUserID(ctx),
		ChangeTime:     register.CreateTime,
		CreateTime:     register.CreateTime,
	}

	attribute := &chatdb.Attribute{
		UserID:         req.User.UserID,
		Account:        req.User.Account,
		PhoneNumber:    req.User.PhoneNumber,
		AreaCode:       req.User.AreaCode,
		Email:          req.User.Email,
		Nickname:       req.User.Nickname,
		FaceURL:        req.User.FaceURL,
		Gender:         req.User.Gender,
		BirthTime:      time.UnixMilli(req.User.Birth),
		ChangeTime:     register.CreateTime,
		CreateTime:     register.CreateTime,
		AllowVibration: constant.DefaultAllowVibration,
		AllowBeep:      constant.DefaultAllowBeep,
		AllowAddFriend: constant.DefaultAllowAddFriend,
		RegisterType:   registerType,
	}
	if err := o.Database.RegisterUser(ctx, register, account, attribute, credentials); err != nil {
		return nil, err
	}
	if usedInvitationCode {
		if err := o.Admin.UseInvitationCode(ctx, req.User.UserID, req.InvitationCode); err != nil {
			log.ZError(ctx, "UseInvitationCode", err, "userID", req.User.UserID, "invitationCode", req.InvitationCode)
		}
	}
	var resp chat.RegisterUserResp
	if req.AutoLogin {
		chatToken, err := o.Admin.CreateToken(ctx, req.User.UserID, constant.NormalUser)
		if err == nil {
			resp.ChatToken = chatToken.Token
		} else {
			log.ZError(ctx, "Admin CreateToken Failed", err, "userID", req.User.UserID, "platform", req.Platform)
		}
	}
	resp.UserID = req.User.UserID
	return &resp, nil
}

func (o *chatSvr) Login(ctx context.Context, req *chat.LoginReq) (*chat.LoginResp, error) {
	resp := &chat.LoginResp{}
	if req.Password == "" && req.VerifyCode == "" {
		return nil, errs.ErrArgs.WrapMsg("password or code must be set")
	}
	var (
		err        error
		credential *chatdb.Credential
		acc        string
	)

	switch {
	case req.Account != "":
		acc = req.Account
	case req.PhoneNumber != "":
		if req.AreaCode == "" {
			return nil, errs.ErrArgs.WrapMsg("area code must")
		}
		if !strings.HasPrefix(req.AreaCode, "+") {
			req.AreaCode = "+" + req.AreaCode
		}
		if _, err := strconv.ParseUint(req.AreaCode[1:], 10, 64); err != nil {
			return nil, errs.ErrArgs.WrapMsg("area code must be number")
		}
		acc = BuildCredentialPhone(req.AreaCode, req.PhoneNumber)
	case req.Email != "":
		acc = req.Email
	default:
		return nil, errs.ErrArgs.WrapMsg("account or phone number or email must be set")
	}
	credential, err = o.Database.TakeCredentialByAccount(ctx, acc)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			// If AD is enabled with auto-create, try authenticating via AD
			// and automatically register the user on first successful login.
			if o.ADClient != nil && o.ADClient.Enabled() && req.Password != "" {
				return o.adLoginAndAutoCreate(ctx, req, acc)
			}
			return nil, eerrs.ErrAccountNotFound.WrapMsg("user unregistered")
		}
		return nil, err
	}

	// After finding a plain-account credential, check if the user was
	// auto-created via AD. If so, switch to AD authentication path.
	if o.ADClient != nil && o.ADClient.Enabled() {
		if adCred, adErr := o.Database.TakeCredentialByAccount(ctx, BuildCredentialAD(acc)); adErr == nil {
			credential = adCred
		}
	}

	// If the credential is an AD account, authenticate via LDAP.
	if credential.Type == constant.CredentialAD {
		if req.Password == "" {
			return nil, errs.ErrArgs.WrapMsg("password required for AD login")
		}
		return o.adAuthenticateAndLogin(ctx, req, credential)
	}

	if err := o.Admin.CheckLogin(ctx, credential.UserID, req.Ip); err != nil {
		return nil, err
	}
	var verifyCodeID *string
	if req.Password == "" {
		var (
			id string
		)

		if req.Email == "" {
			account := o.verifyCodeJoin(req.AreaCode, req.PhoneNumber)
			id, err = o.verifyCode(ctx, account, req.VerifyCode, phone)
			if err != nil {
				return nil, err
			}
		} else {
			account := req.Email
			id, err = o.verifyCode(ctx, account, req.VerifyCode, mail)
			if err != nil {
				return nil, err
			}
		}

		if id != "" {
			verifyCodeID = &id
		}
	} else {
		account, err := o.Database.TakeAccount(ctx, credential.UserID)
		if err != nil {
			return nil, err
		}
		if account.Password != req.Password {
			return nil, eerrs.ErrPassword.Wrap()
		}
	}
	chatToken, err := o.Admin.CreateToken(ctx, credential.UserID, constant.NormalUser)
	if err != nil {
		return nil, err
	}
	record := &chatdb.UserLoginRecord{
		UserID:    credential.UserID,
		LoginTime: time.Now(),
		IP:        req.Ip,
		DeviceID:  req.DeviceID,
		Platform:  constantpb.PlatformIDToName(int(req.Platform)),
	}
	if err := o.Database.LoginRecord(ctx, record, verifyCodeID); err != nil {
		return nil, err
	}
	if verifyCodeID != nil {
		if err := o.Database.DelVerifyCode(ctx, *verifyCodeID); err != nil {
			return nil, err
		}
	}
	resp.UserID = credential.UserID
	resp.ChatToken = chatToken.Token
	return resp, nil
}

// adLoginAndAutoCreate authenticates the user via AD and automatically creates
// a local chat user account on the first successful AD login.
func (o *chatSvr) adLoginAndAutoCreate(ctx context.Context, req *chat.LoginReq, acc string) (*chat.LoginResp, error) {
	log.ZInfo(ctx, "adLoginAndAutoCreate start", "acc", acc, "platform", req.Platform, "deviceID", req.DeviceID, "ip", req.Ip)

	adInfo, err := o.ADClient.Authenticate(acc, req.Password)
	if err != nil {
		log.ZWarn(ctx, "ad authentication failed", err, "username", acc)
		return nil, eerrs.ErrADAuthFailed.WrapMsg("AD authentication failed")
	}

	log.ZInfo(ctx, "ad authentication success, auto-creating user", "username", acc,
		"adUsername", adInfo.Username,
		"adDisplayName", adInfo.DisplayName,
		"adEmail", adInfo.Email,
		"adDN", adInfo.DN,
	)

	// Check if there is already a credential mapped to this AD username.
	// If the user was created by admin ahead of time, use that account.
	adAccount := BuildCredentialAD(acc)
	cred, err := o.Database.TakeCredentialByAccount(ctx, adAccount)
	if err != nil && !dbutil.IsDBNotFound(err) {
		return nil, err
	}
	if err == nil {
		log.ZInfo(ctx, "ad user already has credential, proceeding to adAuthenticateAndLogin", "adAccount", adAccount, "credUserID", cred.UserID)
		// User already registered - proceed to login via the normal AD flow.
		return o.adAuthenticateAndLogin(ctx, req, cred)
	}

	log.ZInfo(ctx, "ad user not found locally, will auto-create", "acc", acc, "adAccount", adAccount)

	// Not registered yet – auto-create a local account.
	if err := o.Admin.CheckLogin(ctx, "", req.Ip); err != nil {
		return nil, err
	}

	userID := o.genUserID()
	for i := 0; i < 20; i++ {
		_, err := o.Database.GetUser(ctx, userID)
		if err == nil {
			userID = o.genUserID()
			continue
		} else if dbutil.IsDBNotFound(err) {
			break
		} else {
			return nil, err
		}
	}

	now := time.Now()
	nickname := extractADNickname(adInfo.DisplayName, acc)
	email := adInfo.Email
	if email == "" {
		email = acc
	}

	log.ZInfo(ctx, "ad auto-create: resolved user info",
		"userID", userID,
		"acc", acc,
		"rawDisplayName", adInfo.DisplayName,
		"extractedNickname", nickname,
		"email", email,
		"adUsername", adInfo.Username,
	)

	register := &chatdb.Register{
		UserID:      userID,
		DeviceID:    req.DeviceID,
		IP:          req.Ip,
		Platform:    constantpb.PlatformIDToName(int(req.Platform)),
		AccountType: constant.Account,
		Mode:        constant.UserMode,
		CreateTime:  now,
	}
	account := &chatdb.Account{
		UserID:         userID,
		Password:       "", // AD users have no local password
		OperatorUserID: mcontext.GetOpUserID(ctx),
		ChangeTime:     now,
		CreateTime:     now,
	}
	attribute := &chatdb.Attribute{
		UserID:         userID,
		Account:        acc,
		Email:          email,
		Nickname:       nickname,
		Gender:         0,
		ChangeTime:     now,
		CreateTime:     now,
		AllowVibration: constant.DefaultAllowVibration,
		AllowBeep:      constant.DefaultAllowBeep,
		AllowAddFriend: constant.DefaultAllowAddFriend,
		RegisterType:   constant.ADRegister,
	}
	credentials := []*chatdb.Credential{
		{
			UserID:      userID,
			Account:     adAccount,
			Type:        constant.CredentialAD,
			AllowChange: false,
		},
	}
	// Also store the AD username as a regular account credential for lookup.
	if acc != "" {
		credentials = append(credentials, &chatdb.Credential{
			UserID:      userID,
			Account:     acc,
			Type:        constant.CredentialAccount,
			AllowChange: false,
		})
	}

	log.ZInfo(ctx, "ad auto-create: writing to database",
		"userID", userID,
		"attribute.Nickname", attribute.Nickname,
		"attribute.Account", attribute.Account,
		"attribute.Email", attribute.Email,
		"attribute.RegisterType", attribute.RegisterType,
		"credentialCount", len(credentials),
	)

	if err := o.Database.RegisterUser(ctx, register, account, attribute, credentials); err != nil {
		log.ZError(ctx, "ad auto-create: database register failed", err, "userID", userID, "acc", acc)
		return nil, err
	}

	log.ZInfo(ctx, "ad user auto-created successfully", "username", acc, "userID", userID, "nickname", nickname)

	// Sync the newly created user to IM server so that nickname / faceURL are consistent.
	if o.IMCaller != nil {
		imToken, imErr := o.IMCaller.ImAdminTokenWithDefaultAdmin(ctx)
		if imErr != nil {
			log.ZWarn(ctx, "ad auto-create: failed to get im admin token", imErr, "userID", userID)
		} else {
			imCtx := mctx.WithApiToken(ctx, imToken)
			if imErr := o.IMCaller.RegisterUser(imCtx, []*sdkws.UserInfo{{
				UserID:   userID,
				Nickname: nickname,
				FaceURL:  "",
			}}); imErr != nil {
				log.ZWarn(ctx, "ad auto-create: failed to register user to IM server", imErr, "userID", userID, "nickname", nickname)
			} else {
				log.ZInfo(ctx, "ad auto-create: user registered to IM server", "userID", userID, "nickname", nickname)
			}
		}
	}

	chatToken, err := o.Admin.CreateToken(ctx, userID, constant.NormalUser)
	if err != nil {
		return nil, err
	}
	record := &chatdb.UserLoginRecord{
		UserID:    userID,
		LoginTime: now,
		IP:        req.Ip,
		DeviceID:  req.DeviceID,
		Platform:  constantpb.PlatformIDToName(int(req.Platform)),
	}
	if err := o.Database.LoginRecord(ctx, record, nil); err != nil {
		return nil, err
	}
	return &chat.LoginResp{
		UserID:    userID,
		ChatToken: chatToken.Token,
	}, nil
}

// adAuthenticateAndLogin authenticates an existing local user via AD (LDAP bind).
func (o *chatSvr) adAuthenticateAndLogin(ctx context.Context, req *chat.LoginReq, cred *chatdb.Credential) (*chat.LoginResp, error) {
	adAccount := cred.Account
	// Strip the "ad:" prefix if present.
	if len(adAccount) > 3 && adAccount[:3] == "ad:" {
		adAccount = adAccount[3:]
	}

	log.ZInfo(ctx, "adAuthenticateAndLogin start", "adAccount", adAccount, "userID", cred.UserID, "credentialType", cred.Type)

	adInfo, err := o.ADClient.Authenticate(adAccount, req.Password)
	if err != nil {
		log.ZWarn(ctx, "ad authentication failed", err, "username", adAccount, "userID", cred.UserID)
		return nil, eerrs.ErrADAuthFailed.WrapMsg("AD authentication failed")
	}

	log.ZInfo(ctx, "adAuthenticateAndLogin: AD auth success",
		"adAccount", adAccount,
		"userID", cred.UserID,
		"adUsername", adInfo.Username,
		"adDisplayName", adInfo.DisplayName,
		"adEmail", adInfo.Email,
	)

	if err := o.Admin.CheckLogin(ctx, cred.UserID, req.Ip); err != nil {
		return nil, err
	}

	// Refresh nickname from AD on each login to keep it in sync.
	newNickname := extractADNickname(adInfo.DisplayName, adAccount)
	log.ZInfo(ctx, "adAuthenticateAndLogin: nickname refresh",
		"userID", cred.UserID,
		"rawDisplayName", adInfo.DisplayName,
		"extractedNickname", newNickname,
	)

	if newNickname != "" {
		if err := o.Database.UpdateUseInfo(ctx, cred.UserID, map[string]any{"nickname": newNickname}, nil, nil); err != nil {
			log.ZWarn(ctx, "failed to refresh AD user nickname", err, "userID", cred.UserID, "nickname", newNickname)
		} else {
			log.ZInfo(ctx, "ad nickname refreshed in database", "userID", cred.UserID, "nickname", newNickname)
			// Sync refreshed nickname to IM server.
			if o.IMCaller != nil {
				imToken, imErr := o.IMCaller.ImAdminTokenWithDefaultAdmin(ctx)
				if imErr != nil {
					log.ZWarn(ctx, "adAuthenticateAndLogin: failed to get im admin token", imErr, "userID", cred.UserID)
				} else {
					imCtx := mctx.WithApiToken(ctx, imToken)
					if imErr := o.IMCaller.UpdateUserInfo(imCtx, cred.UserID, newNickname, ""); imErr != nil {
						log.ZWarn(ctx, "adAuthenticateAndLogin: failed to update IM server nickname", imErr, "userID", cred.UserID, "nickname", newNickname)
					} else {
						log.ZInfo(ctx, "adAuthenticateAndLogin: IM server nickname updated", "userID", cred.UserID, "nickname", newNickname)
					}
				}
			}
		}
	}

	chatToken, err := o.Admin.CreateToken(ctx, cred.UserID, constant.NormalUser)
	if err != nil {
		return nil, err
	}
	record := &chatdb.UserLoginRecord{
		UserID:    cred.UserID,
		LoginTime: time.Now(),
		IP:        req.Ip,
		DeviceID:  req.DeviceID,
		Platform:  constantpb.PlatformIDToName(int(req.Platform)),
	}
	if err := o.Database.LoginRecord(ctx, record, nil); err != nil {
		return nil, err
	}
	return &chat.LoginResp{
		UserID:    cred.UserID,
		ChatToken: chatToken.Token,
	}, nil
}

// extractADNickname extracts the user's display name from AD.
// It handles base64-encoded values and removes the department suffix
// separated by a middle dot (·). For example, "叶君骄·信息技术部" becomes "叶君骄".
func extractADNickname(displayName string, fallback string) string {
	name := displayName
	originalName := displayName
	decoded := false
	dotsplit := false

	// If the name looks like base64 (pure ASCII without unicode chars), try decoding.
	// A raw AD displayName containing Chinese characters will not decode as valid base64.
	if !containsNonASCII(name) {
		if d, err := base64.StdEncoding.DecodeString(name); err == nil && utf8.Valid(d) {
			decodedName := string(d)
			log.ZDebug(nil, "ad extractADNickname: base64 decoded", "original", name, "decoded", decodedName)
			name = decodedName
			decoded = true
		}
	}
	// AD displayName often includes a department suffix separated by
	// a middle dot (·) or a regular period (.), e.g. "叶君骄·信息技术部" or "叶君骄.信息技术部".
	// Keep only the name part before the first separator.
	separators := []string{"·", "."}
	for _, sep := range separators {
		if idx := strings.Index(name, sep); idx >= 0 {
			stripped := strings.TrimSpace(name[:idx])
			log.ZDebug(nil, "ad extractADNickname: dotsplit", "separator", sep, "before", name, "after", stripped, "suffix", name[idx:])
			name = stripped
			dotsplit = true
			break
		}
	}
	if name == "" {
		name = fallback
	}

	log.ZInfo(nil, "ad extractADNickname result",
		"originalDisplayName", originalName,
		"finalNickname", name,
		"fallback", fallback,
		"base64Decoded", decoded,
		"dotSuffixRemoved", dotsplit,
	)

	return name
}

// containsNonASCII returns true if the string contains any non-ASCII character.
func containsNonASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}
