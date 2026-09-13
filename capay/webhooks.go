package capay

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// WebhooksService manages webhook subscriptions. Corporate Alliance POSTs an
// encrypted notification to your endpoint whenever a payment, deposit or
// customer changes state; decrypt it with DecryptWebhook.
//
// Delivery is at-least-once: a non-2xx response is retried with exponential
// backoff for up to an hour, each attempt allowed 30 seconds. Deduplicate on
// WebhookNotification.MessageID.
type WebhooksService interface {
	// Create subscribes an endpoint to event types.
	Create(ctx context.Context, req WebhookRequest, opts ...RequestOption) (*Webhook, error)

	// List returns subscriptions.
	List(ctx context.Context, opts ListOptions) (*Page[Webhook], error)

	// Update replaces a subscription's endpoint and events.
	Update(ctx context.Context, id string, req WebhookRequest, opts ...RequestOption) (*Webhook, error)

	// Delete removes a subscription, returning its final state.
	Delete(ctx context.Context, id string, opts ...RequestOption) (*Webhook, error)
}

// webhooksService implements WebhooksService.
type webhooksService struct{ c *client }

// EventType is a webhook event class. The specific status is carried inside
// each notification.
type EventType string

const (
	// EventPayment fires on payment status changes.
	EventPayment EventType = "PAYMENT"

	// EventDeposit fires on deposit status changes.
	EventDeposit EventType = "DEPOSIT"

	// EventClient fires on customer status changes.
	EventClient EventType = "CLIENT"
)

// WebhookRequest creates or updates a subscription.
type WebhookRequest struct {
	// Endpoint is the HTTPS URL to POST to.
	Endpoint string `json:"endpoint"`

	// Events are the event types to subscribe to.
	Events []EventType `json:"events"`
}

func (r WebhookRequest) validate() error {
	if r.Endpoint == "" {
		return invalidInput("Endpoint is required")
	}
	if u, err := url.Parse(r.Endpoint); err != nil || u.Scheme == "" || u.Host == "" {
		return invalidInput("Endpoint %q is not an absolute URL", r.Endpoint)
	}
	if len(r.Events) == 0 {
		return invalidInput("at least one event type is required")
	}
	return nil
}

// Webhook is a subscription as the API returns it.
type Webhook struct {
	WebhookRequest

	// ID is the subscription's id, echoed as SubscriptionID on every
	// notification it produces.
	ID string `json:"id"`

	// CreatedTime and UpdatedTime are the record's timestamps.
	CreatedTime Time `json:"createdTime"`
	UpdatedTime Time `json:"updatedTime"`
}

