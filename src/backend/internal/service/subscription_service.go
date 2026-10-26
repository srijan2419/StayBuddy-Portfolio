package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type SubscriptionService struct {
	db *sql.DB
}

func NewSubscriptionService(db *sql.DB) *SubscriptionService {
	return &SubscriptionService{db: db}
}

// CheckFeatureAccess verifies that a hotel's subscription plan includes a given feature.
func (s *SubscriptionService) CheckFeatureAccess(ctx context.Context, hotelID int, feature string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	query := `
		SELECT EXISTS(
			SELECT 1 FROM hotels h
			JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
			WHERE h.id = $1
			  AND h.subscription_status IN ('active', 'trialing')
			  AND sp.features @> to_jsonb($2::text)
		)
	`

	var allowed bool
	err := s.db.QueryRowContext(ctx, query, hotelID, feature).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("failed to check feature access: %w", err)
	}

	return allowed, nil
}

// CheckRoomLimit verifies that a hotel hasn't exceeded its plan's room limit.
func (s *SubscriptionService) CheckRoomLimit(ctx context.Context, hotelID int) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	query := `
		SELECT
			COALESCE(sp.max_rooms, 9999) as max_rooms,
			COUNT(r.id) as current_rooms
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		LEFT JOIN rooms r ON r.hotel_id = h.id
		WHERE h.id = $1
		GROUP BY sp.max_rooms
	`

	var maxRooms, currentRooms int
	err := s.db.QueryRowContext(ctx, query, hotelID).Scan(&maxRooms, &currentRooms)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil // No plan found, allow by default
		}
		return false, err
	}

	return currentRooms < maxRooms, nil
}

// CheckUserLimit verifies that a hotel hasn't exceeded its plan's user limit.
func (s *SubscriptionService) CheckUserLimit(ctx context.Context, hotelID int) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	query := `
		SELECT
			COALESCE(sp.max_users, 9999) as max_users,
			COUNT(lc.id) as current_users
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		LEFT JOIN login_credentials lc ON lc.hotel_id = h.id
		WHERE h.id = $1
		GROUP BY sp.max_users
	`

	var maxUsers, currentUsers int
	err := s.db.QueryRowContext(ctx, query, hotelID).Scan(&maxUsers, &currentUsers)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	return currentUsers < maxUsers, nil
}

// CheckOutreachLimit verifies that a hotel hasn't exceeded its plan's outreach message limit.
func (s *SubscriptionService) CheckOutreachLimit(ctx context.Context, hotelID int) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	query := `
		SELECT
			COALESCE(sp.max_outreach_messages, 9999) as max_messages,
			COUNT(om.id) as current_messages
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		LEFT JOIN hotel_outreach_messages om ON om.hotel_id = h.id AND om.is_active = TRUE
		WHERE h.id = $1
		GROUP BY sp.max_outreach_messages
	`

	var maxMsg, currentMsg int
	err := s.db.QueryRowContext(ctx, query, hotelID).Scan(&maxMsg, &currentMsg)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	return currentMsg < maxMsg, nil
}

// IsSubscriptionActive checks if a hotel's subscription is in good standing.
func (s *SubscriptionService) IsSubscriptionActive(ctx context.Context, hotelID int) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	query := `
		SELECT subscription_status FROM hotels WHERE id = $1
	`

	var status string
	err := s.db.QueryRowContext(ctx, query, hotelID).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	return status == "active" || status == "trialing" || status == "past_due", nil
}

// CheckGuestInteractionLimit checks if a hotel has exceeded its monthly unique guest limit.
// Returns (allowed bool, error). If max_guest_interactions is 0, it's unlimited.
func (s *SubscriptionService) CheckGuestInteractionLimit(ctx context.Context, hotelID int) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	query := `
		SELECT COALESCE(sp.max_guest_interactions, 0)
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		WHERE h.id = $1
	`

	var maxGuests int
	err := s.db.QueryRowContext(ctx, query, hotelID).Scan(&maxGuests)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	// 0 means unlimited
	if maxGuests == 0 {
		return true, nil
	}

	// Check current month usage
	monthYear := time.Now().Format("2006-01")
	var currentGuests int
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(unique_guests, 0) FROM guest_usage_monthly WHERE hotel_id = $1 AND month_year = $2`,
		hotelID, monthYear,
	).Scan(&currentGuests)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}

	return currentGuests < maxGuests, nil
}

// IncrementGuestUsage tracks a unique guest interaction for a hotel this month.
// Uses upsert — if phone already counted this month, does nothing.
// Returns the new total unique guests for the month.
func (s *SubscriptionService) IncrementGuestUsage(ctx context.Context, hotelID int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	monthYear := time.Now().Format("2006-01")

	var total int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO guest_usage_monthly (hotel_id, month_year, unique_guests)
		VALUES ($1, $2, 1)
		ON CONFLICT (hotel_id, month_year) DO UPDATE
		SET unique_guests = guest_usage_monthly.unique_guests + 1
		RETURNING unique_guests
	`, hotelID, monthYear).Scan(&total)

	return total, err
}

