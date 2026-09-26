//go:build integration

package consents_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/db/seed"
	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/platform/crypto"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
	"arena-portal-backend/internal/platform/pgtest"
	"arena-portal-backend/internal/platform/ratelimit"
)

type noAssignments struct{}

func (noAssignments) CancelForSubject(context.Context, pgx.Tx, uuid.UUID) (int, error) { return 0, nil }

type noSessions struct{}

func (noSessions) RunningByProfile(context.Context, uuid.UUID) (int, error) { return 0, nil }

type noResolver struct{}

func (noResolver) Resolve(context.Context) (consents.Audience, error) {
	return consents.Audience{}, errors.New("не нужен в тесте службы")
}

type fixture struct {
	pool      *pgxpool.Pool
	service   consents.Service
	staff     people.Service
	profileID uuid.UUID
	subjectID uuid.UUID
}

// newFixture — модули на чистой базе, базовая заливка (профиль по
// умолчанию только с OpenRouter и тексты версии 1) и один сотрудник.
func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.NewDatabase(t)
	masterKey := bytes.Repeat([]byte{7}, 32)

	auditModule := audit.New()
	authModule, err := auth.New(pool, auditModule, auth.Config{HMACSecret: bytes.Repeat([]byte{2}, 32)},
		auth.Limiters{PortalLogin: ratelimit.NewLimiters().PortalLogin}, map[string]httpx.OperationAccess{})
	if err != nil {
		t.Fatal(err)
	}
	peopleModule := people.New(pool, auditModule, authModule.GroupAccess(), noAssignments{}, masterKey)
	profilesModule := profiles.New(pool, auditModule, masterKey, nil, noSessions{})
	consentsModule := consents.New(pool, auditModule, authModule.GroupAccess(), peopleModule.Service(),
		profilesModule.Service(), noResolver{})

	base, err := seed.LoadBase()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := seed.LoadConsentTexts()
	if err != nil {
		t.Fatal(err)
	}
	var subjectID uuid.UUID
	err = pg.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := profilesModule.Provisioner().EnsureDefaultTx(ctx, tx, base.DefaultProfile.Name, base.DefaultProfile.Settings); err != nil {
			return err
		}
		newTexts := make([]consents.NewText, 0, len(texts))
		for _, t := range texts {
			newTexts = append(newTexts, consents.NewText{Kind: gen.ConsentKind(t.Kind), Version: t.Version, Body: t.Body})
		}
		if _, err := consentsModule.Provisioner().EnsureTextsTx(ctx, tx, newTexts); err != nil {
			return err
		}
		groupID, err := peopleModule.Provisioner().CreateGroupTx(ctx, tx, people.NewGroup{Name: "Группа теста"}, nil)
		if err != nil {
			return err
		}
		name, no := "Тестовый Участник", "T-001"
		subjectID, err = peopleModule.Provisioner().CreatePersonTx(ctx, tx, people.NewPerson{GroupID: groupID, FullName: &name, PersonnelNo: &no}, nil)
		return err
	})
	if err != nil {
		t.Fatalf("заливка: %v", err)
	}
	profileID, err := profilesModule.Service().DefaultID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{pool: pool, service: consentsModule.Service(), staff: peopleModule.Service(), profileID: profileID, subjectID: subjectID}
}

func (f fixture) training() consents.Audience {
	return consents.Audience{SubjectID: f.subjectID, Mode: gen.ModeTraining, ProfileID: f.profileID}
}

func answerAll(s consents.Screen, external gen.ConsentAnswerValue) []consents.Answer {
	var out []consents.Answer
	for _, t := range s.Texts {
		a := consents.Answer{Kind: t.Kind, TextID: t.TextID, ShownSHA256: t.SHA256, Answer: t.Answers[0]}
		if t.Kind == gen.ConsentKindConsentExternalAi {
			a.Answer = external
		}
		out = append(out, a)
	}
	return out
}

