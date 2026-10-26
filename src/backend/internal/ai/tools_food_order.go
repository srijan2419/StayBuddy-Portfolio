package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// FoodOrderTool creates a food order service request (I.R.D.).
type FoodOrderTool struct {
	db           *sql.DB
	departmentID int // hotel_departments.id — passed from the matched department
}

// NewFoodOrderTool creates a new food order tool.
func NewFoodOrderTool(db *sql.DB, deptID int) *FoodOrderTool {
	return &FoodOrderTool{db: db, departmentID: deptID}
}

func (t *FoodOrderTool) Name() string { return "order_food" }

func (t *FoodOrderTool) Description() string {
	return "Place a confirmed food order for the guest. ONLY call this AFTER the guest has explicitly confirmed their order. The order will be sent to the kitchen/room service department."
}

func (t *FoodOrderTool) ParameterSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"items": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"item": map[string]interface{}{
							"type":        "string",
							"description": "Name of the food item",
						},
						"price": map[string]interface{}{
							"type":        "integer",
							"description": "Price per unit in INR",
						},
						"quantity": map[string]interface{}{
							"type":        "integer",
							"description": "Quantity ordered",
						},
						"special_instructions": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Special instructions for this item",
						},
					},
					"required": []string{"item", "price", "quantity"},
				},
				"description": "List of food items being ordered",
			},
			"total": map[string]interface{}{
				"type":        "integer",
				"description": "Total order amount in INR (sum of price * quantity for all items)",
			},
		},
		"required": []string{"items", "total"},
	}
}

type foodOrderParams struct {
	Items []foodOrderItem `json:"items"`
	Total int             `json:"total"`
}

type foodOrderItem struct {
	Item                string   `json:"item"`
	Price               int      `json:"price"`
	Quantity            int      `json:"quantity"`
	SpecialInstructions []string `json:"special_instructions"`
}

func (t *FoodOrderTool) Execute(ctx context.Context, params json.RawMessage, guestCtx *GuestContext) (*ToolResult, error) {
	var p foodOrderParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse food order params: %w", err)
	}

	if len(p.Items) == 0 {
		return &ToolResult{
			Success: false,
			Message: "No items in the order.",
		}, nil
	}

	// Server-side price validation against menu
	if len(guestCtx.MenuItems) > 0 {
		calculatedTotal := 0
		for i, item := range p.Items {
			realPrice := findMenuItemPrice(guestCtx.MenuItems, item.Item)
			if realPrice > 0 && realPrice != item.Price {
				log.Printf("[food-order-tool] price mismatch for %q: LLM=%d, menu=%d — using menu price", item.Item, item.Price, realPrice)
				p.Items[i].Price = realPrice
			}
			calculatedTotal += p.Items[i].Price * item.Quantity
		}
		if calculatedTotal > 0 && calculatedTotal != p.Total {
			log.Printf("[food-order-tool] total mismatch: LLM=%d, calculated=%d — using calculated", p.Total, calculatedTotal)
			p.Total = calculatedTotal
		}
	}

	deptID := t.departmentID

	// Build the request_details JSONB
	requestDetails := map[string]interface{}{
		"items": p.Items,
		"total": p.Total,
	}
	detailsJSON, err := json.Marshal(requestDetails)
	if err != nil {
		return nil, fmt.Errorf("marshal order details: %w", err)
	}

	// Build description for display
	description := fmt.Sprintf("Food order: %d item(s), total ₹%d", len(p.Items), p.Total)

	// Insert service request with JSONB details
	query := `
		INSERT INTO service_requests
			(hotel_id, room_id, stay_id, department_id, client_id, request_type, description,
			 request_details, status, ai_generated, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'food_order', $6, $7::jsonb, 'pending', TRUE, NOW(), NOW())
		RETURNING id
	`
	var srID int
	err = t.db.QueryRowContext(ctx, query,
		guestCtx.HotelID, guestCtx.RoomID, guestCtx.StayID, deptID, guestCtx.ClientID,
		description, string(detailsJSON),
	).Scan(&srID)
	if err != nil {
		log.Printf("[food-order-tool] failed to create SR: %v", err)
		return &ToolResult{
			Success: false,
			Message: "Failed to place order. Please call the front desk.",
		}, nil
	}

	log.Printf("[food-order-tool] created SR #%d for stay %d: %d items, ₹%d", srID, guestCtx.StayID, len(p.Items), p.Total)
	return &ToolResult{
		Success: true,
		Message: "Order placed successfully. It has been sent to the kitchen.",
	}, nil
}

// findMenuItemPrice does case-insensitive name matching against menu items.
func findMenuItemPrice(menuItems []MenuItem, itemName string) int {
	lower := strings.ToLower(strings.TrimSpace(itemName))
	for _, mi := range menuItems {
		if strings.ToLower(mi.Name) == lower {
			return mi.Price
		}
	}
	return 0
}

// findFoodDepartment finds the kitchen/I.R.D. department for a hotel.
func findFoodDepartment(ctx context.Context, db *sql.DB, hotelID int) (int, error) {
	// Try common names for the food/kitchen department
	names := []string{"i.r.d", "ird", "kitchen", "restaurant", "room service", "food"}
	for _, name := range names {
		id, err := findDepartmentID(ctx, db, hotelID, name)
		if err == nil {
			return id, nil
		}
	}
	return 0, fmt.Errorf("no food/kitchen department found for hotel %d", hotelID)
}
