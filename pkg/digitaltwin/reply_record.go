package digitaltwin

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/openimsdk/chat/pkg/common/imwebhook"
)

type ReplyRecord struct {
	OwnerUserID        string      `json:"ownerUserID" bson:"owner_user_id"`
	SenderUserID       string      `json:"senderUserID" bson:"sender_user_id"`
	TriggerServerMsgID string      `json:"triggerServerMsgID,omitempty" bson:"trigger_server_msg_id,omitempty"`
	TriggerClientMsgID string      `json:"triggerClientMsgID,omitempty" bson:"trigger_client_msg_id,omitempty"`
	OperationID        string      `json:"operationID,omitempty" bson:"operation_id,omitempty"`
	MessageContent     string      `json:"messageContent,omitempty" bson:"message_content,omitempty"`
	ReplyText          string      `json:"replyText" bson:"reply_text"`
	ReplySource        string      `json:"replySource" bson:"reply_source"`
	ConfigSource       string      `json:"configSource,omitempty" bson:"config_source,omitempty"`
	GeneratorError     string      `json:"generatorError,omitempty" bson:"generator_error,omitempty"`
	Trace              *ReplyTrace `json:"trace,omitempty" bson:"trace,omitempty"`
	CreatedAt          int64       `json:"createdAt" bson:"created_at"`
	ReviewStatus       string      `json:"reviewStatus,omitempty" bson:"review_status,omitempty"`
	ReviewNote         string      `json:"reviewNote,omitempty" bson:"review_note,omitempty"`
	ReviewedAt         int64       `json:"reviewedAt,omitempty" bson:"reviewed_at,omitempty"`
}

type ReplyRecordPage struct {
	Records    []ReplyRecord
	HasMore    bool
	NextCursor int64
	Summary    ReplyRecordSummary
}

type ReplyRecordSummary struct {
	Total         int64 `json:"total"`
	Unreviewed    int64 `json:"unreviewed"`
	NeedsFollowUp int64 `json:"needsFollowUp"`
	Confirmed     int64 `json:"confirmed"`
}

type ReplyRecordStore interface {
	SaveReplyRecord(ctx context.Context, record ReplyRecord) error
	ListReplyRecords(ctx context.Context, ownerUserID string, limit int64, reviewStatus string, beforeCreatedAt int64, senderUserID string) (ReplyRecordPage, error)
	CountReplyRecords(ctx context.Context, ownerUserID string, senderUserID string) (ReplyRecordSummary, error)
	LatestReplyRecord(ctx context.Context, ownerUserID string, senderUserID string) (ReplyRecord, bool, error)
	ReviewReplyRecord(ctx context.Context, ownerUserID string, operationID string, status string, note string, now time.Time) error
}

var (
	replyRecordStoreMu sync.Mutex
	replyRecordStore   ReplyRecordStore
)

func SetReplyRecordStore(store ReplyRecordStore) {
	replyRecordStoreMu.Lock()
	defer replyRecordStoreMu.Unlock()
	replyRecordStore = store
}

func SaveReplyRecord(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq, plan ReplyPlan, configSource string, now time.Time) error {
	store := getReplyRecordStore()
	if store == nil {
		return nil
	}
	return store.SaveReplyRecord(ctx, ReplyRecord{
		OwnerUserID:        req.RecvID,
		SenderUserID:       req.SendID,
		TriggerServerMsgID: req.ServerMsgID,
		TriggerClientMsgID: req.ClientMsgID,
		OperationID:        req.OperationID,
		MessageContent:     ExtractTextContent(req.Content),
		ReplyText:          plan.Content,
		ReplySource:        plan.Source,
		ConfigSource:       configSource,
		GeneratorError:     plan.GeneratorError,
		Trace:              plan.Trace,
		CreatedAt:          now.UnixMilli(),
	})
}

func ListReplyRecords(ctx context.Context, ownerUserID string, limit int64, reviewStatus string, beforeCreatedAt int64, senderUserID string) (ReplyRecordPage, error) {
	store := getReplyRecordStore()
	if store == nil {
		return ReplyRecordPage{Records: []ReplyRecord{}}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	reviewStatus = NormalizeListReviewStatus(reviewStatus)
	senderUserID = strings.TrimSpace(senderUserID)
	page, err := store.ListReplyRecords(ctx, ownerUserID, limit, reviewStatus, beforeCreatedAt, senderUserID)
	if err != nil {
		return ReplyRecordPage{}, err
	}
	summary, err := store.CountReplyRecords(ctx, ownerUserID, senderUserID)
	if err != nil {
		return ReplyRecordPage{}, err
	}
	page.Summary = summary
	return page, nil
}

func LatestReplyRecord(ctx context.Context, ownerUserID string, senderUserID string) (ReplyRecord, bool, error) {
	store := getReplyRecordStore()
	if store == nil || ownerUserID == "" || senderUserID == "" {
		return ReplyRecord{}, false, nil
	}
	return store.LatestReplyRecord(ctx, ownerUserID, senderUserID)
}

func ReviewReplyRecord(ctx context.Context, ownerUserID string, operationID string, status string, note string, now time.Time) error {
	store := getReplyRecordStore()
	if store == nil || ownerUserID == "" || operationID == "" {
		return nil
	}
	status = NormalizeReviewStatus(status)
	if status == "" {
		return nil
	}
	return store.ReviewReplyRecord(ctx, ownerUserID, operationID, status, NormalizeReviewNote(note), now)
}

func NormalizeReviewStatus(status string) string {
	switch status {
	case "confirmed", "needs_follow_up":
		return status
	default:
		return ""
	}
}

func NormalizeListReviewStatus(status string) string {
	switch status {
	case "", "unreviewed", "confirmed", "needs_follow_up":
		return status
	default:
		return ""
	}
}

func NormalizeReviewNote(note string) string {
	note = strings.TrimSpace(note)
	if len([]rune(note)) <= 500 {
		return note
	}
	return string([]rune(note)[:500])
}

func getReplyRecordStore() ReplyRecordStore {
	replyRecordStoreMu.Lock()
	defer replyRecordStoreMu.Unlock()
	return replyRecordStore
}
