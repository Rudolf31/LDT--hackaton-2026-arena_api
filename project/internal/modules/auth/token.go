package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"arena-portal-backend/internal/platform/crypto"
)

const trainerTokenVersion = "v1"

// trainerTokens — токен вида v1.<claims в base64url>.<HMAC>. Подпись
// покрывает версию вместе с содержимым.
type trainerTokens struct {
	key []byte
	now func() time.Time
}

func newTrainerTokens(secret []byte) *trainerTokens {
	return &trainerTokens{key: deriveKey(secret, trainerTokenKeyLabel), now: time.Now}
}

func (t *trainerTokens) Issue(c TrainerClaims) (string, error) {
	switch c.Kind {
	case TrainerTokenParticipant, TrainerTokenGuest, TrainerTokenRehearsal:
	default:
		return "", fmt.Errorf("неизвестный вид токена тренажёра %q", c.Kind)
	}
	if c.ExpiresAt.IsZero() {
		return "", fmt.Errorf("у токена тренажёра не задан срок")
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("сериализация токена тренажёра: %w", err)
	}
	signed := trainerTokenVersion + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signed + "." + base64.RawURLEncoding.EncodeToString(crypto.HMACSHA256(t.key, []byte(signed))), nil
}

func (t *trainerTokens) Verify(token string) (TrainerClaims, error) {
	idx := strings.LastIndexByte(token, '.')
	if idx < 0 {
		return TrainerClaims{}, ErrTokenInvalid
	}
	signed, encMAC := token[:idx], token[idx+1:]
	version, encPayload, ok := strings.Cut(signed, ".")
	if !ok || version != trainerTokenVersion {
		return TrainerClaims{}, ErrTokenInvalid
	}
	mac, err := base64.RawURLEncoding.DecodeString(encMAC)
	if err != nil || !crypto.EqualHMAC(mac, crypto.HMACSHA256(t.key, []byte(signed))) {
		return TrainerClaims{}, ErrTokenInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(encPayload)
	if err != nil {
		return TrainerClaims{}, ErrTokenInvalid
	}
	var c TrainerClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return TrainerClaims{}, ErrTokenInvalid
	}
	if !t.now().Before(c.ExpiresAt) {
		return TrainerClaims{}, ErrTokenExpired
	}
	return c, nil
}

var _ TrainerTokens = (*trainerTokens)(nil)
