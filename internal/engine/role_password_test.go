package engine

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

// Independent RFC5802 Hi, deliberately not using the production PBKDF2 or
// SASLprep helper. Prepared bytes below are manually specified expectations.
func passwordExpectedKeys(password string, salt []byte, n int) ([]byte, []byte) {
	mac := func(key, data []byte) []byte { h := hmac.New(sha256.New, key); h.Write(data); return h.Sum(nil) }
	block := make([]byte, len(salt)+4)
	copy(block, salt)
	binary.BigEndian.PutUint32(block[len(salt):], 1)
	u := mac([]byte(password), block)
	salted := append([]byte(nil), u...)
	for i := 1; i < n; i++ {
		u = mac([]byte(password), u)
		for j := range salted {
			salted[j] ^= u[j]
		}
	}
	client := mac(salted, []byte("Client Key"))
	stored := sha256.Sum256(client)
	return stored[:], mac(salted, []byte("Server Key"))
}
func passwordExpectedSecret(password string, salt []byte, n int) string {
	stored, server := passwordExpectedKeys(password, salt, n)
	return "SCRAM-SHA-256$4096:" + base64.StdEncoding.EncodeToString(salt) + "$" + base64.StdEncoding.EncodeToString(stored) + ":" + base64.StdEncoding.EncodeToString(server)
}
func assertPasswordKeys(t *testing.T, secret, prepared string) {
	t.Helper()
	parts := strings.Split(secret, "$")
	if len(parts) != 3 || parts[0] != "SCRAM-SHA-256" {
		t.Fatal("not a real SCRAM secret", secret)
	}
	meta := strings.Split(parts[1], ":")
	if len(meta) != 2 || meta[0] != "4096" {
		t.Fatal("default iterations", secret)
	}
	salt, err := base64.StdEncoding.DecodeString(meta[1])
	if err != nil || len(salt) != 16 {
		t.Fatal("salt", secret, err)
	}
	want := passwordExpectedSecret(prepared, salt, 4096)
	if secret != want {
		t.Fatalf("key math mismatch for bytes %x", prepared)
	}
}

type rolePasswordReader func([]byte) (int, error)

func (r rolePasswordReader) Read(p []byte) (int, error) { return r(p) }

func TestRoleAttributesPasswordStorage(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	d := roleAttributeCreate(t, e, op, "passwords", false, parser.RoleOptions{Present: parser.RolePassword, Password: "secret"})
	assertPasswordKeys(t, d.Password, "secret")
	for _, plain := range []string{"secret", "md5bad", "SCRAM-SHA-256$bad", "password"} {
		roleAttributeApply(t, e, op, roleAttributeAlter("passwords", parser.RoleOptions{Present: parser.RolePassword, Password: plain}))
		stored, _ := e.Roles().Lookup("passwords")
		assertPasswordKeys(t, stored.Password, plain)
	}
	md5Nonempty := md5.Sum([]byte("nonemptypasswords"))
	encodedMD5 := "md5" + hex.EncodeToString(md5Nonempty[:])
	md5Empty := md5.Sum([]byte("passwords"))
	salt := []byte("0123456789abcdef")
	encoded := passwordExpectedSecret("encoded", salt, 4096)
	empty := passwordExpectedSecret("", salt, 4096)
	cases := []struct {
		input, want string
		notice      authorization.PasswordNotice
	}{
		{encodedMD5, encodedMD5, authorization.PasswordMD5Notice}, {encoded, encoded, authorization.PasswordNoNotice},
		{"", "", authorization.PasswordEmptyNotice}, {"md5" + hex.EncodeToString(md5Empty[:]), "", authorization.PasswordEmptyNotice}, {empty, "", authorization.PasswordEmptyNotice},
		{strings.Replace(encoded, "$4096:", "$0:", 1), strings.Replace(encoded, "$4096:", "$0:", 1), authorization.PasswordNoNotice},
		{strings.Replace(encoded, "$4096:", "$-1:", 1), strings.Replace(encoded, "$4096:", "$-1:", 1), authorization.PasswordNoNotice},
		{strings.Replace(encoded, "$4096:", "$0004096:", 1), strings.Replace(encoded, "$4096:", "$0004096:", 1), authorization.PasswordNoNotice},
	}
	for _, tc := range cases {
		roleAttributeApply(t, e, op, roleAttributeAlter("passwords", parser.RoleOptions{Present: parser.RolePassword, Password: tc.input}))
		got, _ := e.Roles().Lookup("passwords")
		if got.Password != tc.want {
			t.Fatal("stored classification", got.Password, tc.want)
		}
		notices := op.DetachNotices()
		wantCount := 0
		if tc.notice != authorization.PasswordNoNotice {
			wantCount = 1
		}
		if len(notices) != wantCount {
			t.Fatal("notices", notices)
		}
		if wantCount == 1 {
			want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"}
			if tc.notice == authorization.PasswordMD5Notice {
				want = sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."}
			}
			if notices[0] != want {
				t.Fatalf("notice %#v want %#v", notices[0], want)
			}
		}
	}
	longSalt := []byte(strings.Repeat("s", 400))
	long := passwordExpectedSecret("x", longSalt, 4096)
	roleAttributeFailure(t, e, op, roleAttributeAlter("passwords", parser.RoleOptions{Present: parser.RolePassword, Password: long}), "54000")
	roleAttributeApply(t, e, op, roleAttributeAlter("passwords", parser.RoleOptions{Present: parser.RolePassword, PasswordNull: true}))
	got, _ := e.Roles().Lookup("passwords")
	if got.Password != "" || len(op.DetachNotices()) != 0 {
		t.Fatal("NULL password")
	}
	t.Run("random failure is atomic", func(t *testing.T) {
		original := rand.Reader
		defer func() { rand.Reader = original }()
		failure := errors.New("salt source unavailable")
		rand.Reader = rolePasswordReader(func(p []byte) (int, error) {
			if !e.Mu.TryLock() {
				t.Fatal("random I/O holds engine mutex")
			}
			e.Mu.Unlock()
			if !op.Session.Mu.TryLock() {
				t.Fatal("random I/O holds session mutex")
			}
			op.Session.Mu.Unlock()
			return 0, failure
		})
		beforePublication := e.RoleStore.Published
		next := e.RoleStore.NextPrincipal()
		_, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: "random_failure"}, Options: parser.RoleOptions{Present: parser.RolePassword, Password: "secret"}}, 0, nil)
		if !errors.Is(err, failure) || e.RoleStore.Published != beforePublication || e.RoleStore.NextPrincipal() != next {
			t.Fatal("random failure published role or allocator", err)
		}
	})
	t.Run("fresh authority after unlocked preparation", func(t *testing.T) {
		roleAttributeCreate(t, e, op, "preparing_actor", true, parser.RoleOptions{Present: parser.RoleCreateRole, CreateRole: true})
		actor, closeActor := roleOperation(t, e, "preparing_actor")
		defer closeActor()
		original := rand.Reader
		defer func() { rand.Reader = original }()
		rand.Reader = rolePasswordReader(func(p []byte) (int, error) {
			if !e.Mu.TryLock() {
				t.Fatal("password preparation holds engine mutex")
			}
			e.Mu.Unlock()
			if !actor.Session.Mu.TryLock() {
				t.Fatal("password preparation holds session mutex")
			}
			actor.Session.Mu.Unlock()
			roleAttributeApply(t, e, op, roleAttributeAlter("preparing_actor", parser.RoleOptions{Present: parser.RoleCreateRole}))
			for i := range p {
				p[i] = byte(i)
			}
			return len(p), nil
		})
		next := e.RoleStore.NextPrincipal()
		_, err := e.Execute(actor, &parser.CreateRole{Name: parser.Identifier{Text: "stale_authority"}, Options: parser.RoleOptions{Present: parser.RolePassword, Password: "secret"}}, 0, nil)
		state(t, err, "42501")
		if _, ok := e.Roles().Lookup("stale_authority"); ok || e.RoleStore.NextPrincipal() != next {
			t.Fatal("stale creator published role/edge/allocator")
		}
		if len(e.Roles().Memberships()) != 3 {
			t.Fatal("stale creator leaked ADMIN")
		}
	})
}

