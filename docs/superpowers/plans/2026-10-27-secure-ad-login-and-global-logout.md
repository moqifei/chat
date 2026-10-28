# Secure AD Login and Global Logout Implementation Plan

> **Superseded for AD login on 2026-10-28:** The implemented protocol uses the same configured AES-256-GCM key in the Chat API and desktop client, with the existing one-time challenge retained to prevent replay. RSA key files, public-key injection, and Kubernetes Secret mounting steps in this historical plan do not apply to the current implementation.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Encrypt new desktop AD-login credentials with replay protection while preserving legacy clients, and revoke every chat and IM session when a user logs out.

**Architecture:** Chat API issues Redis-backed one-time challenges and decrypts AES-GCM credential payloads whose fresh AES keys are wrapped by RSA-OAEP. The existing AD-login endpoint recognizes encrypted-v1 and legacy plaintext bodies. Global logout revokes IM token state before gateway disconnects and deletes all chat tokens for the authenticated user.

**Tech Stack:** Go 1.23.2, Gin, go-redis/v9, Go standard crypto packages, Chromium Web Crypto API, TypeScript, React Query, Playwright, OpenIM Auth RPC.

## Global Constraints

- Change only `POST /account/login/ad`; generic login, registration, password reset, and password change remain unchanged.
- Preserve the existing endpoint and `{ chatToken, imToken, userID }` response for legacy clients.
- New clients cannot submit a top-level password or fall back to plaintext after an encryption failure.
- Use RSA 3072-bit OAEP-SHA256 only for a fresh AES-256-GCM key; bind `version`, `keyID`, and `challengeID` as GCM AAD.
- Store each Redis challenge for 60 seconds and atomically consume it before AD authentication.
- Keep the server private key in a read-only mounted Secret, outside Git, YAML values, images, logs, errors, metrics, and traces.
- Keep legacy plaintext login available until a separately approved retirement policy.
- Manual logout revokes every chat and IM session for the user on all platforms.
- Never log passwords, decrypted payloads, encryption keys, nonces, ciphertexts, or complete tokens.
- This temporary protocol does not replace the future HTTPS/WSS transport project.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `chat/pkg/common/config/config.go` | Encrypted AD-login API configuration. |
| `chat/config/chat-api-chat.yml` | Key ID, non-secret key mount path, challenge TTL. |
| `chat/internal/api/chat/ad_login_crypto.go` | Challenge store, envelope models, private key loader, decrypt-and-consume service. |
| `chat/internal/api/chat/ad_login_crypto_test.go` | Crypto, tampering, expiry, replay, and startup validation tests. |
| `chat/internal/api/chat/start.go` | Redis/key startup dependencies and routes. |
| `chat/internal/api/chat/chat.go` | Challenge, dual-format AD login, and global logout handlers. |
| `chat/internal/api/chat/auth_session_test.go` | Handler and session-revocation tests. |
| `chat/pkg/common/imapi/caller.go` | IM forced-logout error propagation. |
| `open-im-server/internal/rpc/auth/auth.go` | Token-first IM logout behavior. |
| `open-im-server/internal/rpc/auth/auth_test.go` | IM token ordering test. |
| `openim-electron-demo/src/config/adLoginPublicKeys.ts` | Versioned desktop public-key ring. |
| `openim-electron-demo/src/utils/adLoginCrypto.ts` | Web Crypto envelope creation. |
| `openim-electron-demo/src/api/login.ts` | Challenge, encrypted login, and logout requests. |
| `openim-electron-demo/src/store/user.ts` | Server revocation before local cleanup. |
| `openim-electron-demo/e2e/loginSecurity.spec.ts` | Desktop source-level protocol checks. |

## Task 1: Build the Server Encryption Boundary

**Files:**

- Create: `chat/internal/api/chat/ad_login_crypto.go`
- Create: `chat/internal/api/chat/ad_login_crypto_test.go`
- Modify: `chat/pkg/common/config/config.go`
- Modify: `chat/config/chat-api-chat.yml`

**Interfaces:**

- Consumes: `config.API`, `redis.UniversalClient`, Go cryptography.
- Produces: `ADLoginCrypto`, `ADLoginChallengeStore`, `ADLoginCredentialEnvelope`, `ADLoginPayload`.

- [ ] **Step 1: Write failing cryptography tests**

Create a memory challenge-store fake and generate test RSA keys in setup. Assert valid decrypt, altered ciphertext, altered AAD, unknown key ID, expiry, and reuse of a consumed envelope.

