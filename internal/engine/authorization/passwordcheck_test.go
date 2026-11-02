package authorization

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestPasswordCheckRules(t *testing.T) {
	for _, tc := range []struct { name, password string; minimum int32; message, detail string }{
		{"pc_user", "Abcdef1", 8, "password is too short", `password must be at least "passwordcheck.min_password_length" (8) bytes long`},
		{"pc_user", "pc_user", 8, "password is too short", `password must be at least "passwordcheck.min_password_length" (8) bytes long`},
		{"abcdefgh", "abcdefgh", 8, "password must not contain user name", ""},
		{"pc_user", "pc_user9!", 8, "password must not contain user name", ""},
		{"pc_user", "PC_USER9!", 8, "", ""},
		{"pc_user", "abcdefgh", 8, "password must contain both letters and nonletters", ""},
		{"pc_user", "12345678", 8, "password must contain both letters and nonletters", ""},
		{"pc_user", "abcdeé1", 8, "", ""},
		{"pc_user", "abcdé1", 8, "password is too short", `password must be at least "passwordcheck.min_password_length" (8) bytes long`},
		{"pc_user", "éééé", 8, "password must contain both letters and nonletters", ""},
		{"pc_user", "aééé1", 8, "", ""},
		{"pc_user", "", 0, "password must contain both letters and nonletters", ""},
		{"café", "café1234", 8, "password must not contain user name", ""},
		{"café", "café1234", 8, "", ""},
		{"pc_user", "Abcdef12", 12, "password is too short", `password must be at least "passwordcheck.min_password_length" (12) bytes long`},
	} {
		err := CheckRolePassword(t.Context(), tc.name, tc.password, tc.minimum)
		if tc.message == "" { if err != nil { t.Fatal(tc, err) }; continue }
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message, Detail: tc.detail}
		if !reflect.DeepEqual(err, want) { t.Fatalf("%#v: got %#v want %#v", tc, err, want) }
	}
}

func TestPasswordCheckVerifier(t *testing.T) {
	salt := []byte("0123456789abcdef")
	md5Secret := func(name, plain string) string { return fmt.Sprintf("md5%x", md5.Sum([]byte(plain+name))) }
	for _, tc := range []struct { name, prepared string }{{"pc_user", "pc_user"}, {"I\u00ADX", "IX"}, {"a\u00a0b", "a b"}, {"é\u0007", "é\u0007"}} {
		secret := passwordExpectedSecret(tc.prepared, salt, 4096)
		parts := strings.Split(secret, ":")
		// A different structurally valid StoredKey must not defeat ServerKey-only verification.
		parts[1] = strings.Split(parts[1], "$")[0]+"$"+base64.StdEncoding.EncodeToString(make([]byte, 32))
		for _, encoded := range []string{secret, strings.Join(parts, ":")} {
			err := CheckRolePassword(t.Context(), tc.name, encoded, 2147483647)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "password must not equal user name"}
			if !reflect.DeepEqual(err, want) { t.Fatal("username guess", tc.name, err) }
		}
		parts = strings.Split(secret, ":")
		parts[2] = base64.StdEncoding.EncodeToString(make([]byte, 32))
		if err := CheckRolePassword(t.Context(), tc.name, strings.Join(parts, ":"), 2147483647); err != nil { t.Fatal("StoredKey must not participate", err) }
	}
	if err := CheckRolePassword(t.Context(), "pc_user", md5Secret("pc_user", "pc_user"), 0); err == nil { t.Fatal("MD5 username accepted") }
	for _, secret := range []string{md5Secret("pc_user", "x"), passwordExpectedSecret("x", salt, 4096), passwordExpectedSecret("pc_user-extra", salt, 4096)} {
		if err := CheckRolePassword(t.Context(), "pc_user", secret, 2147483647); err != nil { t.Fatal("weak unequal encoded input rejected", err) }
		got, _, err := PrepareRolePassword(t.Context(), "pc_user", secret)
		if err != nil || got != secret { t.Fatal("encoded spelling changed", got, err) }
	}
	for _, n := range []string{"0", "-1", "+1", "0001"} {
		secret := strings.Replace(passwordExpectedSecret("pc_user", salt, 1), "$4096:", "$"+n+":", 1)
		if err := CheckRolePassword(t.Context(), "pc_user", secret, 0); err == nil { t.Fatal("release iteration classification", n) }
	}
	for _, secret := range []string{"md5bad", "MD5"+strings.Repeat("a", 32), "md5"+strings.Repeat("A", 32), "SCRAM-SHA-256$bad"} {
		if err := CheckRolePassword(t.Context(), "pc_user", secret, 2147483647); err == nil || err.Error() != "stuntgres: password is too short" { t.Fatal("malformed prehash was not plaintext", secret, err) }
	}
}

func TestPasswordCheckCancellation(t *testing.T) {
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &cancelAtErrCheck{Context: base, at: 5, cancel: cancel}
	secret := strings.Replace(passwordExpectedSecret("x", []byte("0123456789abcdef"), 1), "$4096:", "$2147483647:", 1)
	if err := CheckRolePassword(ctx, "pc_user", secret, 8); !errors.Is(err, context.Canceled) { t.Fatal("in-loop cancellation", err) }
	if err := CheckRolePassword(base, "pc_user", "Abcdef12", 8); !errors.Is(err, context.Canceled) { t.Fatal("precanceled plaintext", err) }
}
