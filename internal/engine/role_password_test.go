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
		notice      passwordNotice
	}{
		{encodedMD5, encodedMD5, passwordMD5Notice}, {encoded, encoded, passwordNoNotice},
		{"", "", passwordEmptyNotice}, {"md5" + hex.EncodeToString(md5Empty[:]), "", passwordEmptyNotice}, {empty, "", passwordEmptyNotice},
		{strings.Replace(encoded, "$4096:", "$0:", 1), strings.Replace(encoded, "$4096:", "$0:", 1), passwordNoNotice},
		{strings.Replace(encoded, "$4096:", "$-1:", 1), strings.Replace(encoded, "$4096:", "$-1:", 1), passwordNoNotice},
		{strings.Replace(encoded, "$4096:", "$0004096:", 1), strings.Replace(encoded, "$4096:", "$0004096:", 1), passwordNoNotice},
	}
	for _, tc := range cases {
		result, notice, err := prepareRolePassword(context.Background(), "passwords", tc.input)
		if err != nil || result != tc.want || notice != tc.notice {
			t.Fatalf("classification %q: %q notice%d %v", tc.input, result, notice, err)
		}
		roleAttributeApply(t, e, op, roleAttributeAlter("passwords", parser.RoleOptions{Present: parser.RolePassword, Password: tc.input}))
		got, _ := e.Roles().Lookup("passwords")
		if got.Password != tc.want {
			t.Fatal("stored classification", got.Password, tc.want)
		}
		notices := op.DetachNotices()
		wantCount := 0
		if tc.notice != passwordNoNotice {
			wantCount = 1
		}
		if len(notices) != wantCount {
			t.Fatal("notices", notices)
		}
		if wantCount == 1 {
			want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"}
			if tc.notice == passwordMD5Notice {
				want = sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."}
			}
			if notices[0] != want {
				t.Fatalf("notice %#v want %#v", notices[0], want)
			}
		}
	}
	for _, bad := range []string{strings.TrimSuffix(encoded, "="), strings.Replace(encoded, base64.StdEncoding.EncodeToString(salt), "!!!!", 1)} {
		secret, notice, err := prepareRolePassword(context.Background(), "passwords", bad)
		if err != nil || notice != passwordNoNotice {
			t.Fatal(err, notice)
		}
		assertPasswordKeys(t, secret, bad)
	}
	longSalt := []byte(strings.Repeat("s", 400))
	long := passwordExpectedSecret("x", longSalt, 4096)
	roleAttributeFailure(t, e, op, roleAttributeAlter("passwords", parser.RoleOptions{Present: parser.RolePassword, Password: long}), "54000")
	roleAttributeApply(t, e, op, roleAttributeAlter("passwords", parser.RoleOptions{Present: parser.RolePassword, PasswordNull: true}))
	got, _ := e.Roles().Lookup("passwords")
	if got.Password != "" || len(op.DetachNotices()) != 0 {
		t.Fatal("NULL password")
	}
	// Release Hi computes U1 at nonpositive encoded counts, without Go panic.
	for _, n := range []string{"0", "-1"} {
		lowEmpty := passwordExpectedSecret("", salt, 1)
		lowEmpty = strings.Replace(lowEmpty, "$4096:", "$"+n+":", 1)
		got, notice, err := prepareRolePassword(context.Background(), "passwords", lowEmpty)
		if err != nil || got != "" || notice != passwordEmptyNotice {
			t.Fatal("nonpositive empty metadata", got, notice, err)
		}
	}
	t.Run("random failure is atomic", func(t *testing.T) {
		original := rand.Reader
		defer func() { rand.Reader = original }()
		failure := errors.New("salt source unavailable")
		rand.Reader = rolePasswordReader(func(p []byte) (int, error) {
			if !e.mu.TryLock() {
				t.Fatal("random I/O holds engine mutex")
			}
			e.mu.Unlock()
			if !op.session.mu.TryLock() {
				t.Fatal("random I/O holds session mutex")
			}
			op.session.mu.Unlock()
			return 0, failure
		})
		before := e.Roles()
		next := e.nextPrincipal
		_, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: "random_failure"}, Options: parser.RoleOptions{Present: parser.RolePassword, Password: "secret"}}, 0, nil)
		if !errors.Is(err, failure) || e.Roles().state != before.state || e.nextPrincipal != next {
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
			if !e.mu.TryLock() {
				t.Fatal("password preparation holds engine mutex")
			}
			e.mu.Unlock()
			if !actor.session.mu.TryLock() {
				t.Fatal("password preparation holds session mutex")
			}
			actor.session.mu.Unlock()
			roleAttributeApply(t, e, op, roleAttributeAlter("preparing_actor", parser.RoleOptions{Present: parser.RoleCreateRole}))
			for i := range p {
				p[i] = byte(i)
			}
			return len(p), nil
		})
		next := e.nextPrincipal
		_, err := e.Execute(actor, &parser.CreateRole{Name: parser.Identifier{Text: "stale_authority"}, Options: parser.RoleOptions{Present: parser.RolePassword, Password: "secret"}}, 0, nil)
		state(t, err, "42501")
		if _, ok := e.Roles().Lookup("stale_authority"); ok || e.nextPrincipal != next {
			t.Fatal("stale creator published role/edge/allocator")
		}
		if len(e.Roles().Memberships()) != 3 {
			t.Fatal("stale creator leaked ADMIN")
		}
	})
}

