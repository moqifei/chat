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
	"time"

	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/protocol/chat"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/log"
)

// ────────── AD Organization gRPC API ──────────

// GetADDepartmentList returns all synced departments.
func (o *chatSvr) GetADDepartmentList(ctx context.Context, req *chat.GetADDepartmentListReq) (*chat.GetADDepartmentListResp, error) {
	departments, err := o.Database.GetDepartmentInterface().FindAll(ctx)
	if err != nil {
		return nil, err
	}
	resp := &chat.GetADDepartmentListResp{}
	for _, d := range departments {
		resp.Departments = append(resp.Departments, &chat.ADDepartmentInfo{
			DepartmentID:       d.DepartmentID,
			Name:               d.Name,
			ParentID:           d.ParentID,
			MemberCount:        int32(d.MemberCount),
			SubDepartmentCount: int32(d.SubDeptCount),
			Level:              int32(d.Level),
		})
	}
	log.ZInfo(ctx, "GetADDepartmentList: queried departments", "count", len(resp.Departments))
	return resp, nil
}

// SearchADDepartments searches departments by keyword.
func (o *chatSvr) SearchADDepartments(ctx context.Context, req *chat.SearchADDepartmentsReq) (*chat.SearchADDepartmentsResp, error) {
	departments, err := o.Database.GetDepartmentInterface().Search(ctx, req.Keyword)
	if err != nil {
		return nil, err
	}
	resp := &chat.SearchADDepartmentsResp{}
	for _, d := range departments {
		resp.Departments = append(resp.Departments, &chat.ADDepartmentInfo{
			DepartmentID:       d.DepartmentID,
			Name:               d.Name,
			ParentID:           d.ParentID,
			MemberCount:        int32(d.MemberCount),
			SubDepartmentCount: int32(d.SubDeptCount),
			Level:              int32(d.Level),
		})
	}
	resp.Total = int64(len(resp.Departments))
	return resp, nil
}

// GetADDepartmentMembers returns members of a department with pagination.
func (o *chatSvr) GetADDepartmentMembers(ctx context.Context, req *chat.GetADDepartmentMembersReq) (*chat.GetADDepartmentMembersResp, error) {
	total, members, err := o.Database.GetDepartmentMemberInterface().FindByDepartmentID(ctx, req.DepartmentID, req.Pagination)
	if err != nil {
		return nil, err
	}
	resp := &chat.GetADDepartmentMembersResp{Total: total}
	for _, m := range members {
		resp.Members = append(resp.Members, departmentMemberToProto(m))
	}
	log.ZInfo(ctx, "GetADDepartmentMembers", "departmentID", req.DepartmentID, "total", total)
	return resp, nil
}

// SearchADMembers searches organization members by keyword, optionally filtered by department.
func (o *chatSvr) SearchADMembers(ctx context.Context, req *chat.SearchADMembersReq) (*chat.SearchADMembersResp, error) {
	total, members, err := o.Database.GetDepartmentMemberInterface().Search(ctx, req.Keyword, req.DepartmentID, req.Pagination)
	if err != nil {
		return nil, err
	}
	resp := &chat.SearchADMembersResp{Total: total}
	for _, m := range members {
		resp.Members = append(resp.Members, departmentMemberToProto(m))
	}
	log.ZInfo(ctx, "SearchADMembers", "keyword", req.Keyword, "departmentID", req.DepartmentID, "total", total)
	return resp, nil
}

// SyncADOrganization triggers a manual AD organization sync and returns the sync result.
//
// How to call:
//
//	# Using grpcurl:
//	grpcurl -plaintext -d '{}' <host>:<port> openim.chat.chat/SyncADOrganization
//
//	# Using Go client:
//	client := chat.NewChatClient(conn)
//	resp, err := client.SyncADOrganization(ctx, &chat.SyncADOrganizationReq{})
func (o *chatSvr) SyncADOrganization(ctx context.Context, req *chat.SyncADOrganizationReq) (*chat.SyncADOrganizationResp, error) {
	o.runADSync(ctx)

	departments, err := o.Database.GetDepartmentInterface().FindAll(ctx)
	if err != nil {
		return nil, err
	}

	// Query total member count with minimal pagination.
	total, _, memberErr := o.Database.GetDepartmentMemberInterface().Search(
		ctx, "", "", &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 1})
	if memberErr != nil {
		return nil, memberErr
	}

	now := time.Now().Unix()
	log.ZInfo(ctx, "SyncADOrganization manual trigger completed",
		"departmentCount", len(departments), "memberCount", total, "syncTime", now)

	return &chat.SyncADOrganizationResp{
		DepartmentCount: int32(len(departments)),
		MemberCount:     int32(total),
		SyncTime:        now,
	}, nil
}

// ────────── helper ──────────

func departmentMemberToProto(m *chatdb.DepartmentMember) *chat.ADDepartmentMemberInfo {
	return &chat.ADDepartmentMemberInfo{
		UserID:       m.UserID,
		Username:     m.Username,
		Nickname:     m.Nickname,
		DisplayName:  m.DisplayName,
		Email:        m.Email,
		DepartmentID: m.DepartmentID,
		Position:     m.Position,
		Phone:        m.Phone,
	}
}
