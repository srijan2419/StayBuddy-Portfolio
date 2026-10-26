package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/srijanai/butler-ai/internal/ai"
	"github.com/srijanai/butler-ai/internal/utils"
	"github.com/srijanai/butler-ai/internal/whatsapp"
	"golang.org/x/sync/errgroup"
)

// AIChatService orchestrates the AI chat pipeline: message in → pipeline → response out.
type AIChatService struct {
	db              *sql.DB
	llm             ai.LLMClient
	waAPI           whatsapp.API
	dedup           sync.Map // key: "stayID:msgID" → timestamp
	rl              *rateLimiter
	dryRun          bool
	modelCfg        *ai.ModelConfig
	callLog         *ai.CallLogger
	subSvc          *SubscriptionService
	selfCheckinSvc  *SelfCheckinService
}

// SetSelfCheckinService wires the self-check-in service into the AI pipeline.
func (s *AIChatService) SetSelfCheckinService(svc *SelfCheckinService) {
	s.selfCheckinSvc = svc
}

// NewAIChatService creates the orchestrator with all dependencies.
func NewAIChatService(db *sql.DB, llm ai.LLMClient, waAPI whatsapp.API, dryRun bool, modelCfg *ai.ModelConfig, callLog *ai.CallLogger) *AIChatService {
	return &AIChatService{
		db:       db,
		llm:      llm,
		waAPI:    waAPI,
		dryRun:   dryRun,
		rl:       newRateLimiter(),
		modelCfg: modelCfg,
		callLog:  callLog,
		subSvc:   NewSubscriptionService(db),
	}
}

