package consents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
)

// Готовые фразы отказа (arena-api.md 4, «Когда начать нельзя»: тексты 6.1
// и 6.2 — файла arena-consent-texts.md нет, фразы пишем сами).
const (
	messageAssessmentRefused = "Вы не дали согласия на оценку — сессия не начнётся. На портале записано, что вы отказались; " +
		"это не влечёт для вас последствий. Если передумаете, откройте ссылку ещё раз."
	messageNoModelRoute = "Без согласия на передачу текста во внешнюю нейросеть разговор провести не получится: " +
		"в вашем профиле тренажёра нет сервера моделей оператора. Обратитесь к HR."
)

// template — текущая версия текста вида, с подстановками {{…}} (D-47).
type template struct {
	ID      uuid.UUID
	Kind    gen.ConsentKind
	Version string
	Body    string
}

// screenInput — всё, из чего собирается экран; собирает его service.
type screenInput struct {
	Mode                gen.Mode
	Demo                bool
	OpenRouterURL       string // пусто — маршрута нет
	OwnServerURL        string // пусто — маршрута нет
	ModelProvidersNote  string
	FullName            *string
	Pseudonym           *string
	PersonnelNo         *string
	ExternalAIWithdrawn bool
	WrittenConsentOK    bool
	Templates           map[gen.ConsentKind]template
}

var titles = map[gen.ConsentKind]string{
	gen.ConsentKindNoticeTraining:    "Уведомление перед тренировкой",
	gen.ConsentKindConsentAssessment: "Согласие на обработку персональных данных при оценке",
	gen.ConsentKindConsentExternalAi: "Согласие на передачу текста разговора во внешнюю нейросеть",
	gen.ConsentKindNoticeDemo:        "Уведомление перед демонстрацией",
}

// mainKind — основной текст экрана по режиму: уведомление в тренировке и
// демо, согласие в оценке (FR-AC-06, FR-AC-13).
func mainKind(mode gen.Mode, demo bool) gen.ConsentKind {
	switch {
	case demo:
		return gen.ConsentKindNoticeDemo
	case mode == gen.ModeAssessment:
		return gen.ConsentKindConsentAssessment
	default:
		return gen.ConsentKindNoticeTraining
	}
}

func answersFor(kind gen.ConsentKind) []gen.ConsentAnswerValue {
	if kind == gen.ConsentKindNoticeTraining || kind == gen.ConsentKindNoticeDemo {
		return []gen.ConsentAnswerValue{gen.Acknowledged}
	}
	return []gen.ConsentAnswerValue{gen.Granted, gen.Refused}
}

// buildScreen — экран «Согласие и уведомление»: вариант А/Б/В по
// маршрутам профиля, основной текст по режиму, отдельный вопрос про
// внешнюю нейросеть, если маршрут через OpenRouter есть и согласие на неё
// не отозвано (people.external_ai_withdrawn_at).
func buildScreen(in screenInput) (Screen, error) {
	openRouter := in.OpenRouterURL != "" && !in.ExternalAIWithdrawn
	own := in.OwnServerURL != ""

	s := Screen{ExternalAIOffered: openRouter, OwnServer: own, CanProceed: true}
	switch {
	case openRouter && own:
		s.Variant = gen.ConsentScreenVariantV
	case openRouter:
		s.Variant = gen.ConsentScreenVariantA
	default:
		s.Variant = gen.ConsentScreenVariantB
	}

	kinds := []gen.ConsentKind{mainKind(in.Mode, in.Demo)}
	if openRouter {
		kinds = append(kinds, gen.ConsentKindConsentExternalAi)
	}
	replacer := strings.NewReplacer(
		"{{full_name}}", displayName(in),
		"{{personnel_no}}", personnelNo(in),
		"{{model_destinations}}", destinations(in.OpenRouterURL, in.OwnServerURL, openRouter),
		"{{model_providers}}", providers(in.ModelProvidersNote),
	)
	for _, kind := range kinds {
		t, ok := in.Templates[kind]
		if !ok {
			return Screen{}, fmt.Errorf("в портале нет текста согласия вида %s — базовая заливка при старте не прошла", kind)
		}
		body := replacer.Replace(t.Body)
		s.Texts = append(s.Texts, TextShown{
			Kind: kind, TextID: t.ID, Version: t.Version, Title: titles[kind],
			Body: body, SHA256: sha256Hex(body), Answers: answersFor(kind),
		})
	}

	switch {
	case !openRouter && !own:
		s.block("В профиле тренажёра не задан адрес моделей — обратитесь к HR.")
	case in.Mode == gen.ModeAssessment && !in.Demo && in.FullName == nil:
		s.block("Оценка проводится только поимённо, а в карточке сотрудника нет ФИО — обратитесь к HR.")
	case in.Mode == gen.ModeAssessment && !in.Demo && !in.WrittenConsentOK:
		s.block("Нет отметки о письменном согласии на оценку — обратитесь к HR.")
	case openRouter && strings.TrimSpace(in.ModelProvidersNote) == "":
		s.block("В профиле тренажёра не указаны разработчики моделей для текста о внешней нейросети — обратитесь к HR.")
	}
	return s, nil
}

func (s *Screen) block(reason string) {
	s.CanProceed = false
	s.BlockedReason = &reason
}

func displayName(in screenInput) string {
	switch {
	case in.Demo:
		return "гость демонстрации"
	case in.FullName != nil:
		return *in.FullName
	case in.Pseudonym != nil:
		return *in.Pseudonym
	}
	return "участник"
}

func personnelNo(in screenInput) string {
	if in.PersonnelNo != nil && strings.TrimSpace(*in.PersonnelNo) != "" {
		return *in.PersonnelNo
	}
	return "не указан"
}

// destinations — «куда уходят данные»: фраза меняется вместе с адресами
// моделей в профиле (FR-AC-13: «текст меняется при смене адреса
// моделей»), а значит, меняется и SHA-256 показанного текста.
func destinations(openRouterURL, ownURL string, openRouterOffered bool) string {
	cloud := "облачному сервису OpenRouter (" + openRouterURL + "), который направляет его языковым моделям внешних разработчиков"
	server := "на сервер моделей оператора (" + ownURL + ") и за пределы оператора не уходит"
	switch {
	case openRouterOffered && ownURL != "":
		return "текст разговора передаётся на сервер моделей оператора (" + ownURL + "); " +
			"только с вашего отдельного согласия — также " + cloud + "."
	case openRouterOffered:
		return "текст разговора передаётся только с вашего отдельного согласия " + cloud + "."
	case ownURL != "":
		return "текст разговора передаётся " + server + "."
	}
	return "адрес моделей в профиле тренажёра не задан."
}

func providers(note string) string {
	if strings.TrimSpace(note) == "" {
		return "не указаны."
	}
	return strings.TrimSpace(note)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
