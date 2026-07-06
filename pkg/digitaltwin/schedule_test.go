package digitaltwin

import (
	"testing"
	"time"
)

func TestShouldSkipByScheduleDisabled(t *testing.T) {
	decision := ShouldSkipBySchedule(Config{}, time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))
	if !decision.Handled {
		t.Fatalf("expected disabled schedule to allow reply, got %+v", decision)
	}
}

func TestShouldSkipByScheduleWithinDayWindow(t *testing.T) {
	cfg := Config{ReplySchedule: ReplySchedule{
		Enabled:     true,
		StartMinute: 9 * 60,
		EndMinute:   18 * 60,
		Timezone:    "UTC",
	}}
	decision := ShouldSkipBySchedule(cfg, time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC))
	if !decision.Handled {
		t.Fatalf("expected schedule to allow reply, got %+v", decision)
	}
	decision = ShouldSkipBySchedule(cfg, time.Date(2026, 6, 30, 20, 0, 0, 0, time.UTC))
	if decision.Handled || decision.Reason != "reply_schedule_inactive:0540-1080" {
		t.Fatalf("expected schedule inactive, got %+v", decision)
	}
}

func TestShouldSkipByScheduleCrossMidnightWindow(t *testing.T) {
	cfg := Config{ReplySchedule: ReplySchedule{
		Enabled:     true,
		StartMinute: 18 * 60,
		EndMinute:   9 * 60,
		Timezone:    "UTC",
	}}
	for _, now := range []time.Time{
		time.Date(2026, 6, 30, 23, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 1, 8, 30, 0, 0, time.UTC),
	} {
		decision := ShouldSkipBySchedule(cfg, now)
		if !decision.Handled {
			t.Fatalf("expected cross-midnight schedule to allow reply at %v, got %+v", now, decision)
		}
	}
	decision := ShouldSkipBySchedule(cfg, time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))
	if decision.Handled {
		t.Fatalf("expected schedule inactive, got %+v", decision)
	}
}
