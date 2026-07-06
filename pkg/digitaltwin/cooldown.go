package digitaltwin

import (
	"context"
	"fmt"
	"time"

	"github.com/openimsdk/chat/pkg/common/imwebhook"
)

func ShouldSkipByCooldown(ctx context.Context, cfg Config, req imwebhook.CallbackAfterSendSingleMsgReq, now time.Time) (Decision, error) {
	if cfg.ReplyCooldownSeconds <= 0 {
		return Decision{Handled: true}, nil
	}
	record, ok, err := LatestReplyRecord(ctx, req.RecvID, req.SendID)
	if err != nil || !ok {
		return Decision{Handled: true}, err
	}
	elapsed := now.UnixMilli() - record.CreatedAt
	cooldownMillis := cfg.ReplyCooldownSeconds * int64(time.Second/time.Millisecond)
	if elapsed >= cooldownMillis {
		return Decision{Handled: true}, nil
	}
	remainingSeconds := (cooldownMillis - elapsed + 999) / 1000
	return Decision{Reason: fmt.Sprintf("reply_cooldown:%ds", remainingSeconds)}, nil
}
