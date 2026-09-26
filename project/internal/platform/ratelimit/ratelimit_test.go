package ratelimit

import (
	"testing"
	"time"
)

func TestAllowUpToLimit(t *testing.T) {
	l := New(3, time.Minute)

	for i := 0; i < 3; i++ {
		allowed, _ := l.Allow("k")
		if !allowed {
			t.Fatalf("попытка %d должна быть разрешена", i+1)
		}
	}

	allowed, retryAfter := l.Allow("k")
	if allowed {
		t.Fatal("четвёртая попытка должна быть отклонена")
	}
	if retryAfter <= 0 {
		t.Fatal("retryAfter должен быть положительным")
	}
}

func TestAllowIndependentKeys(t *testing.T) {
	l := New(1, time.Minute)

	if allowed, _ := l.Allow("a"); !allowed {
		t.Fatal("первая попытка ключа a должна быть разрешена")
	}
	if allowed, _ := l.Allow("b"); !allowed {
		t.Fatal("ключ b не должен зависеть от ключа a")
	}
	if allowed, _ := l.Allow("a"); allowed {
		t.Fatal("вторая попытка ключа a должна быть отклонена")
	}
}

func TestAllowSlidesWithWindow(t *testing.T) {
	l := New(1, 20*time.Millisecond)

	if allowed, _ := l.Allow("k"); !allowed {
		t.Fatal("первая попытка должна быть разрешена")
	}
	if allowed, _ := l.Allow("k"); allowed {
		t.Fatal("вторая попытка внутри окна должна быть отклонена")
	}

	time.Sleep(30 * time.Millisecond)

	if allowed, _ := l.Allow("k"); !allowed {
		t.Fatal("после окончания окна попытка снова должна быть разрешена")
	}
}
