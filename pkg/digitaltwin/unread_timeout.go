package digitaltwin

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/openimsdk/chat/pkg/common/imwebhook"
	constantpb "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/log"
)

type UnreadTimeoutExecuteFunc func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error
type UnreadTimeoutTaskExecuteFunc func(ctx context.Context, task UnreadTimeoutTask) error

type UnreadTimeoutScheduleResult struct {
	Scheduled bool          `json:"scheduled"`
	Replaced  bool          `json:"replaced"`
	Key       string        `json:"key,omitempty"`
	Delay     time.Duration `json:"delay"`
}

type unreadTimeoutScheduler struct {
	mu    sync.Mutex
	tasks map[string]*unreadTimeoutTask
}

type unreadTimeoutTask struct {
	timer      *time.Timer
	channelKey string
	req        imwebhook.CallbackAfterSendSingleMsgReq
	execute    UnreadTimeoutExecuteFunc
}

var defaultUnreadTimeoutScheduler = newUnreadTimeoutScheduler()

func ScheduleUnreadTimeoutReply(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq, cfg Config, channelKey string, execute UnreadTimeoutExecuteFunc) UnreadTimeoutScheduleResult {
	if normalizeTriggerMode(cfg.TriggerMode) != TriggerModeUnreadTimeout || execute == nil {
		return UnreadTimeoutScheduleResult{}
	}
	delay := unreadTimeoutDelay(cfg)
	key := unreadTimeoutTaskKey(req)
	if key == "" {
		return UnreadTimeoutScheduleResult{}
	}
	return defaultUnreadTimeoutScheduler.Schedule(ctx, key, channelKey, delay, req, execute)
}

func ResetUnreadTimeoutSchedulerForTest() {
	defaultUnreadTimeoutScheduler.Reset()
}

func newUnreadTimeoutScheduler() *unreadTimeoutScheduler {
	return &unreadTimeoutScheduler{
		tasks: make(map[string]*unreadTimeoutTask),
	}
}

func (s *unreadTimeoutScheduler) Schedule(ctx context.Context, key string, channelKey string, delay time.Duration, req imwebhook.CallbackAfterSendSingleMsgReq, execute UnreadTimeoutExecuteFunc) UnreadTimeoutScheduleResult {
	if delay <= 0 {
		delay = time.Second
	}
	task := &unreadTimeoutTask{
		channelKey: channelKey,
		req:        req,
		execute:    execute,
	}
	task.timer = time.AfterFunc(delay, func() {
		s.fire(key, task)
	})
	now := time.Now()

	s.mu.Lock()
	oldTask, replaced := s.tasks[key]
	if replaced {
		oldTask.timer.Stop()
	}
	s.tasks[key] = task
	s.mu.Unlock()

	if err := SaveUnreadTimeoutTask(ctx, key, channelKey, delay, req, now); err != nil {
		log.ZWarn(ctx, "digital twin unread timeout task save failed", err,
			"key", key,
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
		)
	}
	log.ZInfo(ctx, "digital twin unread timeout scheduled",
		"key", key,
		"delay", delay.String(),
		"replaced", replaced,
		"hasChannelKey", channelKey != "",
		"ownerUserID", req.RecvID,
		"senderUserID", req.SendID,
		"triggerSeq", req.Seq,
		"triggerServerMsgID", req.ServerMsgID,
		"triggerClientMsgID", req.ClientMsgID,
		"operationID", req.OperationID,
	)
	return UnreadTimeoutScheduleResult{
		Scheduled: true,
		Replaced:  replaced,
		Key:       key,
		Delay:     delay,
	}
}

func (s *unreadTimeoutScheduler) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.tasks {
		task.timer.Stop()
	}
	s.tasks = make(map[string]*unreadTimeoutTask)
}

func (s *unreadTimeoutScheduler) fire(key string, task *unreadTimeoutTask) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.ZError(context.Background(), "digital twin unread timeout panic recovered", fmt.Errorf("panic recovered: %v", recovered),
				"key", key,
				"ownerUserID", task.req.RecvID,
				"senderUserID", task.req.SendID,
				"operationID", task.req.OperationID,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
		}
	}()

	s.mu.Lock()
	if s.tasks[key] != task {
		s.mu.Unlock()
		return
	}
	delete(s.tasks, key)
	s.mu.Unlock()

	ctx := context.Background()
	if task.req.OperationID != "" {
		ctx = context.WithValue(ctx, constantpb.OperationID, task.req.OperationID)
	}
	claimed, err := ClaimUnreadTimeoutTask(ctx, key, task.req.OperationID, time.Now())
	if err != nil {
		log.ZWarn(ctx, "digital twin unread timeout task claim failed", err,
			"key", key,
			"ownerUserID", task.req.RecvID,
			"senderUserID", task.req.SendID,
			"operationID", task.req.OperationID,
		)
	} else if !claimed {
		log.ZInfo(ctx, "digital twin unread timeout task already claimed",
			"key", key,
			"ownerUserID", task.req.RecvID,
			"senderUserID", task.req.SendID,
			"operationID", task.req.OperationID,
		)
		return
	}
	if err := task.execute(ctx, task.req); err != nil {
		if markErr := MarkUnreadTimeoutTaskStatus(ctx, key, task.req.OperationID, UnreadTimeoutTaskStatusFailed, err.Error(), time.Now()); markErr != nil {
			log.ZWarn(ctx, "digital twin unread timeout task mark failed failed", markErr,
				"key", key,
				"ownerUserID", task.req.RecvID,
				"senderUserID", task.req.SendID,
				"operationID", task.req.OperationID,
			)
		}
		log.ZWarn(ctx, "digital twin unread timeout execute failed", err,
			"key", key,
			"ownerUserID", task.req.RecvID,
			"senderUserID", task.req.SendID,
			"operationID", task.req.OperationID,
		)
		return
	}
	if err := MarkUnreadTimeoutTaskStatus(ctx, key, task.req.OperationID, UnreadTimeoutTaskStatusCompleted, "", time.Now()); err != nil {
		log.ZWarn(ctx, "digital twin unread timeout task mark completed failed", err,
			"key", key,
			"ownerUserID", task.req.RecvID,
			"senderUserID", task.req.SendID,
			"operationID", task.req.OperationID,
		)
	}
}