```go
func TestDecryptAndConsumeADLoginCredentialRejectsReplay(t *testing.T) {
    service := newTestADLoginCrypto(t, newMemoryChallengeStore(time.Now))
    challenge, err := service.IssueChallenge(context.Background())
    if err != nil { t.Fatal(err) }
    envelope := encryptTestPayload(t, service.PublicKey(), challenge, ADLoginPayload{
        Username: "alice", Password: "secret", Platform: 5, DeviceID: "device-1",
        ChallengeID: challenge.ID, ChallengeNonce: challenge.Nonce,
    })
    if _, err := service.DecryptAndConsumeADLoginCredential(context.Background(), envelope); err != nil { t.Fatal(err) }
    if _, err := service.DecryptAndConsumeADLoginCredential(context.Background(), envelope); err == nil { t.Fatal("replay succeeded") }
}
```

- [ ] **Step 2: Verify the test fails before implementation**

Run `go test ./internal/api/chat -run 'TestDecryptAndConsumeADLoginCredential' -count=1` from `D:\openim\chat`. Expected: FAIL because the service and models do not exist.

- [ ] **Step 3: Define configuration and public contracts**

Add this to `pkg/common/config/config.go` and make it a field on `config.API`:

```go
type ADLoginCrypto struct {
    KeyID string `mapstructure:"keyID"`
    PrivateKeyFile string `mapstructure:"privateKeyFile"`
    ChallengeTTLSeconds int `mapstructure:"challengeTTLSeconds"`
}
```

Add this non-secret configuration to `chat/config/chat-api-chat.yml`:

```yaml
adLoginCrypto:
  keyID: ad-login-rsa-2026-01
  privateKeyFile: /run/secrets/chat-ad-login-rsa-private.pem
  challengeTTLSeconds: 60
```

Define these types in `ad_login_crypto.go`:

```go
type ADLoginChallengeStore interface {
    Issue(context.Context, ADLoginChallenge, time.Duration) error
    Consume(context.Context, string) (*ADLoginChallenge, error)
}
type ADLoginChallenge struct { ID string; Nonce []byte; KeyID string; ExpiresAt time.Time }
type ADLoginCredentialEnvelope struct {
    Version string `json:"version" binding:"required"`
    KeyID string `json:"keyID" binding:"required"`
    ChallengeID string `json:"challengeID" binding:"required"`
    EncryptedKey string `json:"encryptedKey" binding:"required"`
    Nonce string `json:"nonce" binding:"required"`
    Ciphertext string `json:"ciphertext" binding:"required"`
}
type ADLoginPayload struct {
    Username string `json:"username"`; Password string `json:"password"`
    Platform int32 `json:"platform"`; DeviceID string `json:"deviceID"`
    ChallengeID string `json:"challengeID"`; ChallengeNonce string `json:"challengeNonce"`
}
```

- [ ] **Step 4: Implement minimal crypto and Redis behavior**

Load a PKCS#1 or PKCS#8 PEM RSA key using `os.ReadFile`, `pem.Decode`, and `x509`; reject missing keys and modulus sizes below 3072 bits. Generate challenge IDs and 32-byte nonces with `crypto/rand`. Store records under `CHAT_AD_LOGIN_CHALLENGE:`.

Use Redis `GETDEL` or a Lua get-and-delete script for `Consume`. `DecryptAndConsumeADLoginCredential` must decode the envelope, consume the challenge, match its key ID, OAEP-SHA256 decrypt a 32-byte AES key, enforce a 12-byte GCM nonce, decrypt with AAD `v1\n<keyID>\n<challengeID>`, then compare the inner challenge ID and nonce with `subtle.ConstantTimeCompare`. All client-visible crypto failures return one generic invalid-credential error.

- [ ] **Step 5: Verify the crypto boundary passes**

Run `go test ./internal/api/chat -run 'TestDecryptAndConsumeADLoginCredential|TestADLoginCrypto' -count=1`. Expected: PASS for valid, tampered, expired, and replayed input.

- [ ] **Step 6: Commit Task 1**

Run `git add pkg/common/config/config.go config/chat-api-chat.yml internal/api/chat/ad_login_crypto.go internal/api/chat/ad_login_crypto_test.go`, then `git commit -m "feat: add encrypted AD login crypto boundary"` from `D:\openim\chat`.

## Task 2: Add the Dual-Format AD Login API

**Files:**

