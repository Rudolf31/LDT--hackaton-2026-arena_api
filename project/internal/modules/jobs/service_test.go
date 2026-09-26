package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/modules/settings"
)

type fakeCloser struct {
	startedAt time.Time
	timeout   int
}

func (f *fakeCloser) CloseAbandoned(_ context.Context, startedAt time.Time, timeout int) (int, error) {
	f.startedAt, f.timeout = startedAt, timeout
	return 2, nil
}

type fakeSettings struct{ err error }

func (f fakeSettings) Get(context.Context) (settings.Settings, error) {
	return settings.Settings{AbandonTimeoutMinutes: 17}, f.err
}

func (fakeSettings) Update(context.Context, settings.Patch, *uuid.UUID) (settings.Settings, error) {
	return settings.Settings{}, nil
}

// TestTickPassesStartAndTimeout — задание передаёт момент запуска портала
// (I-15) и таймаут из настроек, а не свои константы.
func TestTickPassesStartAndTimeout(t *testing.T) {
	started := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	closer := &fakeCloser{}
	m := New(closer, fakeSettings{}, slog.New(slog.NewTextHandler(io.Discard, nil)), started)
	n, err := m.Tick(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("Tick: %d, %v", n, err)
	}
	if !closer.startedAt.Equal(started) || closer.timeout != 17 {
		t.Fatalf("передано %v и %d", closer.startedAt, closer.timeout)
	}
}

func TestTickReportsSettingsError(t *testing.T) {
	m := New(&fakeCloser{}, fakeSettings{err: errors.New("нет базы")}, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now())
	if _, err := m.Tick(t.Context()); err == nil {
		t.Fatal("ошибка настроек должна дойти до вызывающего")
	}
}
