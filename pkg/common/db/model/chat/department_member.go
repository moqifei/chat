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
	"regexp"

	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/db/pagination"
	"github.com/openimsdk/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
)

func NewDepartmentMember(db *mongo.Database) (chatdb.DepartmentMemberInterface, error) {
	coll := db.Collection("ad_department_members")
	_, err := coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "department_id", Value: 1},
				{Key: "username", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "user_id", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "username", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "nickname", Value: "text"}, {Key: "username", Value: "text"}, {Key: "display_name", Value: "text"}},
		},
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &DepartmentMember{coll: coll}, nil
}

type DepartmentMember struct {
	coll *mongo.Collection
}

func (o *DepartmentMember) Create(ctx context.Context, members []*chatdb.DepartmentMember) error {
	return mongoutil.InsertMany(ctx, o.coll, members)
}

func (o *DepartmentMember) UpsertMany(ctx context.Context, members []*chatdb.DepartmentMember) error {
	for _, m := range members {
		filter := bson.M{"department_id": m.DepartmentID, "username": m.Username}
		update := bson.M{"$set": m}
		opts := options.Update().SetUpsert(true)
		if _, err := o.coll.UpdateOne(ctx, filter, update, opts); err != nil {
			return errs.Wrap(err)
		}
	}
	return nil
}

func (o *DepartmentMember) DeleteAll(ctx context.Context) error {
	_, err := o.coll.DeleteMany(ctx, bson.M{})
	return errs.Wrap(err)
}

func (o *DepartmentMember) DeleteByDepartmentID(ctx context.Context, departmentID string) error {
	_, err := o.coll.DeleteMany(ctx, bson.M{"department_id": departmentID})
	return errs.Wrap(err)
}

func (o *DepartmentMember) Take(ctx context.Context, departmentID string, username string) (*chatdb.DepartmentMember, error) {
	return mongoutil.FindOne[*chatdb.DepartmentMember](ctx, o.coll, bson.M{"department_id": departmentID, "username": username})
}

func (o *DepartmentMember) FindByDepartmentID(ctx context.Context, departmentID string, pagination pagination.Pagination) (int64, []*chatdb.DepartmentMember, error) {
	return mongoutil.FindPage[*chatdb.DepartmentMember](ctx, o.coll, bson.M{"department_id": departmentID}, pagination)
}

func (o *DepartmentMember) FindByDepartmentIDs(ctx context.Context, departmentIDs []string) ([]*chatdb.DepartmentMember, error) {
	return mongoutil.Find[*chatdb.DepartmentMember](ctx, o.coll, bson.M{"department_id": bson.M{"$in": departmentIDs}})
}

func (o *DepartmentMember) FindByUsername(ctx context.Context, username string) ([]*chatdb.DepartmentMember, error) {
	return mongoutil.Find[*chatdb.DepartmentMember](ctx, o.coll, bson.M{"username": username})
}

func (o *DepartmentMember) Search(ctx context.Context, keyword string, departmentID string, pagination pagination.Pagination) (int64, []*chatdb.DepartmentMember, error) {
	escaped := regexp.QuoteMeta(keyword)
	filter := bson.M{
		"$or": []bson.M{
			{"nickname": bson.M{"$regex": escaped, "$options": "i"}},
			{"username": bson.M{"$regex": escaped, "$options": "i"}},
			{"display_name": bson.M{"$regex": escaped, "$options": "i"}},
		},
	}
	if departmentID != "" {
		filter["department_id"] = departmentID
	}
	return mongoutil.FindPage[*chatdb.DepartmentMember](ctx, o.coll, filter, pagination)
}

func (o *DepartmentMember) FindByUserID(ctx context.Context, userID string) ([]*chatdb.DepartmentMember, error) {
	return mongoutil.Find[*chatdb.DepartmentMember](ctx, o.coll, bson.M{"user_id": userID})
}

func (o *DepartmentMember) UpdateUserIDByUsername(ctx context.Context, username string, userID string) error {
	_, err := o.coll.UpdateMany(ctx, bson.M{"username": username}, bson.M{"$set": bson.M{"user_id": userID}})
	return errs.Wrap(err)
}
