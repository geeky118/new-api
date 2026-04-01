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

func TestHandlerMultiKeyRemoveRebuildsPoolState(t *testing.T) {
	ch := &Channel{
		Status: common.ChannelStatusEnabled,
		Key:    "key-a\nkey-b\nkey-c",
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       3,
			MultiKeyPollingIndex: 2,
			MultiKeyStatusList: map[int]int{
				2: common.ChannelStatusAutoDisabled,
			},
			MultiKeyDisabledReason: map[int]string{
				2: "status_code=429, usage_limit_reached",
			},
			MultiKeyDisabledTime: map[int]int64{
				2: 123,
			},
		},
	}

	if !handlerMultiKeyRemove(ch, "key-b") {
		t.Fatal("expected key-b to be removed from multi-key pool")
	}

	if ch.Key != "key-a\nkey-c" {
		t.Fatalf("unexpected key list after removal: %q", ch.Key)
	}
	if ch.ChannelInfo.MultiKeySize != 2 {
		t.Fatalf("expected multi key size to become 2, got %d", ch.ChannelInfo.MultiKeySize)
	}
	if got := ch.ChannelInfo.MultiKeyPollingIndex; got != 1 {
		t.Fatalf("expected polling index to be remapped to 1, got %d", got)
	}
	if got := ch.ChannelInfo.MultiKeyStatusList[1]; got != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected disabled key status to shift to new index 1, got %d", got)
	}
	if _, ok := ch.ChannelInfo.MultiKeyStatusList[2]; ok {
		t.Fatal("expected removed index state to be deleted")
	}
}
