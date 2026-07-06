package chat

import "testing"

func TestSingleConversationPeer(t *testing.T) {
	if got := singleConversationPeer("si_user_a_user_b", "user_a"); got != "" {
		t.Fatalf("user IDs with underscores should not be parsed ambiguously, got %q", got)
	}
	if got := singleConversationPeer("si_5360170321_9414555381", "5360170321"); got != "9414555381" {
		t.Fatalf("unexpected peer %q", got)
	}
	if got := singleConversationPeer("si_5360170321_9414555381", "9414555381"); got != "5360170321" {
		t.Fatalf("unexpected peer %q", got)
	}
	if got := singleConversationPeer("g_group_a", "5360170321"); got != "" {
		t.Fatalf("group conversation should not return peer, got %q", got)
	}
}
