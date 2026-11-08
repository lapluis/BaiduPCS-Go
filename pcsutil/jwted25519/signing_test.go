package jwted25519

import (
	"bytes"
	stded25519 "crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/dgrijalva/jwt-go"
	"golang.org/x/crypto/ed25519"
)

func TestSigningMatchesStandardEd25519(t *testing.T) {
	// Deterministic fixture keys, unrelated to any account or signing identity.
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	public := ed25519.PublicKey(private[ed25519.SeedSize:])
	for _, tc := range []struct{ name, message string }{
		{"empty", ""}, {"ascii", "header.payload"}, {"unicode", "文件名/fixture"},
		{"long", string(bytes.Repeat([]byte("payload"), 100))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := tc.message
			signature, err := SigningMethodED25519.Sign(message, private)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := base64.RawURLEncoding.DecodeString(signature)
			if err != nil || !stded25519.Verify(stded25519.PublicKey(public), []byte(message), decoded) {
				t.Fatalf("signature differs from standard Ed25519: %v", err)
			}
			standard := stded25519.Sign(stded25519.PrivateKey(private), []byte(message))
			if err := SigningMethodED25519.Verify(message, base64.RawURLEncoding.EncodeToString(standard), public); err != nil {
				t.Fatal(err)
			}
			if err := SigningMethodED25519.Verify(message+"changed", signature, public); err == nil {
				t.Fatal("modified message accepted")
			}
		})
	}
}

func TestSigningRejectsInvalidKeysAndSignatures(t *testing.T) {
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	public := ed25519.PublicKey(private[ed25519.SeedSize:])
	for _, tc := range []struct {
		key  interface{}
		want error
	}{
		{nil, jwt.ErrInvalidKeyType}, {"key", jwt.ErrInvalidKeyType},
		{ed25519.PrivateKey{}, jwt.ErrInvalidKey}, {private[:len(private)-1], jwt.ErrInvalidKey},
	} {
		if _, err := SigningMethodED25519.Sign("fixture", tc.key); err != tc.want {
			t.Fatalf("invalid signing key %T: err=%v want=%v", tc.key, err, tc.want)
		}
	}
	for _, tc := range []struct {
		key  interface{}
		want error
	}{
		{nil, jwt.ErrInvalidKeyType}, {"key", jwt.ErrInvalidKeyType},
		{ed25519.PublicKey{}, jwt.ErrInvalidKey}, {public[:len(public)-1], jwt.ErrInvalidKey},
	} {
		if err := SigningMethodED25519.Verify("fixture", "", tc.key); err != tc.want {
			t.Fatalf("invalid verification key %T: err=%v want=%v", tc.key, err, tc.want)
		}
	}
	if err := SigningMethodED25519.Verify("fixture", "!", public); err == nil {
		t.Fatal("malformed base64 signature accepted")
	}
	signature := stded25519.Sign(stded25519.PrivateKey(private), []byte("fixture"))
	signature[0] ^= 1
	if err := SigningMethodED25519.Verify("fixture", base64.RawURLEncoding.EncodeToString(signature), public); err != jwt.ErrSignatureInvalid {
		t.Fatalf("tampered signature: %v", err)
	}
}
