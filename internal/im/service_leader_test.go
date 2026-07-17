package im

import (
	"errors"
	"testing"
	"time"
)

func TestShouldRelinquishWSLeadership(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		result     int64
		elapsed    time.Duration
		relinquish bool
	}{
		{
			name:       "successful renewal keeps leadership",
			result:     1,
			relinquish: false,
		},
		{
			name:       "missing lease relinquishes immediately",
			result:     0,
			relinquish: true,
		},
		{
			name:       "single transient redis error keeps connection",
			err:        errors.New("i/o timeout"),
			elapsed:    wsLeaderRenewInterval,
			relinquish: false,
		},
		{
			name:       "prolonged redis error relinquishes before lease expiry",
			err:        errors.New("i/o timeout"),
			elapsed:    wsLeaderTTL - wsLeaderRenewInterval,
			relinquish: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRelinquishWSLeadership(tt.err, tt.result, tt.elapsed)
			if got != tt.relinquish {
				t.Fatalf("shouldRelinquishWSLeadership() = %v, want %v", got, tt.relinquish)
			}
		})
	}
}

func TestRequeueChannelAfterLeadershipLossStopsMatchingChannel(t *testing.T) {
	stopCh := make(chan struct{})
	close(stopCh)
	cancelled := make(chan struct{}, 1)
	channel := &IMChannel{ID: "feishu-1", Mode: "websocket"}
	svc := &Service{
		channels: map[string]*channelState{
			channel.ID: {
				Channel: channel,
				Cancel:  func() { cancelled <- struct{}{} },
			},
		},
		stopCh: stopCh,
	}

	if !svc.requeueChannelAfterLeadershipLoss(channel) {
		t.Fatal("requeueChannelAfterLeadershipLoss() = false, want true")
	}
	if _, _, ok := svc.GetChannelAdapter(channel.ID); ok {
		t.Fatal("channel still registered after leadership loss")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("channel adapter was not stopped")
	}
}

func TestRequeueChannelAfterLeadershipLossDoesNotStopReplacement(t *testing.T) {
	stale := &IMChannel{ID: "feishu-1", Mode: "websocket"}
	replacement := &IMChannel{ID: stale.ID, Mode: "websocket"}
	svc := &Service{
		channels: map[string]*channelState{
			replacement.ID: {Channel: replacement},
		},
		stopCh: make(chan struct{}),
	}

	if svc.requeueChannelAfterLeadershipLoss(stale) {
		t.Fatal("requeueChannelAfterLeadershipLoss() = true for stale channel")
	}
	_, current, ok := svc.GetChannelAdapter(replacement.ID)
	if !ok || current != replacement {
		t.Fatal("replacement channel was stopped by stale renewal goroutine")
	}
}
