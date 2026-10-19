package main

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

type account struct {
	username  string
	salt, key []byte
	databases []string
}

type accounts map[string]account

func validName(name string, database bool) error {
	if name == "" || len(name) > 63 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n:,") {
		return errors.New("names must be valid UTF-8, 1–63 bytes, without NUL, line terminators, colon or comma")
	}
	if database && (name == "." || name == ".." || strings.ContainsAny(name, "/\\")) {
		return errors.New("database names cannot be path components")
	}
	return nil
}

func validateGrants(databases []string) error {
	if len(databases) == 0 {
		return errors.New("at least one database grant is required")
	}
	seen := make(map[string]bool, len(databases))
	for _, name := range databases {
		if err := validName(name, true); err != nil {
			return err
		}
		if seen[name] {
			return errors.New("duplicate database grant")
		}
		seen[name] = true
	}
	return nil
}

func loadAccounts(path string, secure bool) (accounts, error) {
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
	result := make(accounts)
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
		if err := validName(name, false); err != nil {
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
		if err := validateGrants(grants); err != nil {
			return nil, fmt.Errorf("invalid database grants at line %d: %w", line, err)
		}
		result[name] = account{username: name, salt: salt, key: key, databases: grants}
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

func validPassword(password string) error {
	if password == "" || strings.ContainsAny(password, "\x00\r\n") {
		return errors.New("password must be nonempty and contain no NUL or line terminators")
	}
	return nil
}

func verifyPassword(a account, password string) bool {
	if validPassword(password) != nil || len(a.salt) != 16 || len(a.key) != 32 {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, a.salt, passwordIterations, 32)
	return err == nil && subtle.ConstantTimeCompare(key, a.key) == 1
}

func updateAccount(path, username, password string, databases []string, grantsSet bool) (err error) {
	if err := validName(username, false); err != nil {
		return err
	}
	if err := validPassword(password); err != nil {
		return err
	}
	if grantsSet {
		if err := validateGrants(databases); err != nil {
			return err
		}
	}
	unlock, err := userLock(path + ".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	all, err := loadAccounts(path, false)
	if errors.Is(err, os.ErrNotExist) {
		all, err = make(accounts), nil
	}
	if err != nil {
		return err
	}
	previous, exists := all[username]
	if !grantsSet {
		if !exists {
			return errors.New("new users require an explicit nonempty database list")
		}
		databases = previous.databases
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return err
	}
	all[username] = account{username: username, salt: salt, key: key, databases: append([]string(nil), databases...)}
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
		if _, err := fmt.Fprintf(f, "%s:$pbkdf2-sha256$i=%d$%s$%s:%s\n", a.username, passwordIterations, base64.RawStdEncoding.EncodeToString(a.salt), base64.RawStdEncoding.EncodeToString(a.key), strings.Join(a.databases, ",")); err != nil {
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

func readPasswordPair(in *os.File, out io.Writer) (password string, err error) {
	input, restore, err := disablePasswordEcho(in)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, restore())
		if err != nil {
			password = ""
		}
	}()
	reader := bufio.NewReader(input)
	readLine := func(prompt string) (string, error) {
		if _, err := io.WriteString(out, prompt); err != nil {
			return "", err
		}
		line, err := reader.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && len(line) != 0) {
			return "", err
		}
		line = strings.TrimSuffix(line, "\n")
		if _, err := io.WriteString(out, "\n"); err != nil {
			return "", err
		}
		return line, validPassword(line)
	}
	first, err := readLine("Password: ")
	if err != nil {
		return "", err
	}
	second, err := readLine("Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match")
	}
	return first, nil
}
