package ai

import (
	"log"
	"regexp"
	"strconv"
)

// ValidateAgentResponse runs deterministic checks on LLM output.
// Logs warnings only — does not block responses. Call after every agent response.
func ValidateAgentResponse(resp string, guestCtx *GuestContext) {
	if len(resp) < 10 {
		log.Printf("[guardrail] response too short (%d chars)", len(resp))
	}
	if len(resp) > 2000 {
		log.Printf("[guardrail] response too long for WhatsApp (%d chars)", len(resp))
	}

	// Hallucinated prices: extract ₹ values, verify against menu
	if len(guestCtx.MenuItems) > 0 {
		validPrices := buildValidPriceSet(guestCtx.MenuItems)
		for _, mentioned := range extractMentionedPrices(resp) {
			if !validPrices[mentioned] {
				log.Printf("[guardrail] price ₹%d not in menu — possible hallucination", mentioned)
			}
		}
	}
}

// buildValidPriceSet pre-computes all valid prices: single items, line totals (price×qty up to 20),
// and pairwise sums of line totals. O(n) build, O(1) lookup.
func buildValidPriceSet(items []MenuItem) map[int]bool {
	// Step 1: collect all line totals (price × 1..20)
	lineTotals := make(map[int]bool)
	for _, item := range items {
		if item.Price <= 0 {
			continue
		}
		for q := 1; q <= 20; q++ {
			lineTotals[item.Price*q] = true
		}
	}

	// Step 2: valid = all line totals + all pairwise sums (covers 2-item orders)
	valid := make(map[int]bool, len(lineTotals)*2)
	totals := make([]int, 0, len(lineTotals))
	for p := range lineTotals {
		valid[p] = true
		totals = append(totals, p)
	}
	for i, p1 := range totals {
		for j := i; j < len(totals); j++ {
			valid[p1+totals[j]] = true
		}
	}

	return valid
}

// pricePattern matches ₹NNN patterns in text.
var pricePattern = regexp.MustCompile(`₹(\d+)`)

// extractMentionedPrices finds all ₹NNN values in a response string.
func extractMentionedPrices(resp string) []int {
	matches := pricePattern.FindAllStringSubmatch(resp, -1)
	seen := make(map[int]bool)
	var prices []int
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		p, err := strconv.Atoi(m[1])
		if err != nil || p == 0 {
			continue
		}
		if !seen[p] {
			seen[p] = true
			prices = append(prices, p)
		}
	}
	return prices
}
