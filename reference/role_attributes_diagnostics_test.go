package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceRoleAttributesDiagnosticsAndAtomicity(t *testing.T) {
	// Pinned PostgreSQL 18.0 outcomes; each case independently asserts its
	// diagnostic and true protocol Prepare/Execute boundary.
	type diagnosticCase struct {
		options, code, message, detail, hint, token string
		duplicate, parse, success                   bool
	}
	cases := []diagnosticCase{
		{options: "CONNECTION LIMIT -2", code: "22023", message: "invalid connection limit: -2"},
		{options: "VALID UNTIL 'bad'", code: "22007", message: `invalid input syntax for type timestamp with time zone: "bad"`},
		{options: "VALID UNTIL '2025-02-30'", code: "22008", message: `date/time field value out of range: "2025-02-30"`},
		{options: "VALID UNTIL '2025-01-01 00:00:00+16'", code: "22009", message: `time zone displacement out of range: "2025-01-01 00:00:00+16"`},
		{options: "CONNECTION LIMIT 2147483648", code: "42601", message: `syntax error at or near "2147483648"`, token: "2147483648", parse: true},
		{options: "CONNECTION LIMIT '1'", code: "42601", message: `syntax error at or near "'1'"`, token: "'1'", parse: true},
		{options: "CONNECTION LIMIT $1", code: "42601", message: `syntax error at or near "$1"`, token: "$1", parse: true},
		{options: "CONNECTION LIMIT 1+1", code: "42601", message: `syntax error at or near "+"`, token: "+", parse: true},
		{options: "VALID UNTIL NULL", code: "42601", message: `syntax error at or near "NULL"`, token: "NULL", parse: true},
		{options: "VALID UNTIL $1", code: "42601", message: `syntax error at or near "$1"`, token: "$1", parse: true},
		{options: `"login"`, success: true},
		{options: `"LOGIN"`, code: "42601", message: `unrecognized role option "LOGIN"`, token: `"LOGIN"`, parse: true},
		{options: "foo", code: "42601", message: `unrecognized role option "foo"`, token: "foo", parse: true},
		{options: "WITH", success: true},
		{options: "LOGIN trailing", code: "42601", message: `syntax error at or near "trailing"`, token: "trailing", parse: true},
		{options: "ENCRYPTED PASSWORD NULL", code: "42601", message: `syntax error at or near "NULL"`, token: "NULL", parse: true},
		{options: "UNENCRYPTED PASSWORD 'x'", code: "0A000", message: "UNENCRYPTED PASSWORD is no longer supported", hint: "Remove UNENCRYPTED to store the password in encrypted form instead.", token: "UNENCRYPTED", parse: true},
	}
	for _, pair := range []string{"SUPERUSER NOSUPERUSER", "CREATEDB NOCREATEDB", "CREATEROLE NOCREATEROLE", "LOGIN NOLOGIN", "INHERIT NOINHERIT", "REPLICATION NOREPLICATION", "BYPASSRLS NOBYPASSRLS", "CONNECTION LIMIT 1 CONNECTION LIMIT 2", "PASSWORD NULL PASSWORD 'x'", "VALID UNTIL 'infinity' VALID UNTIL '-infinity'"} {
		cases = append(cases, diagnosticCase{options: pair, code: "42601", message: "conflicting or redundant options", duplicate: true})
	}
	for _, prepared := range []bool{false, true} {
		roleAttributesPriorityQuestions(t, prepared)
		for _, alter := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/alter%v/prepared%v", tc.options, alter, prepared), func(t *testing.T) {
					roleReferenceCleanup(t, []string{"rag_target", "rag_absent"})
					p := newReferenceScenario(t)
					for _, b := range p.backends() {
						run := func(s referenceStep) { roleAttributesRun(t, p, b, b.db, prepared, s) }
						run(referenceExec("CREATE ROLE rag_target LOGIN CONNECTION LIMIT 7 PASSWORD NULL", 0))
						run(referenceExec("CREATE TABLE rag_sentinel(id INT)", 0))
						run(referenceExec("INSERT INTO rag_sentinel VALUES(7)", 1))
						query := "CREATE ROLE rag_absent " + tc.options
						if alter {
							query = "ALTER ROLE rag_target " + tc.options
						}
						if tc.success {
							run(referenceExec(query, 0))
						} else {
							stage := "execute"
							if prepared && tc.parse {
								stage = "prepare"
							}
							s := roleReferenceError(query, tc.code, tc.message, tc.detail, prepared, stage)
							s.diagnostic.Hint = tc.hint
							if tc.token != "" {
								s.diagnostic.Position = strconv.Itoa(strings.LastIndex(query, tc.token) + 1)
							}
							if tc.duplicate {
								fields := strings.Fields(tc.options)
								second := strings.LastIndex(query, fields[1])
								if strings.HasPrefix(tc.options, "CONNECTION") {
									second = strings.LastIndex(query, "CONNECTION")
								}
								if strings.HasPrefix(tc.options, "PASSWORD") {
									second = strings.LastIndex(query, "PASSWORD")
								}
								if strings.HasPrefix(tc.options, "VALID") {
									second = strings.LastIndex(query, "VALID")
								}
								s.diagnostic.Position = strconv.Itoa(second + 1)
							}
							run(s)
						}
						run(referenceSelect("SELECT id FROM rag_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if b.pg {
							want := roleAttributesDefaults(true)
							want[7] = int64(7)
							roleAttributesFacts(t, p, "rag_target", want)
							if !alter {
								if tc.success {
									roleAttributesFacts(t, p, "rag_absent", roleAttributesDefaults(tc.options == `"login"`))
								} else {
									var count int
									if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_roles WHERE rolname='rag_absent'").Scan(&count); err != nil || count != 0 {
										t.Errorf("failed CREATE leaked role: %d %v", count, err)
									}
								}
							}
						}
					}
				})
			}
		}
		for _, query := range []string{"ALTER ROLE rag_missing VALID UNTIL 'bad'", "ALTER ROLE pg_database_owner LOGIN", "ALTER ROLE stuntgres NOSUPERUSER"} {
			t.Run(fmt.Sprintf("precedence/%s/prepared%v", query, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					code, message, detail := "42704", `role "rag_missing" does not exist`, ""
					if strings.Contains(query, "pg_database_owner") {
						code, message, detail = "42939", `role name "pg_database_owner" is reserved`, "Cannot alter reserved roles."
					}
					if strings.Contains(query, "stuntgres") {
						code, message, detail = "0A000", "permission denied to alter role", "The bootstrap superuser must have the SUPERUSER attribute."
					}
					actualQuery := query
					if !b.pg && strings.Contains(query, "stuntgres") {
						actualQuery = strings.Replace(query, "stuntgres", "postgres", 1)
					}
					roleAttributesRun(t, p, b, b.db, prepared, roleReferenceError(actualQuery, code, message, detail, prepared, "execute"))
				}
			})
		}
	}
}