- Modify: `chat/internal/api/chat/start.go`
- Modify: `chat/internal/api/chat/chat.go`
- Modify: `chat/pkg/common/apistruct/chat.go`
- Create: `chat/internal/api/chat/auth_session_test.go`

**Interfaces:**

- Consumes: the Task 1 crypto service and existing `chatpb.ChatClient.Login`.
- Produces: `GET /account/login/ad/challenge` and dual-format `POST /account/login/ad`.

- [ ] **Step 1: Write failing handler tests**

Use fake chat RPC, IM caller, and challenge store. Verify challenge response, decrypted encrypted request forwarding, unchanged legacy request forwarding, and rejection of a body that mixes `credential` with plaintext credentials.

```go
func TestADLoginEncryptedPayloadForwardsDecryptedCredentials(t *testing.T) {
    api, rpc := newTestAPI(t)
    request := newEncryptedADLoginRequest(t, api.adLoginCrypto, "alice", "secret")
    api.ADLogin(newGinContext(httptest.NewRecorder(), request))
    if rpc.loginReq.Account != "alice" || rpc.loginReq.Password != "secret" { t.Fatalf("unexpected request: %#v", rpc.loginReq) }
}
func TestADLoginLegacyRequestRemainsCompatible(t *testing.T) {
    api, rpc := newTestAPI(t)
    request := newJSONRequest(http.MethodPost, "/account/login/ad", map[string]any{"username":"legacy", "password":"legacy-password", "platform":5})
    api.ADLogin(newGinContext(httptest.NewRecorder(), request))
    if rpc.loginReq.Password != "legacy-password" { t.Fatal("legacy request rejected") }
}
```

- [ ] **Step 2: Verify handlers are red**

Run `go test ./internal/api/chat -run 'TestADLogin(EncryptedPayloadForwardsDecryptedCredentials|LegacyRequestRemainsCompatible|RejectsMixedRequest|Challenge)' -count=1`. Expected: FAIL because challenge routing and dispatch are absent.

- [ ] **Step 3: Add HTTP types and initialize dependencies**

Add `ADLoginChallengeResp` and `ADLoginRequest` to `pkg/common/apistruct/chat.go`:

```go
type ADLoginChallengeResp struct { ChallengeID string `json:"challengeID"`; ChallengeNonce string `json:"challengeNonce"`; KeyID string `json:"keyID"`; ExpiresAt int64 `json:"expiresAt"` }
type ADLoginRequest struct { Username string `json:"username"`; Password string `json:"password"`; Platform int32 `json:"platform"`; DeviceID string `json:"deviceID"`; Credential *ADLoginCredentialEnvelope `json:"credential"` }
```

In `start.go`, use `redisutil.NewRedisClient(ctx, cfg.Redis.Build())`, construct `ADLoginCrypto`, attach it to `Api`, and fail startup for invalid key configuration. Register `account.GET("/login/ad/challenge", chat.ADLoginChallenge)` before the existing AD-login POST route.

- [ ] **Step 4: Implement request dispatch**

`Api.ADLogin` parses `ADLoginRequest`. If `credential` exists with a top-level username or password, reject it. If `credential` exists, decrypt it and construct `chatpb.LoginReq` only from `ADLoginPayload`; otherwise preserve the current legacy field mapping. Reuse existing IP lookup, AD RPC call, user auto-registration, IM token issue, and `LoginResp` response code after normalization.

- [ ] **Step 5: Verify Task 2**

Run `go test ./internal/api/chat -run 'TestADLogin|TestDecryptAndConsumeADLoginCredential' -count=1`, then run `gofmt -w internal/api/chat/ad_login_crypto.go internal/api/chat/ad_login_crypto_test.go internal/api/chat/auth_session_test.go pkg/common/apistruct/chat.go pkg/common/config/config.go`, then run `git diff --check`. Expected: all tests PASS and no whitespace errors.

- [ ] **Step 6: Commit Task 2**

Run `git add internal/api/chat/start.go internal/api/chat/chat.go pkg/common/apistruct/chat.go internal/api/chat/auth_session_test.go`, then `git commit -m "feat: support encrypted AD login requests"` from `D:\openim\chat`.

## Task 3: Encrypt New Desktop AD Login Requests

**Files:**

- Create: `openim-electron-demo/src/config/adLoginPublicKeys.ts`
- Create: `openim-electron-demo/src/utils/adLoginCrypto.ts`
- Modify: `openim-electron-demo/src/api/login.ts`
- Create: `openim-electron-demo/e2e/loginSecurity.spec.ts`

