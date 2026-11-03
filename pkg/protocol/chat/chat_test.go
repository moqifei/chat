package chat

import "testing"

func TestGetADMemberByUserIDReqCheck(t *testing.T) {
	if err := (&GetADMemberByUserIDReq{}).Check(); err == nil {
		t.Fatal("empty user ID should be rejected")
	}
	if err := (&GetADMemberByUserIDReq{UserID: "4749158903"}).Check(); err != nil {
		t.Fatalf("valid user ID should be accepted: %v", err)
	}
}
