package meta

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	secret := "test-secret"

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifySignature(body, signature, secret) {
		t.Fatal("expected valid signature")
	}
	if VerifySignature(body, "sha256=bad", secret) {
		t.Fatal("expected invalid signature")
	}
	if !VerifySignature(body, "", "") {
		t.Fatal("empty app secret should skip verification")
	}
}
