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

	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
)

func NewDepartment(db *mongo.Database) (chatdb.DepartmentInterface, error) {
	coll := db.Collection("ad_departments")
	_, err := coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "department_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "parent_id", Value: 1},
				{Key: "name", Value: 1},
			},
		},
		{
			Keys: bson.D{{Key: "name", Value: "text"}},
		},
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &Department{coll: coll}, nil
}

type Department struct {
	coll *mongo.Collection
}

func (o *Department) Create(ctx context.Context, depts []*chatdb.Department) error {
	return mongoutil.InsertMany(ctx, o.coll, depts)
}

func (o *Department) UpsertMany(ctx context.Context, depts []*chatdb.Department) error {
	for _, dept := range depts {
		filter := bson.M{"department_id": dept.DepartmentID}
		update := bson.M{"$set": dept}
		opts := options.Update().SetUpsert(true)
		if _, err := o.coll.UpdateOne(ctx, filter, update, opts); err != nil {
			return errs.Wrap(err)
		}
	}
	return nil
}

func (o *Department) DeleteAll(ctx context.Context) error {
	_, err := o.coll.DeleteMany(ctx, bson.M{})
	return errs.Wrap(err)
}

func (o *Department) Take(ctx context.Context, departmentID string) (*chatdb.Department, error) {
	return mongoutil.FindOne[*chatdb.Department](ctx, o.coll, bson.M{"department_id": departmentID})
}

func (o *Department) Find(ctx context.Context, departmentIDs []string) ([]*chatdb.Department, error) {
	return mongoutil.Find[*chatdb.Department](ctx, o.coll, bson.M{"department_id": bson.M{"$in": departmentIDs}})
}

func (o *Department) FindAll(ctx context.Context) ([]*chatdb.Department, error) {
	opts := options.Find().SetSort(bson.D{{Key: "level", Value: 1}, {Key: "name", Value: 1}})
	return mongoutil.Find[*chatdb.Department](ctx, o.coll, bson.M{}, opts)
}

func (o *Department) Search(ctx context.Context, keyword string) ([]*chatdb.Department, error) {
	return mongoutil.Find[*chatdb.Department](ctx, o.coll, bson.M{"name": bson.M{"$regex": keyword, "$options": "i"}})
}

func (o *Department) UpdateMemberCount(ctx context.Context, departmentID string, count int) error {
	return mongoutil.UpdateOne(ctx, o.coll, bson.M{"department_id": departmentID}, bson.M{
		"$set": bson.M{"member_count": count, "sync_time": time.Now()},
	}, false)
}

func (o *Department) UpdateSubDeptCount(ctx context.Context, departmentID string, count int) error {
	return mongoutil.UpdateOne(ctx, o.coll, bson.M{"department_id": departmentID}, bson.M{
		"$set": bson.M{"sub_dept_count": count, "sync_time": time.Now()},
	}, false)
}
