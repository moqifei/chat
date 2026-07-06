package digitaltwin

import (
	"context"
	"strings"
	"time"

	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const mongoReplyRecordCollection = "digital_twin_reply_record"

type MongoReplyRecordStore struct {
	coll *mongo.Collection
}

func NewMongoReplyRecordStore(db *mongo.Database) (*MongoReplyRecordStore, error) {
	coll := db.Collection(mongoReplyRecordCollection)
	_, err := coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "owner_user_id", Value: 1},
				{Key: "created_at", Value: -1},
			},
		},
		{
			Keys: bson.D{
				{Key: "owner_user_id", Value: 1},
				{Key: "sender_user_id", Value: 1},
				{Key: "created_at", Value: -1},
			},
		},
		{
			Keys: bson.D{
				{Key: "owner_user_id", Value: 1},
				{Key: "review_status", Value: 1},
				{Key: "created_at", Value: -1},
			},
		},
		{
			Keys: bson.D{
				{Key: "operation_id", Value: 1},
			},
		},
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &MongoReplyRecordStore{coll: coll}, nil
}

func (s *MongoReplyRecordStore) SaveReplyRecord(ctx context.Context, record ReplyRecord) error {
	if record.OwnerUserID == "" {
		return nil
	}
	_, err := s.coll.InsertOne(ctx, record)
	if err == nil {
		return nil
	}
	return errs.Wrap(err)
}

func (s *MongoReplyRecordStore) ListReplyRecords(ctx context.Context, ownerUserID string, limit int64, reviewStatus string, beforeCreatedAt int64, senderUserID string) (ReplyRecordPage, error) {
	if ownerUserID == "" {
		return ReplyRecordPage{Records: []ReplyRecord{}}, nil
	}
	filter := replyRecordFilter(ownerUserID, reviewStatus, senderUserID)
	if beforeCreatedAt > 0 {
		filter["created_at"] = bson.M{"$lt": beforeCreatedAt}
	}
	records, err := mongoutil.Find[ReplyRecord](
		ctx,
		s.coll,
		filter,
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit+1),
	)
	if err != nil {
		return ReplyRecordPage{}, err
	}
	if records == nil {
		records = []ReplyRecord{}
	}
	page := ReplyRecordPage{Records: records}
	if int64(len(records)) > limit {
		page.HasMore = true
		page.Records = records[:limit]
	}
	if len(page.Records) > 0 {
		page.NextCursor = page.Records[len(page.Records)-1].CreatedAt
	}
	return page, nil
}

func (s *MongoReplyRecordStore) CountReplyRecords(ctx context.Context, ownerUserID string, senderUserID string) (ReplyRecordSummary, error) {
	if ownerUserID == "" {
		return ReplyRecordSummary{}, nil
	}
	total, err := s.coll.CountDocuments(ctx, replyRecordFilter(ownerUserID, "", senderUserID))
	if err != nil {
		return ReplyRecordSummary{}, errs.Wrap(err)
	}
	unreviewed, err := s.coll.CountDocuments(ctx, replyRecordFilter(ownerUserID, "unreviewed", senderUserID))
	if err != nil {
		return ReplyRecordSummary{}, errs.Wrap(err)
	}
	needsFollowUp, err := s.coll.CountDocuments(ctx, replyRecordFilter(ownerUserID, "needs_follow_up", senderUserID))
	if err != nil {
		return ReplyRecordSummary{}, errs.Wrap(err)
	}
	confirmed, err := s.coll.CountDocuments(ctx, replyRecordFilter(ownerUserID, "confirmed", senderUserID))
	if err != nil {
		return ReplyRecordSummary{}, errs.Wrap(err)
	}
	return ReplyRecordSummary{
		Total:         total,
		Unreviewed:    unreviewed,
		NeedsFollowUp: needsFollowUp,
		Confirmed:     confirmed,
	}, nil
}

func replyRecordFilter(ownerUserID string, reviewStatus string, senderUserID string) bson.M {
	filter := bson.M{"owner_user_id": ownerUserID}
	if senderUserID = strings.TrimSpace(senderUserID); senderUserID != "" {
		filter["sender_user_id"] = senderUserID
	}
	switch reviewStatus {
	case "confirmed", "needs_follow_up":
		filter["review_status"] = reviewStatus
	case "unreviewed":
		filter["$or"] = []bson.M{
			{"review_status": bson.M{"$exists": false}},
			{"review_status": ""},
		}
	}
	return filter
}

func (s *MongoReplyRecordStore) LatestReplyRecord(ctx context.Context, ownerUserID string, senderUserID string) (ReplyRecord, bool, error) {
	if ownerUserID == "" || senderUserID == "" {
		return ReplyRecord{}, false, nil
	}
	var record ReplyRecord
	err := s.coll.FindOne(
		ctx,
		bson.M{"owner_user_id": ownerUserID, "sender_user_id": senderUserID},
		options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}}),
	).Decode(&record)
	if err == mongo.ErrNoDocuments {
		return ReplyRecord{}, false, nil
	}
	if err != nil {
		return ReplyRecord{}, false, errs.Wrap(err)
	}
	return record, true, nil
}

func (s *MongoReplyRecordStore) ReviewReplyRecord(ctx context.Context, ownerUserID string, operationID string, status string, note string, now time.Time) error {
	if ownerUserID == "" || operationID == "" || status == "" {
		return nil
	}
	update := bson.M{
		"$set": bson.M{
			"review_status": status,
			"review_note":   strings.TrimSpace(note),
			"reviewed_at":   now.UnixMilli(),
		},
	}
	_, err := s.coll.UpdateOne(ctx, bson.M{
		"owner_user_id": ownerUserID,
		"operation_id":  operationID,
	}, update)
	if err == nil {
		return nil
	}
	return errs.Wrap(err)
}
