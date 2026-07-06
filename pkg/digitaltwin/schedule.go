package digitaltwin

import (
	"fmt"
	"time"
)

func ShouldSkipBySchedule(cfg Config, now time.Time) Decision {
	schedule := normalizeReplySchedule(cfg.ReplySchedule)
	if !schedule.Enabled {
		return Decision{Handled: true}
	}
	if schedule.StartMinute == schedule.EndMinute {
		return Decision{Handled: true}
	}
	loc := time.Local
	if schedule.Timezone != "" {
		if loaded, err := time.LoadLocation(schedule.Timezone); err == nil {
			loc = loaded
		}
	}
	localNow := now.In(loc)
	currentMinute := localNow.Hour()*60 + localNow.Minute()
	if minuteInWindow(currentMinute, schedule.StartMinute, schedule.EndMinute) {
		return Decision{Handled: true}
	}
	return Decision{Reason: fmt.Sprintf("reply_schedule_inactive:%04d-%04d", schedule.StartMinute, schedule.EndMinute)}
}

func minuteInWindow(current int, start int, end int) bool {
	if start < end {
		return current >= start && current < end
	}
	return current >= start || current < end
}
