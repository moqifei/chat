// Copyright © 2023 OpenIM open source community. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chat

import (
	"context"
	"strings"
	"time"

	"github.com/openimsdk/chat/pkg/common/ad"
	"github.com/openimsdk/chat/pkg/common/constant"
	"github.com/openimsdk/chat/pkg/common/db/dbutil"
	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/mcontext"
	"github.com/openimsdk/tools/utils/idutil"
)

// ────────── AD Organization Sync Scheduler ──────────

// startADSyncScheduler kicks off the periodic AD org sync.
// It runs once immediately on startup, then every day at the configured time.
func (o *chatSvr) startADSyncScheduler(ctx context.Context) {
	if o.ADClient == nil || !o.ADConfig.Sync.Enable {
		log.ZInfo(ctx, "AD org sync is disabled, skipping scheduler")
		return
	}

	log.ZInfo(ctx, "AD org sync scheduler starting", "cron", o.ADConfig.Sync.Cron)

	// Run the first sync in a separate goroutine (non-blocking).
	go func() {
		time.Sleep(5 * time.Second) // Brief delay to let other services initialize.
		o.runADSync(syncBackgroundCtx())
	}()

	// Schedule daily runs.
	go func() {
		for {
			next := nextScheduleTime(o.ADConfig.Sync.Cron)
			delay := time.Until(next)
			log.ZInfo(syncBackgroundCtx(), "AD org sync next scheduled", "next", next.Format(time.RFC3339), "delay", delay)
			time.Sleep(delay)
			o.runADSync(syncBackgroundCtx())
		}
	}()
}

// syncBackgroundCtx creates a background context with an operationID,
// required by the IM server for API calls.
func syncBackgroundCtx() context.Context {
	return mcontext.SetOperationID(context.Background(), "ad_sync_"+idutil.OperationIDGenerator())
}

// runADSync performs a full AD organization sync:
// 1. Discover OUs → build department tree.
// 2. Discover users → map to departments, extract nicknames.
// 3. Batch upsert to MongoDB.
// 4. Sync registered users' info to IM server.
func (o *chatSvr) runADSync(ctx context.Context) {
	log.ZInfo(ctx, "AD org sync: starting full sync")

	startTime := time.Now()

	// ── Step 1: Sync departments from AD ──
	adDeps, err := o.ADClient.SearchOUs()
	if err != nil {
		log.ZError(ctx, "AD org sync: failed to search OUs", err)
		return
	}

	// Compute department levels based on parent-child relationships.
	deptMap := make(map[string]*departmentNode)
	rootDNs := make(map[string]bool)
	for _, d := range adDeps {
		deptMap[d.DN] = &departmentNode{dep: d, children: nil}
		if d.ParentDN == "" {
			rootDNs[d.DN] = true
		}
	}

	// Build parent-child relationships.
	for _, node := range deptMap {
		if node.dep.ParentDN != "" {
			if parent, ok := deptMap[node.dep.ParentDN]; ok {
				parent.children = append(parent.children, node)
			}
		}
	}

	// Compute levels and build flat department list.
	var dbDepts []*chatdb.Department
	now := time.Now()
	for _, root := range findRoots(deptMap, rootDNs) {
		assignLevels(root, 0, &dbDepts, now)
	}

	// Recompute sub-department counts.
	for _, dept := range dbDepts {
		if parent, ok := deptMap[dept.DepartmentID]; ok {
			dept.SubDeptCount = len(flattenChildren(parent))
		}
	}

	log.ZInfo(ctx, "AD org sync: departments resolved", "total", len(dbDepts))

	// Write departments.
	if err := o.Database.GetDepartmentInterface().DeleteAll(ctx); err != nil {
		log.ZError(ctx, "AD org sync: failed to clear old departments", err)
		return
	}
	if len(dbDepts) > 0 {
		if err := o.Database.GetDepartmentInterface().Create(ctx, dbDepts); err != nil {
			log.ZError(ctx, "AD org sync: failed to insert departments", err)
			return
		}
	}
	log.ZInfo(ctx, "AD org sync: departments saved", "count", len(dbDepts))

	// ── Step 2: Sync users from AD ──
	adUsers, err := o.ADClient.SearchAllUsers()
	if err != nil {
		log.ZError(ctx, "AD org sync: failed to search users", err)
		return
	}

	log.ZInfo(ctx, "AD org sync: users discovered", "count", len(adUsers))

	// Build department member list.
	// Each user maps to their primary department (first OU in DN).
	var dbMembers []*chatdb.DepartmentMember
	userDeptCount := make(map[string]int) // department DN → member count

	for _, u := range adUsers {
		// Extract nickname from displayName (same logic as login flow).
		nickname := extractADNickname(u.DisplayName, u.Username)

		primaryDept := u.PrimaryDepartmentDN
		// Check if the department exists in our dept map.
		if _, ok := deptMap[primaryDept]; !ok {
			// If no matching OU found, assign to a virtual "unknown" department
			// or skip (it means the user's OU is not in the OU search results,
			// which can happen if the OU is a built-in container).
			if primaryDept == "" {
				log.ZDebug(ctx, "AD org sync: user has no OU in DN", "username", u.Username, "dn", u.DN)
			}
		}

		member := &chatdb.DepartmentMember{
			DepartmentID: primaryDept,
			Username:     u.Username,
			DisplayName:  u.DisplayName,
			Nickname:     nickname,
			Email:        u.Email,
			DN:           u.DN,
			Position:     u.Title,
			Phone:        u.Phone,
			IsPrimary:    true,
			SyncTime:     now,
			CreateTime:   now,
		}

		// Try to find the corresponding chat user ID.
		adCredAccount := BuildCredentialAD(u.Username)
		cred, credErr := o.Database.TakeCredentialByAccount(ctx, adCredAccount)
		if credErr != nil && !dbutil.IsDBNotFound(credErr) {
			log.ZWarn(ctx, "AD org sync: failed to check credential", credErr, "username", u.Username)
		} else if credErr == nil {
			member.UserID = cred.UserID
		} else {
			// No local chat account yet → auto-create so that the user
			// can be discovered and messaged without a prior login.
			userID, createErr := o.autoCreateChatUserForADMember(ctx, u, nickname)
			if createErr != nil {
				log.ZWarn(ctx, "AD org sync: failed to auto-create chat user", createErr, "username", u.Username)
			} else {
				member.UserID = userID
			}
		}

		dbMembers = append(dbMembers, member)

		if primaryDept != "" {
			userDeptCount[primaryDept]++
		}
	}

	// Write members.
	if err := o.Database.GetDepartmentMemberInterface().DeleteAll(ctx); err != nil {
		log.ZError(ctx, "AD org sync: failed to clear old members", err)
		return
	}
	if len(dbMembers) > 0 {
		if err := o.Database.GetDepartmentMemberInterface().Create(ctx, dbMembers); err != nil {
			log.ZError(ctx, "AD org sync: failed to insert members", err)
			return
		}
	}
	log.ZInfo(ctx, "AD org sync: members saved", "count", len(dbMembers))

	// ── Step 3: Update department member counts ──
	for deptDN, count := range userDeptCount {
		_ = o.Database.GetDepartmentInterface().UpdateMemberCount(ctx, deptDN, count)
	}

	// ── Step 4: Sync registered users to IM server ──
	o.syncADMembersToIMServer(ctx, dbMembers)

	elapsed := time.Since(startTime)
	log.ZInfo(ctx, "AD org sync: completed", "departments", len(dbDepts), "members", len(dbMembers), "duration", elapsed.String())
}

