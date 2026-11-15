package pcsutil

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CryptoMethodSupport 检测是否支持加密解密方法
func CryptoMethodSupport(method string) bool {
	switch method {
	case "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ofb", "aes-192-ofb", "aes-256-ofb":
		return true
	}
	return false
}

// EncryptFile encrypts a regular file without replacing an existing destination.
func EncryptFile(method string, key []byte, filePath string, isGzip bool) (string, error) {
	return transformCryptoFile(method, key, filePath, isGzip, false, defaultCryptoFileOps())
}

// DecryptFile decrypts a regular file without replacing an existing destination.
func DecryptFile(method string, key []byte, filePath string, isGzip bool) (string, error) {
	return transformCryptoFile(method, key, filePath, isGzip, true, defaultCryptoFileOps())
}

type cryptoFile interface {
	io.ReadWriteSeeker
	Stat() (os.FileInfo, error)
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}
type cryptoFileOps struct {
	open            func(string) (cryptoFile, error)
	temp            func(string) (cryptoFile, error)
	publish         func(string, string) error
	syncPublication func(string) error
	remove          func(string) error
}

func defaultCryptoFileOps() cryptoFileOps {
	return cryptoFileOps{
		open:            func(name string) (cryptoFile, error) { return os.Open(name) },
		temp:            func(target string) (cryptoFile, error) { return os.CreateTemp(filepath.Dir(target), ".pcs-crypto-*") },
		publish:         publishCryptoFile,
		syncPublication: syncCryptoPublication,
		remove:          os.Remove,
	}
}
func transformCryptoFile(method string, key []byte, source string, compressed, decrypt bool, fs cryptoFileOps) (result string, err error) {
	if !CryptoMethodSupport(method) {
		return "", fmt.Errorf("unknown crypto method: %s", method)
	}
	target := source + ".encrypt"
	if decrypt {
		target = strings.TrimSuffix(source, ".encrypt")
		if target == source {
			target += ".decrypted"
		}
	}
	if _, e := os.Lstat(target); e == nil {
		return "", &os.PathError{Op: "create", Path: target, Err: os.ErrExist}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	before, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("crypto source is not a regular file: %s", source)
	}
	input, err := fs.open(source)
	if err != nil {
		return "", err
	}
	inputOpen := true
	defer func() {
		if inputOpen {
			err = errors.Join(err, input.Close())
		}
	}()
	info, err := input.Stat()
	if err != nil {
		return "", err
	}
	if !sameCryptoSource(before, info) {
		return "", fmt.Errorf("crypto source changed before reading: %s", source)
	}
	type temporary struct {
		name string
		info os.FileInfo
	}
	var temps []temporary
	cleanTemps := func() error {
		var failures []error
		for _, tmp := range temps {
			current, e := os.Lstat(tmp.name)
			if e == nil {
				if !os.SameFile(tmp.info, current) {
					e = fmt.Errorf("temporary file replaced: %s", tmp.name)
				} else {
					e = fs.remove(tmp.name)
				}
			}
			if e != nil && !os.IsNotExist(e) {
				failures = append(failures, fmt.Errorf("clean up crypto temporary file: %w", e))
			}
		}
		temps = nil
		return errors.Join(failures...)
	}
	defer func() { err = errors.Join(err, cleanTemps()) }()
	newTemp := func() (cryptoFile, error) {
		f, e := fs.temp(target)
		if e == nil {
			info, statErr := f.Stat()
			if statErr != nil {
				return nil, errors.Join(statErr, f.Close(), fs.remove(f.Name()))
			}
			temps = append(temps, temporary{f.Name(), info})
		}
		return f, e
	}
	var finishReaders []func() error
	closeReaders := func() error {
		var es []error
		for _, close := range finishReaders {
			es = append(es, close())
		}
		finishReaders = nil
		return errors.Join(es...)
	}
	defer func() { err = errors.Join(err, closeReaders()) }()
	var reader io.Reader = input
	if compressed && !decrypt {
		stage, e := newTemp()
		if e != nil {
			return "", e
		}
		finishReaders = append(finishReaders, stage.Close)
		// Compress only into an owned random temporary file.
		w := gzip.NewWriter(stage)
		_, copyErr := io.Copy(w, input)
		if e := errors.Join(copyErr, w.Close()); e != nil {
			return "", e
		}
		if _, e := stage.Seek(0, io.SeekStart); e != nil {
			return "", e
		}
		reader = stage
	}
	reader, err = cryptoStream(method, key, reader, decrypt)
	if err != nil {
		return "", err
	}
	if compressed && decrypt {
		r, e := gzip.NewReader(reader)
		if e != nil {
			return "", e
		}
		finishReaders = append(finishReaders, r.Close)
		reader = r
	}
	output, err := newTemp()
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(output, reader)
	if e := errors.Join(copyErr, closeReaders()); e != nil {
		return "", errors.Join(e, output.Close())
	}
	// Do not expose staged plaintext or grant access masked off by CreateTemp.
	if e := output.Chmod(info.Mode().Perm() & 0600); e != nil {
		return "", errors.Join(e, output.Close())
	}
	if e := output.Sync(); e != nil {
		return "", errors.Join(e, output.Close())
	}
	written, e := output.Stat()
	if e != nil {
		return "", errors.Join(e, output.Close())
	}
	if e := output.Close(); e != nil {
		return "", e
	}
	after, statErr := input.Stat()
	closeErr := input.Close()
	inputOpen = false
	if e := errors.Join(statErr, closeErr); e != nil {
		return "", e
	}
	if !sameCryptoSource(info, after) {
		return "", fmt.Errorf("crypto source changed while reading: %s", source)
	}
	current, e := os.Lstat(source)
	if e != nil {
		return "", e
	}
	if !sameCryptoSource(info, current) {
		return "", fmt.Errorf("crypto source was replaced: %s", source)
	}
	if e := fs.publish(output.Name(), target); e != nil {
		return "", fmt.Errorf("publish crypto output: %w", e)
	}
	if e := fs.syncPublication(target); e != nil {
		return target, fmt.Errorf("output published; directory sync failed: %w", e)
	}
	if e := cleanTemps(); e != nil {
		return target, e
	}
	// Preserve legacy raw-decryption behavior: retain its ciphertext.
	if decrypt && !compressed {
		return target, nil
	}
	published, e := os.Lstat(target)
	if e != nil {
		return target, fmt.Errorf("check published output: %w", e)
	}
	if !published.Mode().IsRegular() || !os.SameFile(written, published) || written.Size() != published.Size() {
		return target, fmt.Errorf("published output changed; source retained: %s", source)
	}
	current, e = os.Lstat(source)
	if e != nil {
		return target, fmt.Errorf("output saved; source check failed: %w", e)
	}
	if !sameCryptoSource(info, current) {
		return target, fmt.Errorf("output saved; changed source retained: %s", source)
	}
	if e := fs.remove(source); e != nil {
		return target, fmt.Errorf("output saved; source removal failed: %w", e)
	}
	return target, nil
}
func sameCryptoSource(a, b os.FileInfo) bool {
	return b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// Retain the legacy format: a 16-byte IV followed by the AES stream, with
// keys truncated or zero-padded to the selected AES key size.
func cryptoStream(method string, key []byte, reader io.Reader, decrypt bool) (io.Reader, error) {
	parts := strings.Split(method, "-")
	bits, _ := strconv.Atoi(parts[1])
	padded := make([]byte, bits/8)
	copy(padded, key)
	block, err := aes.NewCipher(padded)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, aes.BlockSize)
	if decrypt {
		_, err = io.ReadFull(reader, iv)
	} else {
		_, err = io.ReadFull(rand.Reader, iv)
	}
	if err != nil {
		return nil, err
	}
	var stream cipher.Stream
	switch parts[2] {
	case "ctr":
		stream = cipher.NewCTR(block, iv)
	case "cfb":
		if decrypt {
			stream = cipher.NewCFBDecrypter(block, iv)
		} else {
			stream = cipher.NewCFBEncrypter(block, iv)
		}
	case "ofb":
		stream = cipher.NewOFB(block, iv)
	}
	converted := &cipher.StreamReader{S: stream, R: reader}
	if decrypt {
		return converted, nil
	}
	return io.MultiReader(bytes.NewReader(iv), converted), nil
}
