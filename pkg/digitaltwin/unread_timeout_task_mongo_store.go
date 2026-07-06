package digitaltwin

import (
	"context"
	"strings"
	"time"

	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const mongoUnreadTimeoutTaskCollection = "digital_twin_unread_timeout_task"

type MongoUnreadTimeoutTaskStore struct {
	coll *mongo.Collection
}

func NewMongoUnreadTimeoutTaskStore(db *mongo.Database) (*MongoUnreadTimeoutTaskStore, error) {
	coll := db.Collection(mongoUnreadTimeoutTaskCollection)
	_, err := coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "task_key", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "owner_user_id", Value: 1},
				{Key: "status", Value: 1},
				{Key: "due_at", Value: 1},
			},
		},
		{
			Keys: bson.D{
				{Key: "owner_user_id", Value: 1},
				{Key: "status", Value: 1},
				{Key: "trigger_seq", Value: 1},
			},
		},
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &MongoUnreadTimeoutTaskStore{coll: coll}, nil
}

func (s *MongoUnreadTimeoutTaskStore) UpsertUnreadTimeoutTask(ctx context.Context, task UnreadTimeoutTask) error {
	if task.TaskKey == "" {
		return nil
	}
	_, err := s.coll.UpdateOne(ctx,
		bson.M{"task_key": task.TaskKey},
		bson.M{
			"$set": bson.M{
				"task_key":              task.TaskKey,
				"channel_key":           task.ChannelKey,
				"owner_user_id":         task.OwnerUserID,
				"sender_user_id":        task.SenderUserID,
				"trigger_server_msg_id": task.TriggerServerMsgID,
				"trigger_client_msg_id": task.TriggerClientMsgID,
				"trigger_seq":           task.TriggerSeq,
				"operation_id":          task.OperationID,
				"message_content":       task.MessageContent,
				"request":               task.Request,
				"status":                task.Status,
				"error":                 "",
				"attempt_count":         task.AttemptCount,
				"due_at":                task.DueAt,
				"updated_at":            task.UpdatedAt,
			},
			"$setOnInsert": bson.M{
				"created_at": task.CreatedAt,
			},
		},
		options.Update().SetUpsert(true),
	)
	if err == nil {
		return nil
	}
	return errs.Wrap(err)
}

func (s *MongoUnreadTimeoutTaskStore) ClaimUnreadTimeoutTask(ctx context.Context, taskKey string, operationID string, now time.Time) (bool, error) {
	taskKey = strings.TrimSpace(taskKey)
	if taskKey == "" {
		return false, nil
	}
	filter := bson.M{
		"task_key": taskKey,
		"status":   UnreadTimeoutTaskStatusPending,
	}
	if operationID != "" {
		filter["operation_id"] = operationID
	}
	result, err := s.coll.UpdateOne(ctx, filter, bson.M{
		"$set": bson.M{
			"status":     UnreadTimeoutTaskStatusExecuting,
			"updated_at": now.UnixMilli(),
		},
		"$inc": bson.M{"attempt_count": 1},
	})
	if err != nil {
		return false, errs.Wrap(err)
	}
	return result.ModifiedCount > 0, nil
}

