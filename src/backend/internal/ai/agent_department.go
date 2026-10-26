package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// DepartmentAgent handles requests for a specific department with isolated context.
// Each instance sees ONLY its department's description, instructions, and recent SRs.
type DepartmentAgent struct {
	llm      LLMClient
	registry *ToolRegistry
	mp       ModelParams
}

// NewDepartmentAgent creates a department-specific agent.
func NewDepartmentAgent(llm LLMClient, registry *ToolRegistry, mp ModelParams) *DepartmentAgent {
	return &DepartmentAgent{llm: llm, registry: registry, mp: mp}
}

// Process runs the department agent with isolated department context.
func (a *DepartmentAgent) Process(ctx context.Context, dept HotelDepartment, guestCtx *GuestContext, userMessage string) (*AgentResponse, error) {
	systemPrompt := BuildDepartmentPrompt(dept, guestCtx)

	messages := []LLMMessage{
		{Role: "system", Content: systemPrompt},
	}

	// Add conversation history
	for _, msg := range guestCtx.ConversationHistory {
		messages = append(messages, msg)
	}

	messages = append(messages, LLMMessage{
		Role:    "user",
		Content: userMessage,
	})

	tools := a.registry.ToolDefinitions()

	resp, err := a.llm.ChatCompletion(ctx, ChatRequest{
		Model:       a.mp.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: a.mp.Temperature,
		MaxTokens:   a.mp.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("department agent (%s) LLM call: %w", dept.DisplayName, err)
	}

	result := &AgentResponse{
		Text:      resp.Content,
		ToolCalls: resp.ToolCalls,
		Usage:     resp.Usage,
	}

	// Execute tool calls if any
	if len(resp.ToolCalls) > 0 {
		result, err = a.handleToolCalls(ctx, guestCtx, dept, messages, resp, tools)
		if err != nil {
			log.Printf("[dept-agent:%s] tool execution failed: %v", dept.DisplayName, err)
			if resp.Content != "" {
				return &AgentResponse{Text: resp.Content + "\n\n_(Your request couldn't be forwarded automatically. Please call the front desk.)_", Usage: resp.Usage}, nil
			}
			return &AgentResponse{Text: "I'm having trouble processing your request. Please call the front desk for assistance.", Usage: resp.Usage}, nil
		}
		// Accumulate usage from tool-call follow-up
		if result.Usage == nil {
			result.Usage = resp.Usage
		} else if resp.Usage != nil {
			result.Usage.PromptTokens += resp.Usage.PromptTokens
			result.Usage.CompletionTokens += resp.Usage.CompletionTokens
			result.Usage.TotalTokens += resp.Usage.TotalTokens
		}
	}

	return result, nil
}

func (a *DepartmentAgent) handleToolCalls(ctx context.Context, guestCtx *GuestContext, dept HotelDepartment, messages []LLMMessage, resp *ChatResponse, tools []ToolDefinition) (*AgentResponse, error) {
	messages = append(messages, LLMMessage{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	})

	for _, tc := range resp.ToolCalls {
		toolResult, err := a.registry.ExecuteTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments), guestCtx)
		if err != nil {
			log.Printf("[dept-agent:%s] tool %s failed: %v", dept.DisplayName, tc.Function.Name, err)
			toolResult = &ToolResult{Success: false, Message: "Tool execution failed"}
		}

		resultJSON, _ := json.Marshal(toolResult)
		messages = append(messages, LLMMessage{
			Role:       "tool",
			Content:    string(resultJSON),
			ToolCallID: tc.ID,
			Name:       tc.Function.Name,
		})
	}

	finalResp, err := a.llm.ChatCompletion(ctx, ChatRequest{
		Model:       a.mp.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: a.mp.Temperature,
		MaxTokens:   a.mp.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("department agent (%s) final LLM call: %w", dept.DisplayName, err)
	}

	return &AgentResponse{Text: finalResp.Content, Usage: finalResp.Usage}, nil
}
