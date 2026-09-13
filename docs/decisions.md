# Design decisions

Judgement calls made while building this SDK, and the evidence behind them.
Where the published documentation and the live sandbox disagree, both are
quoted.

All live evidence was gathered against `https://api.sandbox.capay.com.au`
on 2026-09-12 with a real sandbox credential.

---

## 1. The error body is decoded as the NestJS shape it actually is

The documentation says only "read the response's error description". The live
service has a fixed envelope:

```
POST /authenticate  (short apiKey)
→ 400 {"statusCode":400,"timestamp":"…","path":"/authenticate",
       "message":["apiKey must be longer than or equal to 64 characters"],"error":"Bad Request"}

GET /accounts/balances  (no token)
→ 401 {"statusCode":401,"timestamp":"…","path":"/accounts/balances",
       "message":"Missing or invalid authorization header"}

GET /payments/does-not-exist
→ 404 {"statusCode":404,"timestamp":"…","path":"/payments/does-not-exist","message":"Invalid id"}
```

`message` is a **string on most failures and an array on validation
failures**, one entry per broken rule. `APIError` decodes it through
`json.RawMessage` and exposes both `Message` (the first) and `Messages` (all
of them). A body that is not this envelope — an HTML page from a proxy — still
yields a usable error with the raw `Body`.

## 2. Bad credentials are a 400, not a 401

A wrong `apiKey` that happens to be shorter than 64 characters is rejected by
input validation before the credential is checked, so it arrives as HTTP 400
with the message above. Status alone is therefore a poor signal on the
authenticate path; the SDK preserves the message rather than inventing
sub-errors.

## 3. Token lifetime: `expiresInSeconds` first, JWT `exp` second

The authenticate response — HTTP **201**, not the 200 the guide's prose
shows — carries both a token and its lifetime:

```json
{"accessToken":"eyJhbGciOiJIUzI1NiIs…","expiresInSeconds":3600}
```

The token is an HS256 JWT whose payload also states `exp`, and `exp - iat` is
3600. The SDK uses `expiresInSeconds`, falls back to `exp`, and only then to
the configured lifetime. The signature is not verified: this SDK is the
token's bearer, not its audience.

**Side benefit:** the payload's `companyId` claim is the caller's own
`clientId` — it matches the `clientId` on every balance, virtual account and
deposit the account owns — so `Client.ClientID` costs no extra request.

## 4. Enumerations are open string types

The deposits guide types `type` as `WIRE TRANSFER | PAYID`. The first deposit
the sandbox returned:

```json
{"type":"MANUAL DEPOSIT","referenceNo":"20260911-F9RZIV","status":"COMPLETED",…}
```

A closed Go enum would either fail the decode or lie. Every status-like field
is a named string type with constants for the documented values; an unknown
value decodes cleanly and compares unequal to all of them.

## 5. `Time` decodes what the API sends, not what it documents

Timestamps are documented as ISO 8601 and mostly are
(`"2026-09-11T05:59:48.000Z"`). The ledger's `transactionTime` is not:

```json
{"transactionType":"DEPOSIT",…,"transactionTime":"2026-09-11 05:59:48"}
```

A `time.Time` field would fail the whole page over that one value. `Time`
tries each observed layout, treats zone-less values as UTC (the zone every
other timestamp from the same service carries), and keeps the raw string so
an unparseable value is still reachable rather than silently zeroed.

`Date` is separate: the YYYY-MM-DD fields (`paymentDate`, `depositDate`,
`dateOfBirth`) are calendar dates with no zone, and a zero `Date` marshals to
`null` / is omitted so an unset optional date is never sent as `0001-01-01`.

## 6. Mutations are never retried, even with an idempotency key

Every mutating endpoint accepts `x-idempotency-key`, and a replay is answered
with 409 — confirmed live: the second `CreateBusiness` with the same key
returned `409 Duplicate idempotency key detected` and no second beneficiary.

That makes a *caller's* retry safe, but not an *automatic* one. If the SDK
resent a timed-out payment and received 409, it could not distinguish "the
first attempt landed" from "the caller reused a key from an earlier, different
request". Rather than guess, `client.do` retries only `GET`; a caller who
wants at-most-once can resend with the same key and treat `ErrConflict` as
done.

## 7. The v2 validators answer 201, and COP OTHERS is not an error

Both `POST /v2/beneficiaries/*/validate` calls are documented as 200 and
answer **201**. The SDK treats any 2xx as success, so this is noted rather
than handled.

For an account with no Confirmation-of-Payee responder — every sandbox BSB —
the result is `{"code":"COP OTHERS","message":"Invalid responder BIC."}` with a
2xx. It is returned as a `PayeeConfirmation`, not an error, because the caller
decides what to do with a non-match.

## 8. Request structs are flat

The OpenAPI schema composes request bodies from shared fragments, and an early
draft mirrored that with embedded structs. Go composite literals cannot set
promoted fields through an embedded type, which would have forced every caller
to build a request in two steps. The shared fields are repeated flat in each
request struct; the unexported `paymentFields` exists only so validation can
be written once.

## 9. `Customers()`, not `Clients()`

The API serves underlying customers at `/clients/business` and
`/clients/personal` and identifies them by `clientId`. The guides call the
same entities *Customers* to distinguish them from *you*, the Client, and
warn that "a Customer's clientId works exactly the same way". The SDK follows
the guides: `Customers()` for the service, `ClientID` for the field, because
`Client` is already the SDK's own type.

## 10. Singular/plural paths are reproduced as-is

```
GET  /beneficiaries/businesses      list
GET  /beneficiaries/business/{id}   get
GET  /beneficiaries/personals       list
GET  /beneficiaries/personals/{id}  get
GET  /clients/businesses            list
GET  /clients/personal              list   ← singular
```

That is the API's layout, confirmed against the sandbox. The catalogue in
`apis.go` carries a note so the next person does not "fix" it.

## 11. `Simulate` refuses to run against production

`POST /deposits/simulate` is sandbox-only by the bank's design. The SDK checks
`Environment` locally and returns `ErrInvalidInput` on `Production` (unless a
`WithBaseURL` override is in place, which is what tests use), so a
misconfigured deployment fails in Go rather than with whatever the production
service would say.

The simulator books asynchronously: a deposit accepted at 06:55:20 appeared in
`GET /deposits` at 06:56:01. The integration test polls for 90 seconds and
logs, rather than fails, if it has not appeared.

## 12. Nulls decode to zero values

`accountId`, `referenceId`, `routingNumber`, `payId`, `currencyCode` (on a
multi-currency virtual account) and `failureReason` all arrive as JSON `null`
in some responses. They are plain `string` fields; `encoding/json` leaves a
string untouched on `null`, so they decode to `""`. Pointer fields were
considered and rejected — the distinction between "absent" and "empty" is not
meaningful for any of these.

## 13. Webhook decryption checks padding byte by byte

The documented Go sample strips PKCS#7 padding by reading the last byte only.
With the wrong key, CBC decryption produces random bytes whose last value is
in 1–16 about 6% of the time, in which case the sample would "succeed" and
hand back garbage. `DecryptWebhook` verifies every pad byte so a wrong secret
is reported as `ErrWebhookDecrypt` rather than as a JSON parse failure three
layers up.

`EncryptWebhookPayload` is exported so callers can drive their own handler in
tests without a live delivery.