// Create implements WebhooksService.
func (s *webhooksService) Create(ctx context.Context, req WebhookRequest, opts ...RequestOption) (*Webhook, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	var out Webhook
	if err := s.c.mutate(ctx, apiWebhookCreate, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// List implements WebhooksService.
func (s *webhooksService) List(ctx context.Context, opts ListOptions) (*Page[Webhook], error) {
	var out Page[Webhook]
	if err := s.c.do(ctx, requestSpec{api: apiWebhookList, query: newQuery().page(opts).values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update implements WebhooksService.
func (s *webhooksService) Update(ctx context.Context, id string, req WebhookRequest, opts ...RequestOption) (*Webhook, error) {
	if id == "" {
		return nil, invalidInput("webhook id is required")
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	var out Webhook
	if err := s.c.mutate(ctx, apiWebhookUpdate, idParam(id), req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete implements WebhooksService.
func (s *webhooksService) Delete(ctx context.Context, id string, opts ...RequestOption) (*Webhook, error) {
	if id == "" {
		return nil, invalidInput("webhook id is required")
	}
	var out Webhook
	if err := s.c.mutate(ctx, apiWebhookDelete, idParam(id), nil, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Receiving --------------------------------------------------------------

// ErrWebhookDecrypt is returned by DecryptWebhook when the payload cannot be
// decrypted — a wrong secret, a corrupt body, or bad padding. Match with
// errors.Is.
var ErrWebhookDecrypt = errors.New("capay: webhook decryption failed")

// EncryptedWebhook is the body Corporate Alliance POSTs to your endpoint.
//
//	{"iv":"c6407a24ca95deaec313786321ec1e39","encrypted":"…"}
type EncryptedWebhook struct {
	// IV is the 16-byte AES initialisation vector, hex encoded.
	IV string `json:"iv"`

	// Encrypted is the AES-256-CBC ciphertext, hex encoded.
	Encrypted string `json:"encrypted"`
}

// WebhookNotification is a decrypted notification.
type WebhookNotification struct {
	// SubscriptionID is the Webhook.ID that produced this event.
	SubscriptionID string `json:"subscriptionId"`

	// EventType is PAYMENT, DEPOSIT or CLIENT.
	EventType EventType `json:"eventType"`

	// EventStatus is the object's status after the change, e.g. "COMPLETED".
	// It is a plain string here because its domain depends on EventType;
	// compare against PaymentStatus, DepositStatus or CustomerStatus
	// constants as appropriate.
	EventStatus string `json:"eventStatus"`

	// Timestamp is when the event was published.
	Timestamp Time `json:"timestamp"`

	// MessageID uniquely identifies this message. Deliveries may repeat;
	// deduplicate on it.
	MessageID string `json:"messageId"`

	// EventObject is the full object — a payment, deposit or customer
	// depending on EventType — left undecoded. Use the typed accessors.
	EventObject json.RawMessage `json:"eventObject"`
}

// Payment decodes EventObject as a Payment. For PAYMENT events the object
// may be a summary (id, referenceNo, currencyCode, amount, status) rather
// than the full record; fetch by ID for the rest.
func (n WebhookNotification) Payment() (*Payment, error) {
	if n.EventType != EventPayment {
		return nil, fmt.Errorf("capay: notification is a %s event, not PAYMENT", n.EventType)
	}
	var p Payment
	if err := json.Unmarshal(n.EventObject, &p); err != nil {
		return nil, fmt.Errorf("capay: decoding payment event object: %w", err)
	}
	return &p, nil
}

// Deposit decodes EventObject as a Deposit.
func (n WebhookNotification) Deposit() (*Deposit, error) {
	if n.EventType != EventDeposit {
		return nil, fmt.Errorf("capay: notification is a %s event, not DEPOSIT", n.EventType)
	}
	var d Deposit
	if err := json.Unmarshal(n.EventObject, &d); err != nil {
		return nil, fmt.Errorf("capay: decoding deposit event object: %w", err)
	}
	return &d, nil
}

// DecryptWebhook decrypts and decodes a webhook body.
//
// Every webhook body is AES-256-CBC encrypted with your API secret as the key
// (32 bytes, used as raw UTF-8 — not hashed, not hex-decoded), with the IV
// from the payload and PKCS#7 padding. The body is the raw JSON your HTTP
// handler received; secret is the API secret from your dashboard.
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//		body, _ := io.ReadAll(r.Body)
//		n, err := capay.DecryptWebhook(body, secret)
//		if err != nil {
//			http.Error(w, "bad payload", http.StatusBadRequest)
//			return
//		}
//		// deduplicate on n.MessageID, then act
//		w.WriteHeader(http.StatusOK)
//	}
//
// Respond 2xx promptly: anything else is retried for up to an hour.
func DecryptWebhook(body []byte, secret string) (*WebhookNotification, error) {
	var env EncryptedWebhook
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: body is not the {iv, encrypted} envelope: %v", ErrWebhookDecrypt, err)
	}
	plain, err := DecryptWebhookPayload(env, secret)
	if err != nil {
		return nil, err
	}
	var n WebhookNotification
	if err := json.Unmarshal(plain, &n); err != nil {
		return nil, fmt.Errorf("%w: decrypted payload is not JSON (wrong secret?): %v", ErrWebhookDecrypt, err)
	}
	return &n, nil
}

// DecryptWebhookPayload decrypts an envelope to the plaintext JSON without
// decoding it, for callers who want the raw notification.
func DecryptWebhookPayload(env EncryptedWebhook, secret string) ([]byte, error) {
	key := []byte(secret)
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: secret must be exactly 32 bytes for AES-256, got %d", ErrWebhookDecrypt, len(key))
	}
	iv, err := hex.DecodeString(env.IV)
	if err != nil || len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("%w: iv must be %d bytes hex encoded", ErrWebhookDecrypt, aes.BlockSize)
	}
	ciphertext, err := hex.DecodeString(env.Encrypted)
	if err != nil {
		return nil, fmt.Errorf("%w: encrypted is not hex: %v", ErrWebhookDecrypt, err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: ciphertext length %d is not a multiple of the block size", ErrWebhookDecrypt, len(ciphertext))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWebhookDecrypt, err)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)

	plain, err = unpadPKCS7(plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %v (wrong secret?)", ErrWebhookDecrypt, err)
	}
	return plain, nil
}

// unpadPKCS7 strips PKCS#7 padding, checking every pad byte so a wrong key is
// reported as bad padding rather than as garbage JSON.
func unpadPKCS7(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty plaintext")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, errors.New("invalid padding")
	}
	if !bytes.Equal(b[len(b)-n:], bytes.Repeat([]byte{byte(n)}, n)) {
		return nil, errors.New("invalid padding")
	}
	return b[:len(b)-n], nil
}

// EncryptWebhookPayload is the inverse of DecryptWebhookPayload: it produces
// an envelope Corporate Alliance's delivery would, so you can drive your
// handler in tests. The iv must be 16 bytes.
func EncryptWebhookPayload(plain []byte, secret string, iv []byte) (EncryptedWebhook, error) {
	key := []byte(secret)
	if len(key) != 32 {
		return EncryptedWebhook{}, fmt.Errorf("capay: secret must be exactly 32 bytes for AES-256, got %d", len(key))
	}
	if len(iv) != aes.BlockSize {
		return EncryptedWebhook{}, fmt.Errorf("capay: iv must be %d bytes, got %d", aes.BlockSize, len(iv))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return EncryptedWebhook{}, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return EncryptedWebhook{IV: hex.EncodeToString(iv), Encrypted: hex.EncodeToString(out)}, nil
}
