# StayBuddy — AI Hotel Concierge Built for Paying Customers

**Production software • Independently built • Deployed for hospitality customers**

StayBuddy is a WhatsApp-based AI concierge and hotel-operations platform that I designed, engineered, and delivered through SrijanAI Innovations. It connects guest conversations to the workflows hotels actually need: service requests, departments, food ordering, check-in support, and staff operations.

> **This is real commercial product work, not a tutorial.** This portfolio contains selected, authentic source code from the application. The customer-facing production system and its confidential history remain private.

## 30-second overview

| | |
| --- | --- |
| **Business problem** | Hotel teams handle repetitive guest questions and service requests across multiple departments. |
| **Product shipped** | Multilingual WhatsApp guest assistant, operational AI workflows, and a React-based staff interface. |
| **My ownership** | Product design, AI orchestration, backend/services, integrations, dashboard, deployment, and customer delivery. |
| **Commercial context** | Built and deployed in **paid client engagements**. Customer and payment records are intentionally not public. |
| **Engineering stack** | **Go, PostgreSQL, React, TypeScript, WhatsApp Cloud API, LLM APIs, Docker, Stripe/Razorpay integration code**. |

## Start with the actual engineering

These are **genuine source files**, not rewritten examples.

| System | Open the code | What to look for |
| --- | --- | --- |
| AI orchestration | [Multi-intent orchestrator](src/backend/internal/ai/orchestrator.go) | Recent message context, structured intent classification, retries for malformed model output |
| End-to-end conversation processing | [AI chat service](src/backend/internal/service/ai_chat_service.go) | Guest identification, incoming message handling, rate limiting, contextual workflows |
| Department agents | [Department agent](src/backend/internal/ai/agent_department.go) | Per-department prompts, tool calling, graceful handling of tool failures |
| Business actions | [Food ordering](src/backend/internal/ai/tools_food_order.go) · [Department tools](src/backend/internal/ai/tools_department.go) | Turning a conversation into application operations |
| WhatsApp integration | [Webhook handler](src/backend/internal/whatsapp/webhook_handler.go) · [HMAC verification](src/backend/internal/whatsapp/signature.go) | Third-party messaging integration and request verification |
| Commercial SaaS logic | [Subscriptions](src/backend/internal/service/subscription_service.go) · [Stripe service](src/backend/internal/service/stripe_service.go) | Billing lifecycle and third-party payment API integration |
| Hotel operations UI | [Conversation UI](src/frontend/ChatWindow.tsx) · [Analytics card](src/frontend/AnalyticsCard.tsx) | Real React/TypeScript staff-facing components |

[**Explore the implementation and design decisions →**](ENGINEERING_NOTES.md)

## How the product fits together

~~~mermaid
flowchart TD
    G["Hotel guest on WhatsApp"] --> W["WhatsApp Business API / webhook"]
    W --> B["Go application API"]
    B --> O["LLM intent orchestration"]
    O --> A["Department-scoped agents and tools"]
    A --> S["Hotel service workflows"]
    B --> D[("PostgreSQL")]
    S --> D
    D --> U["React / TypeScript staff dashboard"]
    B -. "Configured billing integrations" .-> P["Stripe / Razorpay"]
~~~

The core engineering challenge wasn't answering a guest with an LLM; it was translating ambiguous conversations into **reliable, context-aware operational actions** while supporting real hotel staff.

## What I would walk through in an interview

1. **From chat to action:** How the orchestrator classifies multiple intents, passes context, and routes operations.
2. **Reliability:** How the message pipeline handles guest lookup, errors, tool execution, and repeat requests.
3. **Production integration:** Webhook verification, the WhatsApp API, PostgreSQL-backed application state, and SaaS subscription flows.
4. **Product delivery:** Translating hotel requirements into deployed features and iterating with client feedback.

## Commercial proof and project scope

The project was delivered as software for paying customers. This code establishes **technical implementation**, not independent proof of invoices or revenue. Commercial details can be discussed separately, with client privacy protected.

**Portfolio scope:** The repository uses a fresh Git history and includes a **curated subset** of real source modules. It deliberately excludes customer data, production settings, keys, logs, uploads, and private deployment infrastructure. Because modules depend on components outside this selection, this repository is **not a standalone runnable distribution** of the entire platform.

**Built by:** Srijan Choudhary • SrijanAI Innovations  
**Source rights:** All rights reserved; provided for technical portfolio review, not as an open-source license.
