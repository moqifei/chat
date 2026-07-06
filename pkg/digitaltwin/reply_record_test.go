package digitaltwin

import (
	"context"
	"testing"
)

func TestNormalizeReviewStatus(t *testing.T) {
	for _, status := range []string{"confirmed", "needs_follow_up"} {
		if got := NormalizeReviewStatus(status); got != status {
			t.Fatalf("expected %q, got %q", status, got)
		}
	}
	if got := NormalizeReviewStatus("done"); got != "" {
		t.Fatalf("expected invalid status to be empty, got %q", got)
	}
}

func TestNormalizeListReviewStatus(t *testing.T) {
	for _, status := range []string{"", "unreviewed", "confirmed", "needs_follow_up"} {
		if got := NormalizeListReviewStatus(status); got != status {
			t.Fatalf("expected %q, got %q", status, got)
		}
	}
	if got := NormalizeListReviewStatus("done"); got != "" {
		t.Fatalf("expected invalid list status to be empty, got %q", got)
	}
}

func TestNormalizeReviewNote(t *testing.T) {
	if got := NormalizeReviewNote(" note "); got != "note" {
		t.Fatalf("expected note to be trimmed, got %q", got)
	}
	longNote := make([]rune, 501)
	for i := range longNote {
		longNote[i] = '好'
	}
	if got := NormalizeReviewNote(string(longNote)); len([]rune(got)) != 500 {
		t.Fatalf("expected note to be capped at 500 runes, got %d", len([]rune(got)))
	}
}

func TestListReplyRecordsReturnsEmptySliceWithoutStore(t *testing.T) {
	SetReplyRecordStore(nil)
	page, err := ListReplyRecords(context.Background(), "user_b", 10, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Records == nil {
		t.Fatal("expected empty records slice, got nil")
	}
	if len(page.Records) != 0 {
		t.Fatalf("expected empty records, got %+v", page.Records)
	}
	if page.Summary != (ReplyRecordSummary{}) {
		t.Fatalf("expected empty summary, got %+v", page.Summary)
	}
}
