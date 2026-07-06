package digitaltwin

func ShouldSkipByTriggerMode(cfg Config) Decision {
	switch normalizeTriggerMode(cfg.TriggerMode) {
	case TriggerModeManual:
		return Decision{Reason: "trigger_mode_manual"}
	case TriggerModeUnreadTimeout:
		return Decision{Reason: "trigger_mode_unread_timeout_pending"}
	default:
		return Decision{Handled: true}
	}
}

func normalizeTriggerMode(mode string) string {
	switch mode {
	case TriggerModeManual, TriggerModeUnreadTimeout:
		return mode
	default:
		return TriggerModeImmediate
	}
}

func normalizeUnreadTimeoutSeconds(seconds int64) int64 {
	if seconds <= 0 {
		return 180
	}
	if seconds < 30 {
		return 30
	}
	if seconds > 86400 {
		return 86400
	}
	return seconds
}
