package authorization

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

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
		got, notice, err := PrepareRolePassword(context.Background(), "retained", secret)
		if err != nil || got != secret || notice != PasswordNoNotice {
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
	secret, notice, err := PrepareRolePassword(ctx, "fixed", "plaintext")
	if !errors.Is(err, context.Canceled) || secret != "" || notice != PasswordNoNotice || ctx.checks.Load() != 4 {
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
		secret, notice, err := PrepareRolePassword(context.Background(), "sasl", tc.input)
		if err != nil || notice != PasswordNoNotice {
			t.Fatal(err, notice)
		}
		assertPasswordKeys(t, secret, tc.prepared)
	}
}

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

func TestRoleAttributesPasswordVerifiers(t *testing.T) {
	md5Nonempty := md5.Sum([]byte("nonemptypasswords"))
	encodedMD5 := "md5" + hex.EncodeToString(md5Nonempty[:])
	md5Empty := md5.Sum([]byte("passwords"))
	salt := []byte("0123456789abcdef")
	encoded := passwordExpectedSecret("encoded", salt, 4096)
	empty := passwordExpectedSecret("", salt, 4096)
	cases := []struct {
		input, want string
		notice      PasswordNotice
	}{
		{encodedMD5, encodedMD5, PasswordMD5Notice}, {encoded, encoded, PasswordNoNotice},
		{"", "", PasswordEmptyNotice}, {"md5" + hex.EncodeToString(md5Empty[:]), "", PasswordEmptyNotice}, {empty, "", PasswordEmptyNotice},
		{strings.Replace(encoded, "$4096:", "$0:", 1), strings.Replace(encoded, "$4096:", "$0:", 1), PasswordNoNotice},
		{strings.Replace(encoded, "$4096:", "$-1:", 1), strings.Replace(encoded, "$4096:", "$-1:", 1), PasswordNoNotice},
		{strings.Replace(encoded, "$4096:", "$0004096:", 1), strings.Replace(encoded, "$4096:", "$0004096:", 1), PasswordNoNotice},
	}
	for _, tc := range cases {
		result, notice, err := PrepareRolePassword(context.Background(), "passwords", tc.input)
		if err != nil || result != tc.want || notice != tc.notice {
			t.Fatalf("classification %q: %q notice%d %v", tc.input, result, notice, err)
		}
	}
	for _, bad := range []string{strings.TrimSuffix(encoded, "="), strings.Replace(encoded, base64.StdEncoding.EncodeToString(salt), "!!!!", 1)} {
		secret, notice, err := PrepareRolePassword(context.Background(), "passwords", bad)
		if err != nil || notice != PasswordNoNotice {
			t.Fatal(err, notice)
		}
		assertPasswordKeys(t, secret, bad)
	}
	// Release Hi computes U1 at nonpositive encoded counts, without Go panic.
	for _, n := range []string{"0", "-1"} {
		lowEmpty := passwordExpectedSecret("", salt, 1)
		lowEmpty = strings.Replace(lowEmpty, "$4096:", "$"+n+":", 1)
		got, notice, err := PrepareRolePassword(context.Background(), "passwords", lowEmpty)
		if err != nil || got != "" || notice != PasswordEmptyNotice {
			t.Fatal("nonpositive empty metadata", got, notice, err)
		}
	}
}
