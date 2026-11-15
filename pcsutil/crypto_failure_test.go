package pcsutil

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type faultyCryptoFile struct {
	cryptoFile
	readErr, writeErr, syncErr, closeErr, chmodErr error
}

func (f *faultyCryptoFile) Read(b []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return f.cryptoFile.Read(b)
}
func (f *faultyCryptoFile) Write(b []byte) (int, error) {
	if f.writeErr != nil {
		n := len(b) / 2
		_, err := f.cryptoFile.Write(b[:n])
		return n, errors.Join(f.writeErr, err)
	}
	return f.cryptoFile.Write(b)
}
func (f *faultyCryptoFile) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.cryptoFile.Sync()
}
func (f *faultyCryptoFile) Close() error { return errors.Join(f.closeErr, f.cryptoFile.Close()) }
func (f *faultyCryptoFile) Chmod(m os.FileMode) error {
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.cryptoFile.Chmod(m)
}

func TestCryptoFailurePreservesInputAndExistingData(t *testing.T) {
	for _, decrypt := range []bool{false, true} {
		for _, state := range []string{"create", "read", "write", "sync", "close", "source-close", "chmod", "publish", "late-collision", "directory-sync", "remove"} {
			name := "encrypt/" + state
			if decrypt {
				name = "decrypt/" + state
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				source := filepath.Join(dir, "fixture")
				target := source + ".encrypt"
				original := []byte("synthetic original input")
				compressed := decrypt // Exercise ciphertext removal only after gzip validation.
				if decrypt {
					target = source
					source += ".encrypt"
					original = cryptoCipherFixture(t, original, true)
				}
				if err := os.WriteFile(source, original, 0600); err != nil {
					t.Fatal(err)
				}
				failure := errors.New("synthetic " + state + " failure")
				fs := defaultCryptoFileOps()
				base := fs
				if state == "directory-sync" {
					fs.syncPublication = func(string) error { return failure }
				}
				fs.open = func(s string) (cryptoFile, error) {
					f, e := base.open(s)
					if e != nil {
						return nil, e
					}
					w := &faultyCryptoFile{cryptoFile: f}
					if state == "read" {
						w.readErr = failure
					}
					if state == "source-close" {
						w.closeErr = failure
					}
					return w, nil
				}
				fs.temp = func(s string) (cryptoFile, error) {
					if state == "create" {
						return nil, failure
					}
					f, e := base.temp(s)
					if e != nil {
						return nil, e
					}
					w := &faultyCryptoFile{cryptoFile: f}
					switch state {
					case "write":
						w.writeErr = failure
					case "sync":
						w.syncErr = failure
					case "close":
						w.closeErr = failure
					case "chmod":
						w.chmodErr = failure
					}
					return w, nil
				}
				fs.publish = func(a, b string) error {
					if state == "publish" {
						return failure
					}
					if state == "late-collision" {
						if err := os.WriteFile(b, []byte("late unique destination"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					return base.publish(a, b)
				}
				fs.remove = func(s string) error {
					if state == "remove" && s == source {
						return failure
					}
					return base.remove(s)
				}
				result, err := transformCryptoFile("aes-128-ctr", cryptoFixtureKey, source, compressed, decrypt, fs)
				if err == nil {
					t.Fatal("injected failure ignored")
				}
				if state != "late-collision" && !errors.Is(err, failure) {
					t.Errorf("original cause lost: %v", err)
				}
				if b, e := os.ReadFile(source); e != nil || !bytes.Equal(b, original) {
					t.Fatalf("source changed or lost: %v", e)
				}
				if state == "late-collision" {
					if b, e := os.ReadFile(target); e != nil || string(b) != "late unique destination" {
						t.Fatalf("late collision overwritten: %v", e)
					}
				} else if state == "remove" || state == "directory-sync" {
					if result != target {
						t.Errorf("published output path lost: %q", result)
					}
					if _, e := os.ReadFile(target); e != nil {
						t.Fatal(e)
					}
				} else if _, e := os.Lstat(target); !os.IsNotExist(e) {
					t.Errorf("failed output published: %v", e)
				}
				stages, e := filepath.Glob(filepath.Join(dir, ".pcs-crypto-*"))
				if e != nil || len(stages) != 0 {
					t.Errorf("temporary data leaked: %v %v", stages, e)
				}
			})
		}
	}
}

func TestCryptoInvalidCiphertextAndSource(t *testing.T) {
	for _, state := range []string{"short-iv", "invalid-gzip", "bad-checksum", "directory", "source-symlink", "no-suffix"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "fixture.encrypt")
			original := []byte("short")
			if state == "invalid-gzip" {
				original = cryptoCipherFixture(t, []byte("not gzip"), false)
			}
			if state == "bad-checksum" {
				original = cryptoCipherFixture(t, []byte("original"), true)
				original[len(original)-5] ^= 1
			}
			if state == "no-suffix" {
				source = filepath.Join(dir, "fixture")
				original = cryptoCipherFixture(t, []byte("plain"), false)
			}
			if state == "directory" {
				if e := os.Mkdir(source, 0700); e != nil {
					t.Fatal(e)
				}
			} else if state == "source-symlink" {
				backing := filepath.Join(dir, "backing")
				if e := os.WriteFile(backing, original, 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(backing, source); e != nil {
					t.Skipf("symlink unavailable: %v", e)
				}
			} else if e := os.WriteFile(source, original, 0600); e != nil {
				t.Fatal(e)
			}
			result, err := DecryptFile("aes-128-ctr", cryptoFixtureKey, source, state != "no-suffix")
			if state == "no-suffix" {
				if err != nil || result != source+".decrypted" {
					t.Fatalf("unsuffixed decrypt: %q %v", result, err)
				}
				if b, e := os.ReadFile(result); e != nil || string(b) != "plain" {
					t.Fatalf("wrong bytes: %v", e)
				}
			} else if err == nil {
				t.Fatal("invalid input accepted")
			}
			if state != "directory" {
				if b, e := os.ReadFile(source); e != nil || !bytes.Equal(b, original) {
					t.Fatalf("invalid source changed: %v", e)
				}
			}
			stages, e := filepath.Glob(filepath.Join(dir, ".pcs-crypto-*"))
			if e != nil || len(stages) != 0 {
				t.Errorf("temporary data leaked: %v %v", stages, e)
			}
		})
	}
}