// syncADMembersToIMServer updates IM server with synced user info.
// It only processes users that are already registered in the chat DB.
func (o *chatSvr) syncADMembersToIMServer(ctx context.Context, members []*chatdb.DepartmentMember) {
	if o.IMCaller == nil {
		return
	}

	imToken, err := o.IMCaller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		log.ZWarn(ctx, "AD org sync: failed to get IM admin token for user sync", err)
		return
	}
	imCtx := mctx.WithApiToken(ctx, imToken)

	// Collect users that need IM registration or update.
	var newUsers []*sdkws.UserInfo
	var updateCount int

	for _, m := range members {
		if m.UserID == "" {
			log.ZWarn(ctx, "AD org sync: member has no userID, skipping IM sync", nil, "username", m.Username)
			continue
		}
		if m.Nickname == "" {
			continue
		}

		// Check if user already exists in IM server.
		imInfo, imErr := o.IMCaller.GetUserInfo(imCtx, m.UserID)
		if imErr != nil {
			// User not in IM server → register.
			newUsers = append(newUsers, &sdkws.UserInfo{
				UserID:   m.UserID,
				Nickname: m.Nickname,
				FaceURL:  "",
			})
			continue
		}

		// User exists → update if nickname differs.
		if imInfo.Nickname != m.Nickname {
			if uErr := o.IMCaller.UpdateUserInfo(imCtx, m.UserID, m.Nickname, ""); uErr != nil {
				log.ZWarn(ctx, "AD org sync: failed to update IM user nickname",
					uErr, "userID", m.UserID, "username", m.Username, "nickname", m.Nickname)
			} else {
				updateCount++
			}
		}
	}

	// Register new users in batch.
	if len(newUsers) > 0 {
		if err := o.IMCaller.RegisterUser(imCtx, newUsers); err != nil {
			log.ZWarn(ctx, "AD org sync: failed to register new users to IM server",
				err, "count", len(newUsers))
		} else {
			log.ZInfo(ctx, "AD org sync: registered new users to IM server",
				"count", len(newUsers))
		}
	}

	log.ZInfo(ctx, "AD org sync: IM server sync done",
		"registered", len(newUsers),
		"updated", updateCount)
}

