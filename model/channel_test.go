package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestHandlerMultiKeyUpdateEnableRestoresChannelStatus(t *testing.T) {
	ch := &Channel{
		Status: common.ChannelStatusAutoDisabled,
		Key:    "key-a\nkey-b",
		OtherInfo: `{"status_reason":"All keys are disabled","status_time":123,"keep":"ok"}`,
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyStatusList: map[int]int{
				1: common.ChannelStatusAutoDisabled,
			},
			MultiKeyDisabledReason: map[int]string{
				1: "status_code=401, token_invalidated",
			},
			MultiKeyDisabledTime: map[int]int64{
				1: 123,
			},
		},
	}

	handlerMultiKeyUpdate(ch, "key-b", common.ChannelStatusEnabled, "")

	if ch.Status != common.ChannelStatusEnabled {
		t.Fatalf("expected channel status to be enabled, got %d", ch.Status)
	}
	if _, ok := ch.ChannelInfo.MultiKeyStatusList[1]; ok {
		t.Fatal("expected key status to be cleared after enabling")
	}
	if _, ok := ch.ChannelInfo.MultiKeyDisabledReason[1]; ok {
		t.Fatal("expected disabled reason to be cleared after enabling")
	}
	if _, ok := ch.ChannelInfo.MultiKeyDisabledTime[1]; ok {
		t.Fatal("expected disabled time to be cleared after enabling")
	}

	otherInfo := ch.GetOtherInfo()
	if _, ok := otherInfo["status_reason"]; ok {
		t.Fatal("expected status_reason to be removed after enabling a key")
	}
	if _, ok := otherInfo["status_time"]; ok {
		t.Fatal("expected status_time to be removed after enabling a key")
	}
	if got := otherInfo["keep"]; got != "ok" {
		t.Fatalf("expected unrelated other_info fields to be preserved, got %v", got)
	}
}