func StartUnreadTimeoutTaskWorker(ctx context.Context, interval time.Duration, batchSize int64, execute UnreadTimeoutTaskExecuteFunc) {
	if execute == nil || getUnreadTimeoutTaskStore() == nil {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if batchSize <= 0 || batchSize > 100 {
		batchSize = 20
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		runDueUnreadTimeoutTasks(ctx, batchSize, execute)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runDueUnreadTimeoutTasks(ctx, batchSize, execute)
			}
		}
	}()
	log.ZInfo(ctx, "digital twin unread timeout task worker started",
		"interval", interval.String(),
		"batchSize", batchSize,
	)
}

func runDueUnreadTimeoutTasks(ctx context.Context, batchSize int64, execute UnreadTimeoutTaskExecuteFunc) {
	tasks, err := ClaimDueUnreadTimeoutTasks(ctx, time.Now(), batchSize)
	if err != nil {
		log.ZWarn(ctx, "digital twin unread timeout task worker claim failed", err)
		return
	}
	for _, task := range tasks {
		executeClaimedUnreadTimeoutTask(ctx, task, execute)
	}
}

func executeClaimedUnreadTimeoutTask(ctx context.Context, task UnreadTimeoutTask, execute UnreadTimeoutTaskExecuteFunc) {
	taskCtx := context.Background()
	if task.OperationID != "" {
		taskCtx = context.WithValue(taskCtx, constantpb.OperationID, task.OperationID)
	}
	log.ZInfo(taskCtx, "digital twin unread timeout task executing",
		"key", task.TaskKey,
		"status", task.Status,
		"attemptCount", task.AttemptCount,
		"ownerUserID", task.OwnerUserID,
		"senderUserID", task.SenderUserID,
		"operationID", task.OperationID,
	)
	if task.Request.RecvID == "" || task.Request.SendID == "" {
		if err := MarkUnreadTimeoutTaskStatus(taskCtx, task.TaskKey, task.OperationID, UnreadTimeoutTaskStatusFailed, "missing persisted callback request", time.Now()); err != nil {
			log.ZWarn(taskCtx, "digital twin unread timeout task mark failed failed", err,
				"key", task.TaskKey,
				"ownerUserID", task.OwnerUserID,
				"senderUserID", task.SenderUserID,
				"operationID", task.OperationID,
			)
		}
		return
	}
	if task.ChannelKey == "" {
		if err := MarkUnreadTimeoutTaskStatus(taskCtx, task.TaskKey, task.OperationID, UnreadTimeoutTaskStatusFailed, "missing persisted callback channel key", time.Now()); err != nil {
			log.ZWarn(taskCtx, "digital twin unread timeout task mark failed failed", err,
				"key", task.TaskKey,
				"ownerUserID", task.OwnerUserID,
				"senderUserID", task.SenderUserID,
				"operationID", task.OperationID,
			)
		}
		return
	}
	if err := execute(taskCtx, task); err != nil {
		if markErr := MarkUnreadTimeoutTaskStatus(taskCtx, task.TaskKey, task.OperationID, UnreadTimeoutTaskStatusFailed, err.Error(), time.Now()); markErr != nil {
			log.ZWarn(taskCtx, "digital twin unread timeout task worker mark failed failed", markErr,
				"key", task.TaskKey,
				"ownerUserID", task.OwnerUserID,
				"senderUserID", task.SenderUserID,
				"operationID", task.OperationID,
			)
		}
		log.ZWarn(taskCtx, "digital twin unread timeout task worker execute failed", err,
			"key", task.TaskKey,
			"ownerUserID", task.OwnerUserID,
			"senderUserID", task.SenderUserID,
			"operationID", task.OperationID,
		)
		return
	}
	if err := MarkUnreadTimeoutTaskStatus(taskCtx, task.TaskKey, task.OperationID, UnreadTimeoutTaskStatusCompleted, "", time.Now()); err != nil {
		log.ZWarn(taskCtx, "digital twin unread timeout task worker mark completed failed", err,
			"key", task.TaskKey,
			"ownerUserID", task.OwnerUserID,
			"senderUserID", task.SenderUserID,
			"operationID", task.OperationID,
		)
	}
}

func unreadTimeoutDelay(cfg Config) time.Duration {
	seconds := cfg.UnreadTimeoutSeconds
	if seconds <= 0 {
		seconds = normalizeUnreadTimeoutSeconds(seconds)
	}
	return time.Duration(seconds) * time.Second
}

func unreadTimeoutTaskKey(req imwebhook.CallbackAfterSendSingleMsgReq) string {
	if req.RecvID == "" || req.SendID == "" {
		return ""
	}
	messageKey := firstNonEmpty(req.ServerMsgID, req.ClientMsgID, req.OperationID)
	if messageKey == "" {
		messageKey = fmt.Sprintf("seq:%d:%d", req.Seq, req.SendTime)
	}
	messageKey = strings.ReplaceAll(messageKey, ":", "_")
	return fmt.Sprintf("%s:%s:%s", req.RecvID, req.SendID, messageKey)
}
