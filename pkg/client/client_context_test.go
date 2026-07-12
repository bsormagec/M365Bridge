package client

import (
	"context"
	"testing"
	"time"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/payload"
)

func TestChatConversationStreamGenContextClosesBeforeDialWhenCanceled(
	t *testing.T,
) {
	client := NewM365Client(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := client.ChatConversationStreamGenContext(
		ctx,
		[]payload.Message{{Role: "user", Content: "test"}},
		"Balanced",
		"",
		"",
		"",
		"",
		false,
	)

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("canceled stream emitted a chunk")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled stream did not close before dialing")
	}
}

func TestChatConversationContextReturnsCancellationBeforeDial(t *testing.T) {
	client := NewM365Client(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, _, _, err := client.ChatConversationContext(
		ctx,
		[]payload.Message{{Role: "user", Content: "test"}},
		"Balanced",
		"",
		"",
		"",
		"",
		false,
	)
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
