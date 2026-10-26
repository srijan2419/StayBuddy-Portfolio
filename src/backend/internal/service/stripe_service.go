package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/srijanai/butler-ai/internal/logger"
)

// StripeService handles Stripe subscription billing for US/Canada clients.
// Mirrors RazorpayService patterns for consistency.
type StripeService struct {
	db            *sql.DB
	secretKey     string
	webhookSecret string
	publishableKey string
}

func NewStripeService(db *sql.DB, secretKey, webhookSecret, publishableKey string) *StripeService {
	return &StripeService{
		db:             db,
		secretKey:      secretKey,
		webhookSecret:  webhookSecret,
		publishableKey: publishableKey,
	}
}

// Configured returns true if Stripe credentials are present.
func (s *StripeService) Configured() bool {
	return s.secretKey != "" && s.webhookSecret != ""
}

// VerifyWebhookSignature validates a Stripe webhook signature.
// Stripe uses a timestamp + HMAC-SHA256 scheme (Stripe-Signature header).
func (s *StripeService) VerifyWebhookSignature(body []byte, sigHeader string) bool {
	// Parse Stripe-Signature header: "t=timestamp,v1=signature"
	var timestamp, sig string
	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			sig = kv[1]
		}
	}

	if timestamp == "" || sig == "" {
		return false
	}

	// Reject webhooks older than 5 minutes (prevents replay attacks)
	tsInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if time.Since(time.Unix(tsInt, 0)) > 5*time.Minute {
		return false
	}

	// Compute expected signature: HMAC-SHA256(webhook_secret, "timestamp.body")
	payload := fmt.Sprintf("%s.%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(sig))
}

// ProcessWebhookEvent handles a Stripe webhook event with idempotency.
func (s *StripeService) ProcessWebhookEvent(ctx context.Context, body []byte) error {
	var event struct {
		ID   string          `json:"id"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return fmt.Errorf("invalid webhook payload: %w", err)
	}

	// Check idempotency using Stripe's event ID
	eventID := "stripe_" + event.ID
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM payment_events WHERE gateway_event_id = $1)`,
		eventID,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("idempotency check failed: %w", err)
	}
	if exists {
		return nil
	}

	// Extract hotel from Stripe metadata
	hotelID, err := s.hotelIDFromEvent(ctx, event.Data)
	if err != nil {
		return fmt.Errorf("cannot map event to hotel: %w", err)
	}

	// Handle event
	switch event.Type {
	case "checkout.session.completed":
		err = s.handleCheckoutCompleted(ctx, hotelID, event.Data)
	case "invoice.paid":
		err = s.handleInvoicePaid(ctx, hotelID)
	case "invoice.payment_failed":
		err = s.updateSubscriptionStatus(ctx, hotelID, "past_due")
	case "customer.subscription.deleted":
		err = s.updateSubscriptionStatus(ctx, hotelID, "cancelled")
	case "customer.subscription.paused":
		err = s.updateSubscriptionStatus(ctx, hotelID, "suspended")
	case "customer.subscription.resumed":
		err = s.updateSubscriptionStatus(ctx, hotelID, "active")
	default:
		logger.FromContext(ctx).Info("unhandled stripe event", "event", event.Type)
		return nil
	}

	if err != nil {
		return err
	}

	// Record event for idempotency
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO payment_events (hotel_id, gateway_event_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
		hotelID, eventID, event.Type, body,
	)
	return err
}

func (s *StripeService) updateSubscriptionStatus(ctx context.Context, hotelID int, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE hotels SET subscription_status = $1, updated_at = NOW() WHERE id = $2`,
		status, hotelID,
	)
	return err
}

func (s *StripeService) handleCheckoutCompleted(ctx context.Context, hotelID int, data json.RawMessage) error {
	var obj struct {
		Object struct {
			SubscriptionID string `json:"subscription"`
			CustomerID     string `json:"customer"`
		} `json:"object"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}

	// Use pending_plan_id to update the actual plan (set during checkout creation)
	_, err := s.db.ExecContext(ctx, `
		UPDATE hotels SET
			subscription_status = 'active',
			subscription_plan_id = COALESCE(pending_plan_id, subscription_plan_id),
			stripe_subscription_id = $1,
			stripe_customer_id = $2,
			subscription_expires_at = NOW() + INTERVAL '30 days',
			pending_plan_id = NULL,
			pending_billing = NULL,
			updated_at = NOW()
		WHERE id = $3
	`, obj.Object.SubscriptionID, obj.Object.CustomerID, hotelID)
	return err
}

func (s *StripeService) handleInvoicePaid(ctx context.Context, hotelID int) error {
	// Extend from current expiry (not reset from NOW) to avoid losing days
	_, err := s.db.ExecContext(ctx,
		`UPDATE hotels SET subscription_status = 'active',
		 subscription_expires_at = GREATEST(NOW(), COALESCE(subscription_expires_at, NOW())) + INTERVAL '30 days',
		 updated_at = NOW() WHERE id = $1`,
		hotelID,
	)
	return err
}