// autoCreateChatUserForADMember creates a local chat account for an AD user
// that has not yet logged in (no credential exists). This allows the user
// to be discovered and messaged by colleagues without requiring a first login.
func (o *chatSvr) autoCreateChatUserForADMember(ctx context.Context, u *ad.SyncUser, nickname string) (string, error) {
	userID := o.genUserID()
	for i := 0; i < 20; i++ {
		_, err := o.Database.GetUser(ctx, userID)
		if err == nil {
			userID = o.genUserID()
			continue
		} else if dbutil.IsDBNotFound(err) {
			break
		} else {
			return "", err
		}
	}

	now := time.Now()
	email := u.Email
	if email == "" {
		email = u.Username
	}

	adAccount := BuildCredentialAD(u.Username)

	register := &chatdb.Register{
		UserID:      userID,
		DeviceID:    "",
		IP:          "",
		Platform:    "",
		AccountType: constant.Account,
		Mode:        constant.UserMode,
		CreateTime:  now,
	}
	account := &chatdb.Account{
		UserID:         userID,
		Password:       "",
		OperatorUserID: "",
		ChangeTime:     now,
		CreateTime:     now,
	}
	attribute := &chatdb.Attribute{
		UserID:         userID,
		Account:        u.Username,
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
	if u.Username != "" {
		credentials = append(credentials, &chatdb.Credential{
			UserID:      userID,
			Account:     u.Username,
			Type:        constant.CredentialAccount,
			AllowChange: false,
		})
	}

	if err := o.Database.RegisterUser(ctx, register, account, attribute, credentials); err != nil {
		return "", err
	}

	log.ZInfo(ctx, "AD org sync: auto-created chat user",
		"userID", userID, "username", u.Username, "nickname", nickname)
	return userID, nil
}

// ────────── Schedule helpers ──────────

// nextScheduleTime parses a cron expression and returns the next run time.
// Supports 5-field cron: "minute hour dom month dow".
// Default: next 2:00 AM.
func nextScheduleTime(cronExpr string) time.Time {
	now := time.Now()

	// Parse cron expression "minute hour dom month dow".
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		// Default: 2:00 AM daily.
		next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
		if next.Before(now) || next.Equal(now) {
			next = next.Add(24 * time.Hour)
		}
		return next
	}

	// Simple cron: just handle "minute hour * * *" pattern.
	// For "0 2 * * *", schedule next 2:00 AM.
	var hour, minute int
	parseCronField(fields[1], &hour, 0)   // hour
	parseCronField(fields[0], &minute, 0) // minute

	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if next.Before(now) || next.Equal(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func parseCronField(field string, dest *int, defaultVal int) {
	if field == "*" {
		*dest = defaultVal
		return
	}
	// Simple integer parsing.
	var val int
	for _, r := range field {
		if r >= '0' && r <= '9' {
			val = val*10 + int(r-'0')
		}
	}
	*dest = val
}

// ────────── Department tree helpers ──────────

type departmentNode struct {
	dep      *ad.SyncDepartment
	children []*departmentNode
}

// findRoots returns the root nodes among departments.
func findRoots(nodes map[string]*departmentNode, rootDNs map[string]bool) []*departmentNode {
	var roots []*departmentNode
	for dn, isRoot := range rootDNs {
		if isRoot {
			if node, ok := nodes[dn]; ok {
				roots = append(roots, node)
			}
		}
	}
	// If no explicit roots, those without a valid parent become roots.
	if len(roots) == 0 {
		for _, node := range nodes {
			if node.dep.ParentDN == "" {
				roots = append(roots, node)
			} else if _, ok := nodes[node.dep.ParentDN]; !ok {
				roots = append(roots, node)
			}
		}
	}
	return roots
}

// assignLevels recursively assigns depth levels and collects departments.
func assignLevels(node *departmentNode, level int, result *[]*chatdb.Department, now time.Time) {
	*result = append(*result, &chatdb.Department{
		DepartmentID: node.dep.DN,
		Name:         node.dep.Name,
		ParentID:     node.dep.ParentDN,
		Level:        level,
		SyncTime:     now,
		CreateTime:   now,
	})
	for _, child := range node.children {
		assignLevels(child, level+1, result, now)
	}
}

// flattenChildren recursively collects all descendant nodes.
func flattenChildren(node *departmentNode) []*departmentNode {
	var result []*departmentNode
	for _, child := range node.children {
		result = append(result, child)
		result = append(result, flattenChildren(child)...)
	}
	return result
}

// ────────── Nickname extraction (uses login.go's extractADNickname) ──────────
// extractADNickname is defined in login.go and shared across this package.

