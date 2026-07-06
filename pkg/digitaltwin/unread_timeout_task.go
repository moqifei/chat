package digitaltwin

import (
	"context"
	"sync"
	"time"

	"github.com/openimsdk/chat/pkg/common/imwebhook"
)

const (
	UnreadTimeoutTaskStatusPending   = "pending"
	UnreadTimeoutTaskStatusExecuting = "executing"
	UnreadTimeoutTaskStatusCompleted = "completed"
	UnreadTimeoutTaskStatusFailed    = "failed"
	UnreadTimeoutTaskStatusCanceled  = "canceled"

	UnreadTimeoutTaskCancelReasonReadBeforeTimeout = "read_before_timeout"

	DefaultUnreadTimeoutTaskLeaseTimeout = 2 * time.Minute
)

type UnreadTimeoutTask struct {
	TaskKey            string                                  `json:"taskKey" bson:"task_key"`
	ChannelKey         string                                  `json:"channelKey,omitempty" bson:"channel_key,omitempty"`
	OwnerUserID        string                                  `json:"ownerUserID" bson:"owner_user_id"`
	SenderUserID       string                                  `json:"senderUserID" bson:"sender_user_id"`
	Request            imwebhook.CallbackAfterSendSingleMsgReq `json:"-" bson:"request,omitempty"`
	TriggerServerMsgID string                                  `json:"triggerServerMsgID,omitempty" bson:"trigger_server_msg_id,omitempty"`
	TriggerClientMsgID string                                  `json:"triggerClientMsgID,omitempty" bson:"trigger_client_msg_id,omitempty"`
	TriggerSeq         int64                                   `json:"triggerSeq,omitempty" bson:"trigger_seq,omitempty"`
	OperationID        string                                  `json:"operationID,omitempty" bson:"operation_id,omitempty"`
	MessageContent     string                                  `json:"messageContent,omitempty" bson:"message_content,omitempty"`
	Status             string                                  `json:"status" bson:"status"`
	Error              string                                  `json:"error,omitempty" bson:"error,omitempty"`
	AttemptCount       int64                                   `json:"attemptCount,omitempty" bson:"attempt_count,omitempty"`
	DueAt              int64                                   `json:"dueAt" bson:"due_at"`
	CreatedAt          int64                                   `json:"createdAt" bson:"created_at"`
	UpdatedAt          int64                                   `json:"updatedAt" bson:"updated_at"`
}

type UnreadTimeoutTaskSummary struct {
	Pending int64 `json:"pending"`
}

type UnreadTimeoutTaskStore interface {
	UpsertUnreadTimeoutTask(ctx context.Context, task UnreadTimeoutTask) error
	ClaimUnreadTimeoutTask(ctx context.Context, taskKey string, operationID string, now time.Time) (bool, error)
	ClaimDueUnreadTimeoutTasks(ctx context.Context, now time.Time, limit int64) ([]UnreadTimeoutTask, error)
	UpdateUnreadTimeoutTaskStatus(ctx context.Context, taskKey string, operationID string, status string, errText string, now time.Time) error
	CancelUnreadTimeoutTasksByReadSeqs(ctx context.Context, ownerUserID string, senderUserID string, seqs []int64, reason string, now time.Time) (int64, error)
	CountUnreadTimeoutTasks(ctx context.Context, ownerUserID string) (UnreadTimeoutTaskSummary, error)
}

var (
	unreadTimeoutTaskStoreMu sync.Mutex
	unreadTimeoutTaskStore   UnreadTimeoutTaskStore
)

func SetUnreadTimeoutTaskStore(store UnreadTimeoutTaskStore) {
	unreadTimeoutTaskStoreMu.Lock()
	defer unreadTimeoutTaskStoreMu.Unlock()
	unreadTimeoutTaskStore = store
}

func SaveUnreadTimeoutTask(ctx context.Context, taskKey string, channelKey string, delay time.Duration, req imwebhook.CallbackAfterSendSingleMsgReq, now time.Time) error {
	store := getUnreadTimeoutTaskStore()
	if store == nil || taskKey == "" {
		return nil
	}
	return store.UpsertUnreadTimeoutTask(ctx, UnreadTimeoutTask{
		TaskKey:            taskKey,
		ChannelKey:         channelKey,
		OwnerUserID:        req.RecvID,
		SenderUserID:       req.SendID,
		Request:            req,
		TriggerServerMsgID: req.ServerMsgID,
		TriggerClientMsgID: req.ClientMsgID,
		TriggerSeq:         int64(req.Seq),
		OperationID:        req.OperationID,
		MessageContent:     ExtractTextContent(req.Content),
		Status:             UnreadTimeoutTaskStatusPending,
		DueAt:              now.Add(delay).UnixMilli(),
		CreatedAt:          now.UnixMilli(),
		UpdatedAt:          now.UnixMilli(),
	})
}

func ClaimUnreadTimeoutTask(ctx context.Context, taskKey string, operationID string, now time.Time) (bool, error) {
	store := getUnreadTimeoutTaskStore()
	if store == nil || taskKey == "" {
		return true, nil
	}
	return store.ClaimUnreadTimeoutTask(ctx, taskKey, operationID, now)
}

func ClaimDueUnreadTimeoutTasks(ctx context.Context, now time.Time, limit int64) ([]UnreadTimeoutTask, error) {
	store := getUnreadTimeoutTaskStore()
	if store == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return store.ClaimDueUnreadTimeoutTasks(ctx, now, limit)
}

func MarkUnreadTimeoutTaskStatus(ctx context.Context, taskKey string, operationID string, status string, errText string, now time.Time) error {
	store := getUnreadTimeoutTaskStore()
	if store == nil || taskKey == "" {
		return nil
	}
	return store.UpdateUnreadTimeoutTaskStatus(ctx, taskKey, operationID, status, errText, now)
}

func CancelUnreadTimeoutTasksByReadSeqs(ctx context.Context, ownerUserID string, senderUserID string, seqs []int64, reason string, now time.Time) (int64, error) {
	store := getUnreadTimeoutTaskStore()
	if store == nil || ownerUserID == "" || len(seqs) == 0 {
		return 0, nil
	}
	if reason == "" {
		reason = UnreadTimeoutTaskCancelReasonReadBeforeTimeout
	}
	return store.CancelUnreadTimeoutTasksByReadSeqs(ctx, ownerUserID, senderUserID, seqs, reason, now)
}

func CountUnreadTimeoutTasks(ctx context.Context, ownerUserID string) (UnreadTimeoutTaskSummary, error) {
	store := getUnreadTimeoutTaskStore()
	if store == nil || ownerUserID == "" {
		return UnreadTimeoutTaskSummary{}, nil
	}
	return store.CountUnreadTimeoutTasks(ctx, ownerUserID)
}

func getUnreadTimeoutTaskStore() UnreadTimeoutTaskStore {
	unreadTimeoutTaskStoreMu.Lock()
	defer unreadTimeoutTaskStoreMu.Unlock()
	return unreadTimeoutTaskStore
}
