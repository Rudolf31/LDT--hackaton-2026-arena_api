package auth

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var testSecret = bytes.Repeat([]byte{7}, 32)

func TestSessionCookieRoundTrip(t *testing.T) {
	c := newSessionCodec(testSecret, true)
	id := uuid.New()
	cookie := c.issue(id)

	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/portal" {
		t.Fatalf("флаги cookie не те: %+v", cookie)
	}
	got, ok := c.parse(cookie.Value)
	if !ok || got != id {
		t.Fatalf("cookie не разобралась: %v %v", got, ok)
	}
}

func TestSessionCookieSecureFlagFollowsConfig(t *testing.T) {
	if newSessionCodec(testSecret, false).issue(uuid.New()).Secure {
		t.Fatal("при ARENA_COOKIE_SECURE=false флаг Secure не ставится")
	}
}

func TestSessionCookieRejectsTamperingAndExpiry(t *testing.T) {
	c := newSessionCodec(testSecret, true)
	value := c.issue(uuid.New()).Value

	payload, mac, _ := strings.Cut(value, ".")
	tampered := []byte(payload)
	tampered[0] ^= 1
	if _, ok := c.parse(string(tampered) + "." + mac); ok {
		t.Fatal("подделанная cookie принята")
	}
	if _, ok := newSessionCodec(bytes.Repeat([]byte{8}, 32), true).parse(value); ok {
		t.Fatal("cookie, подписанная другим секретом, принята")
	}
	if _, ok := c.parse("мусор"); ok {
		t.Fatal("мусор принят за cookie")
	}

	c.now = func() time.Time { return time.Now().Add(sessionTTL + time.Second) }
	if _, ok := c.parse(value); ok {
		t.Fatal("просроченная cookie принята")
	}
}

func TestTrainerTokenRoundTripAndRejections(t *testing.T) {
	tokens := newTrainerTokens(testSecret)
	subject := uuid.New()
	token, err := tokens.Issue(TrainerClaims{Kind: TrainerTokenParticipant, SubjectID: &subject, ExpiresAt: time.Now().Add(12 * time.Hour)})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := tokens.Verify(token)
	if err != nil || claims.Kind != TrainerTokenParticipant || *claims.SubjectID != subject {
		t.Fatalf("Verify: %+v %v", claims, err)
	}

	// Cookie портала и токен тренажёра подписаны разными ключами (D-21).
	if _, ok := newSessionCodec(testSecret, true).parse(token); ok {
		t.Fatal("токен тренажёра принят как cookie портала")
	}
	if _, err := tokens.Verify(token + "x"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("испорченный токен: ожидался ErrTokenInvalid, получили %v", err)
	}

	tokens.now = func() time.Time { return time.Now().Add(13 * time.Hour) }
	if _, err := tokens.Verify(token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("просроченный токен: ожидался ErrTokenExpired, получили %v", err)
	}
}