**Interfaces:**

- Consumes: challenge endpoint, `AD_LOGIN_PUBLIC_KEYS`, and `crypto.subtle`.
- Produces: `createEncryptedADLoginCredential` and encrypted `useADLogin` calls.

- [ ] **Step 1: Write the failing desktop protocol test**

```ts
test("uses a one-time challenge and encrypted credential for AD login", () => {
  const login = read("src/api/login.ts");
  const crypto = read("src/utils/adLoginCrypto.ts");
  expect(login).toContain('"/account/login/ad/challenge"');
  expect(login).toContain("createEncryptedADLoginCredential");
  expect(login).toContain("credential,");
  expect(crypto).toContain('name: "RSA-OAEP"');
  expect(crypto).toContain('name: "AES-GCM"');
  expect(crypto).toContain("additionalData");
});
```

- [ ] **Step 2: Verify the desktop test is red**

Run `npm run e2e -- loginSecurity.spec.ts` from `D:\openim\openim-electron-demo`. Expected: FAIL because no helper or challenge request exists.

- [ ] **Step 3: Implement key lookup and Web Crypto envelope creation**

Export a frozen `AD_LOGIN_PUBLIC_KEYS: Record<string, string>` from `adLoginPublicKeys.ts`. It contains only release-provided public PEM values keyed by `keyID`; private key material is forbidden.

In `adLoginCrypto.ts`, import the selected PEM with RSA-OAEP, generate a fresh AES-GCM key and a 12-byte nonce, encrypt the complete inner payload, use UTF-8 AAD `v1\n${keyID}\n${challengeID}`, export the raw AES key, wrap it with RSA-OAEP, and Base64 encode binary values. Reject an unknown key ID before submission.

```ts
export async function createEncryptedADLoginCredential(challenge: ADLoginChallenge, credentials: ADLoginCredentials): Promise<ADLoginCredential> {
  const publicKeyPem = AD_LOGIN_PUBLIC_KEYS[challenge.keyID];
  if (!publicKeyPem) throw new Error("Unsupported AD login encryption key");
  // Import the RSA public key, encrypt AES-GCM payload, and wrap AES key.
}
```

- [ ] **Step 4: Send the encrypted body**

`useADLogin` fetches the challenge, creates `credential`, then sends this exact new-client body. All credential fields stay inside the ciphertext.

```ts
return getChatAxios().post<LoginResponse>("/account/login/ad", { credential }, { headers: { operationID: uuidv4() } });
```

- [ ] **Step 5: Verify Task 3**

Run `npm run e2e -- loginSecurity.spec.ts`, `npx tsc --noEmit`, and `npm run build`. Expected: protocol test PASS, TypeScript clean, and Vite exits 0.

- [ ] **Step 6: Commit Task 3**

Run `git add src/config/adLoginPublicKeys.ts src/utils/adLoginCrypto.ts src/api/login.ts e2e/loginSecurity.spec.ts`, then `git commit -m "feat: encrypt desktop AD login credentials"` from `D:\openim\openim-electron-demo`.

## Task 4: Make IM Revocation Token-First

**Files:**

- Modify: `open-im-server/internal/rpc/auth/auth.go`
- Create: `open-im-server/internal/rpc/auth/auth_test.go`
- Modify: `chat/pkg/common/imapi/caller.go`

**Interfaces:**

- Consumes: existing `auth.ForceLogoutReq`, token store, and Message Gateway clients.
- Produces: token-first forced logout plus error propagation to chat API.

- [ ] **Step 1: Write failing token-order tests**

Extract token persistence and gateway notification behind small testable methods. Assert token state is written before a gateway call.

```go
func TestForceKickOffMarksTokensBeforeGatewayDisconnect(t *testing.T) {
    order := []string{}
    server := newAuthServerForTest(tokenStoreFunc(func(){ order = append(order, "tokens") }), gatewayFunc(func(){ order = append(order, "gateway") }))
    if err := server.forceKickOff(context.Background(), "user-1", 5); err != nil { t.Fatal(err) }
    if strings.Join(order, ",") != "tokens,gateway" { t.Fatalf("order=%v", order) }
}
```

- [ ] **Step 2: Verify the IM test is red**

Run `go test ./internal/rpc/auth -run 'TestForceKickOff' -count=1` from `D:\openim\open-im-server`. Expected: FAIL because gateway disconnect happens before token persistence.