func TestRoleAttributesValidityInput(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	d := roleAttributeCreate(t, e, op, "expiry", true, parser.RoleOptions{})
	if d.HasValidUntil {
		t.Fatal("absent expiry is not NULL")
	}
	for _, tc := range []struct {
		text string
		want int64
	}{
		{"infinity", sqltype.TimestampPositiveInfinity}, {"-infinity", sqltype.TimestampNegativeInfinity},
		{"2000-01-01 00:00:00+00", 0}, {"2000-01-01 02:00:00+02", 0},
	} {
		roleAttributeApply(t, e, op, roleAttributeAlter("expiry", parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: tc.text}))
		got, _ := e.Roles().Lookup("expiry")
		if !got.HasValidUntil || got.ValidUntil != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, tc := range []struct{ text, code string }{{"bad", "22007"}, {"2025-02-30", "22008"}, {"2025-01-01 00:00:00+16", "22009"}} {
		before := e.RoleStore.Published
		_, err := e.Execute(op, roleAttributeAlter("expiry", parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: tc.text}), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Hint != "" || diagnostic.Position != "" || e.RoleStore.Published != before {
			t.Fatal(tc, err)
		}
	}
	// Trusted admission ignores absent passwords and expired password metadata.
	roleAttributeApply(t, e, op, roleAttributeAlter("expiry", parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: "2000-01-01"}))
	ref, _ := e.ResolveLogin("expiry")
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: ref})
	if err != nil {
		t.Fatal(err)
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	zone, err := sqltype.ResolveZone("Etc/GMT-2")
	if err != nil {
		t.Fatal(err)
	}
	captured := sqltype.InputEnvironment{Zone: zone, DateStyle: sqltype.DateStyle{Order: sqltype.DateDMY}, TimezoneAbbreviations: sqltype.TimezoneAbbreviationsAustralia, TransactionStart: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		text string
		want int64
	}{
		{"2000-01-01 02:00:00", 0}, {"02/01/2000 02:00:00", 86400000000}, {"2000-01-01 09:30 CST", 0}, {"now", 0},
	} {
		scope := &core.StatementScope{Op: op, Input: captured}
		if _, err := ExecuteRoleAttributes(e, scope, roleAttributeAlter("expiry", parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: tc.text}), 0); err != nil {
			t.Fatal(tc, err)
		}
		got, _ := e.Roles().Lookup("expiry")
		if !got.HasValidUntil || got.ValidUntil != tc.want {
			t.Fatal("captured input ignored", tc, got)
		}
	}
}
