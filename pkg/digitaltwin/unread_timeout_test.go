package digitaltwin

import (
	"context"
	"testing"
	"time"

	"github.com/openimsdk/chat/pkg/common/imwebhook"
)

type fakeUnreadTimeoutTaskStore struct {
	tasks    []UnreadTimeoutTask
	dueTasks []UnreadTimeoutTask
	statuses []string
}

func (s *fakeUnreadTimeoutTaskStore) UpsertUnreadTimeoutTask(_ context.Context, task UnreadTimeoutTask) error {
	s.tasks = append(s.tasks, task)
	return nil
}

func (s *fakeUnreadTimeoutTaskStore) ClaimUnreadTimeoutTask(_ context.Context, taskKey string, operationID string, _ time.Time) (bool, error) {
	for idx := range s.tasks {
		if s.tasks[idx].TaskKey == taskKey && (operationID == "" || s.tasks[idx].OperationID == operationID) {
			if s.tasks[idx].Status != UnreadTimeoutTaskStatusPending {
				return false, nil
			}
			s.tasks[idx].AttemptCount++
			s.tasks[idx].Status = UnreadTimeoutTaskStatusExecuting
			s.statuses = append(s.statuses, UnreadTimeoutTaskStatusExecuting)
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeUnreadTimeoutTaskStore) ClaimDueUnreadTimeoutTasks(context.Context, time.Time, int64) ([]UnreadTimeoutTask, error) {
	tasks := s.dueTasks
	for idx := range tasks {
		tasks[idx].AttemptCount++
	}
	s.dueTasks = nil
	return tasks, nil
}

func (s *fakeUnreadTimeoutTaskStore) UpdateUnreadTimeoutTaskStatus(_ context.Context, _ string, _ string, status string, _ string, _ time.Time) error {
	s.statuses = append(s.statuses, status)
	return nil
}

func (s *fakeUnreadTimeoutTaskStore) CancelUnreadTimeoutTasksByReadSeqs(_ context.Context, ownerUserID string, senderUserID string, seqs []int64, reason string, _ time.Time) (int64, error) {
	seqSet := make(map[int64]struct{}, len(seqs))
	for _, seq := range seqs {
		seqSet[seq] = struct{}{}
	}
	var canceled int64
	for idx := range s.tasks {
		if s.tasks[idx].OwnerUserID != ownerUserID || s.tasks[idx].Status != UnreadTimeoutTaskStatusPending {
			continue
		}
		if senderUserID != "" && s.tasks[idx].SenderUserID != senderUserID {
			continue
		}
		if _, ok := seqSet[s.tasks[idx].TriggerSeq]; !ok && s.tasks[idx].TriggerSeq != 0 {
			continue
		}
		s.tasks[idx].Status = UnreadTimeoutTaskStatusCanceled
		s.tasks[idx].Error = reason
		s.statuses = append(s.statuses, UnreadTimeoutTaskStatusCanceled)
		canceled++
	}
	return canceled, nil
}

func (s *fakeUnreadTimeoutTaskStore) CountUnreadTimeoutTasks(context.Context, string) (UnreadTimeoutTaskSummary, error) {
	return UnreadTimeoutTaskSummary{Pending: int64(len(s.tasks))}, nil
}

func TestScheduleUnreadTimeoutReplyKeepsSeparateMessages(t *testing.T) {
	ResetUnreadTimeoutSchedulerForTest()
	defer ResetUnreadTimeoutSchedulerForTest()

	fired := make(chan string, 2)
	cfg := Config{TriggerMode: TriggerModeUnreadTimeout, UnreadTimeoutSeconds: 1}
	req := imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{
			SendID:      "user_a",
			OperationID: "first",
			ServerMsgID: "server_msg_1",
		},
		RecvID: "user_b",
	}
	result := ScheduleUnreadTimeoutReply(context.Background(), req, cfg, "callback_channel_key", func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
		fired <- req.OperationID
		return nil
	})
	if !result.Scheduled || result.Replaced {
		t.Fatalf("expected first task to be scheduled without replacement, got %+v", result)
	}

	req.OperationID = "second"
	req.ServerMsgID = "server_msg_2"
	result = ScheduleUnreadTimeoutReply(context.Background(), req, cfg, "callback_channel_key", func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
		fired <- req.OperationID
		return nil
	})
	if !result.Scheduled || result.Replaced {
		t.Fatalf("expected second message to be scheduled separately, got %+v", result)
	}

	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case operationID := <-fired:
			got[operationID] = true
		case <-time.After(1500 * time.Millisecond):
			t.Fatal("expected both unread timeout tasks to fire")
		}
	}
	if !got["first"] || !got["second"] {
		t.Fatalf("expected both messages to fire, got %+v", got)
	}
}