// ProcessIncomingMessage is the main pipeline entry point. Called in a goroutine per message.
func (s *AIChatService) ProcessIncomingMessage(msg whatsapp.Message, from string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	log.Printf("[ai-pipeline] incoming message from %s, type=%s, id=%s", from, msg.Type, msg.ID)

	// Step 1: Identify guest FIRST (needed for all paths including shortcuts)
	guest, err := s.identifyGuest(ctx, from)
	if err != nil {
		log.Printf("[ai-pipeline] guest not found for %s: %v", from, err)

		// Check if this is a self-check-in flow (QR code scan)
		if s.selfCheckinSvc != nil {
			msgText := extractTextBody(msg)
			if s.selfCheckinSvc.HandleSelfCheckinMessage(ctx, from, msgText) {
				log.Printf("[ai-pipeline] handled by self-check-in flow for %s", from)
				return
			}
		}

		s.sendResponse(ctx, from, "Hello! It seems you're not currently checked in at any of our properties. If you believe this is a mistake, please contact the front desk. 🏨")
		return
	}

	// Step 2: Rate limiting
	if !s.rl.allow(from) {
		log.Printf("[ai-pipeline] rate limit exceeded for %s", from)
		s.sendAndLog(ctx, from, msg.ID, guest.HotelID, guest.StayID, guest.ClientID,
			"You've sent quite a few messages! Please wait a moment before sending more, or call the front desk for immediate assistance. 🙏")
		return
	}

	// Step 2b: Guest interaction limit (plan-based monthly cap)
	allowed, err := s.subSvc.CheckGuestInteractionLimit(ctx, guest.HotelID)
	if err != nil {
		log.Printf("[ai-pipeline] guest limit check error for hotel %d: %v", guest.HotelID, err)
	} else if !allowed {
		log.Printf("[ai-pipeline] guest interaction limit reached for hotel %d", guest.HotelID)
		s.sendResponse(ctx, from, "This hotel has reached its monthly messaging limit. Please contact the front desk directly for assistance. 🏨")
		return
	}

	// Track this unique guest interaction for the month (counted by unique phone per hotel)
	s.trackUniqueGuest(ctx, guest.HotelID, from)

	// Step 3: Classify message type and extract text
	text, messageType, err := s.classifyAndExtractText(ctx, msg)
	if err != nil {
		log.Printf("[ai-pipeline] message extraction failed: %v", err)
		if msg.Type == "audio" {
			s.sendAndLog(ctx, from, msg.ID, guest.HotelID, guest.StayID, guest.ClientID,
				"I couldn't process your voice message. Could you please type your request instead? 🙏")
		}
		return
	}

	// WiFi button shortcut — skip AI entirely
	if messageType == "wifi_shortcut" {
		wifiMsg := fmt.Sprintf("📶 *WiFi Credentials*\n\n*Username:* %s\n*Password:* %s\n\nEnjoy browsing! 🌐", guest.WifiName, guest.WifiPassword)
		s.sendAndLog(ctx, from, "", guest.HotelID, guest.StayID, guest.ClientID, wifiMsg)
		return
	}

	// "Hi StayBuddy" template button — fixed greeting, no LLM
	if messageType == "greeting_shortcut" {
		greetMsg := fmt.Sprintf("Hello %s! 👋 Welcome — I'm your StayBuddy.\n\nJust type whatever you need — room service, housekeeping, towels, restaurant orders, or any questions about the hotel. I'm here 24/7! 🏨", guest.ClientName)
		s.sendAndLog(ctx, from, "", guest.HotelID, guest.StayID, guest.ClientID, greetMsg)
		return
	}

	// Unsupported type
	if messageType == "unsupported" {
		s.sendAndLog(ctx, from, msg.ID, guest.HotelID, guest.StayID, guest.ClientID,
			"I can only process text and voice messages at the moment. Please type your request or send a voice note! 🎤")
		return
	}

	// Step 4: Deduplication (5-second window)
	dedupKey := fmt.Sprintf("%d:%s", guest.StayID, msg.ID)
	if _, loaded := s.dedup.LoadOrStore(dedupKey, time.Now()); loaded {
		log.Printf("[ai-pipeline] duplicate message %s, skipping", msg.ID)
		return
	}
	time.AfterFunc(10*time.Second, func() {
		s.dedup.Delete(dedupKey)
	})

	// Step 5: Save incoming message (with sender's phone for attribution)
	s.saveChatMessage(ctx, guest.HotelID, guest.StayID, "client", messageType, text, msg.ID, from)

	// Step 6: Reaction + typing now fired immediately at webhook level (before queue)

	// Step 7: Load conversation history + context (scoped to this guest's phone thread)
	history := s.loadConversationHistory(ctx, guest.HotelID, guest.StayID, from)
	guestCtx := s.buildGuestContext(ctx, guest, history)

	// Step 8: Translate incoming message to English (if needed)
	t0 := time.Now()
	detectedLang, englishText, transInUsage, _ := ai.TranslateToEnglish(ctx, s.llm, text, s.modelCfg.Translation)
	s.callLog.Log(ctx, guest.HotelID, guest.StayID, "translation_in", s.modelCfg.Translation.Model, "", int(time.Since(t0).Milliseconds()), transInUsage)
	guestCtx.DetectedLang = detectedLang

	// Step 8b: Language gating — free plan only supports 5 languages
	if detectedLang != "en" {
		langAllowed, _ := s.subSvc.CheckLanguageAccess(ctx, guest.HotelID, detectedLang)
		if !langAllowed {
			log.Printf("[ai-pipeline] language %s not available on hotel %d plan, falling back to English", detectedLang, guest.HotelID)
			detectedLang = "en" // Force English response
			guestCtx.DetectedLang = "en"
		}
	}

	// Strip the current message from history — it was just saved in Step 5 and would
	// appear as the last user entry. Department agents receive the sub-request as the
	// user message instead, so keeping the full message in history would cause the LLM
	// to process everything instead of just its department's portion.
	agentHistory := history
	if len(agentHistory) > 0 && agentHistory[len(agentHistory)-1].Role == "user" {
		agentHistory = agentHistory[:len(agentHistory)-1]
	}
	guestCtx.ConversationHistory = agentHistory

	// Step 9: Orchestrate — classify to department(s)
	t1 := time.Now()
	orchResult, orchUsage, err := ai.Orchestrate(ctx, s.llm, englishText, history, guestCtx.Departments, s.modelCfg.Orchestrator)
	s.callLog.Log(ctx, guest.HotelID, guest.StayID, "orchestrator", s.modelCfg.Orchestrator.Model, "", int(time.Since(t1).Milliseconds()), orchUsage)
	if err != nil {
		log.Printf("[ai-pipeline] orchestration failed: %v", err)
		// Fallback: treat as general greeting
		orchResult = &ai.OrchestratorResult{
			Departments: []ai.MatchedDept{{Name: "none"}},
		}
	}

	// Handle clarification
	if orchResult.Clarify {
		reply := orchResult.Question
		if detectedLang != "en" {
			reply, _, _ = ai.TranslateFromEnglish(ctx, s.llm, reply, detectedLang, s.modelCfg.Translation)
		}
		s.sendAndLog(ctx, from, "", guest.HotelID, guest.StayID, guest.ClientID, reply)
		return
	}

	// Step 10: Execute matched department agents in parallel, collect responses
	type deptResp struct {
		idx      int
		deptName string
		text     string
	}

	g, gCtx := errgroup.WithContext(ctx)
	var respMu sync.Mutex
	var deptResponses []deptResp

	for i, match := range orchResult.Departments {
		i, match := i, match
		g.Go(func() error {
			agentMsg := match.SubRequest
			if agentMsg == "" {
				agentMsg = englishText
			}
			log.Printf("[ai-pipeline] dept %s agent receiving: %q", match.Name, agentMsg)

			tAgent := time.Now()
			var agentResp *ai.AgentResponse
			var agentErr error
			var agentType string

			if match.Name == "none" {
				agentType = "simple_response"
				agentResp, agentErr = s.simpleResponse(gCtx, guestCtx, agentMsg)
			} else if match.IsFood {
				agentType = "menu_agent"
				// Lazy-load menu items + previous orders only for food paths
				s.loadFoodContext(gCtx, guestCtx)
				menuRegistry := ai.NewToolRegistry()
				menuRegistry.Register(ai.NewFoodOrderTool(s.db, match.Department.ID))
				menuRegistry.Register(ai.NewCancelOrderTool(s.db))
				agent := ai.NewMenuAgent(s.llm, menuRegistry, s.modelCfg.MenuAgent)
				agentResp, agentErr = agent.Process(gCtx, guestCtx, agentMsg)
			} else {
				agentType = "dept_agent"
				tool := ai.NewDepartmentForwardTool(s.db, match.Department)
				registry := ai.NewToolRegistry()
				registry.Register(tool)
				agent := ai.NewDepartmentAgent(s.llm, registry, s.modelCfg.DeptAgent)
				agentResp, agentErr = agent.Process(gCtx, match.Department, guestCtx, agentMsg)
			}

			// One retry on agent error
			if agentErr != nil {
				log.Printf("[ai-pipeline] agent error for %s: %v, retrying once", match.Name, agentErr)
				if match.Name == "none" {
					agentResp, agentErr = s.simpleResponse(gCtx, guestCtx, agentMsg)
				} else if match.IsFood {
					s.loadFoodContext(gCtx, guestCtx) // ensure menu loaded for retry
					menuRegistry := ai.NewToolRegistry()
					menuRegistry.Register(ai.NewFoodOrderTool(s.db, match.Department.ID))
					menuRegistry.Register(ai.NewCancelOrderTool(s.db))
					agent := ai.NewMenuAgent(s.llm, menuRegistry, s.modelCfg.MenuAgent)
					agentResp, agentErr = agent.Process(gCtx, guestCtx, agentMsg)
				} else {
					tool := ai.NewDepartmentForwardTool(s.db, match.Department)
					registry := ai.NewToolRegistry()
					registry.Register(tool)
					agent := ai.NewDepartmentAgent(s.llm, registry, s.modelCfg.DeptAgent)
					agentResp, agentErr = agent.Process(gCtx, match.Department, guestCtx, agentMsg)
				}
				if agentErr != nil {
					log.Printf("[ai-pipeline] agent retry failed for %s: %v", match.Name, agentErr)
					return nil // don't fail the group
				}
			}

			var loggedModel string
			switch agentType {
			case "menu_agent":
				loggedModel = s.modelCfg.MenuAgent.Model
			case "simple_response":
				loggedModel = s.modelCfg.SimpleResponse.Model
			default:
				loggedModel = s.modelCfg.DeptAgent.Model
			}
			var agentUsage *ai.UsageInfo
			if agentResp != nil {
				agentUsage = agentResp.Usage
			}
			s.callLog.Log(gCtx, guest.HotelID, guest.StayID, agentType, loggedModel, "", int(time.Since(tAgent).Milliseconds()), agentUsage)

			if agentResp != nil && agentResp.Text != "" {
				// Deterministic quality guardrails (log-only)
				ai.ValidateAgentResponse(agentResp.Text, guestCtx)

				respMu.Lock()
				deptResponses = append(deptResponses, deptResp{
					idx:      i,
					deptName: match.Department.DisplayName,
					text:     agentResp.Text,
				})
				respMu.Unlock()
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		log.Printf("[ai-pipeline] parallel agent error: %v", err)
	}

	// Sort by original index to preserve order
	sort.Slice(deptResponses, func(i, j int) bool {
		return deptResponses[i].idx < deptResponses[j].idx
	})

	if len(deptResponses) == 0 {
		s.sendAndLog(ctx, from, "", guest.HotelID, guest.StayID, guest.ClientID,
			"I'm having a little trouble right now. Please try again in a moment, or call the front desk for immediate assistance. 🙏")
		return
	}

	// Step 11: Merge multi-department responses intelligently, then translate
	var finalText string
	if len(deptResponses) == 1 {
		finalText = deptResponses[0].text
	} else {
		mergeInputs := make([]mergedDeptResponse, len(deptResponses))
		for i, dr := range deptResponses {
			mergeInputs[i] = mergedDeptResponse{DeptName: dr.deptName, Text: dr.text}
		}
		merged, err := s.mergeMultiDeptResponses(ctx, englishText, mergeInputs)
		if err != nil {
			log.Printf("[ai-pipeline] merger failed, using fallback: %v", err)
			texts := make([]string, len(deptResponses))
			for i, dr := range deptResponses {
				texts[i] = dr.text
			}
			merged = strings.Join(texts, " ")
		}
		finalText = merged
	}

	finalReply := finalText
	if detectedLang != "en" {
		tOut := time.Now()
		var transOutUsage *ai.UsageInfo
		finalReply, transOutUsage, _ = ai.TranslateFromEnglish(ctx, s.llm, finalText, detectedLang, s.modelCfg.Translation)
		s.callLog.Log(ctx, guest.HotelID, guest.StayID, "translation_out", s.modelCfg.Translation.Model, "", int(time.Since(tOut).Milliseconds()), transOutUsage)
	}

	// Step 12: Save and send response
	s.sendAndLog(ctx, from, "", guest.HotelID, guest.StayID, guest.ClientID, finalReply)
}

// mergedDeptResponse holds a department name and its agent's response text.
type mergedDeptResponse struct {
	DeptName string
	Text     string
}

// mergeMultiDeptResponses uses an LLM call to combine multiple department responses
// into one coherent WhatsApp message. Only called when len(responses) > 1.
func (s *AIChatService) mergeMultiDeptResponses(ctx context.Context, guestMessage string, responses []mergedDeptResponse) (string, error) {
	var sb strings.Builder

	sb.WriteString(`You are StayBuddy's response editor. You receive separate responses from different hotel departments that each handled part of a guest's message. Your job: combine them into ONE natural, coherent WhatsApp message.

# Guest's original message
`)
	sb.WriteString(guestMessage)
	sb.WriteString("\n\n# Department responses to combine\n")

	for _, dr := range responses {
		sb.WriteString(fmt.Sprintf("\n--- %s ---\n%s\n", dr.DeptName, dr.Text))
	}

	sb.WriteString(`
# Rules
1. Output ONLY the final merged message. No preamble, no explanation.
2. ONE greeting, ONE sign-off. Remove all duplicate greetings/closings from individual responses.
3. Structure: action confirmations first (e.g. "request forwarded"), then options/questions (e.g. menu items).
4. Do NOT mention internal department names (Housekeeping, Front Desk, I.R.D.). The guest doesn't need to know which team handles what. Say "our team" or "staff" if needed.
5. WhatsApp formatting: *bold* for emphasis, bullet points for lists.
6. Keep it warm, concise, and natural. Light emoji use.
7. If both departments forwarded requests, combine into one confirmation sentence.
8. If one department forwarded a request and another is showing menu options, separate them with a line break.
9. Preserve all prices, item names, and factual details exactly as provided.
10. Respond ONLY in English.
11. Do NOT include any internal reference numbers, order IDs, or ticket numbers. These are internal system details the guest should never see.
`)

	resp, err := s.llm.ChatCompletion(ctx, ai.ChatRequest{
		Model:       s.modelCfg.Merger.Model,
		Messages:    []ai.LLMMessage{{Role: "user", Content: sb.String()}},
		Temperature: s.modelCfg.Merger.Temperature,
		MaxTokens:   s.modelCfg.Merger.MaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("response merger LLM call: %w", err)
	}

	log.Printf("[ai-pipeline] merged %d department responses into one", len(responses))
	return resp.Content, nil
}

// simpleResponse generates a no-tool response for general greetings/chat.
func (s *AIChatService) simpleResponse(ctx context.Context, guestCtx *ai.GuestContext, message string) (*ai.AgentResponse, error) {
	prompt := ai.BuildSimpleResponsePrompt(guestCtx)

	messages := []ai.LLMMessage{
		{Role: "system", Content: prompt},
	}
	for _, msg := range guestCtx.ConversationHistory {
		messages = append(messages, msg)
	}
	messages = append(messages, ai.LLMMessage{Role: "user", Content: message})

	resp, err := s.llm.ChatCompletion(ctx, ai.ChatRequest{
		Model:       s.modelCfg.SimpleResponse.Model,
		Messages:    messages,
		Temperature: s.modelCfg.SimpleResponse.Temperature,
		MaxTokens:   s.modelCfg.SimpleResponse.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("simple response LLM call: %w", err)
	}

	return &ai.AgentResponse{Text: resp.Content, Usage: resp.Usage}, nil
}

// guestInfo is the minimal guest identification result.
type guestInfo struct {
	StayID       int
	ClientID     int
	ClientName   string
	Phone        string
	RoomNumber   string
	RoomID       int
	HotelID      int
	HotelName    string
	WifiName     string
	WifiPassword string
}

// classifyAndExtractText determines message type and extracts text content.
func (s *AIChatService) classifyAndExtractText(ctx context.Context, msg whatsapp.Message) (string, string, error) {
	switch msg.Type {
	case "text":
		if msg.Text == nil || strings.TrimSpace(msg.Text.Body) == "" {
			return "", "unsupported", nil
		}
		return msg.Text.Body, "text", nil

	case "audio":
		if msg.Audio == nil {
			return "", "unsupported", fmt.Errorf("audio message with no audio data")
		}
		audioData, mimeType, err := s.waAPI.DownloadMedia(ctx, msg.Audio.ID)
		if err != nil {
			return "", "audio", fmt.Errorf("download audio: %w", err)
		}
		text, err := s.llm.Transcribe(ctx, audioData, mimeType)
		if err != nil {
			return "", "audio", fmt.Errorf("transcribe audio: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return "", "unsupported", nil
		}
		return text, "audio", nil

	case "button":
		if msg.Button != nil {
			payload := strings.ToLower(msg.Button.Payload)
			if strings.Contains(payload, "wifi") {
				return "", "wifi_shortcut", nil
			}
			// "Hi StayBuddy" button from check-in template — fixed greeting, no LLM
			if strings.Contains(payload, "hi") || strings.Contains(payload, "hello") || strings.Contains(payload, "staybuddy") {
				return "", "greeting_shortcut", nil
			}
			return msg.Button.Text, "text", nil
		}
		return "", "unsupported", nil

	case "interactive":
		if msg.Interactive != nil {
			if msg.Interactive.ButtonReply != nil {
				payload := strings.ToLower(msg.Interactive.ButtonReply.ID)
				if strings.Contains(payload, "wifi") {
					return "", "wifi_shortcut", nil
				}
				return msg.Interactive.ButtonReply.Title, "text", nil
			}
			if msg.Interactive.ListReply != nil {
				return msg.Interactive.ListReply.Title, "text", nil
			}
		}
		return "", "unsupported", nil

	default:
		return "", "unsupported", nil
	}
}

// identifyGuest looks up a checked-in guest by phone number.
// Checks primary guests (clients table) first, then additional guests (guests table).
func (s *AIChatService) identifyGuest(ctx context.Context, phone string) (*guestInfo, error) {
	phoneCandidates := []string{"+" + phone, phone}
	if strings.HasPrefix(phone, "+") {
		phoneCandidates = []string{phone, strings.TrimPrefix(phone, "+")}
	}

	// Query 1: Primary guest lookup via clients table
	primaryQuery := `
		SELECT
			s.id AS stay_id,
			c.id AS client_id,
			c.name AS client_name,
			c.phone,
			r.number AS room_number,
			r.id AS room_id,
			h.id AS hotel_id,
			h.hotel_name,
			COALESCE(s.wifi_name, '') AS wifi_name,
			COALESCE(s.wifi_password, '') AS wifi_password
		FROM stays s
		JOIN clients c ON s.client_id = c.id
		JOIN rooms r ON s.room_id = r.id
		JOIN hotels h ON c.hotel_id = h.id
		WHERE c.phone = $1 AND s.status = 'checked_in'
		ORDER BY s.check_in DESC
		LIMIT 1
	`

	var g guestInfo
	for _, candidate := range phoneCandidates {
		err := s.db.QueryRowContext(ctx, primaryQuery, candidate).Scan(
			&g.StayID, &g.ClientID, &g.ClientName, &g.Phone,
			&g.RoomNumber, &g.RoomID, &g.HotelID, &g.HotelName,
			&g.WifiName, &g.WifiPassword,
		)
		if err == nil {
			return &g, nil
		}
	}

	// Query 2: Additional guest lookup via guests table
	additionalQuery := `
		SELECT
			s.id AS stay_id,
			COALESCE(ac.id, 0) AS client_id,
			g.name AS client_name,
			g.phone,
			r.number AS room_number,
			r.id AS room_id,
			h.id AS hotel_id,
			h.hotel_name,
			COALESCE(s.wifi_name, '') AS wifi_name,
			COALESCE(s.wifi_password, '') AS wifi_password
		FROM guests g
		JOIN stays s ON g.stay_id = s.id
		JOIN rooms r ON s.room_id = r.id
		JOIN clients pc ON s.client_id = pc.id
		JOIN hotels h ON pc.hotel_id = h.id
		LEFT JOIN clients ac ON ac.phone = g.phone AND ac.hotel_id = h.id
		WHERE g.phone = $1 AND s.status = 'checked_in'
		ORDER BY s.check_in DESC
		LIMIT 1
	`

	for _, candidate := range phoneCandidates {
		err := s.db.QueryRowContext(ctx, additionalQuery, candidate).Scan(
			&g.StayID, &g.ClientID, &g.ClientName, &g.Phone,
			&g.RoomNumber, &g.RoomID, &g.HotelID, &g.HotelName,
			&g.WifiName, &g.WifiPassword,
		)
		if err == nil {
			return &g, nil
		}
	}

	return nil, fmt.Errorf("guest lookup for phone %s: no checked-in stay found", phone)
}

// historyTokenBudget is the max estimated tokens for conversation history.
// ~4 chars per token. 1500 tokens ≈ 6000 chars. This prevents prompt blowup
// from long conversations while preserving enough context for continuity.
const historyTokenBudget = 1500

// estimateTokens returns a rough token count (~4 chars/token for English text).
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// loadConversationHistory fetches recent messages with token-budgeted windowing.
// Fetches up to 20 messages from the last 2 hours, applies session-break detection,
// then trims from the oldest end to fit within historyTokenBudget.
// Messages are scoped to the specific guest phone thread to prevent cross-contamination
// between primary and additional guests on the same stay.
func (s *AIChatService) loadConversationHistory(ctx context.Context, hotelID, stayID int, guestPhone string) []ai.LLMMessage {
	// Normalize phone: strip leading '+' for matching
	normalizedPhone := strings.TrimLeft(guestPhone, "+")

	query := `
		SELECT sender_type, message, sent_at
		FROM chats
		WHERE hotel_id = $1 AND stay_id = $2
		  AND sent_at > NOW() - INTERVAL '2 hours'
		  AND (
		    sender_phone IS NULL
		    OR LTRIM(sender_phone, '+') = $3
		  )
		ORDER BY sent_at DESC
		LIMIT 20
	`

	rows, err := s.db.QueryContext(ctx, query, hotelID, stayID, normalizedPhone)
	if err != nil {
		log.Printf("[ai-pipeline] failed to load history: %v", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	type chatMsg struct {
		role    string
		content string
		sentAt  time.Time
	}

	var msgs []chatMsg
	for rows.Next() {
		var senderType, message string
		var sentAt time.Time
		if err := rows.Scan(&senderType, &message, &sentAt); err != nil {
			continue
		}
		var role string
		switch senderType {
		case "staff":
			role = "assistant"
		case "client":
			role = "user"
		default:
			continue
		}
		msgs = append(msgs, chatMsg{role: role, content: message, sentAt: sentAt})
	}

	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	// Session break: if gap > 30 min, only keep messages after the break
	if len(msgs) > 1 {
		lastBreak := 0
		for i := 1; i < len(msgs); i++ {
			gap := msgs[i].sentAt.Sub(msgs[i-1].sentAt)
			if gap > 30*time.Minute {
				lastBreak = i
			}
		}
		if lastBreak > 0 {
			msgs = msgs[lastBreak:]
		}
	}

	// Token-budgeted trimming: keep the most recent messages that fit within budget.
	// Trim from the oldest end to preserve recency.
	totalTokens := 0
	cutoff := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		totalTokens += estimateTokens(msgs[i].content)
		if totalTokens > historyTokenBudget {
			cutoff = i + 1
			break
		}
	}
	if cutoff > 0 && cutoff < len(msgs) {
		msgs = msgs[cutoff:]
	}

	// Convert to LLMMessage
	result := make([]ai.LLMMessage, len(msgs))
	for i, m := range msgs {
		result[i] = ai.LLMMessage{Role: m.role, Content: m.content}
	}
	return result
}

// buildGuestContext assembles the core context for an agent.
// Menu items and previous orders are NOT loaded here — they're lazy-loaded
// only when a food department is matched (see loadFoodContext).
func (s *AIChatService) buildGuestContext(ctx context.Context, guest *guestInfo, history []ai.LLMMessage) *ai.GuestContext {
	loc := utils.HotelTimezone(ctx, s.db, guest.HotelID)

	guestCtx := &ai.GuestContext{
		StayID:              guest.StayID,
		ClientID:            guest.ClientID,
		ClientName:          guest.ClientName,
		Phone:               guest.Phone,
		RoomNumber:          guest.RoomNumber,
		RoomID:              guest.RoomID,
		HotelID:             guest.HotelID,
		HotelName:           guest.HotelName,
		WifiName:            guest.WifiName,
		WifiPassword:        guest.WifiPassword,
		CurrentTime:         time.Now().In(loc).Format("03:04 PM, Mon Jan 02 2006"),
		ConversationHistory: history,
	}

	// Load hotel general details (needed by all agents) — but skip menu for non-food paths
	s.loadHotelDetailsLite(ctx, guestCtx)

	// Load dynamic departments with per-hotel AI instructions
	guestCtx.Departments = s.loadHotelDepartments(ctx, guest.HotelID)

	// Load special instructions
	guestCtx.SpecialInstructions = s.loadSpecialInstructions(ctx, guest.HotelID)

	// Load recent service requests (3 most recent)
	guestCtx.RecentServiceRequests = s.loadRecentServiceRequests(ctx, guest.HotelID, guest.ClientID, loc)

	return guestCtx
}

// loadFoodContext lazily loads menu items and previous orders into guestCtx.
// Called only when a food department is matched — saves a DB query + tokens for non-food paths.
func (s *AIChatService) loadFoodContext(ctx context.Context, guestCtx *ai.GuestContext) {
	if len(guestCtx.MenuItems) > 0 {
		return // already loaded
	}

	// Load structured menu items
	var menuItemsJSON sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT menu_items FROM hotels WHERE id = $1`, guestCtx.HotelID,
	).Scan(&menuItemsJSON)
	if err == nil && menuItemsJSON.Valid && menuItemsJSON.String != "" && menuItemsJSON.String != "[]" {
		var items []ai.MenuItem
		if err := json.Unmarshal([]byte(menuItemsJSON.String), &items); err == nil {
			guestCtx.MenuItems = items
		}
	}

	// Load previous food orders (3 most recent)
	loc := utils.HotelTimezone(ctx, s.db, guestCtx.HotelID)
	guestCtx.PreviousOrders = s.loadPreviousOrders(ctx, guestCtx.HotelID, guestCtx.ClientID, loc)
}

// loadHotelDetailsLite fetches hotel general details and raw menu text (for fallback).
// Structured menu_items are lazy-loaded by loadFoodContext only when needed.
func (s *AIChatService) loadHotelDetailsLite(ctx context.Context, guestCtx *ai.GuestContext) {
	query := `SELECT COALESCE(hotel_general_details, ''), COALESCE(hotel_menu_details, '') FROM hotels WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, guestCtx.HotelID).Scan(
		&guestCtx.HotelGeneralDetails,
		&guestCtx.HotelMenuDetails,
	)
	if err != nil {
		log.Printf("[ai-pipeline] failed to load hotel details: %v", err)
	}
}

// loadHotelDepartments fetches all departments for a hotel with their per-hotel AI instructions.
func (s *AIChatService) loadHotelDepartments(ctx context.Context, hotelID int) []ai.HotelDepartment {
	query := `
		SELECT hd.id, d.id, d.name, COALESCE(hd.display_name, d.name),
		       COALESCE(d.description, ''),
		       COALESCE(apc.prompt_text, ''),
		       COALESCE(d.department_type, 'general')
		FROM hotel_departments hd
		JOIN departments d ON d.id = hd.department_id
		LEFT JOIN ai_prompt_configs apc ON apc.hotel_id = hd.hotel_id
		    AND apc.config_key = 'dept_' || LOWER(d.name) AND apc.is_active = TRUE
		WHERE hd.hotel_id = $1 AND LOWER(d.name) != 'master'
		ORDER BY d.name
	`

	rows, err := s.db.QueryContext(ctx, query, hotelID)
	if err != nil {
		log.Printf("[ai-pipeline] failed to load hotel departments: %v", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var depts []ai.HotelDepartment
	for rows.Next() {
		var dept ai.HotelDepartment
		var displayName sql.NullString
		if err := rows.Scan(&dept.ID, &dept.DepartmentID, &dept.Name, &displayName,
			&dept.Description, &dept.DeptInstructions, &dept.DepartmentType); err != nil {
			log.Printf("[ai-pipeline] failed to scan department: %v", err)
			continue
		}
		if displayName.Valid && displayName.String != "" {
			dept.DisplayName = displayName.String
		} else {
			dept.DisplayName = dept.Name
		}
		depts = append(depts, dept)
	}

	log.Printf("[ai-pipeline] loaded %d departments for hotel %d", len(depts), hotelID)
	return depts
}

// loadSpecialInstructions fetches the hotel-wide special instructions from ai_prompt_configs.
// Supports both JSON array (new) and plain text (legacy) formats.
func (s *AIChatService) loadSpecialInstructions(ctx context.Context, hotelID int) string {
	var raw string
	err := s.db.QueryRowContext(ctx, `
		SELECT prompt_text FROM ai_prompt_configs
		WHERE hotel_id = $1 AND config_key = 'special_instructions' AND is_active = TRUE
	`, hotelID).Scan(&raw)
	if err != nil {
		return ""
	}

	// Try JSON array format (new)
	var items []string
	if json.Unmarshal([]byte(raw), &items) == nil && len(items) > 0 {
		var sb strings.Builder
		for i, item := range items {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(item)))
		}
		return sb.String()
	}

	// Legacy: plain text
	return raw
}

// loadRecentServiceRequests fetches the last 3 service requests for context.
func (s *AIChatService) loadRecentServiceRequests(ctx context.Context, hotelID, clientID int, loc *time.Location) []ai.ServiceRequestSummary {
	query := `
		SELECT sr.id, COALESCE(d.name, 'Unknown'), sr.description, sr.status, sr.created_at
		FROM service_requests sr
		LEFT JOIN hotel_departments hd ON hd.id = sr.department_id
		LEFT JOIN departments d ON d.id = hd.department_id
		WHERE sr.hotel_id = $1 AND sr.client_id = $2
		ORDER BY sr.created_at DESC
		LIMIT 3
	`

	rows, err := s.db.QueryContext(ctx, query, hotelID, clientID)
	if err != nil {
		log.Printf("[ai-pipeline] failed to load recent SRs: %v", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var srs []ai.ServiceRequestSummary
	for rows.Next() {
		var sr ai.ServiceRequestSummary
		var createdAt time.Time
		if err := rows.Scan(&sr.ID, &sr.Department, &sr.Description, &sr.Status, &createdAt); err != nil {
			continue
		}
		sr.CreatedAt = createdAt.In(loc).Format("03:04 PM, Jan 02")
		srs = append(srs, sr)
	}
	return srs
}

// loadPreviousOrders fetches previous food orders for smart reordering.
func (s *AIChatService) loadPreviousOrders(ctx context.Context, hotelID, clientID int, loc *time.Location) []ai.PreviousOrder {
	query := `
		SELECT sr.request_details, sr.created_at
		FROM service_requests sr
		LEFT JOIN hotel_departments hd ON hd.id = sr.department_id
		LEFT JOIN departments d ON d.id = hd.department_id
		WHERE sr.hotel_id = $1 AND sr.client_id = $2
			AND sr.request_details IS NOT NULL
			AND COALESCE(d.department_type, 'general') = 'food'
		ORDER BY sr.created_at DESC
		LIMIT 3
	`

	rows, err := s.db.QueryContext(ctx, query, hotelID, clientID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var orders []ai.PreviousOrder
	for rows.Next() {
		var detailsJSON string
		var createdAt time.Time
		if err := rows.Scan(&detailsJSON, &createdAt); err != nil {
			continue
		}

		var details struct {
			Items []struct {
				Item     string `json:"item"`
				Quantity int    `json:"quantity"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
			continue
		}

		var itemNames []string
		for _, item := range details.Items {
			if item.Quantity > 1 {
				itemNames = append(itemNames, fmt.Sprintf("%s x%d", item.Item, item.Quantity))
			} else {
				itemNames = append(itemNames, item.Item)
			}
		}

		orders = append(orders, ai.PreviousOrder{
			Items:     strings.Join(itemNames, ", "),
			Total:     details.Total,
			CreatedAt: createdAt.In(loc).Format("03:04 PM, Jan 02"),
		})
	}
	return orders
}

// saveChatMessage stores a message in the chats table.
func (s *AIChatService) saveChatMessage(ctx context.Context, hotelID, stayID int, senderType, messageType, message, waMessageID, senderPhone string) {
	if messageType == "audio" {
		messageType = "text"
	}

	query := `
		INSERT INTO chats (hotel_id, stay_id, sender_type, message_type, message, whatsapp_message_id, sender_phone, sent_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), NOW())
	`
	_, err := s.db.ExecContext(ctx, query, hotelID, stayID, senderType, messageType, message, waMessageID, senderPhone)
	if err != nil {
		log.Printf("[ai-pipeline] failed to save chat: %v", err)
	}
}

// sendAndLog saves the bot response to chats and sends via WhatsApp.
// Stores `to` as sender_phone so bot responses are attributed to the guest they were sent to.
func (s *AIChatService) sendAndLog(ctx context.Context, to, waMessageID string, hotelID, stayID, clientID int, text string) {
	if hotelID > 0 && stayID > 0 {
		s.saveChatMessage(ctx, hotelID, stayID, "staff", "text", text, "", to)
	}
	s.sendResponse(ctx, to, text)
}

// sendResponse sends a text message via WhatsApp, with one retry.
func (s *AIChatService) sendResponse(ctx context.Context, to, text string) {
	if s.dryRun {
		log.Printf("[ai-pipeline] DRY RUN — would send to %s: %s", to, truncate(text, 100))
		return
	}

	err := s.waAPI.SendTextMessage(ctx, to, text)
	if err != nil {
		log.Printf("[ai-pipeline] WhatsApp send failed, retrying: %v", err)
		time.Sleep(2 * time.Second)
		if err := s.waAPI.SendTextMessage(ctx, to, text); err != nil {
			log.Printf("[ai-pipeline] WhatsApp send retry failed: %v", err)
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// extractTextBody pulls the plain text body from a WhatsApp message for self-check-in matching.
func extractTextBody(msg whatsapp.Message) string {
	if msg.Type == "text" && msg.Text != nil {
		return msg.Text.Body
	}
	if msg.Type == "interactive" && msg.Interactive != nil {
		if msg.Interactive.ButtonReply != nil {
			return msg.Interactive.ButtonReply.Title
		}
	}
	if msg.Type == "button" && msg.Button != nil {
		return msg.Button.Text
	}
	return ""
}

// --- Rate Limiter ---

type rateLimiter struct {
	mu       sync.Mutex
	counters map[string]*rateBucket
}

type rateBucket struct {
	fiveMin   int
	hourly    int
	daily     int
	resetFive time.Time
	resetHour time.Time
	resetDay  time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		counters: make(map[string]*rateBucket),
	}
}

func (rl *rateLimiter) allow(phone string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	bucket, ok := rl.counters[phone]
	if !ok {
		bucket = &rateBucket{
			resetFive: now.Add(5 * time.Minute),
			resetHour: now.Add(1 * time.Hour),
			resetDay:  now.Add(24 * time.Hour),
		}
		rl.counters[phone] = bucket
	}

	// Reset expired windows
	if now.After(bucket.resetFive) {
		bucket.fiveMin = 0
		bucket.resetFive = now.Add(5 * time.Minute)
	}
	if now.After(bucket.resetHour) {
		bucket.hourly = 0
		bucket.resetHour = now.Add(1 * time.Hour)
	}
	if now.After(bucket.resetDay) {
		bucket.daily = 0
		bucket.resetDay = now.Add(24 * time.Hour)
	}

	// Check limits
	if bucket.fiveMin >= 30 || bucket.hourly >= 100 || bucket.daily >= 300 {
		return false
	}

	bucket.fiveMin++
	bucket.hourly++
	bucket.daily++
	return true
}

// trackUniqueGuest increments the monthly unique guest counter for a hotel.
// Only counts each phone number once per month using a distinct check.
func (s *AIChatService) trackUniqueGuest(ctx context.Context, hotelID int, phone string) {
	normalizedPhone := strings.TrimLeft(phone, "+")
	monthYear := time.Now().Format("2006-01")

	// Check if this phone already interacted with this hotel this month
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM chats
			WHERE hotel_id = $1 AND sender_phone IS NOT NULL
			  AND LTRIM(sender_phone, '+') = $2
			  AND sent_at >= date_trunc('month', NOW())
			  AND sender_type = 'client'
			LIMIT 1
		)
	`, hotelID, normalizedPhone).Scan(&exists)
	if err != nil {
		log.Printf("[ai-pipeline] trackUniqueGuest check failed: %v", err)
		return
	}

	// If this is a NEW unique guest this month, increment the counter
	if !exists {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO guest_usage_monthly (hotel_id, month_year, unique_guests)
			VALUES ($1, $2, 1)
			ON CONFLICT (hotel_id, month_year) DO UPDATE
			SET unique_guests = guest_usage_monthly.unique_guests + 1
		`, hotelID, monthYear)
		if err != nil {
			log.Printf("[ai-pipeline] trackUniqueGuest increment failed: %v", err)
		}
	}
}
