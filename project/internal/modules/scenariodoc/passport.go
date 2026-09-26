package scenariodoc

import (
	"fmt"
	"strings"
)

// Passport — выборка паспорта документа для библиотеки сценариев: название,
// сфера, тип переговоров, теги. Требует, чтобы паспорт документа хотя бы
// разбирался (title, sphere, negotiation_type, tags) — при более глубоких
// проблемах документа остальные поля вызывающий код всё равно получает
// через Validate.
func Passport(document []byte) (PassportSummary, error) {
	doc, diags := decodeDocument(document)
	passportPath := ptr("passport")
	for _, d := range diags {
		if d.Severity == SeverityError && (d.Path == passportPath || strings.HasPrefix(d.Path, passportPath+"/")) {
			return PassportSummary{}, fmt.Errorf("документ не проходит проверку схемы: %s", d.Message)
		}
	}
	return PassportSummary{
		Title:           doc.Passport.Title,
		Sphere:          doc.Passport.Sphere,
		NegotiationType: doc.Passport.NegotiationType,
		Tags:            doc.Passport.Tags,
	}, nil
}
