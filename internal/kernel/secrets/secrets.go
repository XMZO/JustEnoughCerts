// Package secrets seals what JEC stores but must not keep in the clear,
// such as private keys and API credentials, with AES-256-GCM (PLAN.md 10).
//
// The master key is JEC_SECRET_KEY, base64 of 32 random bytes, or else the
// key file in the data directory, created on first start. Back up that file
// with the database: without it, nothing sealed can be opened again.
package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// KeyFile is the name of the key file in the data directory.
const KeyFile = "secret.key"

const (
	keySize = 32
	version = 1 // the first byte of everything sealed
)

// ErrOpen means that sealed data cannot be opened: it was sealed with
// another master key or for another context, or it was changed.
var ErrOpen = errors.New("cannot open sealed data: wrong secret key, or damaged data")

// Box seals and opens data with the master key.
type Box struct {
	aead cipher.AEAD
}

// Load returns the box for the master key: key, when it is given, or else
// the key file in dataDir, which it creates when missing. created reports
// that it did.
func Load(dataDir, key string) (box *Box, created bool, err error) {
	var raw []byte
	switch {
	case key != "":
		raw, err = decodeKey(key)
		if err != nil {
			return nil, false, fmt.Errorf("JEC_SECRET_KEY: %w", err)
		}
	default:
		path := filepath.Join(dataDir, KeyFile)
		raw, created, err = loadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("secret key file %s: %w", path, err)
		}
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, false, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false, err
	}
	return &Box{aead: aead}, created, nil
}

func decodeKey(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != keySize {
		return nil, fmt.Errorf("want base64 of %d random bytes, as from openssl rand -base64 %d", keySize, keySize)
	}
	return raw, nil
}

// loadFile reads the key file, or creates it with a new random key.
func loadFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		raw, err := decodeKey(string(data))
		return raw, false, err
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	raw := make([]byte, keySize)
	if _, err := rand.Read(raw); err != nil {
		return nil, false, err
	}
	// O_EXCL: a key file another process created meanwhile is kept.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return loadFile(path)
	}
	if err != nil {
		return nil, false, err
	}
	_, err = f.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n")
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		_ = os.Remove(path)
		return nil, false, err
	}
	return raw, true, nil
}

// Seal encrypts plaintext. context names where the result is stored, such
// as "certs.version:42:key"; Open needs the same context, so sealed data
// copied to another place cannot be opened there.
func (b *Box) Seal(plaintext []byte, context string) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+b.aead.Overhead())
	out = append(out, version)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plaintext, []byte(context))
}

// Open decrypts what Seal returned for the same context.
func (b *Box) Open(sealed []byte, context string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != version {
		return nil, ErrOpen
	}
	plaintext, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(context))
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

// Scope returns the box for module, whose contexts are kept apart from
// every other module's.
func (b *Box) Scope(module string) *Scope { return &Scope{box: b, prefix: module + "/"} }

// Scope is a module's view of the box.
type Scope struct {
	box    *Box
	prefix string
}

// Seal is Box.Seal within the module.
func (s *Scope) Seal(plaintext []byte, context string) []byte {
	return s.box.Seal(plaintext, s.prefix+context)
}

// Open is Box.Open within the module.
func (s *Scope) Open(sealed []byte, context string) ([]byte, error) {
	return s.box.Open(sealed, s.prefix+context)
}

// checkContext and checkPlaintext make the value stored to tell, at
// startup, whether the master key is the one the database was sealed with.
const (
	checkContext   = "kernel/secret_check"
	checkPlaintext = "jec"
)

// Check returns a value to store, and later to pass to Verify.
func (b *Box) Check() []byte { return b.Seal([]byte(checkPlaintext), checkContext) }

// Verify reports whether check came from Check with this master key.
func (b *Box) Verify(check []byte) bool {
	got, err := b.Open(check, checkContext)
	return err == nil && bytes.Equal(got, []byte(checkPlaintext))
}
