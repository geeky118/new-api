package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

func TestShouldDisableChannelWithTokenInvalidated(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() {
		common.AutomaticDisableChannelEnabled = previous
	}()

	err := types.WithOpenAIError(types.OpenAIError{
		Message: "Your authentication token has been invalidated. Please try signing in again.",
		Type:    "invalid_request_error",
		Code:    "token_invalidated",
	}, http.StatusUnauthorized)

	if !ShouldDisableChannel(0, err) {
		t.Fatal("expected token_invalidated to trigger automatic disable")
	}
}