func (s *MongoUnreadTimeoutTaskStore) ClaimDueUnreadTimeoutTasks(ctx context.Context, now time.Time, limit int64) ([]UnreadTimeoutTask, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tasks := make([]UnreadTimeoutTask, 0, limit)
	leaseCutoff := now.Add(-DefaultUnreadTimeoutTaskLeaseTimeout).UnixMilli()
	for int64(len(tasks)) < limit {
		var task UnreadTimeoutTask
		err := s.coll.FindOneAndUpdate(
			ctx,
			bson.M{
				"$or": []bson.M{
					{
						"status": UnreadTimeoutTaskStatusPending,
						"due_at": bson.M{"$lte": now.UnixMilli()},
					},
					{
						"status":     UnreadTimeoutTaskStatusExecuting,
						"updated_at": bson.M{"$lte": leaseCutoff},
					},
				},
			},
			bson.M{
				"$set": bson.M{
					"status":     UnreadTimeoutTaskStatusExecuting,
					"updated_at": now.UnixMilli(),
				},
				"$inc": bson.M{"attempt_count": 1},
			},
			options.FindOneAndUpdate().
				SetSort(bson.D{{Key: "due_at", Value: 1}}).
				SetReturnDocument(options.After),
		).Decode(&task)
		if err == mongo.ErrNoDocuments {
			return tasks, nil
		}
		if err != nil {
			return nil, errs.Wrap(err)
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *MongoUnreadTimeoutTaskStore) UpdateUnreadTimeoutTaskStatus(ctx context.Context, taskKey string, operationID string, status string, errText string, now time.Time) error {
	taskKey = strings.TrimSpace(taskKey)
	if taskKey == "" {
		return nil
	}
	update := bson.M{
		"status":     status,
		"updated_at": now.UnixMilli(),
	}
	if errText != "" {
		update["error"] = errText
	}
	filter := bson.M{"task_key": taskKey}
	if operationID != "" {
		filter["operation_id"] = operationID
	}
	_, err := s.coll.UpdateOne(ctx, filter, bson.M{"$set": update})
	if err == nil {
		return nil
	}
	return errs.Wrap(err)
}

func (s *MongoUnreadTimeoutTaskStore) CancelUnreadTimeoutTasksByReadSeqs(ctx context.Context, ownerUserID string, senderUserID string, seqs []int64, reason string, now time.Time) (int64, error) {
	ownerUserID = strings.TrimSpace(ownerUserID)
	if ownerUserID == "" || len(seqs) == 0 {
		log.ZInfo(ctx, "digital twin unread timeout read cancel skipped",
			"ownerUserID", ownerUserID,
			"senderUserID", senderUserID,
			"seqs", seqs,
			"reason", "missing_owner_or_seqs",
		)
		return 0, nil
	}
	baseFilter := bson.M{
		"owner_user_id": ownerUserID,
		"status":        UnreadTimeoutTaskStatusPending,
	}
	if senderUserID = strings.TrimSpace(senderUserID); senderUserID != "" {
		baseFilter["sender_user_id"] = senderUserID
	}
	exactSeqFilter := cloneBSONMap(baseFilter)
	exactSeqFilter["trigger_seq"] = bson.M{"$in": seqs}
	fallbackSeqFilter := cloneBSONMap(baseFilter)
	fallbackSeqFilter["$or"] = []bson.M{
		{"trigger_seq": 0},
		{"trigger_seq": bson.M{"$exists": false}},
	}
	filter := cloneBSONMap(baseFilter)
	filter["$or"] = []bson.M{
		{"trigger_seq": bson.M{"$in": seqs}},
		{"trigger_seq": 0},
		{"trigger_seq": bson.M{"$exists": false}},
	}
	baseCount, baseErr := mongoutil.Count(ctx, s.coll, baseFilter)
	exactSeqCount, exactErr := mongoutil.Count(ctx, s.coll, exactSeqFilter)
	fallbackSeqCount, fallbackErr := mongoutil.Count(ctx, s.coll, fallbackSeqFilter)
	if baseErr != nil || exactErr != nil || fallbackErr != nil {
		log.ZWarn(ctx, "digital twin unread timeout read cancel diagnostics count failed", firstNonNilError(baseErr, exactErr, fallbackErr),
			"ownerUserID", ownerUserID,
			"senderUserID", senderUserID,
			"seqs", seqs,
			"baseErr", errorString(baseErr),
			"exactErr", errorString(exactErr),
			"fallbackErr", errorString(fallbackErr),
		)
	} else {
		log.ZInfo(ctx, "digital twin unread timeout read cancel diagnostics",
			"ownerUserID", ownerUserID,
			"senderUserID", senderUserID,
			"seqs", seqs,
			"pendingByOwnerSender", baseCount,
			"pendingByExactSeq", exactSeqCount,
			"pendingByFallbackSeq", fallbackSeqCount,
		)
	}
	result, err := s.coll.UpdateMany(ctx,
		filter,
		bson.M{
			"$set": bson.M{
				"status":     UnreadTimeoutTaskStatusCanceled,
				"error":      reason,
				"updated_at": now.UnixMilli(),
			},
		},
	)
	if err != nil {
		return 0, errs.Wrap(err)
	}
	log.ZInfo(ctx, "digital twin unread timeout read cancel updated",
		"ownerUserID", ownerUserID,
		"senderUserID", senderUserID,
		"seqs", seqs,
		"modified", result.ModifiedCount,
	)
	return result.ModifiedCount, nil
}

func cloneBSONMap(src bson.M) bson.M {
	dst := make(bson.M, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func firstNonNilError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *MongoUnreadTimeoutTaskStore) CountUnreadTimeoutTasks(ctx context.Context, ownerUserID string) (UnreadTimeoutTaskSummary, error) {
	if ownerUserID == "" {
		return UnreadTimeoutTaskSummary{}, nil
	}
	pending, err := mongoutil.Count(ctx, s.coll, bson.M{
		"owner_user_id": ownerUserID,
		"status": bson.M{
			"$in": []string{UnreadTimeoutTaskStatusPending, UnreadTimeoutTaskStatusExecuting},
		},
	})
	if err != nil {
		return UnreadTimeoutTaskSummary{}, errs.Wrap(err)
	}
	return UnreadTimeoutTaskSummary{Pending: pending}, nil
}
