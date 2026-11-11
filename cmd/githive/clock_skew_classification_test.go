package main

import (
	"testing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/core/event"
)

func TestClassifyClockSkewHasRecoveryAndRejectsStoredOrder(t *testing.T) {
	err := &entitychain.ClockSkewError{EventID: "new", LatestID: "known", LatestTimestamp: "future"}
	code, info := classifyError(err)
	if code != 5 || info.Code != "clock_skew" || info.Retryable || info.Data["recovery"] != err.Recovery() {
		t.Fatalf("clock skew needs explicit recovery: exit=%d info=%+v", code, info)
	}
	code, info = classifyError(event.ErrEventOrderViolation)
	if code != 4 || info.Code != "event_order_violation" || info.Retryable {
		t.Fatalf("unsafe stored order must not be accepted: exit=%d info=%+v", code, info)
	}
}
