// Package crypto — мастер-ключ, ключ участника (обёртка и распаковка),
// AES-256-GCM, HMAC-SHA256 для кодов и токенов, argon2id для паролей входа
// (arena-portal-backend-architecture.md 3.2, 9.1).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const keySize = 32 // AES-256

// NewDataKey генерирует случайный ключ данных участника (32 байта) —
// оборачивается мастер-ключом в subjects.data_key_wrapped
// (arena-portal-backend-architecture.md 9.1).
func NewDataKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("генерация ключа данных: %w", err)
	}
	return key, nil
}

// Encrypt шифрует plaintext ключом key (ровно 32 байта) алгоритмом
// AES-256-GCM. nonce — случайный, идёт перед шифротекстом в возвращаемых
// байтах (arena-portal-backend-architecture.md 9.1: «nonce перед шифром»).
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("генерация nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt расшифровывает то, что вернул Encrypt тем же ключом.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("шифротекст короче nonce")
	}
	nonce, encrypted := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("расшифровка: %w", err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("ключ должен быть %d байт, получено %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("создание шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("создание GCM: %w", err)
	}
	return gcm, nil
}

// WrapKey оборачивает ключ участника мастер-ключом
// (subjects.data_key_wrapped). Это то же AES-256-GCM, что и Encrypt —
// отдельное имя только для ясности вызова.
func WrapKey(masterKey, dataKey []byte) ([]byte, error) {
	return Encrypt(masterKey, dataKey)
}

// UnwrapKey распаковывает ключ участника мастер-ключом.
func UnwrapKey(masterKey, wrapped []byte) ([]byte, error) {
	return Decrypt(masterKey, wrapped)
}

// HMACSHA256 считает HMAC-SHA256(secret, data) — коды доступа, ссылки на
// репетицию, токены участника (arena-portal-backend-architecture.md 9.1).
func HMACSHA256(secret, data []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return mac.Sum(nil)
}

// EqualHMAC сравнивает два HMAC за постоянное время — иначе сравнение кода
// доступа превращается в оракул по времени ответа.
func EqualHMAC(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// --- argon2id ---

const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // КиБ
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword хеширует пароль argon2id и возвращает кодированную строку
// в формате PHC ($argon2id$v=19$m=...,t=...,p=...$соль$хеш), которая incapsulates
// параметры — их можно будет менять не теряя способности проверить старые
// хеши.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("генерация соли: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword сверяет пароль с хешем, сделанным HashPassword. Параметры
// (m, t, p) читаются из самой строки — так что смена параметров по
// умолчанию не ломает проверку уже сохранённых хешей.
func VerifyPassword(password, encoded string) (bool, error) {
	var version, memory, time, threads int
	var saltB64, hashB64 string

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("неверный формат хеша пароля")
	}
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("неверный формат версии в хеше пароля: %w", err)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("неверный формат параметров в хеше пароля: %w", err)
	}
	saltB64, hashB64 = parts[4], parts[5]

	salt, err := base64.RawStdEncoding.DecodeString(saltB64)
	if err != nil {
		return false, fmt.Errorf("неверная соль в хеше пароля: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(hashB64)
	if err != nil {
		return false, fmt.Errorf("неверный хеш в хеше пароля: %w", err)
	}

	got := argon2.IDKey([]byte(password), salt, uint32(time), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
