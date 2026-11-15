package pcsutil

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/Baidu-Login/bdcrypto"
)

var cryptoFixtureKey = []byte("synthetic-test-key")

func cryptoCipherFixture(t *testing.T, plain []byte, compressed bool) []byte {
	t.Helper()
	if compressed {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		if _, err := w.Write(plain); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		plain = b.Bytes()
	}
	r, err := bdcrypto.Aes128CTREncrypt(bdcrypto.Convert16bytes(cryptoFixtureKey), bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCryptoExistingDestinationPreservesAllData(t *testing.T) {
	for _, decrypt := range []bool{false, true} {
		for _, compressed := range []bool{false, true} {
			for _, kind := range []string{"file", "directory", "hardlink", "symlink"} {
				name := kind
				if decrypt {
					name = "decrypt/" + name
				} else {
					name = "encrypt/" + name
				}
				if compressed {
					name += "/gzip"
				} else {
					name += "/raw"
				}
				t.Run(name, func(t *testing.T) {
					dir := t.TempDir()
					source := filepath.Join(dir, "fixture.bin")
					target := source + ".encrypt"
					original := []byte("owned original plaintext")
					if decrypt {
						source += ".encrypt"
						target = filepath.Join(dir, "fixture.bin")
						original = cryptoCipherFixture(t, original, compressed)
					}
					if err := os.WriteFile(source, original, 0600); err != nil {
						t.Fatal(err)
					}
					marker := []byte("unique existing destination")
					switch kind {
					case "file":
						if err := os.WriteFile(target, marker, 0600); err != nil {
							t.Fatal(err)
						}
					case "directory":
						if err := os.Mkdir(target, 0700); err != nil {
							t.Fatal(err)
						}
					case "hardlink":
						if err := os.Link(source, target); err != nil {
							t.Skipf("hardlink unavailable: %v", err)
						}
						marker = original
					case "symlink":
						backing := filepath.Join(dir, "backing")
						if err := os.WriteFile(backing, marker, 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(backing, target); err != nil {
							t.Skipf("symlink unavailable: %v", err)
						}
					}
					var err error
					if decrypt {
						_, err = DecryptFile("aes-128-ctr", cryptoFixtureKey, source, compressed)
					} else {
						_, err = EncryptFile("aes-128-ctr", cryptoFixtureKey, source, compressed)
					}
					if err == nil {
						t.Error("existing destination accepted")
					}
					if b, e := os.ReadFile(source); e != nil || !bytes.Equal(b, original) {
						t.Errorf("source changed: %v", e)
					}
					if kind != "directory" {
						if b, e := os.ReadFile(target); e != nil || !bytes.Equal(b, marker) {
							t.Errorf("destination changed: %v", e)
						}
					} else if st, e := os.Stat(target); e != nil || !st.IsDir() {
						t.Errorf("destination directory changed: %v", e)
					}
				})
			}
		}
	}
}

func TestCryptoRoundTripAndLegacyFormat(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		name := "raw"
		if compressed {
			name = "gzip"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "日本語 fixture.bin")
			plain := bytes.Repeat([]byte("synthetic payload\x00\xff\n"), 4096)
			if err := os.WriteFile(source, plain, 0600); err != nil {
				t.Fatal(err)
			}
			// Historical temporary names must never be reused or truncated.
			for _, suffix := range []string{".gzip.tmp", ".decrypted", ".decrypted.gzip.tmp"} {
				if err := os.WriteFile(source+suffix, []byte("owned marker"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			encrypted, err := EncryptFile("aes-128-ctr", cryptoFixtureKey, source, compressed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(source); !os.IsNotExist(err) {
				t.Fatalf("successful encryption did not remove source: %v", err)
			}
			ciphertext, err := os.ReadFile(encrypted)
			if err != nil {
				t.Fatal(err)
			}
			r, err := bdcrypto.Aes128CTRDecrypt(bdcrypto.Convert16bytes(cryptoFixtureKey), bytes.NewReader(ciphertext))
			if err != nil {
				t.Fatal(err)
			}
			if compressed {
				r, err = gzip.NewReader(r)
				if err != nil {
					t.Fatal(err)
				}
			}
			decoded, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(decoded, plain) {
				t.Fatalf("legacy wire format changed: %v", err)
			}
			decrypted, err := DecryptFile("aes-128-ctr", cryptoFixtureKey, encrypted, compressed)
			if err != nil || decrypted != source {
				t.Fatalf("decrypt: %q %v", decrypted, err)
			}
			if b, e := os.ReadFile(source); e != nil || !bytes.Equal(b, plain) {
				t.Fatalf("round trip: %v", e)
			}
			if _, e := os.Stat(encrypted); compressed && !os.IsNotExist(e) {
				t.Fatalf("gzip cipher source retained: %v", e)
			} else if !compressed && e != nil {
				t.Fatalf("raw cipher source removed: %v", e)
			}
			for _, suffix := range []string{".gzip.tmp", ".decrypted", ".decrypted.gzip.tmp"} {
				if b, e := os.ReadFile(source + suffix); e != nil || string(b) != "owned marker" {
					t.Errorf("historical temp changed: %s %v", suffix, e)
				}
			}
		})
	}
}
