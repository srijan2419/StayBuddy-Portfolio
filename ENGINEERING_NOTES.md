# StayBuddy — Engineering Deep Dive

This document maps **real source code** to the decisions required to build a hotel product for paying customers. It is a technical walkthrough of a **curated portfolio edition**, not a deployment guide for a complete standalone system.

## 1. Guest conversations have to cause real-world actions

Start with [orchestrator.go](src/backend/internal/ai/orchestrator.go).

The orchestration layer constructs a classification prompt, adds recent conversation history, makes a model call, parses structured results, and retries malformed outputs. It allows multi-department matching: a guest can ask more than one thing in a single message.

The [department agent](src/backend/internal/ai/agent_department.go) scopes context to a department, provides tool definitions, and handles tool-call results and fallbacks. Compare [food-order tooling](src/backend/internal/ai/tools_food_order.go) and [department tooling](src/backend/internal/ai/tools_department.go) to see how an LLM is integrated with business-specific workflows rather than just returning text.

The [typed contracts](src/backend/internal/ai/types.go) make these boundaries explicit. [Response diagnostics](src/backend/internal/ai/guardrails.go) check response length and suspicious menu prices; they **log warnings** rather than blocking model output. That distinction matters.

## 2. The application is more than the model

The [AI chat service](src/backend/internal/service/ai_chat_service.go) connects the AI layer to guest identification, message processing, conversation state, timeouts, and operational workflows. The [WhatsApp webhook handler](src/backend/internal/whatsapp/webhook_handler.go) receives platform events, and [HMAC signature verification](src/backend/internal/whatsapp/signature.go) verifies incoming request authenticity.

The [staff conversation view](src/frontend/ChatWindow.tsx) and [analytics card](src/frontend/AnalyticsCard.tsx) illustrate the separate operations interface. These are genuine application components, but dependencies on CSS, utility modules and the rest of the app have not been included.

## 3. Commercial SaaS concerns

The [subscription service](src/backend/internal/service/subscription_service.go) and [Stripe integration service](src/backend/internal/service/stripe_service.go) demonstrate how billing and account lifecycle concerns were engineered alongside guest-facing features.

**Important distinction:** The presence of payment integration code demonstrates implementation, **not the amount billed or which provider processed a given client payment**. Evidence of actual paid deployments must be supplied separately, without disclosing private customer records.

## 4. Testing and practical trade-offs

[Middleware tests](src/backend/internal/router/middleware_test.go) show real test cases for request IDs, security headers and HTTP handling.

Interview discussion topics include:
- Recovering from malformed LLM responses without confusing hotel operations.
- Department-scoped context versus a single unconstrained agent.
- Separating live WhatsApp interactions from staff-facing workflows.
- Handling failed tool calls, errors, and duplicated incoming messages.
- Balancing speed of client delivery with reliability, monitoring, and clear boundaries.

## 5. Scope of this portfolio edition

**Included:** selected, real Go and React/TypeScript code from the application, with a fresh Git history.

**Intentionally not included:** private source history, environment files, tokens, customer records, live uploads, operational logs, full schema/deployment state, and private account configurations.

Individual excerpts may import application modules absent from this portfolio; **a full product build cannot be reproduced solely from these files**. No build success, production security certification, or independent validation of revenue is claimed.

Before publishing, confirm the rights to share the corporate-owned source, perform a final secret/privacy scan, and manually review any later screenshots or videos. No license has been granted to redistribute or reuse the code.
