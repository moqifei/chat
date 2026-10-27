# Secure AD Login and Global Logout Design

## Goal

Prevent the current desktop client from submitting an AD password as plaintext over the login request while preserving the existing login endpoint for old client versions. Add a server-confirmed logout flow that invalidates every chat and IM session for the logged-in user.

## Scope

This design applies only to the desktop client's AD login endpoint, `POST /account/login/ad`.

It does not change the protocol for the generic login endpoint, registration, password reset, or password change. It does not replace TLS. HTTPS and WSS remain the required long-term transport protection for passwords, tokens, API traffic, and WebSocket traffic.

## Current State

The desktop client sends an AD username and password directly to `POST /account/login/ad` over an HTTP base URL. The API forwards the password to the chat RPC service, which authenticates against AD and returns a chat token and IM token.

Client logout currently logs out of the IM SDK and clears local state. It does not revoke the chat token held in the chat service's Redis token cache. A copied chat token therefore remains usable until normal expiration. The IM service has token invalidation and forced-offline behavior, but it must be included in the explicit logout transaction.

## Constraints

- The deployment may not yet have a trusted certificate or TLS ingress.
- Old desktop versions cannot be upgraded immediately and must continue to call `POST /account/login/ad` with `username` and `password`.
- New desktop versions must not send a plaintext password in the request body.
- A manually initiated logout invalidates the user's sessions on every device and platform.
- Passwords, private keys, decrypted payloads, AES keys, full chat tokens, and full IM tokens must never be logged.

## Options Considered

### Static password-field encryption

The client encrypts only the password field with a server public key.

This prevents a passive packet capture from displaying the password directly, but an attacker can replay the same ciphertext and log in. It is rejected because it does not meet the replay-resistance requirement.

### RSA-OAEP challenge envelope encryption

The client requests a short-lived, one-time challenge. It encrypts a random AES key with an embedded server RSA public key and encrypts the full login payload with AES-GCM. The encrypted payload is bound to the one-time challenge. The server atomically consumes the challenge before authenticating.

This prevents plaintext-password exposure and replay of an intercepted login request while retaining backward compatibility at the same endpoint. This is the selected temporary design.

### HTTPS/WSS transport protection

TLS protects the password, tokens, subsequent HTTP APIs, and WebSocket traffic and authenticates the server. It is the required long-term solution but cannot be assumed available for the current rollout.

## Selected Login Protocol

### Key management

The desktop client ships with a public-key ring that maps a `keyID` to an RSA 3072-bit public key. Public keys are compiled into the signed desktop application and are not fetched over HTTP. Fetching a public key over the untrusted transport would allow an active attacker to replace it.

The chat API receives the matching private keys through a mounted secret file or a key-management service. Private keys must not be stored in Git, application YAML, Docker images, or logs. These keys are separate from existing JWT and OpenIM shared secrets.

The server supports a current key and a small number of previous keys during a release transition. A `keyID` identifies the selected key. A key may only be removed after all supported encrypted-login client versions no longer rely on it.

### Challenge endpoint

The API adds an unauthenticated endpoint:

```text
GET /account/login/ad/challenge
```

The response contains:

```json
{
  "challengeID": "opaque-random-id",
  "challengeNonce": "base64-random-32-bytes",
  "keyID": "ad-login-rsa-2026-01",
  "expiresAt": 1760000000
}
```

The server stores the `challengeID`, nonce, selected key ID, creation time, and expiry in Redis with a 60-second TTL. It also rate-limits challenge creation by client IP using the project's existing login-protection policy.

`challengeID` and `keyID` are identifiers rather than secrets. The nonce is included inside the encrypted payload so an observer cannot construct a valid login request from challenge metadata alone.

### Encrypted login request

New clients continue calling the existing endpoint:

```text
POST /account/login/ad
```

They send the following request body and omit top-level `username` and `password`:

```json
{
  "credential": {
    "version": "v1",
    "keyID": "ad-login-rsa-2026-01",
    "challengeID": "opaque-random-id",
    "encryptedKey": "base64(RSA-OAEP-SHA256(aesKey))",
    "nonce": "base64(aes-gcm-nonce)",
    "ciphertext": "base64(aes-gcm-ciphertext-and-tag)"
  }
}
```

The client generates a fresh 256-bit AES key and 96-bit AES-GCM nonce for every request. It encrypts this payload:

```json
{
  "username": "alice",
  "password": "password",
  "platform": 5,
  "deviceID": "device-uuid",
  "challengeID": "opaque-random-id",
  "challengeNonce": "base64-random-32-bytes"
}
```

The AES-GCM additional authenticated data is a canonical UTF-8 concatenation of `version`, `keyID`, and `challengeID`. This prevents ciphertext substitution between challenges or keys.

RSA-OAEP with SHA-256 encrypts only the AES key. AES-GCM encrypts the credentials and associated login metadata, avoiding RSA plaintext-size limits and providing payload integrity.

### Server verification order

The `POST /account/login/ad` handler detects the request form:

- When `credential` exists, it uses encrypted-login v1.
- When `credential` is absent and legacy `username` and `password` exist, it uses the current legacy path.
- Any mixed request, malformed encrypted request, unsupported version, unknown key, or missing required legacy field is rejected.

For encrypted-login v1, the server:

