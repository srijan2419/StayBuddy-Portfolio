package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
)

// DepartmentForwardTool creates a service request for a pre-resolved department.
// The department is set at construction time by the orchestrator — the LLM cannot
// pick the wrong department because it only has one tool with one hardwired target.
type DepartmentForwardTool struct {
	db         *sql.DB
	department HotelDepartment
}

// NewDepartmentForwardTool creates a department forward tool with the target department pre-resolved.
func NewDepartmentForwardTool(db *sql.DB, dept HotelDepartment) *DepartmentForwardTool {
	return &DepartmentForwardTool{db: db, department: dept}
}

func (t *DepartmentForwardTool) Name() string { return "forward_to_department" }

func (t *DepartmentForwardTool) Description() string {
	return fmt.Sprintf("Forward a guest request to the %s department. Use this when the guest makes a specific actionable request that needs staff attention.", t.department.DisplayName)
}

func (t *DepartmentForwardTool) ParameterSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"details": map[string]interface{}{
				"type":        "string",
				"description": "Description of the request to forward (in English)",
			},
		},
		"required":             []string{"details"},
		"additionalProperties": false,
	}
}

type departmentForwardParams struct {
	Details string `json:"details"`
}

func (t *DepartmentForwardTool) Execute(ctx context.Context, params json.RawMessage, guestCtx *GuestContext) (*ToolResult, error) {
	var p departmentForwardParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse department forward params: %w", err)
	}

	if p.Details == "" {
		return &ToolResult{
			Success: false,
			Message: "No details provided for the request.",
		}, nil
	}

	// Create service request using the pre-resolved department ID
	srID, err := createServiceRequest(ctx, t.db, guestCtx, t.department.ID, t.department.Name, p.Details)
	if err != nil {
		log.Printf("[dept-tool] failed to create SR for %s: %v", t.department.DisplayName, err)
		return &ToolResult{
			Success: false,
			Message: fmt.Sprintf("Failed to forward request to %s. Please call the front desk.", t.department.DisplayName),
		}, nil
	}

	log.Printf("[dept-tool] created SR #%d for stay %d → %s: %s", srID, guestCtx.StayID, t.department.DisplayName, p.Details)
	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Request forwarded to %s. Our team has been notified.", t.department.DisplayName),
	}, nil
}
