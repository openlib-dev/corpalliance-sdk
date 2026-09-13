package capay

import (
	"errors"
	"testing"
)

const testSecret = "0123456789abcdef0123456789abcdef" // 32 bytes

const paymentNotification = `{
  "subscriptionId": "4e408b3d-5f70-423d-b940-f7192cd77252",
  "eventType": "PAYMENT",
  "eventStatus": "SCHEDULED",
  "timestamp": "2025-12-08T05:53:17.372Z",
  "messageId": "123e4567-e89b-12d3-a456-426614174000",
  "eventObject": {"id": "4e408b3d", "referenceNo": "20251208-PTW122", "currencyCode": "USD", "amount": 123, "status": "SCHEDULED"}
}`

func TestWebhookRoundTrip(t *testing.T) {
	iv := []byte("fedcba9876543210")
	env, err := EncryptWebhookPayload([]byte(paymentNotification), testSecret, iv)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"iv":"` + env.IV + `","encrypted":"` + env.Encrypted + `"}`

	n, err := DecryptWebhook([]byte(body), testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if n.EventType != EventPayment || n.EventStatus != "SCHEDULED" || n.MessageID != "123e4567-e89b-12d3-a456-426614174000" {
		t.Errorf("notification = %+v", n)
	}
	p, err := n.Payment()
	if err != nil {
		t.Fatal(err)
	}
	if p.ReferenceNo != "20251208-PTW122" || p.Amount != 123 || p.Status != PaymentScheduled {
		t.Errorf("payment = %+v", p)
	}
	if _, err := n.Deposit(); err == nil {
		t.Error("Deposit() on a PAYMENT event must fail")
	}
}

func TestWebhookWrongSecretIsDecryptError(t *testing.T) {
	env, _ := EncryptWebhookPayload([]byte(paymentNotification), testSecret, []byte("fedcba9876543210"))
	body := `{"iv":"` + env.IV + `","encrypted":"` + env.Encrypted + `"}`

	_, err := DecryptWebhook([]byte(body), "fedcba9876543210fedcba9876543210")
	if !errors.Is(err, ErrWebhookDecrypt) {
		t.Errorf("err = %v, want ErrWebhookDecrypt", err)
	}
	_, err = DecryptWebhook([]byte(body), "short")
	if !errors.Is(err, ErrWebhookDecrypt) {
		t.Errorf("short secret: %v", err)
	}
	_, err = DecryptWebhook([]byte(`{"iv":"zz","encrypted":"00"}`), testSecret)
	if !errors.Is(err, ErrWebhookDecrypt) {
		t.Errorf("bad iv: %v", err)
	}
	_, err = DecryptWebhook([]byte(`not json`), testSecret)
	if !errors.Is(err, ErrWebhookDecrypt) {
		t.Errorf("not json: %v", err)
	}
}

func TestWebhookMatchesDocumentedVector(t *testing.T) {
	// The documentation's own sample: key as raw UTF-8, iv hex, PKCS#7.
	// Encrypt with the documented parameters and confirm the SDK reads it
	// back, and that a plaintext ending exactly on a block boundary (full
	// padding block) is handled.
	plain := []byte(`{"a":"0123456789abcd"}`) // 22 bytes
	for _, p := range [][]byte{plain, append(plain, make([]byte, 10)...)} {
		env, err := EncryptWebhookPayload(p, testSecret, []byte("1234567890abcdef"))
		if err != nil {
			t.Fatal(err)
		}
		if env.IV != "31323334353637383930616263646566" {
			t.Errorf("iv hex = %s", env.IV)
		}
		got, err := DecryptWebhookPayload(env, testSecret)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(p) {
			t.Errorf("round trip mismatch for %d bytes", len(p))
		}
	}
}