// GetGuestUsage returns the current month's unique guest count and limit.
func (s *SubscriptionService) GetGuestUsage(ctx context.Context, hotelID int) (current int, max int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	monthYear := time.Now().Format("2006-01")

	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sp.max_guest_interactions, 0),
		       COALESCE((SELECT unique_guests FROM guest_usage_monthly WHERE hotel_id = h.id AND month_year = $2), 0)
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		WHERE h.id = $1
	`, hotelID, monthYear).Scan(&max, &current)

	return current, max, err
}

// CheckLanguageAccess checks if the hotel's plan supports a given language.
// Returns true if plan has max_languages=0 (unlimited) or the language is in the allowed set.
func (s *SubscriptionService) CheckLanguageAccess(ctx context.Context, hotelID int, langCode string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var maxLangs int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sp.max_languages, 0)
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		WHERE h.id = $1
	`, hotelID).Scan(&maxLangs)
	if err != nil {
		return true, err // default allow on error
	}

	// 0 means unlimited languages
	if maxLangs == 0 {
		return true, nil
	}

	// Free plan: only allow EN, ES, FR, HI, AR
	freeLangs := map[string]bool{"en": true, "es": true, "fr": true, "hi": true, "ar": true}
	return freeLangs[langCode], nil
}

// GetUsageSummary returns subscription status and resource usage for a hotel.
func (s *SubscriptionService) GetUsageSummary(ctx context.Context, hotelID int) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT
			h.subscription_status,
			h.subscription_expires_at,
			sp.name as plan_name,
			sp.display_name,
			COALESCE(sp.max_rooms, 9999),
			COALESCE(sp.max_users, 9999),
			COALESCE(sp.max_outreach_messages, 9999),
			(SELECT COUNT(*) FROM rooms r WHERE r.hotel_id = h.id),
			(SELECT COUNT(*) FROM login_credentials lc WHERE lc.hotel_id = h.id),
			(SELECT COUNT(*) FROM hotel_outreach_messages om WHERE om.hotel_id = h.id AND om.is_active = TRUE)
		FROM hotels h
		JOIN subscription_plans sp ON h.subscription_plan_id = sp.id
		WHERE h.id = $1
	`

	var status, planName, displayName string
	var expiresAt sql.NullTime
	var maxRooms, maxUsers, maxOutreach int
	var curRooms, curUsers, curOutreach int

	err := s.db.QueryRowContext(ctx, query, hotelID).Scan(
		&status, &expiresAt, &planName, &displayName,
		&maxRooms, &maxUsers, &maxOutreach,
		&curRooms, &curUsers, &curOutreach,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage summary: %w", err)
	}

	// Get guest usage for this month
	guestCurrent, guestMax, _ := s.GetGuestUsage(ctx, hotelID)

	result := map[string]any{
		"status":       status,
		"plan_name":    planName,
		"display_name": displayName,
		"rooms":        map[string]int{"current": curRooms, "max": maxRooms},
		"users":        map[string]int{"current": curUsers, "max": maxUsers},
		"outreach":     map[string]int{"current": curOutreach, "max": maxOutreach},
		"guests":       map[string]int{"current": guestCurrent, "max": guestMax},
	}

	if expiresAt.Valid {
		result["expires_at"] = expiresAt.Time.Format(time.RFC3339)
		daysRemaining := int(time.Until(expiresAt.Time).Hours() / 24)
		if daysRemaining < 0 {
			daysRemaining = 0
		}
		result["days_remaining"] = daysRemaining
	}

	return result, nil
}
