package ai

import (
	"context"
	"database/sql"
	"fmt"
)

// findDepartmentID looks up the hotel_departments ID by department name.
// It first checks what department_id pattern existing SRs use for this hotel.
// If the hotel has existing SRs, it matches their convention (some hotels use
// global department IDs from a shared n8n workflow). Otherwise, uses the
// hotel-specific hotel_departments.id.
func findDepartmentID(ctx context.Context, db *sql.DB, hotelID int, deptNameLike string) (int, error) {
	// First: check if existing SRs for this hotel use a specific department_id
	// for this department name. This ensures we match the convention already in use.
	query := `
		SELECT sr.department_id FROM service_requests sr
		JOIN hotel_departments hd ON hd.id = sr.department_id
		JOIN departments d ON d.id = hd.department_id
		WHERE sr.hotel_id = $1 AND LOWER(d.name) LIKE '%' || $2 || '%'
		LIMIT 1
	`
	var deptID int
	err := db.QueryRowContext(ctx, query, hotelID, deptNameLike).Scan(&deptID)
	if err == nil {
		return deptID, nil
	}

	// Fallback: look up from hotel_departments directly (new hotel, no SRs yet)
	query2 := `
		SELECT hd.id FROM hotel_departments hd
		JOIN departments d ON d.id = hd.department_id
		WHERE hd.hotel_id = $1 AND LOWER(d.name) LIKE '%' || $2 || '%'
		LIMIT 1
	`
	err = db.QueryRowContext(ctx, query2, hotelID, deptNameLike).Scan(&deptID)
	if err != nil {
		return 0, fmt.Errorf("department %q not found for hotel %d: %w", deptNameLike, hotelID, err)
	}
	return deptID, nil
}

// createServiceRequest inserts a service_request and returns its ID.
func createServiceRequest(ctx context.Context, db *sql.DB, guestCtx *GuestContext, deptID int, requestType, description string) (int, error) {
	query := `
		INSERT INTO service_requests
			(hotel_id, room_id, stay_id, department_id, client_id, request_type, description,
			 other_request_detail, status, ai_generated, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', TRUE, NOW(), NOW())
		RETURNING id
	`
	var id int
	err := db.QueryRowContext(ctx, query,
		guestCtx.HotelID, guestCtx.RoomID, guestCtx.StayID, deptID, guestCtx.ClientID,
		requestType, description, description,
	).Scan(&id)
	return id, err
}
