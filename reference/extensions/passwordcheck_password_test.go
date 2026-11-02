package reference_test

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferencePasswordCheckNullAndEmpty(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			passwordCheckScenario(t, []string{"pc_null", "pc_null_create"}, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
				run := passwordCheckRun(t, p, c, r, prepared)
				*notices = nil
				run(fixture.Exec("CREATE ROLE pc_null_create PASSWORD NULL", 0))
				passwordCheckCredential(t, p, b, "pc_null_create", sql.NullString{})
				passwordCheckNotices(t, *notices)
				run(fixture.Exec("CREATE ROLE pc_null LOGIN", 0))
				passwordCheckCredential(t, p, b, "pc_null", sql.NullString{})
				stored := passwordCheckSCRAM(t, "role-attributes-fixture")
				run(fixture.Exec("ALTER ROLE pc_null PASSWORD "+pq.QuoteLiteral(stored), 0))
				run(fixture.Exec("ALTER USER pc_null CONNECTION LIMIT 2", 0))
				passwordCheckCredential(t, p, b, "pc_null", sql.NullString{String: stored, Valid: true})
				*notices = nil
				run(fixture.Exec("ALTER ROLE pc_null PASSWORD NULL", 0))
				passwordCheckCredential(t, p, b, "pc_null", sql.NullString{})
				passwordCheckNotices(t, *notices)
				for _, minimum := range []int{8, 0} {
					run(fixture.Exec(fmt.Sprintf("SET passwordcheck.min_password_length=%d", minimum), 0))
					*notices = nil
					query := "ALTER ROLE pc_null PASSWORD ''"
					if minimum == 8 { run(passwordCheckShort(query, 8)) } else { run(passwordCheckError(query, "password must contain both letters and nonletters", "")) }
					passwordCheckNotices(t, *notices)
					passwordCheckCredential(t, p, b, "pc_null", sql.NullString{})
				}
				run(fixture.Exec("RESET passwordcheck.min_password_length", 0))
				run(passwordCheckShort("ALTER ROLE pc_null PASSWORD 'NULL'", 8))
				for _, encoded := range []string{passwordCheckMD5("pc_null", ""), passwordCheckSCRAM(t, "")} {
					run(fixture.Exec("ALTER ROLE pc_null PASSWORD "+pq.QuoteLiteral(stored), 0))
					*notices = nil
					run(fixture.Exec("ALTER ROLE pc_null PASSWORD "+pq.QuoteLiteral(encoded), 0))
					passwordCheckNotices(t, *notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"})
					passwordCheckCredential(t, p, b, "pc_null", sql.NullString{})
				}
				*notices = nil
				md5 := passwordCheckMD5("pc_null", "x")
				run(fixture.Exec("ALTER ROLE pc_null PASSWORD "+pq.QuoteLiteral(md5), 0))
				passwordCheckNotices(t, *notices, passwordCheckMD5Notice())
				passwordCheckCredential(t, p, b, "pc_null", sql.NullString{String: md5, Valid: true})
				*notices = nil
				run(passwordCheckError("ALTER ROLE pc_null PASSWORD "+pq.QuoteLiteral(passwordCheckMD5("pc_null", "pc_null")), "password must not equal user name", ""))
				passwordCheckNotices(t, *notices)
				passwordCheckCredential(t, p, b, "pc_null", sql.NullString{String: md5, Valid: true})
			})
		})
	}
}

func TestReferencePasswordCheckPrehashed(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			passwordCheckScenario(t, []string{"pc_hash", "Pc\u00a0Hash"}, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
				run := passwordCheckRun(t, p, c, r, prepared)
				run(fixture.Exec("CREATE ROLE pc_hash", 0))
				run(fixture.Exec("SET passwordcheck.min_password_length=64", 0))
				for _, password := range []string{"pc_hash", "x", "pc_hash1"} {
					for _, kind := range []string{"MD5", "SCRAM"} {
						encoded := passwordCheckSCRAM(t, password)
						if kind == "MD5" { encoded = passwordCheckMD5("pc_hash", password) }
						query := "ALTER ROLE pc_hash PASSWORD "+pq.QuoteLiteral(encoded)
						*notices = nil
						if password == "pc_hash" {
							run(passwordCheckError(query, "password must not equal user name", ""))
							passwordCheckNotices(t, *notices)
						} else {
							run(fixture.Exec(query, 0))
							passwordCheckCredential(t, p, b, "pc_hash", sql.NullString{String: encoded, Valid: true})
							if kind == "MD5" { passwordCheckNotices(t, *notices, passwordCheckMD5Notice()) } else { passwordCheckNotices(t, *notices) }
						}
					}
				}
				// Candidate SASLprep maps NBSP to ASCII space, but preserves case.
				run(fixture.Exec("CREATE ROLE "+pq.QuoteIdentifier("Pc\u00a0Hash"), 0))
				for _, v := range []struct{ password string; rejected bool }{{"Pc Hash", true}, {"pc hash", false}} {
					encoded := passwordCheckSCRAM(t, v.password)
					query := "ALTER ROLE "+pq.QuoteIdentifier("Pc\u00a0Hash")+" PASSWORD "+pq.QuoteLiteral(encoded)
					if v.rejected { run(passwordCheckError(query, "password must not equal user name", "")) } else {
						run(fixture.Exec(query, 0))
						passwordCheckCredential(t, p, b, "Pc\u00a0Hash", sql.NullString{String: encoded, Valid: true})
					}
				}
				username := passwordCheckSCRAM(t, "pc_hash")
				parts := strings.Split(username, "$")
				keys := strings.Split(parts[2], ":")
				other := base64.StdEncoding.EncodeToString(make([]byte, 32))
				// Only ServerKey is checked. A valid but different StoredKey does
				// not bypass rejection; a different ServerKey does not match.
				changedStored := parts[0]+"$"+parts[1]+"$"+other+":"+keys[1]
				changedServer := parts[0]+"$"+parts[1]+"$"+keys[0]+":"+other
				*notices = nil
				run(passwordCheckError("ALTER ROLE pc_hash PASSWORD "+pq.QuoteLiteral(changedStored), "password must not equal user name", ""))
				passwordCheckNotices(t, *notices)
				run(fixture.Exec("ALTER ROLE pc_hash PASSWORD "+pq.QuoteLiteral(changedServer), 0))
				passwordCheckCredential(t, p, b, "pc_hash", sql.NullString{String: changedServer, Valid: true})
				run(fixture.Exec("RESET passwordcheck.min_password_length", 0))
				// These verifier-looking strings are plaintext, not accepted hashes.
				for _, value := range []string{"MD5"+strings.Repeat("a", 32), "md5"+strings.Repeat("A", 32), "md5"+strings.Repeat("g", 32), "md5broken", "SCRAM-SHA-256$broken"} {
					*notices = nil
					run(fixture.Exec("ALTER ROLE pc_hash PASSWORD "+pq.QuoteLiteral(value), 0))
					passwordCheckPlainCredential(t, p, b, "pc_hash", value)
					passwordCheckNotices(t, *notices)
				}
				run(passwordCheckShort("ALTER ROLE pc_hash PASSWORD 'md5bad'", 8))
				run(passwordCheckError("ALTER ROLE pc_hash PASSWORD 'SCRAMSHA'", "password must contain both letters and nonletters", ""))
			})
		})
	}
}
