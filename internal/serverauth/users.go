package serverauth

import (
	"bufio"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Account struct {
	Username  string
	Salt, Key []byte
	Databases []string
}

type Accounts map[string]Account

func ValidName(name string, database bool) error {
	if name == "" || len(name) > 63 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n:,") {
		return errors.New("names must be valid UTF-8, 1–63 bytes, without NUL, line terminators, colon or comma")
	}
	if database && (name == "." || name == ".." || strings.ContainsAny(name, "/\\")) {
		return errors.New("database names cannot be path components")
	}
	return nil
}

func ValidateGrants(databases []string) error {
	if len(databases) == 0 {
		return errors.New("at least one database grant is required")
	}
	seen := make(map[string]bool, len(databases))
	for _, name := range databases {
		if err := ValidName(name, true); err != nil {
			return err
		}
		if seen[name] {
			return errors.New("duplicate database grant")
		}
		seen[name] = true
	}
	return nil
}

func Load(path string, secure bool) (Accounts, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("userfile must be a regular file")
	}
	if secure {
		if err := validateUserFilePermissions(f, info); err != nil {
			return nil, err
		}
	}
	result := make(Accounts)
	reader := bufio.NewReader(f)
	for line := 1; ; line++ {
		text, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read userfile: %w", err)
		}
		if len(text) == 0 && errors.Is(err, io.EOF) {
			break
		}
		// Remove only LF, not CR: records must not be silently normalized.
		fields := strings.Split(strings.TrimSuffix(text, "\n"), ":")
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid userfile record at line %d", line)
		}
		name := fields[0]
		if err := ValidName(name, false); err != nil {
			return nil, fmt.Errorf("invalid userfile name at line %d: %w", line, err)
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("duplicate user at line %d", line)
		}
		parts := strings.Split(fields[1], "$")
		if len(parts) != 5 || parts[0] != "" || parts[1] != "pbkdf2-sha256" || parts[2] != "i="+strconv.Itoa(passwordIterations) {
			return nil, fmt.Errorf("unsupported or malformed verifier at line %d", line)
		}
		salt, err := decodeVerifierPart(parts[3], 16)
		if err != nil {
			return nil, fmt.Errorf("invalid verifier salt at line %d", line)
		}
		key, err := decodeVerifierPart(parts[4], 32)
		if err != nil {
			return nil, fmt.Errorf("invalid verifier key at line %d", line)
		}
		grants := strings.Split(fields[2], ",")
		if err := ValidateGrants(grants); err != nil {
			return nil, fmt.Errorf("invalid database grants at line %d: %w", line, err)
		}
		result[name] = Account{Username: name, Salt: salt, Key: key, Databases: grants}
	}
	return result, nil
}

func decodeVerifierPart(text string, size int) ([]byte, error) {
	value, err := base64.RawStdEncoding.Strict().DecodeString(text)
	if err != nil || len(value) != size || base64.RawStdEncoding.EncodeToString(value) != text {
		return nil, errors.New("invalid verifier encoding")
	}
	return value, nil
}

func ValidPassword(password string) error {
	if password == "" || strings.ContainsAny(password, "\x00\r\n") {
		return errors.New("password must be nonempty and contain no NUL or line terminators")
	}
	return nil
}

func VerifyPassword(a Account, password string) bool {
	if ValidPassword(password) != nil || len(a.Salt) != 16 || len(a.Key) != 32 {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, a.Salt, passwordIterations, 32)
	return err == nil && subtle.ConstantTimeCompare(key, a.Key) == 1
}

func UpdateAccount(path, username, password string, databases []string, grantsSet bool) (err error) {
	if err := ValidName(username, false); err != nil {
		return err
	}
	if err := ValidPassword(password); err != nil {
		return err
	}
	if grantsSet {
		if err := ValidateGrants(databases); err != nil {
			return err
		}
	}
	unlock, err := userLock(path + ".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	all, err := Load(path, false)
	if errors.Is(err, os.ErrNotExist) {
		all, err = make(Accounts), nil
	}
	if err != nil {
		return err
	}
	previous, exists := all[username]
	if !grantsSet {
		if !exists {
			return errors.New("new users require an explicit nonempty database list")
		}
		databases = previous.Databases
	}
	a, err := NewAccount(username, password, databases)
	if err != nil {
		return err
	}
	all[username] = a
	f, err := os.CreateTemp(filepath.Dir(path), ".stuntgres-users-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := all[name]
		if _, err := fmt.Fprintf(f, "%s:$pbkdf2-sha256$i=%d$%s$%s:%s\n", a.Username, passwordIterations, base64.RawStdEncoding.EncodeToString(a.Salt), base64.RawStdEncoding.EncodeToString(a.Key), strings.Join(a.Databases, ",")); err != nil {
			return err
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// NewAccount validates and hashes one plaintext account without retaining its password.
func NewAccount(username, password string, databases []string) (Account, error) {
	if err := ValidName(username, false); err != nil {
		return Account{}, err
	}
	if err := ValidPassword(password); err != nil {
		return Account{}, err
	}
	if err := ValidateGrants(databases); err != nil {
		return Account{}, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Account{}, err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return Account{}, err
	}
	return Account{Username: username, Salt: salt, Key: key, Databases: append([]string(nil), databases...)}, nil
}
