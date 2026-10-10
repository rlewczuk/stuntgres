package reference_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// Expected keys come from RFC5802's equations and explicitly specified prepared
// bytes, not a native password helper or backend comparison.
func roleAttributesKeys(t *testing.T, password string, salt []byte, iterations int) ([]byte, []byte) {
	t.Helper()
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, salted)
	mac.Write([]byte("Client Key"))
	stored := sha256.Sum256(mac.Sum(nil))
	mac = hmac.New(sha256.New, salted)
	mac.Write([]byte("Server Key"))
	return stored[:], mac.Sum(nil)
}

func roleAttributesEncoded(t *testing.T, password string, salt []byte, iterations int) string {
	t.Helper()
	stored, server := roleAttributesKeys(t, password, salt, iterations)
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(stored), base64.StdEncoding.EncodeToString(server))
}

func roleAttributesCheckSCRAM(t *testing.T, verifier, preparedPassword string) {
	t.Helper()
	parts := strings.Split(verifier, "$")
	if len(parts) != 3 || parts[0] != "SCRAM-SHA-256" {
		t.Fatal("not SCRAM", verifier)
	}
	middle := strings.Split(parts[1], ":")
	keys := strings.Split(parts[2], ":")
	if len(middle) != 2 || len(keys) != 2 {
		t.Fatal("malformed SCRAM", verifier)
	}
	iterations, err := strconv.Atoi(middle[0])
	if err != nil || iterations != 4096 {
		t.Fatal("default iterations", middle[0], err)
	}
	salt, err := base64.StdEncoding.DecodeString(middle[1])
	if err != nil || len(salt) != 16 {
		t.Fatal("default salt", len(salt), err)
	}
	stored, server := roleAttributesKeys(t, preparedPassword, salt, iterations)
	for i, want := range [][]byte{stored, server} {
		got, err := base64.StdEncoding.DecodeString(keys[i])
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("independent SCRAM key %d mismatch for expected prepared bytes %q: %v", i, preparedPassword, err)
		}
	}
}