- [ ] **Step 3: Reorder and expose failures**

In `open-im-server/internal/rpc/auth/auth.go`, change `forceKickOff` to load the user/platform token map, set each token to `constant.KickedToken`, and persist it before Message Gateway notifications. Return persistence errors. Log gateway notification failures after token revocation and never restore revoked tokens.

In `chat/pkg/common/imapi/caller.go`, collect all platform `/auth/force_logout` RPC errors instead of ignoring them, returning non-nil when IM Auth cannot confirm revocation.

- [ ] **Step 4: Verify Task 4**

Run `go test ./internal/rpc/auth -run 'TestForceKickOff' -count=1` from `open-im-server`, then `go test ./pkg/common/imapi -run 'TestForceOffLine' -count=1` from `chat`. Expected: PASS; storage failure is visible while post-revocation gateway failures remain logged.

- [ ] **Step 5: Commit Task 4**

In `open-im-server`, run `git add internal/rpc/auth/auth.go internal/rpc/auth/auth_test.go`, then `git commit -m "fix: revoke IM tokens before disconnecting gateways"`. In `chat`, run `git add pkg/common/imapi/caller.go`, then `git commit -m "fix: surface IM logout failures to chat API"`.

## Task 5: Add Global Logout and Client Ordering

**Files:**

- Modify: `chat/internal/api/chat/start.go`
- Modify: `chat/internal/api/chat/chat.go`
- Modify: `chat/internal/api/chat/auth_session_test.go`
- Modify: `openim-electron-demo/src/api/login.ts`
- Modify: `openim-electron-demo/src/store/user.ts`
- Modify: `openim-electron-demo/e2e/loginSecurity.spec.ts`

**Interfaces:**

- Consumes: `mw.CheckToken`, `mctx.GetOpUserID`, `imapi.CallerInterface.ForceOffLine`, `admin.AdminClient.InvalidateToken`.
- Produces: `POST /account/logout` and `logoutSession`.

- [ ] **Step 1: Write failing global-logout regression**

```go
func TestLogoutInvalidatesAllChatSessions(t *testing.T) {
    router, admin, im := newLogoutRouter(t, map[string]int32{"device-a": constantpb.NormalToken, "device-b": constantpb.NormalToken})
    postWithToken(t, router, "/user/protected", "device-a", http.StatusOK)
    postWithToken(t, router, "/account/logout", "device-a", http.StatusOK)
    postWithToken(t, router, "/user/protected", "device-a", http.StatusUnauthorized)
    postWithToken(t, router, "/user/protected", "device-b", http.StatusUnauthorized)
    if admin.invalidatedUserID != "user-1" || im.forcedUserID != "user-1" { t.Fatal("global revocation missing") }
}
```

- [ ] **Step 2: Verify global logout is red**

Run `go test ./internal/api/chat -run 'TestLogoutInvalidatesAllChatSessions' -count=1`. Expected: FAIL because `/account/logout` does not exist.

- [ ] **Step 3: Implement authenticated global logout**

Register `account.POST("/logout", mw.CheckToken, chat.Logout)`. `Api.Logout` derives the user only with `mctx.GetOpUserID(c)`, gets the IM admin token, calls all-platform `ForceOffLine`, then calls existing `adminClient.InvalidateToken`. Return success only after both token-state writes succeed. Ignore no client-supplied user ID. Treat a missing chat token map as successful to make a repeated logout idempotent.

Add this desktop helper:

```ts
export const logoutSession = async () => {
  const token = (await getChatToken()) as string;
  return getChatAxios().post("/account/logout", {}, { headers: { token, operationID: uuidv4() } });
};
```

Manual `userLogout` awaits `logoutSession()` before `IMSDK.logout()`, `clearIMProfile()`, store clearing, and navigation. Forced local logout due to an expired token preserves the current no-server-call behavior.

- [ ] **Step 4: Add client logout ordering test**

```ts
test("revokes server session before local cleanup", () => {
  const api = read("src/api/login.ts");
  const user = read("src/store/user.ts");
  expect(api).toContain('"/account/logout"');
  expect(user.indexOf("await logoutSession()")).toBeLessThan(user.indexOf("await IMSDK.logout()"));
  expect(user.indexOf("await logoutSession()")).toBeLessThan(user.indexOf("await clearIMProfile()"));
});
```

- [ ] **Step 5: Verify Task 5**

