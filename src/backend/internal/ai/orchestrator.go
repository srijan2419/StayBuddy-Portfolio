package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Orchestrate classifies a guest message to one or more departments using a lightweight LLM call.
// Returns an OrchestratorResult with matched departments (can be multiple for multi-intent messages).
func Orchestrate(ctx context.Context, llm LLMClient, message string, history []LLMMessage, departments []HotelDepartment, mp ModelParams) (*OrchestratorResult, *UsageInfo, error) {
	prompt := buildOrchestratorPrompt(departments)

	messages := []LLMMessage{
		{Role: "system", Content: prompt},
	}

	// Add recent conversation context (last 4 messages) for classification accuracy
	if len(history) > 0 {
		start := 0
		if len(history) > 4 {
			start = len(history) - 4
		}
		for _, msg := range history[start:] {
			messages = append(messages, LLMMessage{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}

	messages = append(messages, LLMMessage{
		Role:    "user",
		Content: message,
	})

	resp, err := llm.ChatCompletion(ctx, ChatRequest{
		Model:       mp.Model,
		Messages:    messages,
		Temperature: mp.Temperature,
		MaxTokens:   mp.MaxTokens,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator LLM call failed: %w", err)
	}

	usage := resp.Usage

	orchResult, parseErr := parseOrchestratorResponse(resp.Content, departments)
	if parseErr != nil {
		// Retry: feed malformed output back to the same model
		log.Printf("[orchestrator] JSON parse failed, retrying: %v", parseErr)
		retryMessages := make([]LLMMessage, len(messages), len(messages)+2)
		copy(retryMessages, messages)
		retryMessages = append(retryMessages,
			LLMMessage{Role: "assistant", Content: resp.Content},
			LLMMessage{Role: "user", Content: "Your response was not valid JSON. Respond with ONLY a JSON object in the exact format specified. No text before or after."},
		)
		resp2, err2 := llm.ChatCompletion(ctx, ChatRequest{
			Model:       mp.Model,
			Messages:    retryMessages,
			Temperature: mp.Temperature,
			MaxTokens:   mp.MaxTokens,
		})
		if err2 == nil {
			orchResult, _ = parseOrchestratorResponse(resp2.Content, departments)
			if resp2.Usage != nil && usage != nil {
				usage.PromptTokens += resp2.Usage.PromptTokens
				usage.CompletionTokens += resp2.Usage.CompletionTokens
				usage.TotalTokens += resp2.Usage.TotalTokens
			}
		}
		// If still nil, fall back to "none"
		if orchResult == nil {
			orchResult = &OrchestratorResult{Departments: []MatchedDept{{Name: "none"}}}
		}
	}

	return orchResult, usage, nil
}

// buildOrchestratorPrompt creates the classifier system prompt with dynamic department list.
func buildOrchestratorPrompt(departments []HotelDepartment) string {
	var sb strings.Builder

	sb.WriteString(`# Role
You are a hotel request classifier. You classify guest messages to the correct department(s).

# Instructions
- Read the guest message and classify it to ONE OR MORE departments from the list below
- A message can target multiple departments. Example: "I need extra towels and can I order a coffee?" → Housekeeping + Food department
- If the message is a general greeting or doesn't need any department, respond with ["none"]
- WiFi password, checkout time, pool hours, gym info, hotel policies → respond with ["none"] (the simple response agent has this info)
- Off-topic questions unrelated to hotel services (stock prices, news, math, general knowledge) → respond with ["none"] and the agent will politely decline
- If you're unsure which department, respond with "clarify" and include a brief question
- NEVER guess. If genuinely ambiguous (not multi-intent), ask.
- Cutlery, plates, bowls, glasses, salt, pepper → Housekeeping (NOT food)
- Short confirmations ("yes", "ok", "done") → look at last AI reply to determine context
- Food item explicitly mentioned → ALWAYS include the food department
- If last 2 turns are food flow, keep routing to the food department (continuation)

# Available Departments
`)

	for _, dept := range departments {
		desc := dept.Description
		if desc == "" {
			desc = dept.DisplayName + " services"
		}
		sb.WriteString(fmt.Sprintf("<dept name=\"%s\">%s</dept>\n", dept.DisplayName, desc))
	}

	sb.WriteString(`
# Output Format
Respond with ONLY a JSON object. Each department entry MUST include "sub_request" — the specific part of the guest's message relevant to that department.

For single-department messages:
{"departments": [{"name": "Housekeeping", "sub_request": "I need extra towels"}]}

For multi-department messages, SPLIT the request — each department gets ONLY its relevant portion:
{"departments": [{"name": "Housekeeping", "sub_request": "toothpaste"}, {"name": "I.R.D. Order", "sub_request": "tea"}]}

CRITICAL: Each sub_request must contain ONLY the part relevant to that department. Do NOT duplicate items across departments.

For none/clarify:
{"departments": [{"name": "none", "sub_request": ""}]}
{"departments": [{"name": "clarify", "sub_request": ""}], "question": "Could you specify..."}

Use the EXACT department names from the list above.
`)

	return sb.String()
}

// orchestratorResponse is the parsed JSON from the classifier LLM.
// Supports both old format ([]string) and new format ([]object with sub_request).
type orchestratorResponse struct {
	Departments []json.RawMessage `json:"departments"`
	Question    string            `json:"question,omitempty"`
}

// deptEntry is a single department in the new object format.
type deptEntry struct {
	Name       string `json:"name"`
	SubRequest string `json:"sub_request"`
}

// parseOrchestratorResponse parses the LLM output and resolves department names to HotelDepartment structs.
// Supports both object format [{"name":"X","sub_request":"Y"}] and legacy string format ["X"].
func parseOrchestratorResponse(content string, departments []HotelDepartment) (*OrchestratorResult, error) {
	content = strings.TrimSpace(content)

	var parsed orchestratorResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		log.Printf("[orchestrator] failed to parse response %q: %v", truncateStr(content, 100), err)
		return nil, fmt.Errorf("invalid JSON from orchestrator: %w", err)
	}

	if len(parsed.Departments) == 0 {
		return &OrchestratorResult{
			Departments: []MatchedDept{{Name: "none"}},
		}, nil
	}

	// Parse each department entry (supports both string and object formats)
	type parsedEntry struct {
		Name       string
		SubRequest string
	}
	var entries []parsedEntry

	for _, raw := range parsed.Departments {
		// Try object format first: {"name": "X", "sub_request": "Y"}
		var entry deptEntry
		if err := json.Unmarshal(raw, &entry); err == nil && entry.Name != "" {
			entries = append(entries, parsedEntry{Name: entry.Name, SubRequest: entry.SubRequest})
			continue
		}
		// Fallback: plain string format "X"
		var name string
		if err := json.Unmarshal(raw, &name); err == nil {
			entries = append(entries, parsedEntry{Name: name, SubRequest: ""})
			continue
		}
		log.Printf("[orchestrator] skipping unparseable department entry: %s", string(raw))
	}

	if len(entries) == 0 {
		return &OrchestratorResult{
			Departments: []MatchedDept{{Name: "none"}},
		}, nil
	}

	// Check for clarify
	if len(entries) == 1 && strings.ToLower(entries[0].Name) == "clarify" {
		return &OrchestratorResult{
			Clarify:  true,
			Question: parsed.Question,
		}, nil
	}

	// Check for none
	if len(entries) == 1 && strings.ToLower(entries[0].Name) == "none" {
		return &OrchestratorResult{
			Departments: []MatchedDept{{Name: "none"}},
		}, nil
	}

	// Resolve each department name to a HotelDepartment
	var matched []MatchedDept
	for _, e := range entries {
		dept, found := findDeptByName(departments, e.Name)
		if !found {
			log.Printf("[orchestrator] department %q not found in hotel departments, skipping", e.Name)
			continue
		}
		matched = append(matched, MatchedDept{
			Name:       e.Name,
			Department: dept,
			IsFood:     dept.IsFood(),
			SubRequest: e.SubRequest,
		})
	}

	if len(matched) == 0 {
		log.Printf("[orchestrator] no valid departments matched from entries, defaulting to none")
		return &OrchestratorResult{
			Departments: []MatchedDept{{Name: "none"}},
		}, nil
	}

	log.Printf("[orchestrator] classified to %d department(s): %v", len(matched), departmentNames(matched))
	return &OrchestratorResult{Departments: matched}, nil
}

// findDeptByName does a case-insensitive match of the LLM-returned name against available departments.
func findDeptByName(departments []HotelDepartment, name string) (HotelDepartment, bool) {
	nameLower := strings.ToLower(strings.TrimSpace(name))

	// Exact match on DisplayName first
	for _, d := range departments {
		if strings.ToLower(d.DisplayName) == nameLower {
			return d, true
		}
	}

	// Exact match on Name
	for _, d := range departments {
		if strings.ToLower(d.Name) == nameLower {
			return d, true
		}
	}

	// Substring match (LLM might say "Housekeeping" for a dept named "Housekeeping Department")
	for _, d := range departments {
		if strings.Contains(strings.ToLower(d.DisplayName), nameLower) || strings.Contains(nameLower, strings.ToLower(d.DisplayName)) {
			return d, true
		}
	}
	for _, d := range departments {
		if strings.Contains(strings.ToLower(d.Name), nameLower) || strings.Contains(nameLower, strings.ToLower(d.Name)) {
			return d, true
		}
	}

	return HotelDepartment{}, false
}

func departmentNames(matched []MatchedDept) []string {
	names := make([]string, len(matched))
	for i, m := range matched {
		names[i] = m.Name
	}
	return names
}