func TestScheduleUnreadTimeoutReplyReplacesDuplicateCallback(t *testing.T) {
	ResetUnreadTimeoutSchedulerForTest()
	defer ResetUnreadTimeoutSchedulerForTest()

	fired := make(chan string, 1)
	cfg := Config{TriggerMode: TriggerModeUnreadTimeout, UnreadTimeoutSeconds: 1}
	req := imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{
			SendID:      "user_a",
			OperationID: "operation_a",
			ServerMsgID: "server_msg_a",
		},
		RecvID: "user_b",
	}
	result := ScheduleUnreadTimeoutReply(context.Background(), req, cfg, "callback_channel_key", func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
		fired <- "first"
		return nil
	})
	if !result.Scheduled || result.Replaced {
		t.Fatalf("expected first callback to be scheduled, got %+v", result)
	}
	result = ScheduleUnreadTimeoutReply(context.Background(), req, cfg, "callback_channel_key", func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
		fired <- "second"
		return nil
	})
	if !result.Scheduled || !result.Replaced {
		t.Fatalf("expected duplicate callback to replace previous task, got %+v", result)
	}
	select {
	case marker := <-fired:
		if marker != "second" {
			t.Fatalf("expected replacement task to fire, got %q", marker)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("expected replacement unread timeout task to fire")
	}
}

func TestScheduleUnreadTimeoutReplyPersistsTaskLifecycle(t *testing.T) {
	ResetUnreadTimeoutSchedulerForTest()
	defer ResetUnreadTimeoutSchedulerForTest()
	store := &fakeUnreadTimeoutTaskStore{}
	SetUnreadTimeoutTaskStore(store)
	defer SetUnreadTimeoutTaskStore(nil)

	done := make(chan struct{}, 1)
	result := ScheduleUnreadTimeoutReply(context.Background(), imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{
			SendID:      "user_a",
			OperationID: "operation_a",
			ServerMsgID: "server_msg_a",
		},
		RecvID: "user_b",
	}, Config{TriggerMode: TriggerModeUnreadTimeout, UnreadTimeoutSeconds: 1}, "callback_channel_key", func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
		done <- struct{}{}
		return nil
	})
	if !result.Scheduled {
		t.Fatalf("expected unread timeout task to be scheduled, got %+v", result)
	}
	if len(store.tasks) != 1 || store.tasks[0].Status != UnreadTimeoutTaskStatusPending {
		t.Fatalf("expected pending task to be persisted, got %+v", store.tasks)
	}
	if store.tasks[0].ChannelKey != "callback_channel_key" {
		t.Fatalf("expected callback channel key to be persisted, got %q", store.tasks[0].ChannelKey)
	}
	if store.tasks[0].TriggerSeq != 0 {
		t.Fatalf("expected empty seq to be persisted as 0, got %d", store.tasks[0].TriggerSeq)
	}

	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("expected unread timeout task to fire")
	}
	if len(store.statuses) != 2 ||
		store.statuses[0] != UnreadTimeoutTaskStatusExecuting ||
		store.statuses[1] != UnreadTimeoutTaskStatusCompleted {
		t.Fatalf("unexpected task status lifecycle %+v", store.statuses)
	}
	if store.tasks[0].AttemptCount != 1 {
		t.Fatalf("expected attempt count to be incremented, got %d", store.tasks[0].AttemptCount)
	}
}

func TestCancelUnreadTimeoutTasksByReadSeqsPreventsScheduledReply(t *testing.T) {
	ResetUnreadTimeoutSchedulerForTest()
	defer ResetUnreadTimeoutSchedulerForTest()
	store := &fakeUnreadTimeoutTaskStore{}
	SetUnreadTimeoutTaskStore(store)
	defer SetUnreadTimeoutTaskStore(nil)

	fired := make(chan struct{}, 1)
	result := ScheduleUnreadTimeoutReply(context.Background(), imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{
			SendID:      "user_a",
			OperationID: "operation_a",
			ServerMsgID: "server_msg_a",
			Seq:         12,
		},
		RecvID: "user_b",
	}, Config{TriggerMode: TriggerModeUnreadTimeout, UnreadTimeoutSeconds: 1}, "callback_channel_key", func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
		fired <- struct{}{}
		return nil
	})
	if !result.Scheduled {
		t.Fatalf("expected unread timeout task to be scheduled, got %+v", result)
	}
	canceled, err := CancelUnreadTimeoutTasksByReadSeqs(context.Background(), "user_b", "user_a", []int64{12}, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if canceled != 1 {
		t.Fatalf("expected one task to be canceled, got %d", canceled)
	}

	select {
	case <-fired:
		t.Fatal("read-canceled unread timeout task should not fire")
	case <-time.After(1200 * time.Millisecond):
	}
	if len(store.statuses) != 1 || store.statuses[0] != UnreadTimeoutTaskStatusCanceled {
		t.Fatalf("expected only canceled status, got %+v", store.statuses)
	}
	if store.tasks[0].Error != UnreadTimeoutTaskCancelReasonReadBeforeTimeout {
		t.Fatalf("expected default cancel reason, got %q", store.tasks[0].Error)
	}
}

