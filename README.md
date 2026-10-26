# StayBuddy — AI Hotel Concierge for WhatsApp

**Commercially deployed AI product • Independently engineered • Real hospitality workflows**

StayBuddy is an AI-enabled guest service platform built for hotels. Guests interact through WhatsApp; staff manage requests, check-ins, orders, and operational activity through a web dashboard. I designed and built the product as the founder/engineer behind SrijanAI Innovations, including paid customer deployments.

> **Portfolio edition.** This repository contains a curated, sanitized selection of real application source code, not a simulated project or a turnkey production distribution. Private infrastructure, secrets, customer data, billing account configuration, and Git history from the production repository are intentionally excluded. The original production repository remains private.

## Why this is different from a demo

- Built for **paying customer engagements** and real hospitality operations, rather than a tutorial or hackathon.
- Integrates **LLM-powered WhatsApp conversations** with hotel-specific operations, department routing, and staff workflows.
- Uses a **Go backend**, **PostgreSQL**, and **React/TypeScript** dashboard with web APIs and operational data models.
- Includes production-oriented infrastructure for authentication, auditing, deployments, and billing integrations.
- Designed and implemented end-to-end: product requirements, code, third-party integrations, and client deployment.

## Product capabilities

| User | Capability |
| --- | --- |
| Hotel guest | Multilingual WhatsApp assistant and contextual answers |
| Hotel guest | Service requests, room-related queries, and food-order workflows |
| Operations team | Department routing, conversations, check-in management, and analytics |
| Hotel management | Property setup, staff-facing dashboard, operational visibility |
| Business | Subscription/billing integrations and multi-property foundations |

## System architecture

```text
Guest on WhatsApp
       |
       v
WhatsApp Cloud API / webhook
       |
       v
Go API service (validation, routing, application state)
       |
       +--> AI orchestration: context, intent classification, tool calls
       |       |
       |       +--> departmental workflows / service request handling
       |
       +--> PostgreSQL (properties, guests, stays, requests, audit data)
       |
       +--> React + TypeScript staff dashboard
       |
       +--> Payments / messaging / other configured integrations
```

## Engineering evidence

The included code is from actual product development, not rewritten pseudocode. Sections in this portfolio demonstrate:

1. **Applied AI:** LLM interfaces, multilingual guest communication, multi-intent routing, guardrails, and agent tools.
2. **Backend engineering:** Go services, API design, webhook handling, state management, and data models.
3. **Customer-facing UX:** React/TypeScript components and dashboard workflows.

See [ENGINEERING_NOTES.md](ENGINEERING_NOTES.md) for implementation details, design decisions, and limitations of this curated edition.

## Technology

Go • Chi • PostgreSQL • React • TypeScript • Vite • OpenAI API • WhatsApp Business Cloud API • Docker • Stripe / Razorpay integration layers

## Commercial delivery

**Status:** Built and deployed for paying client engagements. Specific customer names, invoices, live endpoints, and commercially sensitive operational details are not included in this public-facing portfolio. The commercial context is provided by the founder and can be discussed or substantiated privately when appropriate.

## What is intentionally omitted?

No production secrets, API keys, user accounts, customer records, private video/uploads, logs, environment files, or historical commits from the production repository. Some source modules depend on excluded application infrastructure; **this curated portfolio is not advertised as an immediately runnable production deployment**.

**Author:** Srijan Choudhary — product founder and engineer.
