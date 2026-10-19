package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETMaskAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nm_owner", "nm_reader"})
	fixture.SchemaReferenceCleanup(t, "nm_auth")
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE nm_owner", 0))
		run(fixture.Exec("CREATE ROLE nm_reader", 0))
		run(fixture.Exec("CREATE SCHEMA nm_auth AUTHORIZATION nm_owner", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("SET ROLE nm_owner", 0))
		run(fixture.Exec("CREATE TABLE nm_auth.source(id integer,ip inet,net cidr,ni inet,nc cidr,n integer,nn integer)", 0))
		run(fixture.Exec("CREATE TABLE nm_auth.empty(id integer,ip inet,net cidr,ni inet,nc cidr,n integer,nn integer)", 0))
		run(fixture.Exec("INSERT INTO nm_auth.source VALUES(1,'192.168.1.5/24','192.168.1.0/24',NULL,NULL,16,NULL)", 1))
		run(fixture.Exec("GRANT SELECT ON nm_auth.source,nm_auth.empty TO nm_reader", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA nm_auth TO nm_reader", 0))
		for _, tc := range []struct {
			name, sig, args, nullargs, typ string
			want                           any
		}{
			{"hostmask", "inet", "ip", "ni", "INET", "0.0.0.255"}, {"netmask", "inet", "ip", "ni", "INET", "255.255.255.0"}, {"network", "inet", "ip", "ni", "CIDR", "192.168.1.0/24"},
			{"inet_merge", "inet,inet", "ip,INET '192.168.2.5/24'", "ni,INET '192.168.2.5/24'", "CIDR", "192.168.0.0/22"},
			{"set_masklen", "inet,integer", "ip,n", "ni,n", "INET", "192.168.1.5/16"}, {"set_masklen", "cidr,integer", "net,n", "nc,n", "CIDR", "192.168.0.0/16"},
		} {
			t.Run(tc.name+"_"+tc.sig, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				target := "pg_catalog." + tc.name + "(" + tc.sig + ")"
				defer func() {
					for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION " + target + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + target + " FROM nm_reader", "DROP VIEW IF EXISTS nm_auth.saved"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				step := func(expr, tail string, w [][]any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM nm_auth.source"+tail, []string{"v"}, []string{tc.typ}, w, args...)
				}
				row := step(tc.name+"("+tc.args+")", "", [][]any{{tc.want}})
				run(fixture.Exec("CREATE VIEW nm_auth.saved AS "+row.Query, 0))
				run(fixture.Exec("GRANT SELECT ON nm_auth.saved TO nm_reader", 0))
				type frontier struct {
					s            fixture.Step
					need         bool
					warmConstant bool
				}
				cases := []frontier{
					{row, true, false}, {step(tc.name+"("+tc.nullargs+")", "", [][]any{{nil}}), true, false},
					{step(tc.name+"("+tc.args+")", " WHERE FALSE", nil), true, false}, {step(tc.name+"("+tc.args+")", " WHERE id>99", nil), true, false},
					{step(tc.name+"("+tc.args+")", " LIMIT 0", nil), true, false},
					{fixture.Select("SELECT "+tc.name+"("+tc.args+") AS v FROM nm_auth.empty", []string{"v"}, []string{tc.typ}, nil), true, false},
					{step("pg_catalog."+tc.name+"("+tc.args+")", "", [][]any{{tc.want}}), true, false},
					{fixture.Select("SELECT v FROM nm_auth.saved", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}), true, false},
				}
				base, typ, input := "ip", "inet", "192.168.1.5/24"
				if strings.HasPrefix(tc.sig, "cidr") {
					base, typ, input = "net", "cidr", "192.168.1.0/24"
				}
				runtime := strings.Replace(tc.args, base, "$1::"+typ, 1)
				literal := strings.Replace(tc.args, base, "NULL::"+typ, 1)
				constant := strings.Replace(tc.args, base, typ+" '"+input+"'", 1)
				constant = strings.ReplaceAll(constant, ",n", ",16")
				cases = append(cases, frontier{step(tc.name+"("+runtime+")", "", [][]any{{nil}}, nil), false, false}, frontier{step(tc.name+"("+literal+")", "", [][]any{{nil}}), false, false})
				// Pinned18.6 discovery observed all six constant calls survive revocation
				// only on the same already-warmed PG statement. Direct/new calls still
				// check EXECUTE; native analysis identities do not cache executable results.
				cases = append(cases, frontier{fixture.Select("SELECT "+tc.name+"("+constant+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}), true, true})
				param := strings.Replace(tc.args, base, "CASE WHEN id=1 THEN $1::"+typ+" ELSE "+base+" END", 1)
				cases = append(cases, frontier{step(tc.name+"("+param+")", "", [][]any{{tc.want}}, input), true, false})
				if tc.name == "inet_merge" {
					cases = append(cases, frontier{step("inet_merge(ip,ni)", "", [][]any{{nil}}), true, false})
				}
				if tc.name == "set_masklen" {
					cases = append(cases, frontier{step("set_masklen("+base+",nn)", "", [][]any{{nil}}), true, false}, frontier{step("set_masklen("+base+",$1::integer)", "", [][]any{{nil}}, nil), false, false})
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
				run(fixture.Exec("SET ROLE nm_reader", 0))
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
					q := "REVOKE EXECUTE ON FUNCTION " + target + " FROM PUBLIC"
					if phase == 1 {
						q = "GRANT EXECUTE ON FUNCTION " + target + " TO nm_reader"
					}
					if phase == 2 {
						q = "REVOKE EXECUTE ON FUNCTION " + target + " FROM nm_reader"
					}
					if phase == 3 {
						q = "GRANT EXECUTE ON FUNCTION " + target + " TO PUBLIC"
					}
					run(fixture.Exec(q, 0))
					run(fixture.Exec("SET ROLE nm_reader", 0))
					for i, cs := range cases {
						t.Run(fmt.Sprintf("phase_%d/case_%02d", phase, i), func(t *testing.T) {
							s := cs.s
							if !allowed && cs.need {
								s = fixture.ErrorDiagnostic(s.Query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+tc.name), s.Args...)
								s.SelectRows = true
								s.ErrorStage = "execute"
							}
							t.Run("direct", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) })
							t.Run("new_prepare", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s)) })
							t.Run("same_warmed_statement", func(t *testing.T) {
								warm := s
								if b.PG && cs.warmConstant {
									warm = cs.s
								}
								fixture.RunStep(t, p.Ctx, held[i], b.Recorder, warm)
							})
						})
					}
					run(fixture.Select("SELECT host(ip) AS h FROM nm_auth.source", []string{"h"}, []string{"TEXT"}, [][]any{{"192.168.1.5"}}))
					if tc.name == "set_masklen" {
						sibling, sibtype, w := "set_masklen(net,16)", "CIDR", "192.168.0.0/16"
						if typ == "cidr" {
							sibling, sibtype, w = "set_masklen(ip,16)", "INET", "192.168.1.5/16"
						}
						run(fixture.Select("SELECT "+sibling+" AS v FROM nm_auth.source", []string{"v"}, []string{sibtype}, [][]any{{w}}))
					}
					run(fixture.Exec("RESET ROLE", 0))
				}
			})
		}
		run(fixture.Exec("REVOKE SELECT ON nm_auth.source FROM nm_reader", 0))
		run(fixture.Exec("SET ROLE nm_reader", 0))
		s := fixture.ErrorDiagnostic("SELECT hostmask(ip) FROM nm_auth.source", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table source"))
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("RESET ROLE", 0))
	})
}
