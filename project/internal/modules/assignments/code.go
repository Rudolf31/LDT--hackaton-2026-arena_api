package assignments

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"arena-portal-backend/internal/platform/crypto"
)

// Код доступа ARENA-XXXX-XXXX-XXX (FR-AC-02, архитектура 9.5): десять
// случайных символов Crockford Base32 (50 бит) и контрольный (D-54).
const (
	codeAlphabet  = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	codeRandomLen = 10
	codeLen       = codeRandomLen + 1
	selectorLen   = 4
	codePrefix    = "ARENA"
	codeKeyLabel  = "arena/access-code"
)

// newCode — нормализованный код: 10 случайных символов и контрольный.
func newCode() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(codeAlphabet)))
	for range codeRandomLen {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("случайные символы кода доступа: %w", err)
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	body := b.String()
	return body + string(checkChar(body)), nil
}

// checkChar — контрольный символ: Σ (2i+1)·v_i mod 32. Веса нечётные, а
// значит взаимно просты с 32 — любая одиночная опечатка меняет сумму.
// Классический mod 37 Крокфорда дал бы символы *~$=U, неудобные в ссылке
// и на слух (D-54).
func checkChar(body string) byte {
	sum := 0
	for i := 0; i < len(body); i++ {
		sum += (2*i + 1) * strings.IndexByte(codeAlphabet, body[i])
	}
	return codeAlphabet[sum%len(codeAlphabet)]
}

// formatCode — код в том виде, в каком его видит человек.
func formatCode(normalized string) string {
	return codePrefix + "-" + normalized[:4] + "-" + normalized[4:8] + "-" + normalized[8:]
}

// normalizeCode — введённый код в нормальной форме (arena-api.yaml,
// EnterRequest): регистр, пробелы и дефисы не важны, O→0, I/L→1, префикс
// ARENA необязателен. false — не код: не тот алфавит, не та длина или не
// сошёлся контрольный символ.
func normalizeCode(in string) (string, bool) {
	s := strings.ToUpper(in)
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
	if len(s) == len(codePrefix)+codeLen {
		s = strings.TrimPrefix(s, codePrefix)
	}
	if len(s) != codeLen {
		return "", false
	}
	s = strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(s)
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(codeAlphabet, s[i]) < 0 {
			return "", false
		}
	}
	if checkChar(s[:codeRandomLen]) != s[codeRandomLen] {
		return "", false
	}
	return s, true
}

// codeHasher — два HMAC кода на ключе, выведенном из
// ARENA_CODE_HMAC_SECRET со своей меткой (D-21, D-54).
type codeHasher struct {
	key []byte
}

func newCodeHasher(secret []byte) codeHasher {
	return codeHasher{key: crypto.HMACSHA256(secret, []byte(codeKeyLabel))}
}

func (h codeHasher) selector(normalized string) []byte {
	return crypto.HMACSHA256(h.key, []byte(normalized[:selectorLen]))
}

func (h codeHasher) full(normalized string) []byte {
	return crypto.HMACSHA256(h.key, []byte(normalized))
}
