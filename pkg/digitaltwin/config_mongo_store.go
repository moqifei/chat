package digitaltwin

import (
	"context"
	"time"

	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const mongoConfigCollection = "digital_twin_config"

type mongoConfigRecord struct {
	UserID    string     `bson:"user_id"`
	Config    UserConfig `bson:"config"`
	CreatedAt time.Time  `bson:"created_at"`
	UpdatedAt time.Time  `bson:"updated_at"`
}

type MongoConfigStore struct {
	coll *mongo.Collection
}

func NewMongoConfigStore(db *mongo.Database) (*MongoConfigStore, error) {
	coll := db.Collection(mongoConfigCollection)
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys: bson.D{
			{Key: "user_id", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &MongoConfigStore{coll: coll}, nil
}

func (s *MongoConfigStore) LoadUserConfig(ctx context.Context, userID string) (UserConfig, bool, error) {
	if userID == "" {
		return UserConfig{}, false, nil
	}
	record, err := mongoutil.FindOne[*mongoConfigRecord](ctx, s.coll, bson.M{"user_id": userID})
	if err != nil {
		if errs.Unwrap(err) == mongo.ErrNoDocuments {
			return UserConfig{}, false, nil
		}
		return UserConfig{}, false, err
	}
	return record.Config, true, nil
}

func (s *MongoConfigStore) SaveUserConfig(ctx context.Context, userID string, cfg UserConfig, now time.Time) error {
	if userID == "" {
		return nil
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.UpdatedAt == 0 {
		cfg.UpdatedAt = now.UnixMilli()
	}
	return mongoutil.UpdateOne(ctx, s.coll,
		bson.M{"user_id": userID},
		bson.M{
			"$set": bson.M{
				"user_id":    userID,
				"config":     cfg,
				"updated_at": now,
			},
			"$setOnInsert": bson.M{
				"created_at": now,
			},
		},
		false,
		options.Update().SetUpsert(true),
	)
}
