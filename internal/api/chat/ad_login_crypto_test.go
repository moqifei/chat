package chat

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

type memoryADLoginChallengeStore struct {
	mu sync.Mutex
	m  map[string]ADLoginChallenge
}

func newMemoryADLoginChallengeStore() *memoryADLoginChallengeStore {
	return &memoryADLoginChallengeStore{m: make(map[string]ADLoginChallenge)}
}

func (s *memoryADLoginChallengeStore) Issue(_ context.Context, challenge ADLoginChallenge, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[challenge.ID] = challenge
	return nil
}

func (s *memoryADLoginChallengeStore) Consume(_ context.Context, id string) (*ADLoginChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.m[id]
	if !ok {
		return nil, errInvalidADLoginCredential
	}
	delete(s.m, id)
	return &challenge, nil
}

func newTestADLoginCrypto(t *testing.T) *ADLoginCrypto {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatal(err)
	}
	service, err := newADLoginCrypto(key, "test-key", time.Minute, newMemoryADLoginChallengeStore())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestDecryptAndConsumeADLoginCredentialRejectsReplay(t *testing.T) {
	service := newTestADLoginCrypto(t)
	challenge, err := service.IssueChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	envelope := encryptADLoginPayload(t, service.sharedKey, *challenge, ADLoginPayload{Username: "alice", Password: "secret", Platform: 5, DeviceID: "device-1", ChallengeID: challenge.ID, ChallengeNonce: challenge.Nonce})
	payload, err := service.DecryptAndConsume(context.Background(), envelope)
	if err != nil || payload.Username != "alice" || payload.Password != "secret" {
		t.Fatalf("decrypt payload: %#v, %v", payload, err)
	}
	if _, err := service.DecryptAndConsume(context.Background(), envelope); !errorsIsInvalidCredential(err) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestDecryptAndConsumeADLoginCredentialRejectsTampering(t *testing.T) {
	service := newTestADLoginCrypto(t)
	challenge, err := service.IssueChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	envelope := encryptADLoginPayload(t, service.sharedKey, *challenge, ADLoginPayload{Username: "alice", Password: "secret", ChallengeID: challenge.ID, ChallengeNonce: challenge.Nonce})
	envelope.Ciphertext = envelope.Ciphertext[:len(envelope.Ciphertext)-2] + "AA"
	if _, err := service.DecryptAndConsume(context.Background(), envelope); !errorsIsInvalidCredential(err) {
		t.Fatalf("tampered ciphertext error = %v", err)
	}
}

func TestDecryptAndConsumeADLoginCredentialRejectsAlteredAAD(t *testing.T) {
	service := newTestADLoginCrypto(t)
	challenge, err := service.IssueChallenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	envelope := encryptADLoginPayload(t, service.sharedKey, *challenge, ADLoginPayload{Username: "alice", Password: "secret", ChallengeID: challenge.ID, ChallengeNonce: challenge.Nonce})
	envelope.KeyID = "other-key"
	if _, err := service.DecryptAndConsume(context.Background(), envelope); !errorsIsInvalidCredential(err) {
		t.Fatalf("altered AAD error = %v", err)
	}
}

func TestDecodeADLoginSharedKeyRejectsInvalidKey(t *testing.T) {
	if _, err := decodeADLoginSharedKey("not-a-key"); err == nil {
		t.Fatal("accepted an invalid shared key")
	}
}

func errorsIsInvalidCredential(err error) bool { return err == errInvalidADLoginCredential }

func encryptADLoginPayload(t *testing.T, key []byte, challenge ADLoginChallenge, payload ADLoginPayload) ADLoginCredentialEnvelope {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := gcm.Seal(nil, nonce, plain, []byte("v1\n"+challenge.KeyID+"\n"+challenge.ID))
	return ADLoginCredentialEnvelope{Version: "v1", KeyID: challenge.KeyID, ChallengeID: challenge.ID, Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(ciphertext)}
}
