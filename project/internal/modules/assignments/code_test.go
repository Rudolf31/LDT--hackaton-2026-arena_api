package assignments

import (
	"math"
	"regexp"
	"strings"
	"testing"
)

var codeFormat = regexp.MustCompile(`^ARENA-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{3}$`)

func TestNewCodeFormatAndCheck(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		code, err := newCode()
		if err != nil {
			t.Fatal(err)
		}
		shown := formatCode(code)
		if !codeFormat.MatchString(shown) {
			t.Fatalf("код %q не в формате ARENA-XXXX-XXXX-XXX", shown)
		}
		normalized, ok := normalizeCode(shown)
		if !ok || normalized != code {
			t.Fatalf("показанный код %q не разбирается обратно", shown)
		}
		seen[code] = true
	}
	if len(seen) < 1000 {
		t.Fatal("случайные коды повторяются")
	}
	// FR-AC-02: не меньше 50 бит энтропии.
	if bits := codeRandomLen * math.Log2(float64(len(codeAlphabet))); bits < 50 {
		t.Fatalf("энтропия кода %.1f бит, нужно не меньше 50", bits)
	}
}

func TestNormalizeCodeAcceptsHumanInput(t *testing.T) {
	code, _ := newCode()
	shown := formatCode(code)
	variants := []string{
		shown,
		strings.ToLower(shown),
		strings.ReplaceAll(shown, "-", " "),
		code,
		strings.TrimPrefix(shown, "ARENA-"),
		"  " + shown + "\n",
	}
	for _, v := range variants {
		if got, ok := normalizeCode(v); !ok || got != code {
			t.Fatalf("ввод %q должен дать код %q, получено %q (%v)", v, code, got, ok)
		}
	}
	// Crockford: O → 0, I и L → 1.
	body := "0I0L000000"
	withCheck := body + string(checkChar("0101000000"))
	if got, ok := normalizeCode(strings.ReplaceAll(withCheck, "0", "O")); !ok || got != "0101000000"+withCheck[10:] {
		t.Fatalf("O/I/L должны читаться как 0/1: %q %v", got, ok)
	}
}

// TestCheckCharCatchesEverySingleTypo — любая одиночная замена символа
// ловится контрольным символом (D-54): такая опечатка не тратит попытку.
func TestCheckCharCatchesEverySingleTypo(t *testing.T) {
	for range 50 {
		code, _ := newCode()
		for i := 0; i < codeLen; i++ {
			for j := 0; j < len(codeAlphabet); j++ {
				if codeAlphabet[j] == code[i] {
					continue
				}
				typo := code[:i] + string(codeAlphabet[j]) + code[i+1:]
				if _, ok := normalizeCode(typo); ok {
					t.Fatalf("опечатка %q в коде %q не поймана", typo, code)
				}
			}
		}
	}
}

func TestNormalizeCodeRejectsGarbage(t *testing.T) {
	for _, v := range []string{"", "ARENA", "ARENA-XXXX-XXXX-XX", "ARENA-UUUU-UUUU-UUU", "ARENA-7K3Q-9DXM-2PA-1"} {
		if _, ok := normalizeCode(v); ok {
			t.Fatalf("%q не код", v)
		}
	}
}

func TestSelectorIsFirstFourCharacters(t *testing.T) {
	h := newCodeHasher(make([]byte, 32))
	a, b := "ABCD000000", "ABCD111111"
	a += string(checkChar(a))
	b += string(checkChar(b))
	if string(h.selector(a)) != string(h.selector(b)) {
		t.Fatal("селектор — HMAC первых четырёх символов")
	}
	if string(h.full(a)) == string(h.full(b)) || len(h.full(a)) != 32 {
		t.Fatal("HMAC всего кода различает коды и занимает 32 байта")
	}
}