func TestRoleAttributesPasswordDerivationCancellation(t *testing.T) {
	salt := []byte("0123456789abcdef")
	for _, n := range []int{-1, 0, 1, 2, 17, 4096} {
		stored, server, err := roleSCRAMKeys(context.Background(), "prepared", salt, n)
		wantStored, wantServer := passwordExpectedKeys("prepared", salt, n)
		if err != nil || !hmac.Equal(stored, wantStored) || !hmac.Equal(server, wantServer) {
			t.Fatalf("independent Hi math count %d: %x %x %v", n, stored, server, err)
		}
	}
	for _, n := range []string{"1", "000001", "+1", "0", "-1"} {
		secret := strings.Replace(passwordExpectedSecret("retained", salt, 1), "$4096:", "$"+n+":", 1)
		got, notice, err := prepareRolePassword(context.Background(), "retained", secret)
		if err != nil || got != secret || notice != passwordNoNotice {
			t.Fatal("completed derivation changed valid verifier spelling", n, got, notice, err)
		}
	}
	allocations := func(n int) float64 {
		return testing.AllocsPerRun(10, func() {
			if _, _, err := roleSCRAMKeys(context.Background(), "prepared", salt, n); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Include the first Reset in both measurements: standard HMAC may
	// initialize its reusable marshaled hash state on that first reset.
	if short, long := allocations(2), allocations(4096); long != short {
		t.Fatalf("derivation allocations grow with iterations: two=%g 4096=%g", short, long)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Check 1 precedes U1; check 2 precedes U2; check 3 is
			// reached only after U2 has been computed and XORed.
			ctx := &cancelAtErrCheck{Context: base, at: 3}
			ctx.cancel = func() {
				if cause == context.DeadlineExceeded {
					expired, stop := context.WithDeadline(base, time.Unix(0, 0))
					stop()
					ctx.Context = expired
				} else {
					cancel()
				}
			}
			stored, server, err := roleSCRAMKeys(ctx, "", salt, 2147483647)
			if !errors.Is(err, cause) || stored != nil || server != nil || ctx.checks.Load() != 3 {
				t.Fatal("in-derivation cancellation returned keys or continued work", stored, server, err, ctx.checks.Load())
			}
		})
	}
	// The fixed4096 plaintext path uses the same cancellable math, not a
	// noninterruptible standard PBKDF2 call.
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtErrCheck{Context: base, at: 4, cancel: cancel}
	secret, notice, err := prepareRolePassword(ctx, "fixed", "plaintext")
	if !errors.Is(err, context.Canceled) || secret != "" || notice != passwordNoNotice || ctx.checks.Load() != 4 {
		t.Fatal("fixed-count cancellation", secret, notice, err)
	}
}

func TestRoleAttributesPasswordSASLprep(t *testing.T) {
	cases := []struct{ input, prepared string }{
		{"I\u00ADX", "IX"}, {"a\u00A0b", "a b"}, {"a\u200Bb", "a b"}, {"\u2168", "IX"},
		{"a\u0007", "a\u0007"}, // REL_18_0 pure-ASCII fast path
		{"\u00E9\u0007", "\u00E9\u0007"}, {"\u0221", "\u0221"}, {"\u00AD", "\u00AD"},
		{"\u0627\u0628", "\u0627\u0628"}, {"\u0627A\u0628", "\u0627A\u0628"},
		{"\u06271", "\u06271"}, {"1\u0627", "1\u0627"}, {"\u0340", "\u0340"},
		{string([]byte{0xff, 0xfe}), string([]byte{0xff, 0xfe})}, {"\u00E9\uE000", "\u00E9\uE000"},
	}
	for _, tc := range cases {
		secret, notice, err := prepareRolePassword(context.Background(), "sasl", tc.input)
		if err != nil || notice != passwordNoNotice {
			t.Fatal(err, notice)
		}
		assertPasswordKeys(t, secret, tc.prepared)
	}
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
		before := e.Roles().state
		_, err := e.Execute(op, roleAttributeAlter("expiry", parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: tc.text}), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Hint != "" || diagnostic.Position != "" || e.Roles().state != before {
			t.Fatal(tc, err)
		}
	}
	// Trusted admission ignores absent passwords and expired password metadata.
	roleAttributeApply(t, e, op, roleAttributeAlter("expiry", parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: "2000-01-01"}))
	ref, _ := e.ResolveLogin("expiry")
	s, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(testSessionID.Add(1)), Authenticated: ref})
	if err != nil {
		t.Fatal(err)
	}
	s.RequestEnd(CleanClose)
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
		scope := &StatementScope{op: op, input: captured}
		if _, err := e.executeRoleAttributes(scope, roleAttributeAlter("expiry", parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: tc.text}), 0); err != nil {
			t.Fatal(tc, err)
		}
		got, _ := e.Roles().Lookup("expiry")
		if !got.HasValidUntil || got.ValidUntil != tc.want {
			t.Fatal("captured input ignored", tc, got)
		}
	}
}
