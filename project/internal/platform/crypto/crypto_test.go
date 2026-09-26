package crypto

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := NewDataKey()
	if err != nil {
		t.Fatalf("NewDataKey: %v", err)
	}
	plaintext := []byte("Сергей отказался от скидки")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(ciphertext) == string(plaintext) {
		t.Fatal("шифротекст совпал с открытым текстом")
	}

	got, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("расшифровка не совпала: получили %q", got)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	key1, _ := NewDataKey()
	key2, _ := NewDataKey()
	ciphertext, err := Encrypt(key1, []byte("текст"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(key2, ciphertext); err == nil {
		t.Fatal("расшифровка чужим ключом должна была упасть")
	}
}

func TestWrapUnwrapKeyRoundTrip(t *testing.T) {
	masterKey, _ := NewDataKey()
	dataKey, _ := NewDataKey()

	wrapped, err := WrapKey(masterKey, dataKey)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	got, err := UnwrapKey(masterKey, wrapped)
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}
	if string(got) != string(dataKey) {
		t.Fatal("распакованный ключ не совпал с исходным")
	}
}

func TestHMACSHA256Deterministic(t *testing.T) {
	secret := []byte("секрет")
	a := HMACSHA256(secret, []byte("ARENA-AAAA-BBBB-CCC"))
	b := HMACSHA256(secret, []byte("ARENA-AAAA-BBBB-CCC"))
	if !EqualHMAC(a, b) {
		t.Fatal("одинаковые входы должны давать одинаковый HMAC")
	}
	c := HMACSHA256(secret, []byte("ARENA-AAAA-BBBB-CCD"))
	if EqualHMAC(a, c) {
		t.Fatal("разные входы не должны давать одинаковый HMAC")
	}
}

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("верный-пароль-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := VerifyPassword("верный-пароль-123", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("верный пароль должен пройти проверку")
	}

	ok, err = VerifyPassword("неверный-пароль", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Fatal("неверный пароль не должен пройти проверку")
	}
}
