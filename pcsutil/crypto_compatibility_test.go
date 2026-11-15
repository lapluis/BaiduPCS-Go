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

func legacyReader[K any](convert func([]byte) K, open func(K, io.Reader) (io.Reader, error)) func(io.Reader) (io.Reader, error) {
	return func(r io.Reader) (io.Reader, error) { return open(convert(cryptoFixtureKey), r) }
}

func TestCryptoBidirectionalCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name             string
		encrypt, decrypt func(io.Reader) (io.Reader, error)
	}{
		{"aes-128-ctr", legacyReader(bdcrypto.Convert16bytes, bdcrypto.Aes128CTREncrypt), legacyReader(bdcrypto.Convert16bytes, bdcrypto.Aes128CTRDecrypt)},
		{"aes-128-cfb", legacyReader(bdcrypto.Convert16bytes, bdcrypto.Aes128CFBEncrypt), legacyReader(bdcrypto.Convert16bytes, bdcrypto.Aes128CFBDecrypt)},
		{"aes-128-ofb", legacyReader(bdcrypto.Convert16bytes, bdcrypto.Aes128OFBEncrypt), legacyReader(bdcrypto.Convert16bytes, bdcrypto.Aes128OFBDecrypt)},
		{"aes-192-ctr", legacyReader(bdcrypto.Convert24bytes, bdcrypto.Aes192CTREncrypt), legacyReader(bdcrypto.Convert24bytes, bdcrypto.Aes192CTRDecrypt)},
		{"aes-192-cfb", legacyReader(bdcrypto.Convert24bytes, bdcrypto.Aes192CFBEncrypt), legacyReader(bdcrypto.Convert24bytes, bdcrypto.Aes192CFBDecrypt)},
		{"aes-192-ofb", legacyReader(bdcrypto.Convert24bytes, bdcrypto.Aes192OFBEncrypt), legacyReader(bdcrypto.Convert24bytes, bdcrypto.Aes192OFBDecrypt)},
		{"aes-256-ctr", legacyReader(bdcrypto.Convert32bytes, bdcrypto.Aes256CTREncrypt), legacyReader(bdcrypto.Convert32bytes, bdcrypto.Aes256CTRDecrypt)},
		{"aes-256-cfb", legacyReader(bdcrypto.Convert32bytes, bdcrypto.Aes256CFBEncrypt), legacyReader(bdcrypto.Convert32bytes, bdcrypto.Aes256CFBDecrypt)},
		{"aes-256-ofb", legacyReader(bdcrypto.Convert32bytes, bdcrypto.Aes256OFBEncrypt), legacyReader(bdcrypto.Convert32bytes, bdcrypto.Aes256OFBDecrypt)},
	} {
		for _, compressed := range []bool{false, true} {
			for _, plain := range [][]byte{nil, bytes.Repeat([]byte("synthetic fixture"), 4097)} {
				t.Run(tc.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "sample")
					if err := os.WriteFile(path, plain, 0600); err != nil {
						t.Fatal(err)
					}
					encrypted, err := EncryptFile(tc.name, cryptoFixtureKey, path, compressed)
					if err != nil {
						t.Fatal(err)
					}
					wire, err := os.ReadFile(encrypted)
					if err != nil {
						t.Fatal(err)
					}
					r, err := tc.decrypt(bytes.NewReader(wire))
					if err != nil {
						t.Fatal(err)
					}
					var payload bytes.Buffer
					if compressed {
						gz, err := gzip.NewReader(r)
						if err != nil {
							t.Fatal(err)
						}
						r = gz
						defer gz.Close()
						w := gzip.NewWriter(&payload)
						if _, err = w.Write(plain); err != nil {
							t.Fatal(err)
						}
						if err = w.Close(); err != nil {
							t.Fatal(err)
						}
					} else {
						payload.Write(plain)
					}
					decoded, err := io.ReadAll(r)
					if err != nil || !bytes.Equal(decoded, plain) {
						t.Fatalf("legacy decode: %v", err)
					}
					r, err = tc.encrypt(&payload)
					if err != nil {
						t.Fatal(err)
					}
					wire, err = io.ReadAll(r)
					if err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(encrypted, wire, 0600); err != nil {
						t.Fatal(err)
					}
					_, err = DecryptFile(tc.name, cryptoFixtureKey, encrypted, compressed)
					if err != nil {
						t.Fatal(err)
					}
					decoded, err = os.ReadFile(path)
					if err != nil || !bytes.Equal(decoded, plain) {
						t.Fatalf("current decode: %v", err)
					}
				})
			}
		}
	}
}

func TestCryptoSourceChange(t *testing.T) {
	for _, which := range []string{"while-reading", "after-publish"} {
		t.Run(which, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
				t.Fatal(err)
			}
			fs := defaultCryptoFileOps()
			base := fs
			marker := []byte("updated file contents")
			if which == "while-reading" {
				fs.temp = func(target string) (cryptoFile, error) {
					if err := os.WriteFile(path, marker, 0600); err != nil {
						t.Fatal(err)
					}
					return base.temp(target)
				}
			} else {
				fs.syncPublication = func(target string) error {
					if err := os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
					return os.WriteFile(path, marker, 0600)
				}
			}
			result, err := transformCryptoFile("aes-128-ctr", cryptoFixtureKey, path, false, false, fs)
			if err == nil {
				t.Fatal("source change ignored")
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, marker) {
				t.Fatalf("changed file lost: %v", err)
			}
			if which == "while-reading" {
				if _, err := os.Stat(path + ".encrypt"); !os.IsNotExist(err) {
					t.Fatal("published changed input")
				}
			} else if result != path+".encrypt" {
				t.Fatal("published path lost")
			}
		})
	}
}
