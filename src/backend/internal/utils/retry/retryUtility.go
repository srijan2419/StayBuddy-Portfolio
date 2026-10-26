package retry

import (
	"context"
	"fmt"
	"time"
)

type MessageContext struct {
	PhoneNumber  string
	ClientName   string
	RoomNumber   string
	TemplateName string
	HotelName    string
	Message      string
	HotelID      int
}

func RetryWhatsAppTemplate(attempts int, initialDelay time.Duration, fn func() error) error {
	delay := initialDelay
	var lastErr error

	for i := 0; i < attempts; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(delay)
			delay *= 2
		}
	}

	return fmt.Errorf("all %d attempts failed: %w", attempts, lastErr)
}

func RetryWhatsAppCall(
	ctx context.Context,
	attempts int,
	initialDelay time.Duration,
	sendFunc func(context.Context, MessageContext) error,
	msgCtx MessageContext,
) error {
	return RetryWhatsAppTemplate(attempts, initialDelay, func() error {
		return sendFunc(ctx, msgCtx)
	})
}
