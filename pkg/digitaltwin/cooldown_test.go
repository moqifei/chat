package digitaltwin

import (
	"context"
	"testing"
	"time"

	"github.com/openimsdk/chat/pkg/common/imwebhook"
)

type fakeReplyRecordStore struct {
	record ReplyRecord
	ok     bool
}

func (s fakeReplyRecordStore) SaveReplyRecord(context.Context, ReplyRecord) error {
	return nil
}

func (s fakeReplyRecordStore) ListReplyRecords(context.Context, string, int64, string, int64, string) (ReplyRecordPage, error) {
	return ReplyRecordPage{}, nil
}

func (s fakeReplyRecordStore) CountReplyRecords(context.Context, string, string) (ReplyRecordSummary, error) {
	return ReplyRecordSummary{}, nil
}

func (s fakeReplyRecordStore) LatestReplyRecord(context.Context, string, string) (ReplyRecord, bool, error) {
	return s.record, s.ok, nil
}

func (s fakeReplyRecordStore) ReviewReplyRecord(context.Context, string, string, string, string, time.Time) error {
	return nil
}

func TestShouldSkipByCooldownDisabled(t *testing.T) {
	SetReplyRecordStore(fakeReplyRecordStore{
		record: ReplyRecord{CreatedAt: time.UnixMilli(1000).UnixMilli()},
		ok:     true,
	})
	defer SetReplyRecordStore(nil)

	decision, err := ShouldSkipByCooldown(context.Background(), Config{}, imwebhook.CallbackAfterSendSingleMsgReq{}, time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Handled {
		t.Fatalf("expected no cooldown skip, got %+v", decision)
	}
}

func TestShouldSkipByCooldownWithinWindow(t *testing.T) {
	SetReplyRecordStore(fakeReplyRecordStore{
		record: ReplyRecord{CreatedAt: time.UnixMilli(1000).UnixMilli()},
		ok:     true,
	})
	defer SetReplyRecordStore(nil)

	decision, err := ShouldSkipByCooldown(context.Background(), Config{ReplyCooldownSeconds: 10}, imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{SendID: "user_a"},
		RecvID:            "user_b",
	}, time.UnixMilli(5000))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Handled || decision.Reason != "reply_cooldown:6s" {
		t.Fatalf("expected cooldown skip, got %+v", decision)
	}
}

func TestShouldSkipByCooldownExpired(t *testing.T) {
	SetReplyRecordStore(fakeReplyRecordStore{
		record: ReplyRecord{CreatedAt: time.UnixMilli(1000).UnixMilli()},
		ok:     true,
	})
	defer SetReplyRecordStore(nil)

	decision, err := ShouldSkipByCooldown(context.Background(), Config{ReplyCooldownSeconds: 3}, imwebhook.CallbackAfterSendSingleMsgReq{
		CommonCallbackReq: imwebhook.CommonCallbackReq{SendID: "user_a"},
		RecvID:            "user_b",
	}, time.UnixMilli(5000))
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Handled {
		t.Fatalf("expected cooldown to be expired, got %+v", decision)
	}
}
