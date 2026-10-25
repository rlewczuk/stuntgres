package main

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// scramClient is one SCRAM-SHA-256 exchange (RFC 5802/7677) without channel
// binding, as a PostgreSQL client performs it.
type scramClient struct {
	user, password, nonce string
	clientFirstBare       string
	serverSignature       []byte
}

func newSCRAMClient(user, password string) (*scramClient, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return &scramClient{user: user, password: password, nonce: base64.StdEncoding.EncodeToString(raw)}, nil
}

// first is the client-first-message. PostgreSQL ignores its user name and
// uses the startup user, so libpq sends an empty one.
func (s *scramClient) first() string {
	s.clientFirstBare = "n=" + scramName(s.user) + ",r=" + s.nonce
	return "n,," + s.clientFirstBare
}

// final checks the server-first-message and returns the client-final-message.
func (s *scramClient) final(serverFirst string) (string, error) {
	attributes := scramAttributes(serverFirst)
	nonce, salt64, iterText := attributes["r"], attributes["s"], attributes["i"]
	if !strings.HasPrefix(nonce, s.nonce) || len(nonce) == len(s.nonce) {
		return "", errors.New("SCRAM server nonce does not extend the client nonce")
	}
	salt, err := base64.StdEncoding.DecodeString(salt64)
	if err != nil || len(salt) == 0 {
		return "", errors.New("SCRAM server sent an invalid salt")
	}
	iterations, err := strconv.Atoi(iterText)
	if err != nil || iterations < 1 {
		return "", errors.New("SCRAM server sent an invalid iteration count")
	}
	salted, err := pbkdf2.Key(sha256.New, s.password, salt, iterations, sha256.Size)
	if err != nil {
		return "", err
	}
	clientKey := scramHMAC(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	withoutProof := "c=biws,r=" + nonce
	authMessage := s.clientFirstBare + "," + serverFirst + "," + withoutProof
	signature := scramHMAC(storedKey[:], authMessage)
	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ signature[i]
	}
	s.serverSignature = scramHMAC(scramHMAC(salted, "Server Key"), authMessage)
	return withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof), nil
}

// verify checks the server-final-message signature.
func (s *scramClient) verify(serverFinal string) error {
	attributes := scramAttributes(serverFinal)
	if message, failed := attributes["e"]; failed {
		return errors.New("SCRAM authentication failed: " + message)
	}
	got, err := base64.StdEncoding.DecodeString(attributes["v"])
	if err != nil || s.serverSignature == nil || subtle.ConstantTimeCompare(got, s.serverSignature) != 1 {
		return errors.New("SCRAM server signature is invalid")
	}
	return nil
}

func scramHMAC(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

func scramAttributes(message string) map[string]string {
	attributes := make(map[string]string)
	for _, part := range strings.Split(message, ",") {
		if len(part) >= 2 && part[1] == '=' {
			attributes[part[:1]] = part[2:]
		}
	}
	return attributes
}

func scramName(name string) string {
	return strings.NewReplacer("=", "=3D", ",", "=2C").Replace(name)
}
