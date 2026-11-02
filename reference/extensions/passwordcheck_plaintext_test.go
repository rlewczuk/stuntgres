package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferencePasswordCheckPlaintext(t *testing.T) {
	vectors := []struct{ label, role, password, message string }{
		{"short", "pc_user", "Abcdef1", "password is too short"},
		{"boundary", "pc_user", "Abcdef12", ""},
		{"length-before-name", "a", "a1", "password is too short"},
		{"name-before-variety", "pcuser", "pcuserabcdefgh", "password must not contain user name"},
		{"username", "pc_user", "pc_user9!", "password must not contain user name"},
		{"case-sensitive", "pc_user", "PC_USER9!", ""},
		{"letters", "pc_user", "abcdefgh", "password must contain both letters and nonletters"},
		{"nonletters", "pc_user", "12345678", "password must contain both letters and nonletters"},
		{"utf8-eight-bytes", "pc_user", "abcdeé1", ""},
		{"utf8-seven-bytes", "pc_user", "abcdé1", "password is too short"},
		{"utf8-not-ascii-alpha", "pc_user", "éééé", "password must contain both letters and nonletters"},
		{"utf8-variety", "pc_user", "aééé1", ""},
		{"unicode-substring", "café", "xxcafé19!", "password must not contain user name"},
		{"no-normalization", "café", "xxcafe\u030119!", ""},
		{"unicode-case-sensitive", "café", "xxCAFÉ19!", ""},
	}
	for _, prepared := range []bool{false, true} {
		for _, command := range []string{"CREATE ROLE", "CREATE USER", "ALTER ROLE", "ALTER USER"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", strings.ReplaceAll(command, " ", "_"), prepared), func(t *testing.T) {
				passwordCheckScenario(t, []string{"pc_user", "a", "pcuser", "café"}, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
					for _, v := range vectors {
						t.Run(v.label, func(t *testing.T) {
							name := pq.QuoteIdentifier(v.role)
							alter := strings.HasPrefix(command, "ALTER")
							run := passwordCheckRun(t, p, c, r, prepared)
							var before sql.NullString
							if alter {
								run(fixture.Exec("CREATE ROLE "+name+" PASSWORD "+pq.QuoteLiteral(passwordCheckSCRAM(t, "role-attributes-fixture")), 0))
								if b.PG { before = passwordCheckReadCredential(t, p, b, v.role) }
							}
							query := command+" "+name+" PASSWORD "+pq.QuoteLiteral(v.password)
							*notices = nil
							if v.message != "" {
								s := passwordCheckError(query, v.message, "")
								if v.message == "password is too short" { s = passwordCheckShort(query, 8) }
								run(s)
								if alter { passwordCheckCredential(t, p, b, v.role, before) }
							} else {
								run(fixture.Exec(query, 0))
								storedPassword := v.password
								// Strength sees original bytes; normal storage subsequently
								// SASLpreps this independently specified decomposed input.
								if v.label == "no-normalization" { storedPassword = "xxcafé19!" }
								passwordCheckPlainCredential(t, p, b, v.role, storedPassword)
							}
							passwordCheckNotices(t, *notices)
							present := alter || v.message == ""
							run(fixture.RoleTransactionExists(v.role, present))
							if present { run(fixture.Exec("DROP ROLE "+name, 0)) }
						})
					}
				})
			})
		}
	}
}
