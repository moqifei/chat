package digitaltwin

import (
	"context"
	"time"

	"github.com/openimsdk/chat/pkg/common/imapi"
	"github.com/openimsdk/chat/pkg/common/imwebhook"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/tools/log"
)

func SendReply(ctx context.Context, caller imapi.CallerInterface, key string, req imwebhook.CallbackAfterSendSingleMsgReq, cfg Config, configSource string, now time.Time) (ReplyPlan, error) {
	replyPlan := BuildReplyPlan(ctx, req, cfg)
	ex, err := BuildReplyExWithSourceTraceTextAndError(req, now, replyPlan.Source, replyPlan.Trace, replyPlan.Content, replyPlan.GeneratorError)
	if err != nil {
		return replyPlan, err
	}
	imToken, err := caller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return replyPlan, err
	}
	sendCtx := mctx.WithApiToken(ctx, imToken)
	if err := caller.SendSimpleMsg(sendCtx, &imapi.SendSingleMsgReq{
		SendID:  req.RecvID,
		Content: replyPlan.Content,
		Ex:      ex,
	}, key); err != nil {
		return replyPlan, err
	}
	if err := SaveReplyRecord(ctx, req, replyPlan, configSource, now); err != nil {
		log.ZWarn(ctx, "digital twin save reply record failed", err,
			"ownerUserID", req.RecvID,
			"senderUserID", req.SendID,
			"operationID", req.OperationID,
		)
	}
	return replyPlan, nil
}
