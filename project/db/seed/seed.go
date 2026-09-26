// Package seed — данные первичной заливки (NFR-D-04, D-23). Заливает их
// код приложения (модуль demo), а не SQL: так они проходят те же проверки,
// что и в работе. Этап 11 дополняет этот набор сценариями и сессиями.
package seed

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed base.json
var baseJSON []byte

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

type Base struct {
	Users  []User   `json:"users"`
	Groups []Group  `json:"groups"`
	People []Person `json:"people"`
}

func LoadBase() (Base, error) {
	var b Base
	if err := json.Unmarshal(baseJSON, &b); err != nil {
		return Base{}, fmt.Errorf("разбор db/seed/base.json: %w", err)
	}
	return b, nil
}