1. Validates the envelope encoding, version, and key ID.
2. Atomically reads and deletes the Redis challenge record. A Redis `GETDEL` operation or Lua script is required so two concurrent requests cannot consume the same challenge.
3. Rejects a missing, expired, already consumed, or key-mismatched challenge.
4. RSA-OAEP decrypts `encryptedKey` with the private key identified by `keyID`.
5. AES-GCM decrypts `ciphertext` using the request nonce and specified authenticated data.
6. Validates that the inner challenge ID and nonce exactly match the consumed Redis record.
7. Uses the inner username, password, platform, and device ID to invoke the existing AD authentication path.
8. Returns the existing `chatToken`, `imToken`, and `userID` response shape.

All failures in steps 2 through 6 return the existing generic login-failure response without disclosing whether a challenge, private key, ciphertext, or nonce was invalid.

### Client implementation

The Electron renderer uses the Chromium Web Crypto API. No new browser encryption library is required.

The client selects the public key from its embedded key ring using the challenge response's `keyID`. If the key ID is unknown, it stops before collecting a password submission and reports that the client must be upgraded. It must not fall back to plaintext when an encrypted-login capable client receives a challenge or encryption error.

## Legacy Compatibility

The existing endpoint URL and response schema remain unchanged. Old clients continue to submit:

```json
{
  "username": "alice",
  "password": "password",
  "platform": 5,
  "deviceID": "device-uuid"
}
```

The server keeps this branch only for compatibility. It records a metric or audit event with the protocol classification `legacy-plaintext` without recording the password. New encrypted clients record `encrypted-v1`.

The release process monitors legacy usage. A later, separately approved migration can restrict the legacy branch by application version, tenant, account group, or a configuration cutoff. Until that restriction is activated, old clients retain their existing HTTP transport risk.

## Global Logout

### Endpoint and identity

The API adds:

```text
POST /account/logout
token: <current-chat-token>
```

The endpoint is protected by the existing chat-token middleware. The server derives the user ID only from that validated token and ignores any user ID supplied by the request body.

### Revocation sequence

The logout operation is idempotent for a valid authenticated caller and returns success if the user's sessions have already been revoked.

1. Mark all IM tokens for the user, across every platform, as `KickedToken` in the IM auth store.
2. Notify every Message Gateway to disconnect the user's active WebSocket connections for every platform.
3. Delete all chat tokens for the user from the chat Redis token cache.
4. Return success only after the token-state writes in steps 1 and 3 succeed.

The token-state writes form the authorization boundary. Gateway notifications clear live connections but cannot be the only mechanism because a notification may be delayed or a gateway may be temporarily unavailable. An IM token marked kicked must fail any future HTTP or WebSocket token verification before the disconnect notification succeeds.

If a token-state write fails, the server returns an error and records an audit event containing the user ID, stage, and correlation ID. It does not report logout success. Operations should be retried safely because token revocation is idempotent.

### Client behavior

On a manual logout, the desktop client first calls `POST /account/logout`. When the server confirms revocation, it calls the IM SDK logout method, clears its persisted chat profile and IM profile, clears in-memory contact and conversation state, marks the manual-logout flag, and navigates to the login screen.

If the logout request fails before confirmation, the client keeps its local session data and presents a retryable error. If the server completed revocation but the response was lost, a retry returns success because the operation is idempotent.

This is intentionally a global logout: a user logging out from one desktop client forces every device and platform for that user to authenticate again.

## Logging and Audit

Login and logout audit records include a correlation ID, user ID after successful authentication, client IP, device ID, platform, protocol classification, key ID, success or failure category, and timestamp.

The following values must be excluded from all logs, errors, metrics labels, traces, and audit payloads: password, decrypted credential payload, AES key, RSA private key, full ciphertext, full chat token, full IM token, and challenge nonce.

## Verification Plan

### Encrypted login tests

- A v1 envelope generated with a known public key authenticates a valid AD user.
- The password does not appear in the serialized client request body.
- Reusing the same envelope fails after its first attempt.
- A challenge older than 60 seconds fails.
- Changing ciphertext, AES-GCM nonce, key ID, challenge ID, authenticated data, or the inner challenge nonce fails.
- Unknown key ID and malformed Base64 data fail without disclosing internal details.
- The existing plaintext legacy request still succeeds unchanged.
- A mixed legacy and encrypted request fails.

### Global logout tests

- A valid chat token can access a protected chat endpoint before logout.
- After logout, every pre-existing chat token for the user fails protected chat API authorization.
- After logout, every pre-existing IM token for the user and every platform fails IM token parsing and new WebSocket authorization.
- Active WebSocket connections receive an offline event or are closed.
- A repeated logout request after revocation succeeds without recreating token state.
- A simulated IM token-store failure or chat token-store failure returns an error and produces an audit record.

### Manual environment verification

1. Sign in from two desktop clients or platforms with the same account.
2. Copy both chat tokens and IM tokens before logout.
3. Log out from one client.
4. Use Postman to call a chat protected endpoint with each copied chat token and confirm rejection.
5. Attempt IM API requests and WebSocket connections with each copied IM token and confirm rejection.
6. Confirm both clients are returned to login and must authenticate again.

## Security Boundary and Follow-up

This temporary protocol prevents new desktop clients from sending a plaintext AD password and prevents replay of a captured encrypted login envelope. It does not protect the unencrypted login response, chat token, IM token, later HTTP API requests, or WebSocket payloads on an HTTP deployment.

The follow-up production security project is TLS termination with a trusted certificate, HTTPS for all HTTP APIs, WSS for WebSocket traffic, blocked direct access to internal service ports, and mandatory HTTPS/WSS in production client configuration. The desktop client's remembered AD password should also move from browser local storage to the operating system credential store.
