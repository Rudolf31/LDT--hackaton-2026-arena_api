package audit

import (
	"context"
	"strings"
	"testing"
)

// TestInsertRejectsEmotionKey — I-1: ключ emotion не должен попасть в журнал
// ни на какой глубине. tx остаётся nil: проверка отказывает раньше, чем
// store дотрагивается до соединения.
func TestInsertRejectsEmotionKey(t *testing.T) {
	s := newStore()
	err := s.insert(context.Background(), nil, Entry{
		ActorKind: ActorSystem,
		Action:    "settings_changed",
		Details:   map[string]any{"camera": map[string]any{"emotion": "happy"}},
	})
	if err == nil {
		t.Fatal("ожидалась ошибка — ключ emotion в details")
	}
	if !strings.Contains(err.Error(), "emotion") {
		t.Fatalf("ошибка должна называть emotion: %v", err)
	}
}

// TestInsertRejectsForbiddenNameFields — CLAUDE.md, правило 5: имена и
// тексты реплик в журнал не пишем.
func TestInsertRejectsForbiddenNameFields(t *testing.T) {
	for _, field := range forbiddenDetailKeys {
		field := field
		t.Run(field, func(t *testing.T) {
			s := newStore()
			err := s.insert(context.Background(), nil, Entry{
				ActorKind: ActorSystem,
				Action:    "settings_changed",
				Details:   map[string]any{field: "значение"},
			})
			if err == nil {
				t.Fatalf("ожидалась ошибка — запрещённое поле %q", field)
			}
		})
	}
}
