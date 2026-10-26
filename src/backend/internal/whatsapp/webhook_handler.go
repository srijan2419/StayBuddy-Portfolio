package whatsapp

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// IncomingMessageHandler is called for each valid incoming message.
type IncomingMessageHandler func(msg Message, from string)

// phoneQueue serializes message processing per phone number.
// Each phone gets a buffered channel; messages are processed one at a time
// so each pipeline run sees the full history from the previous one.
type phoneQueue struct {
	mu      sync.Mutex
	queues  map[string]chan messageJob
	handler IncomingMessageHandler
}

type messageJob struct {
	msg  Message
	from string
}

func newPhoneQueue(handler IncomingMessageHandler) *phoneQueue {
	return &phoneQueue{
		queues:  make(map[string]chan messageJob),
		handler: handler,
	}
}

// enqueue adds a message to the phone's queue. If no worker exists yet, one is spawned.
func (pq *phoneQueue) enqueue(msg Message, from string) {
	pq.mu.Lock()
	ch, exists := pq.queues[from]
	if !exists {
		ch = make(chan messageJob, 50)
		pq.queues[from] = ch
		go pq.worker(from, ch)
	}
	pq.mu.Unlock()

	select {
	case ch <- messageJob{msg: msg, from: from}:
	default:
		log.Printf("[webhook] message queue full for %s, dropping message %s", from, msg.ID)
	}
}

// worker processes messages sequentially for a single phone number.
// Exits after 5 minutes of inactivity to free resources.
func (pq *phoneQueue) worker(phone string, ch chan messageJob) {
	idleTimeout := 5 * time.Minute
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	for {
		select {
		case job := <-ch:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			pq.handler(job.msg, job.from)
			timer.Reset(idleTimeout)

		case <-timer.C:
			pq.mu.Lock()
			if len(ch) == 0 {
				delete(pq.queues, phone)
				pq.mu.Unlock()
				log.Printf("[webhook] phone queue worker for %s exited (idle)", phone)
				return
			}
			pq.mu.Unlock()
			timer.Reset(idleTimeout)
		}
	}
}

// WebhookHandler handles Meta WhatsApp webhook verification and incoming messages.
type WebhookHandler struct {
	verifyToken string
	appSecret   string
	onMessage   IncomingMessageHandler
	pq          *phoneQueue
	waAPI       API // for immediate reactions + typing indicators
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(verifyToken, appSecret string, onMessage IncomingMessageHandler, waAPI ...API) *WebhookHandler {
	h := &WebhookHandler{
		verifyToken: verifyToken,
		appSecret:   appSecret,
		onMessage:   onMessage,
		pq:          newPhoneQueue(onMessage),
	}
	if len(waAPI) > 0 {
		h.waAPI = waAPI[0]
	}
	return h
}

// HandleVerify handles the GET webhook verification challenge from Meta.
func (h *WebhookHandler) HandleVerify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == h.verifyToken {
		log.Printf("[webhook] verification successful")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(challenge))
		return
	}

	log.Printf("[webhook] verification failed: mode=%s token_match=%v", mode, token == h.verifyToken)
	http.Error(w, "Forbidden", http.StatusForbidden)
}

// HandleIncoming handles POST webhook events from Meta.
func (h *WebhookHandler) HandleIncoming(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[webhook] failed to read body: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if !VerifySignature(signature, body, h.appSecret) {
		log.Printf("[webhook] invalid signature")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Return 200 immediately (Meta requirement)
	w.WriteHeader(http.StatusOK)

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("[webhook] failed to parse payload: %v", err)
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			for _, msg := range change.Value.Messages {
				// Fire reaction + typing IMMEDIATELY — before any queue wait
				if h.waAPI != nil {
					go h.fireImmediateFeedback(msg, msg.From)
				}
				h.pq.enqueue(msg, msg.From)
			}
		}
	}
}

// fireImmediateFeedback sends reaction + typing indicator right away so the
// guest sees acknowledgement even if their message is queued behind another.
func (h *WebhookHandler) fireImmediateFeedback(msg Message, to string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Send reaction
	reactions := []string{"👀", "🤖", "✨", "👋", "💬", "🙌", "⚡", "🔔", "🫡", "👍"}
	if err := h.waAPI.SendReaction(ctx, to, msg.ID, reactions[rand.IntN(len(reactions))]); err != nil {
		log.Printf("[webhook] reaction failed for %s: %v", to, err)
	}

	// 2. Show typing indicator (mark as read + typing in one call, lasts up to 25s)
	if err := h.waAPI.SendTypingForMessage(ctx, msg.ID); err != nil {
		log.Printf("[webhook] typing indicator failed for %s: %v", to, err)
	}
	log.Printf("[webhook] immediate feedback sent for %s (msg %s)", to, msg.ID)
}
