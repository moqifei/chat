package botstruct

import (
	"strings"

	"github.com/openimsdk/chat/pkg/common/constant"
)

func IsAgentUserID(userID string) bool {
	return strings.HasPrefix(userID, constant.AgentUserIDPrefix)
}

func IsAgentPlatformID(platformID int32) bool {
	return platformID == constant.AgentPlatformID
}
