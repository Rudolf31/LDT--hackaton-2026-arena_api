// Package seed — данные первичной заливки (NFR-D-04, D-23). Заливает их
// код приложения (модуль demo), а не SQL: так они проходят те же проверки,
// что и в работе. Этап 11 дополняет этот набор сценариями и сессиями.
package seed

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed base.json
var baseJSON []byte

//go:embed consents/*.txt
var consentFS embed.FS

type User struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
}

type Group struct {
	Name       string   `json:"name"`
	Department string   `json:"department"`
	AccessFor  []string `json:"access_for"`
}

type Person struct {
	Group       string  `json:"group"`
	FullName    *string `json:"full_name"`
	Pseudonym   *string `json:"pseudonym"`
	PersonnelNo *string `json:"personnel_no"`
	JobTitle    *string `json:"job_title"`
}

// DefaultProfile — профиль тренажёра по умолчанию (FR-PF-05, D-46): без
// ключей, их задаёт администратор в портале.
type DefaultProfile struct {
	Name     string          `json:"name"`
	Settings json.RawMessage `json:"settings"`
}

type Base struct {
	Users          []User         `json:"users"`
	Groups         []Group        `json:"groups"`
	People         []Person       `json:"people"`
	DefaultProfile DefaultProfile `json:"default_profile"`
}

// ConsentText — версия 1 текста согласия или уведомления (D-46, D-47).
// Подстановки {{…}} заполняет портал перед показом.
type ConsentText struct {
	Kind    string
	Version string
	Body    string
}

// consentKinds — четыре вида экранных текстов (arena-db.sql, consent_kind
// без written_assessment — у письменного согласия текста в портале нет).
var consentKinds = []string{"notice_training", "consent_assessment", "consent_external_ai", "notice_demo"}

// LoadConsentTexts — тексты из db/seed/consents/<вид>.txt, версия "1".
func LoadConsentTexts() ([]ConsentText, error) {
	out := make([]ConsentText, 0, len(consentKinds))
	for _, kind := range consentKinds {
		body, err := consentFS.ReadFile("consents/" + kind + ".txt")
		if err != nil {
			return nil, fmt.Errorf("чтение текста согласия %s: %w", kind, err)
		}
		out = append(out, ConsentText{Kind: kind, Version: "1", Body: strings.TrimSpace(string(body))})
	}
	return out, nil
}

func LoadBase() (Base, error) {
	var b Base
	if err := json.Unmarshal(baseJSON, &b); err != nil {
		return Base{}, fmt.Errorf("разбор db/seed/base.json: %w", err)
	}
	return b, nil
}
