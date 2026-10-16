package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id with the second recommended parameters of RFC 9106: 3 passes
// over 64 MiB with 4 lanes. Tests lower them; a hash keeps the parameters
// it was made with. hashing bounds how many run at once, so a burst of
// logins cannot take hundreds of megabytes.
var (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
)

const (
	argonKeyLen = 32
	saltLen     = 16
)

var hashing = make(chan struct{}, 2)

func derive(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	hashing <- struct{}{}
	defer func() { <-hashing }()
	return argon2.IDKey(password, salt, time, memory, threads, keyLen)
}

// hashPassword returns password in the PHC string format,
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>.
func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := derive([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("not an argon2id PHC string")

// checkPassword reports whether password matches hash, which keeps the
// parameters it was made with.
func checkPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	// Bounds keep a damaged or planted hash from asking for gigabytes.
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil ||
		memory == 0 || memory > 1<<20 || time == 0 || time > 16 || threads == 0 || threads > 16 {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, errBadHash
	}
	got := derive([]byte(password), salt, time, memory, threads, uint32(len(want))) //nolint:gosec // G115: a hash is far shorter than 4 GiB
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is checked against when a username does not exist, so that
// the answer takes as long as for a wrong password.
var dummyHash = sync.OnceValue(func() string {
	h, err := hashPassword("not a real password")
	if err != nil {
		panic(err)
	}
	return h
})