func (s *StripeService) hotelIDFromEvent(ctx context.Context, data json.RawMessage) (int, error) {
	// Try to find hotel by Stripe subscription ID or customer ID
	var obj struct {
		Object struct {
			SubscriptionID string            `json:"subscription"`
			CustomerID     string            `json:"customer"`
			Metadata       map[string]string `json:"metadata"`
		} `json:"object"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return 0, err
	}

	// Try subscription ID first
	if obj.Object.SubscriptionID != "" {
		var hotelID int
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM hotels WHERE stripe_subscription_id = $1`,
			obj.Object.SubscriptionID,
		).Scan(&hotelID)
		if err == nil {
			return hotelID, nil
		}
	}

	// Try customer ID
	if obj.Object.CustomerID != "" {
		var hotelID int
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM hotels WHERE stripe_customer_id = $1`,
			obj.Object.CustomerID,
		).Scan(&hotelID)
		if err == nil {
			return hotelID, nil
		}
	}

	// Try metadata.hotel_id (set during checkout session creation)
	if hotelIDStr, ok := obj.Object.Metadata["hotel_id"]; ok {
		var hotelID int
		_, err := fmt.Sscanf(hotelIDStr, "%d", &hotelID)
		if err == nil && hotelID > 0 {
			return hotelID, nil
		}
	}

	return 0, fmt.Errorf("cannot determine hotel from Stripe event data")
}

// CreateCheckoutSession creates a Stripe Checkout Session for a hotel.
// Returns the Checkout Session URL for redirect.
func (s *StripeService) CreateCheckoutSession(ctx context.Context, hotelID int, planID int, successURL, cancelURL string) (map[string]any, error) {
	if !s.Configured() {
		return nil, fmt.Errorf("stripe billing is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Get the Stripe price ID from our DB
	var stripePriceID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT stripe_price_id_monthly FROM subscription_plans WHERE id = $1 AND is_active = TRUE`,
		planID,
	).Scan(&stripePriceID)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %w", err)
	}
	if !stripePriceID.Valid || stripePriceID.String == "" {
		return nil, fmt.Errorf("stripe price not configured for plan %d", planID)
	}

	// Create Checkout Session via Stripe API
	formData := fmt.Sprintf(
		"mode=subscription&line_items[0][price]=%s&line_items[0][quantity]=1&success_url=%s&cancel_url=%s&metadata[hotel_id]=%d",
		stripePriceID.String, successURL, cancelURL, hotelID,
	)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.stripe.com/v1/checkout/sessions",
		strings.NewReader(formData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stripe API error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("stripe API returned %d: %s", resp.StatusCode, string(body))
	}

	var session struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, err
	}

	// Store pending plan_id (confirmed on webhook, not here — avoids premature plan change)
	_, _ = s.db.ExecContext(ctx,
		`UPDATE hotels SET pending_plan_id = $1, updated_at = NOW() WHERE id = $2`,
		planID, hotelID,
	)

	return map[string]any{
		"session_id":  session.ID,
		"checkout_url": session.URL,
	}, nil
}

// CancelSubscription cancels a Stripe subscription at period end.
func (s *StripeService) CancelSubscription(ctx context.Context, hotelID int) error {
	if !s.Configured() {
		return fmt.Errorf("stripe billing is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var stripeSubID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT stripe_subscription_id FROM hotels WHERE id = $1`,
		hotelID,
	).Scan(&stripeSubID)
	if err != nil {
		return err
	}
	if !stripeSubID.Valid || stripeSubID.String == "" {
		return fmt.Errorf("no active Stripe subscription found")
	}

	// Cancel at period end via Stripe API
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("https://api.stripe.com/v1/subscriptions/%s", stripeSubID.String),
		strings.NewReader("cancel_at_period_end=true"))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("stripe cancel API error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stripe cancel API returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetSubscriptionInfo returns the current subscription details for a Stripe hotel.
func (s *StripeService) GetSubscriptionInfo(ctx context.Context, hotelID int) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT h.subscription_status, h.subscription_expires_at, h.stripe_subscription_id,
		       sp.display_name, sp.price_monthly_usd, sp.price_yearly_usd, sp.max_rooms, sp.max_users, sp.max_outreach_messages,
		       (SELECT COUNT(*) FROM rooms WHERE hotel_id = h.id) AS room_count,
		       (SELECT COUNT(*) FROM login_credentials WHERE hotel_id = h.id) AS user_count
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		WHERE h.id = $1
	`

	var status, planName string
	var expiresAt sql.NullTime
	var stripeSubID sql.NullString
	var priceMonthly, priceYearly, maxRooms, maxUsers, maxOutreach, roomCount, userCount int

	err := s.db.QueryRowContext(ctx, query, hotelID).Scan(
		&status, &expiresAt, &stripeSubID,
		&planName, &priceMonthly, &priceYearly, &maxRooms, &maxUsers, &maxOutreach,
		&roomCount, &userCount,
	)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"status":        status,
		"plan_name":     planName,
		"price_monthly": priceMonthly,
		"price_yearly":  priceYearly,
		"currency":      "usd",
		"max_rooms":     maxRooms,
		"max_users":     maxUsers,
		"max_outreach":  maxOutreach,
		"current_rooms": roomCount,
		"current_users": userCount,
		"gateway":       "stripe",
	}

	if expiresAt.Valid {
		result["expires_at"] = expiresAt.Time
	}

	return result, nil
}