func TestReferenceRoleAttributesPasswordAndValidity(t *testing.T) {
	// REL_18_0 saslprep.c: ASCII fast path; mapped-input checks; failure uses raw.
	vectors := []struct{ label, input, prepared string }{
		{"ascii", "password", "password"},
		{"space", "a\u00a0b", "a b"},
		{"soft-hyphen", "a\u00adb", "ab"},
		{"compatibility", "\u2168", "IX"},
		{"overlap-zero-width-space", "a\u200bb", "a b"},
		{"ascii-control-fast-path", "a\x07b", "a\x07b"},
		{"nonascii-control-fallback", "\u00e9\x07", "\u00e9\x07"},
		{"prohibited-fallback", "a\ue000b", "a\ue000b"},
		{"unassigned-32-fallback", "a\u0221b", "a\u0221b"},
		{"bidi-fallback", "a\u0627", "a\u0627"},
		{"bidi-valid", "\u0627\u0628", "\u0627\u0628"},
		{"map-to-empty-fallback", "\u00ad", "\u00ad"},
		{"mapped-input-normalization", "\u00aa", "a"},
		{"mapped-input-prohibited-before-normalization", "\u0340", "\u0340"},
	}
	for _, prepared := range []bool{false, true} {
		roleAttributesValidityQuestions(t, prepared)
		for _, vector := range vectors {
			t.Run(fmt.Sprintf("SASLprep/%s/prepared%v", vector.label, prepared), func(t *testing.T) {
				roleReferenceCleanup(t, []string{"rap_vector"})
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					roleAttributesRun(t, p, b, b.db, prepared, referenceExec("CREATE ROLE rap_vector PASSWORD "+pq.QuoteLiteral(vector.input), 0))
					if b.pg {
						var verifier string
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='rap_vector'").Scan(&verifier); err != nil {
							t.Fatal(err)
						}
						roleAttributesCheckSCRAM(t, verifier, vector.prepared)
					}
				}
			})
		}
		t.Run(fmt.Sprintf("storage/prepared%v", prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"rap_store"})
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				var notices []referenceDiagnostic
				_, c, recorder := noticeReferencePool(t, p, b.pg, &notices)
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, recorder, s) }
				run(referenceExec("CREATE ROLE rap_store LOGIN PASSWORD 'role-attributes-fixture'", 0))
				read := func() sql.NullString {
					var v sql.NullString
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='rap_store'").Scan(&v); err != nil {
						t.Fatal(err)
					}
					return v
				}
				var before sql.NullString
				if b.pg {
					before = read()
					roleAttributesCheckSCRAM(t, before.String, "role-attributes-fixture")
				}
				run(referenceExec("ALTER ROLE rap_store CREATEDB", 0))
				if b.pg {
					if got := read(); got != before {
						t.Error("omitted password changed", got, before)
					}
				}
				salt := []byte("0123456789abcdef")
				encoded := roleAttributesEncoded(t, "fixed-password", salt, 4096)
				md5Password := fmt.Sprintf("md5%x", md5.Sum([]byte("fixed-passwordrap_store")))
				for _, value := range []string{encoded, md5Password} {
					notices = nil
					run(referenceExec("ALTER ROLE rap_store PASSWORD "+pq.QuoteLiteral(value), 0))
					if b.pg {
						if got := read(); !got.Valid || got.String != value {
							t.Errorf("encoded verifier not retained: %v", got)
						}
					}
					var want []referenceDiagnostic
					if value == md5Password {
						want = []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."}}
					}
					if !reflect.DeepEqual(notices, want) {
						t.Errorf("encoded password notices=%#v want=%#v", notices, want)
					}
				}
				for _, value := range []string{"md5broken", "SCRAM-SHA-256$broken"} {
					notices = nil
					run(referenceExec("ALTER ROLE rap_store ENCRYPTED PASSWORD "+pq.QuoteLiteral(value), 0))
					if b.pg {
						roleAttributesCheckSCRAM(t, read().String, value)
					}
					if len(notices) != 0 {
						t.Errorf("plaintext fallback notices=%#v", notices)
					}
				}
				emptyMD5 := fmt.Sprintf("md5%x", md5.Sum([]byte("rap_store")))
				for _, literal := range []string{"NULL", "''", pq.QuoteLiteral(emptyMD5), pq.QuoteLiteral(roleAttributesEncoded(t, "", salt, 4096))} {
					notices = nil
					run(referenceExec("ALTER ROLE rap_store PASSWORD "+literal, 0))
					if b.pg {
						if got := read(); got.Valid {
							t.Errorf("empty password not cleared: %v", got)
						}
					}
					var want []referenceDiagnostic
					if literal != "NULL" {
						want = []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"}}
					}
					if !reflect.DeepEqual(notices, want) {
						t.Errorf("empty password notices=%#v want=%#v", notices, want)
					}
				}
				long := roleAttributesEncoded(t, "long", bytes.Repeat([]byte("s"), 360), 4096)
				run(roleReferenceError("ALTER ROLE rap_store PASSWORD "+pq.QuoteLiteral(long), "54000", "encrypted password is too long", "Encrypted passwords must be no longer than 512 bytes.", prepared, "execute"))
				for _, tc := range []struct {
					value    string
					retained bool
				}{
					{strings.Replace(encoded, "$4096:", "$0:", 1), true},
					{strings.Replace(encoded, "$4096:", "$-1:", 1), true},
					{strings.Replace(encoded, "$4096:", "$0004096:", 1), true},
					{strings.TrimRight(encoded, "="), false},
					{strings.Replace(encoded, ":MDE", ":!MDE", 1), false},
				} {
					notices = nil
					run(referenceExec("ALTER ROLE rap_store PASSWORD "+pq.QuoteLiteral(tc.value), 0))
					if len(notices) != 0 {
						t.Errorf("verifier grammar notices=%#v", notices)
					}
					if b.pg {
						got := read()
						if tc.retained {
							if !got.Valid || got.String != tc.value {
								t.Errorf("encoded verifier not retained: %v want %q", got, tc.value)
							}
						} else {
							roleAttributesCheckSCRAM(t, got.String, tc.value)
						}
					}
				}
				for _, expiry := range []string{"infinity", "-infinity", "2030-01-02 03:04:05+00"} {
					run(referenceExec("ALTER USER rap_store VALID UNTIL "+pq.QuoteLiteral(expiry), 0))
					want := roleAttributesDefaults(true)
					want[1] = true
					want[8] = false
					want[9] = expiry
					if b.pg {
						roleAttributesFacts(t, p, "rap_store", want)
					}
				}
			}
		})
	}
}
