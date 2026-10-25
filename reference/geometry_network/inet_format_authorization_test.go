package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Demand-frontier booleans are independently reasoned hypotheses until pinned
// oracle discovery. Every route keeps exact expected rows/full diagnostics.
func TestReferenceINETFormatAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nf_owner", "nf_reader"})
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE nf_owner", 0))
		run(fixture.Exec("CREATE ROLE nf_reader", 0))
		run(fixture.Exec("CREATE SCHEMA nf_auth AUTHORIZATION nf_owner", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
			if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA nf_auth CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("SET ROLE nf_owner", 0))
		run(fixture.Exec("CREATE TABLE nf_auth.source(id integer,ip inet,net cidr,ni inet,nc cidr)", 0))
		run(fixture.Exec("CREATE TABLE nf_auth.empty(id integer,ip inet,net cidr,ni inet,nc cidr)", 0))
		run(fixture.Exec("GRANT SELECT ON nf_auth.empty TO nf_reader", 0))
		run(fixture.Exec("INSERT INTO nf_auth.source VALUES(1,'192.168.1.5/24','10.1',NULL,NULL)", 1))
		run(fixture.Exec("GRANT SELECT ON nf_auth.source TO nf_reader", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA nf_auth TO nf_reader", 0))
		for _, tc := range []struct {
			name, sig, arg, nullarg, typ string
			want                         any
		}{
			{"abbrev", "inet", "ip", "ni", "TEXT", "192.168.1.5/24"}, {"abbrev", "cidr", "net", "nc", "TEXT", "10.1/16"}, {"host", "inet", "ip", "ni", "TEXT", "192.168.1.5"}, {"text", "inet", "ip", "ni", "TEXT", "192.168.1.5/24"}, {"family", "inet", "ip", "ni", "INT4", int64(4)}, {"masklen", "inet", "ip", "ni", "INT4", int64(24)}, {"broadcast", "inet", "ip", "ni", "INET", "192.168.1.255/24"}, {"inet_same_family", "inet,inet", "ip,INET '::1'", "ni,INET '::1'", "BOOL", false},
		} {
			t.Run(tc.name+"_"+tc.sig, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				target := "pg_catalog." + tc.name + "(" + tc.sig + ")"
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(p.Ctx, "REVOKE EXECUTE ON FUNCTION "+target+" FROM nf_reader"); err != nil {
						t.Error(err)
					}
				}()
				step := func(expr, tail string, want [][]any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM nf_auth.source"+tail, []string{"v"}, []string{tc.typ}, want, args...)
				}
				row := step(tc.name+"("+tc.arg+")", "", [][]any{{tc.want}})
				run(fixture.Exec("CREATE VIEW nf_auth.saved AS "+row.Query, 0))
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(p.Ctx, "DROP VIEW nf_auth.saved"); err != nil {
						t.Error(err)
					}
				}()
				run(fixture.Exec("GRANT SELECT ON nf_auth.saved TO nf_reader", 0))
				type authorityCase struct {
					s              fixture.Step
					need           bool
					pgWarmConstant bool
				}
				cases := []authorityCase{
					{s: row, need: true},
					{s: step(tc.name+"("+tc.nullarg+")", "", [][]any{{nil}}), need: true},
					{s: step(tc.name+"("+tc.arg+")", " WHERE FALSE", nil), need: true},
					{s: step(tc.name+"("+tc.arg+")", " WHERE id>99", nil), need: true},
					{s: step(tc.name+"("+tc.arg+")", " LIMIT 0", nil), need: true},
					{s: fixture.Select("SELECT v FROM nf_auth.saved", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}), need: true},
				}
				cases = append(cases, authorityCase{s: fixture.Select("SELECT "+tc.name+"("+tc.arg+") AS v FROM nf_auth.empty", []string{"v"}, []string{tc.typ}, nil), need: true})
				cases = append(cases, authorityCase{s: step("pg_catalog."+tc.name+"("+tc.arg+")", "", [][]any{{tc.want}}), need: true})
				base, typ, input := "ip", "inet", "192.168.1.5/24"
				if tc.sig == "cidr" {
					base, typ, input = "net", "cidr", "10.1"
				}
				parameter := "CASE WHEN id=1 THEN $1::" + typ + " ELSE " + base + " END"
				paramArgs := strings.Replace(tc.arg, base, parameter, 1)
				cases = append(cases, authorityCase{s: step(tc.name+"("+paramArgs+")", "", [][]any{{tc.want}}, input), need: true})
				runtimeArgs := strings.Replace(tc.arg, base, "$1::"+typ, 1)
				cases = append(cases, authorityCase{s: step(tc.name+"("+runtimeArgs+")", "", [][]any{{nil}}, nil)})
				constantArgs := strings.Replace(tc.arg, base, typ+" '"+input+"'", 1)
				// Pinned18.6 retains this folded constant on the same warmed PG
				// statement. Fresh direct/newly prepared calls still check EXECUTE.
				// Native analysis identity is not an executable-result cache.
				cases = append(cases, authorityCase{
					s:    fixture.Select("SELECT "+tc.name+"("+constantArgs+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}),
					need: true, pgWarmConstant: true,
				})
				if tc.name == "inet_same_family" {
					cases = append(cases, authorityCase{s: step("inet_same_family(ip,ni)", "", [][]any{{nil}}), need: true})
				}
				literal := "NULL::inet"
				if tc.sig == "cidr" {
					literal = "NULL::cidr"
				}
				if tc.name == "inet_same_family" {
					literal += ",INET '::1'"
				}
				cases = append(cases, authorityCase{s: step(tc.name+"("+literal+")", "", [][]any{{nil}})})
				if tc.name == "text" {
					cases = append(cases,
						authorityCase{s: step("ip::text", "", [][]any{{tc.want}}), need: true},
						authorityCase{s: step("CAST(ip AS text)", "", [][]any{{tc.want}}), need: true})
					for _, expr := range []string{"NULL::inet::text", "CAST(NULL::inet AS text)", "($1::inet)::text", "CAST($1::inet AS text)"} {
						args := []any(nil)
						if strings.Contains(expr, "$1") {
							args = []any{nil}
						}
						cases = append(cases, authorityCase{s: step(expr, "", [][]any{{nil}}, args...)})
					}
					cases = append(cases, authorityCase{s: step("text(net)", "", [][]any{{"10.1.0.0/16"}}), need: true})
				}
				held := make([]fixture.HeldStatement, len(cases))
				defer func() {
					for _, h := range held {
						if h.Statement != nil {
							if err := h.Statement.Close(); err != nil {
								t.Error(err)
							}
						}
					}
				}()
				run(fixture.Exec("SET ROLE nf_reader", 0))
				for i, cs := range cases {
					stmt, err := c.PrepareContext(p.Ctx, cs.s.Query)
					if err != nil {
						t.Fatal(err)
					}
					held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: cs.s.Query}
					fixture.RunStep(t, p.Ctx, held[i], b.Recorder, cs.s)
				}
				run(fixture.Exec("RESET ROLE", 0))
				for phase, allowed := range []bool{false, true, false, true} {
					if phase == 0 {
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
					} else if phase == 3 {
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
					} else if allowed {
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO nf_reader", 0))
					} else {
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM nf_reader", 0))
					}
					run(fixture.Exec("SET ROLE nf_reader", 0))
					for i, cs := range cases {
						s := cs.s
						if !allowed && cs.need {
							s = fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name}, s.Args...)
							s.SelectRows = true
							s.ErrorStage = "execute"
						}
						t.Run(fmt.Sprintf("phase_%d/case_%02d", phase, i), func(t *testing.T) {
							t.Run("direct", func(t *testing.T) {
								t.Logf("query=%s args=%#v", s.Query, s.Args)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							})
							t.Run("new_prepare", func(t *testing.T) {
								t.Logf("query=%s args=%#v", s.Query, s.Args)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
							})
							t.Run("same_warmed_statement", func(t *testing.T) {
								warm := s
								if b.PG && cs.pgWarmConstant {
									warm = cs.s
								}
								t.Logf("query=%s args=%#v", warm.Query, warm.Args)
								fixture.RunStep(t, p.Ctx, held[i], b.Recorder, warm)
							})
						})
					}
					run(fixture.Select("SELECT ip FROM nf_auth.source", []string{"ip"}, []string{"INET"}, [][]any{{"192.168.1.5/24"}}))
					run(fixture.Select("SELECT text(1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"1"}}))
					sibling, siblingWant := "abbrev(ip)", "192.168.1.5/24"
					if tc.name == "abbrev" && tc.sig == "inet" {
						sibling, siblingWant = "abbrev(net)", "10.1/16"
					}
					run(fixture.Select("SELECT "+sibling+" AS v FROM nf_auth.source", []string{"v"}, []string{"TEXT"}, [][]any{{siblingWant}}))
					run(fixture.Select("SELECT ip+0::bigint AS v FROM nf_auth.source", []string{"v"}, []string{"INET"}, [][]any{{"192.168.1.5/24"}}))
					run(fixture.Exec("RESET ROLE", 0))
				}
			})
		}
		run(fixture.Exec("REVOKE SELECT ON nf_auth.source FROM nf_reader", 0))
		run(fixture.Exec("SET ROLE nf_reader", 0))
		s := fixture.ImplicitError("SELECT host(ip) FROM nf_auth.source", fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT ON nf_auth.source TO nf_reader", 0))
	})
}
