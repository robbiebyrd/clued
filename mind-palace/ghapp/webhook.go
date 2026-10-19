package ghapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Webhook request headers.
const (
	// SignatureHeader carries "sha256=" and the hex HMAC-SHA256 of the raw
	// body keyed with the webhook secret.
	SignatureHeader = "X-Hub-Signature-256"
	// EventHeader names the event (push, pull_request, …).
	EventHeader = "X-GitHub-Event"
	// DeliveryHeader is the delivery's unique id (kept for dedupe).
	DeliveryHeader = "X-GitHub-Delivery"
)

// MaxWebhookBody is GitHub's maximum delivery size (25 MiB); a receiver caps
// the body it reads at this.
const MaxWebhookBody = 25 << 20

const signaturePrefix = "sha256="

// Verify errors. They say what was wrong without echoing the header.
var (
	ErrNoSecret         = errors.New("ghapp: webhook secret is not set")
	ErrMissingSignature = errors.New("ghapp: missing " + SignatureHeader + " header")
	ErrMalformedSig     = errors.New("ghapp: malformed " + SignatureHeader + " header")
	ErrBadSignature     = errors.New("ghapp: webhook signature does not match")
)

// Verify checks a delivery: header is the X-Hub-Signature-256 value
// ("sha256=<64 hex>"), body the raw request body exactly as received. It
// returns nil only when the HMAC-SHA256 of body under secret matches,
// compared with hmac.Equal; an empty secret verifies nothing.
func Verify(secret, body []byte, header string) error {
	if len(secret) == 0 {
		return ErrNoSecret
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return ErrMissingSignature
	}
	hexSig, ok := strings.CutPrefix(header, signaturePrefix)
	if !ok || len(hexSig) != sha256.Size*2 {
		return ErrMalformedSig
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return ErrMalformedSig
	}
	if !hmac.Equal(got, mac(secret, body)) {
		return ErrBadSignature
	}
	return nil
}

// Sign returns the X-Hub-Signature-256 value GitHub would send for body
// under secret, for fakes and tests that deliver webhooks.
func Sign(secret, body []byte) string {
	return signaturePrefix + hex.EncodeToString(mac(secret, body))
}

func mac(secret, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return m.Sum(nil)
}