func TestCancelUnreadTimeoutTasksByReadSeqsFallsBackWhenTriggerSeqMissing(t *testing.T) {
	store := &fakeUnreadTimeoutTaskStore{
		tasks: []UnreadTimeoutTask{
			{
				TaskKey:      "user_b:user_a:server_msg_a",
				OwnerUserID:  "user_b",
				SenderUserID: "user_a",
				TriggerSeq:   0,
				Status:       UnreadTimeoutTaskStatusPending,
			},
		},
	}
	SetUnreadTimeoutTaskStore(store)
	defer SetUnreadTimeoutTaskStore(nil)

	canceled, err := CancelUnreadTimeoutTasksByReadSeqs(context.Background(), "user_b", "user_a", []int64{63}, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if canceled != 1 {
		t.Fatalf("expected pending task with trigger_seq=0 to be canceled by read callback fallback, got %d", canceled)
	}
	if store.tasks[0].Status != UnreadTimeoutTaskStatusCanceled {
		t.Fatalf("expected task to be canceled, got %+v", store.tasks[0])
	}
}

func TestRunDueUnreadTimeoutTasksExecutesClaimedTask(t *testing.T) {
	store := &fakeUnreadTimeoutTaskStore{
		dueTasks: []UnreadTimeoutTask{
			{
				TaskKey:      "user_b:user_a:server_msg_a",
				ChannelKey:   "persisted_channel_key",
				OwnerUserID:  "user_b",
				SenderUserID: "user_a",
				OperationID:  "operation_a",
				Request: imwebhook.CallbackAfterSendSingleMsgReq{
					CommonCallbackReq: imwebhook.CommonCallbackReq{
						SendID:      "user_a",
						OperationID: "operation_a",
					},
					RecvID: "user_b",
				},
			},
		},
	}
	SetUnreadTimeoutTaskStore(store)
	defer SetUnreadTimeoutTaskStore(nil)

	executed := false
	runDueUnreadTimeoutTasks(context.Background(), 20, func(ctx context.Context, task UnreadTimeoutTask) error {
		executed = true
		if task.TaskKey != "user_b:user_a:server_msg_a" || task.ChannelKey != "persisted_channel_key" || task.Request.RecvID != "user_b" {
			t.Fatalf("unexpected task %+v", task)
		}
		return nil
	})
	if !executed {
		t.Fatal("expected due task to be executed")
	}
	if len(store.statuses) != 1 || store.statuses[0] != UnreadTimeoutTaskStatusCompleted {
		t.Fatalf("expected completed status, got %+v", store.statuses)
	}
}

func TestRunDueUnreadTimeoutTasksRetriesStaleExecutingTask(t *testing.T) {
	store := &fakeUnreadTimeoutTaskStore{
		dueTasks: []UnreadTimeoutTask{
			{
				TaskKey:      "user_b:user_a:server_msg_a",
				ChannelKey:   "persisted_channel_key",
				OwnerUserID:  "user_b",
				SenderUserID: "user_a",
				OperationID:  "operation_a",
				Status:       UnreadTimeoutTaskStatusExecuting,
				AttemptCount: 1,
				Request: imwebhook.CallbackAfterSendSingleMsgReq{
					CommonCallbackReq: imwebhook.CommonCallbackReq{
						SendID:      "user_a",
						OperationID: "operation_a",
					},
					RecvID: "user_b",
				},
			},
		},
	}
	SetUnreadTimeoutTaskStore(store)
	defer SetUnreadTimeoutTaskStore(nil)

	runDueUnreadTimeoutTasks(context.Background(), 20, func(ctx context.Context, task UnreadTimeoutTask) error {
		if task.Status != UnreadTimeoutTaskStatusExecuting || task.AttemptCount != 2 {
			t.Fatalf("expected stale executing task with incremented attempt count, got %+v", task)
		}
		return nil
	})
	if len(store.statuses) != 1 || store.statuses[0] != UnreadTimeoutTaskStatusCompleted {
		t.Fatalf("expected completed status, got %+v", store.statuses)
	}
}

func TestScheduleUnreadTimeoutReplyIgnoresImmediateMode(t *testing.T) {
	ResetUnreadTimeoutSchedulerForTest()
	defer ResetUnreadTimeoutSchedulerForTest()

	result := ScheduleUnreadTimeoutReply(context.Background(), imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{SendID: "user_a"},
		RecvID:            "user_b",
	}, Config{TriggerMode: TriggerModeImmediate}, "callback_channel_key", func(ctx context.Context, req imwebhook.CallbackAfterSendSingleMsgReq) error {
		t.Fatal("immediate mode should not schedule unread timeout task")
		return nil
	})
	if result.Scheduled {
		t.Fatalf("expected immediate mode to be ignored, got %+v", result)
	}
}
