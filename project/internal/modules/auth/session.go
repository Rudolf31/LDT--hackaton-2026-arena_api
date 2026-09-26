package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/platform/crypto"
)

const (
	sessionCookieName = "arena_session"
	sessionCookiePath = "/api/portal"
	sessionTTL        = 8 * time.Hour
)

// Ключи подписи выводятся из ARENA_CODE_HMAC_SECRET с меткой назначения
// (D-21): cookie портала и токен тренажёра не должны подделываться друг
// из друга.
const (
	sessionKeyLabel      = "arena/portal-session"
	trainerTokenKeyLabel = "arena/trainer-token"
)

func deriveKey(secret []byte, label string) []byte {
	return crypto.HMACSHA256(secret, []byte(label))
}

// sessionCodec — cookie arena_session: номер пользователя и срок, подпись
// HMAC (D-19). Роли в ней нет: роль читается из базы на каждом запросе.
type sessionCodec struct {
	key    []byte
	secure bool
	now    func() time.Time
}

func newSessionCodec(secret []byte, secure bool) *sessionCodec {
	return &sessionCodec{key: deriveKey(secret, sessionKeyLabel), secure: secure, now: time.Now}
}

func (c *sessionCodec) issue(userID uuid.UUID) *http.Cookie {
	expires := c.now().Add(sessionTTL)
	payload := make([]byte, 16+8)
	copy(payload, userID[:])
	binary.BigEndian.PutUint64(payload[16:], uint64(expires.Unix()))
	mac := crypto.HMACSHA256(c.key, payload)

	return c.cookie(
		base64.RawURLEncoding.EncodeToString(payload)+"."+base64.RawURLEncoding.EncodeToString(mac),
		int(sessionTTL/time.Second),
	)
}

func (c *sessionCodec) clear() *http.Cookie {
	return c.cookie("", -1)
}

func (c *sessionCodec) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     sessionCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: http.SameSiteStrictMode,
	}
}

// parse проверяет подпись и срок; любая неудача — «входа нет».
func (c *sessionCodec) parse(value string) (uuid.UUID, bool) {
	encPayload, encMAC, ok := strings.Cut(value, ".")
	if !ok {
		return uuid.Nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encPayload)
	if err != nil || len(payload) != 24 {
		return uuid.Nil, false
	}
	mac, err := base64.RawURLEncoding.DecodeString(encMAC)
	if err != nil || !crypto.EqualHMAC(mac, crypto.HMACSHA256(c.key, payload)) {
		return uuid.Nil, false
	}
	expires := time.Unix(int64(binary.BigEndian.Uint64(payload[16:])), 0)
	if !c.now().Before(expires) {
		return uuid.Nil, false
	}
	var id uuid.UUID
	copy(id[:], payload[:16])
	if bytes.Equal(id[:], uuid.Nil[:]) {
		return uuid.Nil, false
	}
	return id, true
}
