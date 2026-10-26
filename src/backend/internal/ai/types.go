package ai

import "encoding/json"

// LLMMessage represents a single message in a conversation with the LLM.
type LLMMessage struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents an LLM function call request.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall is the function name + arguments from an LLM tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolDefinition is the OpenAI-compatible tool spec sent with chat completions.
type ToolDefinition struct {
	Type     string         `json:"type"` // "function"
	Function FunctionSchema `json:"function"`
}

// FunctionSchema describes a tool for the LLM.
type FunctionSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ChatRequest is passed to LLMClient.ChatCompletion.
type ChatRequest struct {
	Model       string           `json:"model"`
	Messages    []LLMMessage     `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	Temperature float64          `json:"temperature"`
	MaxTokens   int              `json:"max_tokens"`
}

// UsageInfo holds token counts returned by the OpenAI API.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is returned from LLMClient.ChatCompletion.
type ChatResponse struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     *UsageInfo `json:"usage,omitempty"`
}

// ToolResult is returned from Tool.Execute.
type ToolResult struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// HotelDepartment holds a department loaded from DB with per-hotel AI instructions.
type HotelDepartment struct {
	ID               int    `json:"id"`                // hotel_departments.id
	DepartmentID     int    `json:"department_id"`     // departments.id (global)
	Name             string `json:"name"`              // departments.name
	DisplayName      string `json:"display_name"`      // COALESCE(display_name, name)
	Description      string `json:"description"`       // departments.description
	DeptInstructions string `json:"dept_instructions"` // from ai_prompt_configs
	DepartmentType   string `json:"department_type"`   // "general" or "food"
}

// IsFood returns true if this is a food/kitchen/IRD department.
func (d HotelDepartment) IsFood() bool {
	return d.DepartmentType == "food"
}

// MenuItem is a structured menu item parsed from free-form text.
type MenuItem struct {
	Name          string `json:"name"`
	Price         int    `json:"price"`
	Category      string `json:"category"`
	AvailableFrom string `json:"available_from,omitempty"`
	AvailableUntil string `json:"available_until,omitempty"`
	IsAvailable   bool   `json:"is_available"`
}

// OrchestratorResult is the output of the orchestrator classifier.
type OrchestratorResult struct {
	Departments []MatchedDept
	Clarify     bool
	Question    string
}

// MatchedDept is a single department matched by the orchestrator.
type MatchedDept struct {
	Name       string          // department name as returned by LLM
	Department HotelDepartment // resolved from DB
	IsFood     bool
	SubRequest string          // extracted sub-request relevant to this department only
}

// GuestContext holds all contextual info about the current guest conversation.
type GuestContext struct {
	// Guest info
	StayID     int    `json:"stay_id"`
	ClientID   int    `json:"client_id"`
	ClientName string `json:"client_name"`
	Phone      string `json:"phone"`
	RoomNumber string `json:"room_number"`
	RoomID     int    `json:"room_id"`
	HotelID    int    `json:"hotel_id"`
	HotelName  string `json:"hotel_name"`

	// WiFi
	WifiName     string `json:"wifi_name"`
	WifiPassword string `json:"wifi_password"`

	// Hotel info
	HotelGeneralDetails string `json:"hotel_general_details"`
	HotelMenuDetails    string `json:"hotel_menu_details"`

	// Structured menu items (parsed from free-form text)
	MenuItems []MenuItem `json:"menu_items,omitempty"`

	// Dynamic departments with per-hotel AI instructions
	Departments         []HotelDepartment `json:"departments,omitempty"`
	SpecialInstructions string            `json:"special_instructions,omitempty"`

	// Time
	CurrentTime string `json:"current_time"` // hotel timezone formatted

	// Conversation history
	ConversationHistory []LLMMessage `json:"conversation_history"`

	// Recent service requests (last 5)
	RecentServiceRequests []ServiceRequestSummary `json:"recent_service_requests"`

	// Previous food orders
	PreviousOrders []PreviousOrder `json:"previous_orders"`

	// Detected language of the current message (set by translation pipeline)
	DetectedLang string `json:"detected_lang,omitempty"`
}

// ServiceRequestSummary is a compact view of a recent service request.
type ServiceRequestSummary struct {
	ID          int    `json:"id"`
	Department  string `json:"department"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// PreviousOrder is a compact view of a past food order.
type PreviousOrder struct {
	Items     string `json:"items"`
	Total     int    `json:"total"`
	CreatedAt string `json:"created_at"`
}

// AgentResponse is returned by an agent after processing.
type AgentResponse struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     *UsageInfo `json:"-"` // accumulated token usage for logging
}
