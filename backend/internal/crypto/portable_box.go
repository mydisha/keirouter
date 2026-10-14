package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// PortableBox encrypts many secrets under one passphrase-derived key. Unlike
// SealPortable (one argon2 derivation per secret), the key is stretched once
// per box, so a full backup with thousands of secrets stays fast. Every
// encryption still uses a fresh random GCM nonce.
type PortableBox struct {
	gcm  cipher.AEAD
	salt []byte
}

// NewPortableBox derives the box key from passphrase. A nil salt generates a
// fresh one (export); pass the stored salt to reopen a box (import).
func NewPortableBox(passphrase string, salt []byte) (*PortableBox, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("crypto: empty passphrase")
	}
	if salt == nil {
		salt = make([]byte, argonSaltLen)
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return nil, fmt.Errorf("generate salt: %w", err)
		}
	}
	gcm, err := newGCM(derivePortableKey(passphrase, salt))
	if err != nil {
		return nil, err
	}
	return &PortableBox{gcm: gcm, salt: salt}, nil
}

// Salt returns the base64 salt to store alongside the box's ciphertexts.
func (b *PortableBox) Salt() string { return base64.StdEncoding.EncodeToString(b.salt) }

// SealString encrypts plaintext and returns base64 ciphertext.
func (b *PortableBox) SealString(plaintext string) (string, error) {
	ct, err := encrypt(b.gcm, []byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("encrypt portable: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// OpenString decrypts base64 ciphertext; a wrong passphrase fails AEAD
// authentication with ErrMalformedCiphertext.
func (b *PortableBox) OpenString(ciphertext string) (string, error) {
	ct, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: ciphertext: %v", ErrMalformedCiphertext, err)
	}
	pt, err := decrypt(b.gcm, ct)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// DecodePortableSalt parses a salt produced by PortableBox.Salt.
func DecodePortableSalt(s string) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(salt) == 0 {
		return nil, fmt.Errorf("%w: salt", ErrMalformedCiphertext)
	}
	return salt, nil
}
