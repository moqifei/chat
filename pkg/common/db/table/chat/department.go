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
)

// Department stores a synchronized Active Directory organizational unit.
// Collection: ad_departments
type Department struct {
	DepartmentID  string    `bson:"department_id"`  // Full DN, unique identifier
	Name          string    `bson:"name"`           // Short OU name
	ParentID      string    `bson:"parent_id"`      // Parent department DN (empty for root)
	Level         int       `bson:"level"`          // Depth in hierarchy (0 = root level)
	MemberCount   int       `bson:"member_count"`   // Number of members in this department
	SubDeptCount  int       `bson:"sub_dept_count"` // Number of child departments
	SyncTime      time.Time `bson:"sync_time"`      // Last sync time
	CreateTime    time.Time `bson:"create_time"`
}

func (Department) TableName() string {
	return "ad_departments"
}

type DepartmentInterface interface {
	Create(ctx context.Context, depts []*Department) error
	UpsertMany(ctx context.Context, depts []*Department) error
	DeleteAll(ctx context.Context) error
	Take(ctx context.Context, departmentID string) (*Department, error)
	Find(ctx context.Context, departmentIDs []string) ([]*Department, error)
	FindAll(ctx context.Context) ([]*Department, error)
	Search(ctx context.Context, keyword string) ([]*Department, error)
	UpdateMemberCount(ctx context.Context, departmentID string, count int) error
	UpdateSubDeptCount(ctx context.Context, departmentID string, count int) error
}
