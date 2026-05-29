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

	"github.com/openimsdk/tools/db/pagination"
)

// DepartmentMember stores a synchronized AD user-to-department mapping.
// Collection: ad_department_members
type DepartmentMember struct {
	DepartmentID string    `bson:"department_id"` // Department DN
	UserID       string    `bson:"user_id"`       // OpenIM / chat user ID (empty for users not yet registered)
	Username     string    `bson:"username"`      // AD sAMAccountName
	DisplayName  string    `bson:"display_name"`  // Full AD displayName
	Nickname     string    `bson:"nickname"`      // Extracted nickname (without department suffix)
	Email        string    `bson:"email"`         // AD email (mail)
	DN           string    `bson:"dn"`            // Full user DN
	Position     string    `bson:"position"`      // Job title
	Phone        string    `bson:"phone"`         // Phone number
	IsPrimary    bool      `bson:"is_primary"`    // Whether this is the primary department
	SyncTime     time.Time `bson:"sync_time"`
	CreateTime   time.Time `bson:"create_time"`
}

func (DepartmentMember) TableName() string {
	return "ad_department_members"
}

type DepartmentMemberInterface interface {
	Create(ctx context.Context, members []*DepartmentMember) error
	UpsertMany(ctx context.Context, members []*DepartmentMember) error
	DeleteAll(ctx context.Context) error
	DeleteByDepartmentID(ctx context.Context, departmentID string) error
	Take(ctx context.Context, departmentID string, username string) (*DepartmentMember, error)
	FindByDepartmentID(ctx context.Context, departmentID string, pagination pagination.Pagination) (int64, []*DepartmentMember, error)
	FindByDepartmentIDs(ctx context.Context, departmentIDs []string) ([]*DepartmentMember, error)
	FindByUsername(ctx context.Context, username string) ([]*DepartmentMember, error)
	Search(ctx context.Context, keyword string, departmentID string, pagination pagination.Pagination) (int64, []*DepartmentMember, error)
	FindByUserID(ctx context.Context, userID string) ([]*DepartmentMember, error)
	UpdateUserIDByUsername(ctx context.Context, username string, userID string) error
}
