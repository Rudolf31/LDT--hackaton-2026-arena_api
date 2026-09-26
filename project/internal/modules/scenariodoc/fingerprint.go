package scenariodoc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/gowebpki/jcs"
)

// Fingerprint — SHA-256 от канонической записи JSON по RFC 8785 (JCS),
// в кодировке UTF-8, после удаления passport.version, passport.tags,
// passport.origin и authoring целиком (раздел 19 arena-scenario-format.md,
// arena-portal-backend-architecture.md 10.1). Работает на обобщённом
// дереве, не через Document: отпечаток обязан быть нечувствителен ровно к
// перечисленным четырём полям и ни к чему больше, а JCS сам снимает
// расхождения записи (порядок ключей, "1.0" и "1") — заново кодировать
// документ через Document было бы лишним риском случайно нормализовать
// что-то ещё.
func Fingerprint(document []byte) (string, error) {
	var tree map[string]any
	if err := json.Unmarshal(document, &tree); err != nil {
		return "", fmt.Errorf("документ должен быть JSON-объектом")
	}

	if passport := treeObj(tree["passport"]); passport != nil {
		delete(passport, "version")
		delete(passport, "tags")
		delete(passport, "origin")
	}
	delete(tree, "authoring")

	marshaled, err := json.Marshal(tree)
	if err != nil {
		return "", fmt.Errorf("не удалось сериализовать документ: %w", err)
	}
	canonical, err := jcs.Transform(marshaled)
	if err != nil {
		return "", fmt.Errorf("не удалось канонизировать документ: %w", err)
	}

	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
