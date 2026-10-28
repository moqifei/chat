package chat

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/openimsdk/chat/pkg/common/config"
	"github.com/redis/go-redis/v9"
)

const adLoginChallengePrefix = "CHAT_AD_LOGIN_CHALLENGE:"

var errInvalidADLoginCredential = errors.New("invalid AD login credential")

type ADLoginChallenge struct {
	ID        string    `json:"id"`
	Nonce     string    `json:"nonce"`
	KeyID     string    `json:"keyID"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type ADLoginCredentialEnvelope struct {
	Version     string `json:"version"`
	KeyID       string `json:"keyID"`
	ChallengeID string `json:"challengeID"`
	Nonce       string `json:"nonce"`
	Ciphertext  string `json:"ciphertext"`
}

type ADLoginPayload struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	Platform       int32  `json:"platform"`
	DeviceID       string `json:"deviceID"`
	ChallengeID    string `json:"challengeID"`
	ChallengeNonce string `json:"challengeNonce"`
}

type ADLoginChallengeStore interface {
	Issue(context.Context, ADLoginChallenge, time.Duration) error
	Consume(context.Context, string) (*ADLoginChallenge, error)
}

type redisADLoginChallengeStore struct{ client redis.UniversalClient }

func newRedisADLoginChallengeStore(client redis.UniversalClient) ADLoginChallengeStore {
	return &redisADLoginChallengeStore{client: client}
}

func (s *redisADLoginChallengeStore) Issue(ctx context.Context, challenge ADLoginChallenge, ttl time.Duration) error {
	value, err := json.Marshal(challenge)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, adLoginChallengePrefix+challenge.ID, value, ttl).Err()
}

func (s *redisADLoginChallengeStore) Consume(ctx context.Context, id string) (*ADLoginChallenge, error) {
	result, err := s.client.Eval(ctx, "local value=redis.call('GET', KEYS[1]); if value then redis.call('DEL', KEYS[1]); end; return value", []string{adLoginChallengePrefix + id}).Result()
	if err != nil || result == nil {
		return nil, errInvalidADLoginCredential
	}
	value, ok := result.(string)
	if !ok {
		return nil, errInvalidADLoginCredential
	}
	var challenge ADLoginChallenge
	if err := json.Unmarshal([]byte(value), &challenge); err != nil {
		return nil, errInvalidADLoginCredential
	}
	return &challenge, nil
}

type ADLoginCrypto struct {
	sharedKey []byte
	keyID     string
	ttl       time.Duration
	store     ADLoginChallengeStore
}

func NewADLoginCrypto(cfg config.ADLoginCrypto, client redis.UniversalClient) (*ADLoginCrypto, error) {
	key, err := decodeADLoginSharedKey(cfg.SharedKey)
	if err != nil || cfg.KeyID == "" || cfg.ChallengeTTLSeconds <= 0 {
		return nil, fmt.Errorf("invalid AD login crypto configuration")
	}
	return newADLoginCrypto(key, cfg.KeyID, time.Duration(cfg.ChallengeTTLSeconds)*time.Second, newRedisADLoginChallengeStore(client))
}

func newADLoginCrypto(sharedKey []byte, keyID string, ttl time.Duration, store ADLoginChallengeStore) (*ADLoginCrypto, error) {
	if len(sharedKey) != 32 || keyID == "" || ttl <= 0 || store == nil {
		return nil, fmt.Errorf("invalid AD login crypto configuration")
	}
	return &ADLoginCrypto{sharedKey: sharedKey, keyID: keyID, ttl: ttl, store: store}, nil
}

func decodeADLoginSharedKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("AD login shared key must be base64-encoded 32 bytes")
	}
	return key, nil
}

func (s *ADLoginCrypto) IssueChallenge(ctx context.Context) (*ADLoginChallenge, error) {
	id, err := randomADLoginValue(24)
	if err != nil {
		return nil, err
	}
	nonce, err := randomADLoginValue(32)
	if err != nil {
		return nil, err
	}
	challenge := ADLoginChallenge{ID: id, Nonce: nonce, KeyID: s.keyID, ExpiresAt: time.Now().Add(s.ttl)}
	if err := s.store.Issue(ctx, challenge, s.ttl); err != nil {
		return nil, err
	}
	return &challenge, nil
}

func randomADLoginValue(size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (s *ADLoginCrypto) DecryptAndConsume(ctx context.Context, envelope ADLoginCredentialEnvelope) (*ADLoginPayload, error) {
	if envelope.Version != "v1" || envelope.KeyID != s.keyID || envelope.ChallengeID == "" || envelope.Nonce == "" || envelope.Ciphertext == "" {
		return nil, errInvalidADLoginCredential
	}
	challenge, err := s.store.Consume(ctx, envelope.ChallengeID)
	if err != nil || !challenge.ExpiresAt.After(time.Now()) || challenge.KeyID != envelope.KeyID {
		return nil, errInvalidADLoginCredential
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, errInvalidADLoginCredential
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errInvalidADLoginCredential
	}
	block, err := aes.NewCipher(s.sharedKey)
	if err != nil {
		return nil, errInvalidADLoginCredential
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, errInvalidADLoginCredential
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte("v1\n"+envelope.KeyID+"\n"+envelope.ChallengeID))
	if err != nil {
		return nil, errInvalidADLoginCredential
	}
	var payload ADLoginPayload
	if err := json.Unmarshal(plain, &payload); err != nil || payload.Username == "" || payload.Password == "" || payload.ChallengeID != challenge.ID || subtle.ConstantTimeCompare([]byte(payload.ChallengeNonce), []byte(challenge.Nonce)) != 1 {
		return nil, errInvalidADLoginCredential
	}
	return &payload, nil
}
