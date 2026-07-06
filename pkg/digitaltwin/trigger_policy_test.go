package digitaltwin

import "testing"

func TestShouldSkipByTriggerMode(t *testing.T) {
	if decision := ShouldSkipByTriggerMode(Config{}); !decision.Handled {
		t.Fatalf("expected default trigger mode to be immediate, got %+v", decision)
	}
	if decision := ShouldSkipByTriggerMode(Config{TriggerMode: TriggerModeManual}); decision.Handled || decision.Reason != "trigger_mode_manual" {
		t.Fatalf("expected manual mode to skip, got %+v", decision)
	}
	if decision := ShouldSkipByTriggerMode(Config{TriggerMode: TriggerModeUnreadTimeout}); decision.Handled || decision.Reason != "trigger_mode_unread_timeout_pending" {
		t.Fatalf("expected unread timeout mode to be pending, got %+v", decision)
	}
}

func TestNormalizeUnreadTimeoutSeconds(t *testing.T) {
	tests := map[int64]int64{
		0:     180,
		10:    30,
		180:   180,
		90000: 86400,
	}
	for input, want := range tests {
		if got := normalizeUnreadTimeoutSeconds(input); got != want {
			t.Fatalf("normalizeUnreadTimeoutSeconds(%d)=%d, want %d", input, got, want)
		}
	}
}