From `chat`, run `go test ./internal/api/chat -run 'TestADLogin|TestDecryptAndConsumeADLoginCredential|TestLogoutInvalidatesAllChatSessions' -count=1`, run `gofmt` on touched Go files, then run `git diff --check`. From `openim-electron-demo`, run `npm run e2e -- loginSecurity.spec.ts`, `npx tsc --noEmit`, and `npm run build`. Expected: all pass and no whitespace errors.

- [ ] **Step 6: Verify manually with two devices and Postman**

1. Configure the mounted private key and matching desktop public key.
2. Log in as one AD user from two clients or platform IDs.
3. Copy both chat tokens and both IM tokens before logout.
4. Log out from one client.
5. Confirm Postman rejects both chat tokens at `/user/find/full`.
6. Confirm IM API and WebSocket authentication reject both IM tokens.
7. Confirm both clients must authenticate again.

- [ ] **Step 7: Commit Task 5**

In `chat`, run `git add internal/api/chat/start.go internal/api/chat/chat.go internal/api/chat/auth_session_test.go`, then `git commit -m "feat: revoke all user sessions on logout"`. In `openim-electron-demo`, run `git add src/api/login.ts src/store/user.ts e2e/loginSecurity.spec.ts`, then `git commit -m "feat: revoke server session before desktop logout"`.

## Task 6: Mount Private Key and Validate Release Inputs

**Files:**

- Modify: `chat/deployments/deploy/openim-chat-api-deployment.yml`
- Modify: `chat/internal/api/chat/ad_login_crypto_test.go`
- Modify: `openim-electron-demo/src/config/adLoginPublicKeys.ts`

**Interfaces:**

- Consumes: release-generated RSA key pair and deployment Secret.
- Produces: read-only private-key mount and a matching desktop public key.

- [ ] **Step 1: Add failing startup validation tests**

```go
func TestNewADLoginCryptoRejectsInvalidConfiguration(t *testing.T) {
    cases := []config.ADLoginCrypto{{PrivateKeyFile: validKeyFile, ChallengeTTLSeconds: 60}, {KeyID: "key-1", PrivateKeyFile: validKeyFile}}
    for _, cfg := range cases {
        if _, err := NewADLoginCrypto(cfg, newMemoryChallengeStore(time.Now)); err == nil { t.Fatal("invalid configuration initialized") }
    }
}
```

- [ ] **Step 2: Verify startup validation is red**

Run `go test ./internal/api/chat -run 'TestNewADLoginCryptoRejectsInvalidConfiguration' -count=1`. Expected: FAIL until empty key IDs, unreadable/malformed keys, undersized keys, and non-positive TTL are rejected.

- [ ] **Step 3: Add non-secret deployment configuration**

Mount Secret key `chat-ad-login-rsa-private.pem` as `/run/secrets/chat-ad-login-rsa-private.pem` read-only in `openim-chat-api-deployment.yml`. Keep Secret data outside source control.

Generate deployment keys outside the repository with `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out chat-ad-login-rsa-private.pem`, then `openssl rsa -pubout -in chat-ad-login-rsa-private.pem -out chat-ad-login-rsa-public.pem`. Set the same key ID in the server config and desktop key ring. Add only the public PEM to the client.

- [ ] **Step 4: Verify release inputs**

Run `go test ./internal/api/chat -run 'TestNewADLoginCrypto' -count=1` and `rg -n --glob '!docs/**' 'BEGIN (RSA )?PRIVATE KEY|chat-ad-login-rsa-private' D:\openim\chat D:\openim\openim-electron-demo`. Expected: startup tests PASS and scan contains no private PEM data; a deployment filename reference is allowed.

- [ ] **Step 5: Commit Task 6**

In `chat`, run `git add config/chat-api-chat.yml deployments/deploy/openim-chat-api-deployment.yml internal/api/chat/ad_login_crypto_test.go`, then `git commit -m "chore: configure encrypted AD login key mount"`. In `openim-electron-demo`, run `git add src/config/adLoginPublicKeys.ts`, then `git commit -m "chore: add AD login public key"`.

## Plan Self-Review

The plan covers the approved replay-resistant temporary protocol, public-key pinning, service-only key management, 60-second one-time challenges, legacy client compatibility, global token revocation, token-first IM invalidation, desktop cleanup ordering, automated tests, Postman verification, and no-private-key release checks. It does not change non-AD credential flows or claim that the temporary protocol replaces TLS/WSS.