func TestCryptoCompressionStageCloseFailure(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "fixture")
	original := []byte("synthetic compression source")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	fs := defaultCryptoFileOps()
	base := fs.temp
	count := 0
	failure := io.ErrClosedPipe
	fs.temp = func(s string) (cryptoFile, error) {
		f, e := base(s)
		if e != nil {
			return nil, e
		}
		count++
		w := &faultyCryptoFile{cryptoFile: f}
		if count == 1 {
			w.closeErr = failure
		}
		return w, nil
	}
	_, err := transformCryptoFile("aes-128-ctr", cryptoFixtureKey, source, true, false, fs)
	if !errors.Is(err, failure) {
		t.Fatalf("stage close failure lost: %v", err)
	}
	if b, e := os.ReadFile(source); e != nil || !bytes.Equal(b, original) {
		t.Fatalf("source changed: %v", e)
	}
	if _, e := os.Stat(source + ".encrypt"); !os.IsNotExist(e) {
		t.Fatalf("failed compressed output published: %v", e)
	}
}

func TestCryptoTemporaryCleanupFailureRetainsSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(source, []byte("synthetic original"), 0600); err != nil {
		t.Fatal(err)
	}
	fs := defaultCryptoFileOps()
	fs.remove = func(string) error { return io.ErrClosedPipe }
	result, err := transformCryptoFile("aes-128-ctr", cryptoFixtureKey, source, true, false, fs)
	if !errors.Is(err, io.ErrClosedPipe) || result != source+".encrypt" {
		t.Fatalf("cleanup outcome: %q %v", result, err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "synthetic original" {
		t.Fatalf("original lost: %v", err)
	}
}