func TestRecordWritesHMACOfShownTextNotPlainHash(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	screen, err := f.service.TextsFor(ctx, f.training())
	if err != nil {
		t.Fatal(err)
	}
	if screen.Variant != gen.ConsentScreenVariantA || len(screen.Texts) != 2 || !screen.CanProceed {
		t.Fatalf("профиль по умолчанию — только OpenRouter, ждали вариант А с двумя текстами: %+v", screen)
	}

	out, err := f.service.Record(ctx, f.training(), answerAll(screen, gen.Granted))
	if err != nil {
		t.Fatal(err)
	}
	if !out.CanStart || !out.ExternalAIAllowed || out.MainConsentID == nil || out.ExternalAIConsentID == nil {
		t.Fatalf("исход: %+v", out)
	}

	key, err := f.staff.DataKey(ctx, f.subjectID)
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := f.pool.QueryRow(ctx, `SELECT shown_text_hmac FROM consent_records WHERE id = $1`, *out.MainConsentID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	plain := sha256.Sum256([]byte(screen.Texts[0].Body))
	if len(stored) != 32 || bytes.Equal(stored, plain[:]) {
		t.Fatal("в базе должен быть HMAC на ключе участника, а не простой SHA-256")
	}
	if !bytes.Equal(stored, crypto.HMACSHA256(key, []byte(screen.Texts[0].Body))) {
		t.Fatal("HMAC не совпал с текстом, который увидел участник")
	}
}

func TestRefusalIsRecordedAndBlocksStartWithoutOwnServer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	screen, err := f.service.TextsFor(ctx, f.training())
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.service.Record(ctx, f.training(), answerAll(screen, gen.Refused))
	if err != nil {
		t.Fatal(err)
	}
	if out.CanStart || out.ExternalAIAllowed || out.Message == nil || *out.Message == "" {
		t.Fatalf("без согласия на OpenRouter и без своего сервера начать нельзя: %+v", out)
	}
	var refused int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM consent_records WHERE kind = 'consent_external_ai' AND NOT usable`).Scan(&refused); err != nil {
		t.Fatal(err)
	}
	if refused != 1 {
		t.Fatalf("отказ должен быть записан: %d", refused)
	}
}

func TestChangedTextIs409AndNothingIsWritten(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	screen, err := f.service.TextsFor(ctx, f.training())
	if err != nil {
		t.Fatal(err)
	}
	answers := answerAll(screen, gen.Granted)
	answers[0].ShownSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = f.service.Record(ctx, f.training(), answers)
	var httpErr *httpx.Error
	if !errors.As(err, &httpErr) || httpErr.Kind != httpx.KindConsentRequired {
		t.Fatalf("ждали 409 consent_required, получили %v", err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM consent_records`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("при несовпавшем тексте ничего не пишется, записей: %d", n)
	}
}

func TestRepeatCreatesAnotherRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	screen, err := f.service.TextsFor(ctx, f.training())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.service.Record(ctx, f.training(), answerAll(screen, gen.Granted)); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM consent_records WHERE kind = 'notice_training'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("повтор — ещё одна запись, получили %d", n)
	}
}

func TestAssessmentNeedsValidWrittenConsent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	assessment := consents.Audience{SubjectID: f.subjectID, Mode: gen.ModeAssessment, ProfileID: f.profileID}

	screen, err := f.service.TextsFor(ctx, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if screen.CanProceed {
		t.Fatal("без письменного согласия оценочный экран должен быть заблокирован")
	}
	if _, err := f.service.Record(ctx, assessment, answerAll(screen, gen.Granted)); err == nil {
		t.Fatal("ответ на заблокированный экран не принимается")
	}

	var userID uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO portal_users (login, password_hash, full_name, role)
		VALUES ('hr.test', 'x', 'HR теста', 'observer') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	insertWritten := func(validUntil string) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, `INSERT INTO consent_records
			(subject_id, kind, answer, document_ref, document_channel, signed_on, valid_until, recorded_by)
			VALUES ($1, 'written_assessment', 'granted', '№1', 'paper', current_date - 400, $2::date, $3)`,
			f.subjectID, validUntil, userID); err != nil {
			t.Fatal(err)
		}
	}

	insertWritten("2000-01-01")
	if _, ok, err := f.service.Usable(ctx, f.subjectID, nil, gen.ConsentKindWrittenAssessment); err != nil || ok {
		t.Fatalf("истёкшее письменное согласие не годится: ok=%v err=%v", ok, err)
	}

	insertWritten("2999-01-01")
	if _, ok, err := f.service.Usable(ctx, f.subjectID, nil, gen.ConsentKindWrittenAssessment); err != nil || !ok {
		t.Fatalf("действующее письменное согласие годится: ok=%v err=%v", ok, err)
	}
	screen, err = f.service.TextsFor(ctx, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if !screen.CanProceed || screen.Texts[0].Kind != gen.ConsentKindConsentAssessment {
		t.Fatalf("с действующим письменным согласием экран оценки открыт: %+v", screen)
	}
}
