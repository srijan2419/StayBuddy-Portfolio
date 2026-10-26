# Engineering Notes — StayBuddy

This is a **curated selection of real production-project source files**, not pseudocode or a full runnable distribution. The original repository and its old Git history remain private.

## Source walkthrough

| Focus | Code | What it shows |
| --- | --- | --- |
| Multi-intent classification | [orchestrator.go](src/backend/internal/ai/orchestrator.go) | Guest message context, LLM orchestration, typed parsing, malformed-output retries |
| Department agent | [agent_department.go](src/backend/internal/ai/agent_department.go) | Scoped context, function/tool calling, fallbacks |
| Guardrails and contracts | [guardrails.go](src/backend/internal/ai/guardrails.go), [types.go](src/backend/internal/ai/types.go) | Safety constraints and typed model/application interfaces |
| Operational actions | [Food ordering](src/backend/internal/ai/tools_food_order.go), [department requests](src/backend/internal/ai/tools_department.go), [shared tools](src/backend/internal/ai/tools_common.go) | AI-to-business workflow integration |
| Webhook verification | [signature.go](src/backend/internal/whatsapp/signature.go) | WhatsApp signature handling |
| Reliability | [retryUtility.go](src/backend/internal/utils/retry/retryUtility.go) | Retries for operations |
| Frontend | [AnalyticsCard.tsx](src/frontend/AnalyticsCard.tsx), [ChatWindow.tsx](src/frontend/ChatWindow.tsx) | Actual React/TypeScript UI modules |

## Engineering narrative

StayBuddy combines LLM-based conversational understanding with hotel-specific application data and tools. Messages can be classified into operational intents and routed into departmental actions; staff interact with a separate dashboard. This architecture addresses service handoffs rather than providing a disconnected chatbot.

**Commercial context:** The author reports paid customer deployments. The code is inspectable engineering evidence; invoices, authorized testimonials or a private product walkthrough are separate evidence of payment.

## Publication scope

Production environment files, API tokens, guest data, media uploads, logs, billing credentials, and historical production commits were **not imported**. Some code depends on production modules intentionally excluded. This sample is **not a standalone build or customer deployment**.

Basic text screening for obvious secrets has been performed. Before any public release, perform an independent secrets scan, manually review every included file and any media, and get owner approval.

All rights reserved unless the owner grants a license.
