package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifySignature checks the X-Hub-Signature-256 header from Meta.
// signature is the full header value (e.g. "sha256=abc123...").
// body is the raw request body.
// appSecret is the WhatsApp app secret.
func VerifySignature(signature string, body []byte, appSecret string) bool {
	if appSecret == "" || signature == "" {
		return false
	}

	// Remove "sha256=" prefix
	parts := strings.SplitN(signature, "=", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return false
	}

	expectedSig, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	computedSig := mac.Sum(nil)

	return hmac.Equal(computedSig, expectedSig)
}
