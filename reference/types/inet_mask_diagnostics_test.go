package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func inetMaskProvider(message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22023", message)
}

func TestReferenceINETMaskDiagnostics(t *testing.T) {
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		errorBoth := func(t *testing.T, q string, d fixture.Diagnostic, args ...any) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d, args...)
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, typ := range []string{"inet", "cidr"} {
			for _, tc := range []struct {
				ip string
				n  int64
			}{
				{"192.168.1.0/24", -2}, {"192.168.1.0/24", 33}, {"192.168.1.0/24", -2147483648}, {"192.168.1.0/24", 2147483647},
				{"2001:db8::/64", -2}, {"2001:db8::/64", 129}, {"2001:db8::/64", -2147483648}, {"2001:db8::/64", 2147483647},
			} {
				t.Run(fmt.Sprintf("%s/%s/%d", typ, tc.ip, tc.n), func(t *testing.T) {
					d := inetMaskProvider(fmt.Sprintf("invalid mask length: %d", tc.n))
					// Hypothesis: correctly typed constants prepare quietly; providers fail
					// at Execute with all unmentioned diagnostic fields absent.
					errorBoth(t, fmt.Sprintf("SELECT set_masklen('%s'::%s,%d)", tc.ip, typ, tc.n), d)
					q := "SELECT set_masklen($1::" + typ + ",$2::integer) AS v"
					stmt, err := b.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := stmt.Close(); err != nil {
							t.Error(err)
						}
					}()
					held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
					s := fixture.ErrorDiagnostic(q, d, tc.ip, tc.n)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					result, dbtype := "192.168.1.0/24", "INET"
					if typ == "cidr" {
						dbtype = "CIDR"
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{dbtype}, [][]any{{result}}, "192.168.1.0/24", 24))
				})
			}
		}
		for _, ip := range []string{"::1", "::ffff:1.2.3.4"} {
			d := inetMaskProvider("cannot merge addresses from different families")
			errorBoth(t, "SELECT inet_merge(INET '1.2.3.4',INET '"+ip+"')", d)
			q := "SELECT inet_merge($1::inet,$2::inet) AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			s := fixture.ErrorDiagnostic(q, d, "1.2.3.4", ip)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"CIDR"}, [][]any{{"::/126"}}, "::1", "::2"))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			q, typ, input string
			host          bool
		}{
			{"SELECT hostmask('bad')", "inet", "bad", false}, {"SELECT netmask(INET 'bad')", "inet", "bad", false},
			{"SELECT network('192.168.0.1/24'::cidr)", "cidr", "192.168.0.1/24", true},
			{"SELECT set_masklen(CIDR 'bad',16)", "cidr", "bad", false}, {"SELECT inet_merge(INET '::1',INET 'bad')", "inet", "bad", false},
		} {
			d := inetDiagnostic(tc.typ, tc.input, tc.host)
			d.Position = fixture.Position(strings.Index(tc.q, "'"+tc.input+"'") + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		for _, expr := range []string{
			"set_masklen(CAST('bad'::text AS inet),NULL::integer)", "set_masklen(CAST('bad'::text AS cidr),NULL::integer)",
			"inet_merge(NULL::inet,CAST('bad'::text AS inet))", "inet_merge(CAST('bad'::text AS inet),NULL::inet)",
		} {
			typ := "inet"
			if strings.Contains(expr, "AS cidr") {
				typ = "cidr"
			}
			errorBoth(t, "SELECT "+expr, inetDiagnostic(typ, "bad", false))
		}
		for _, expr := range []string{
			"set_masklen(NULL::inet,CAST('bad'::text AS integer))",
			"set_masklen(NULL::cidr,CAST('bad'::text AS integer))",
		} {
			errorBoth(t, "SELECT "+expr, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`))
		}
		for _, tc := range []struct {
			expr string
			args []any
		}{
			{"inet_merge(NULL::inet,set_masklen(INET '::1',129))", nil},
			{"inet_merge($1::inet,set_masklen(INET '::1',129))", []any{nil}},
		} {
			errorBoth(t, "SELECT "+tc.expr, inetMaskProvider("invalid mask length: 129"), tc.args...)
		}
		run(fixture.Exec("CREATE TABLE nm_demand(ip inet,n integer)", 0))
		for _, tc := range []struct {
			expr, typ, msg string
			args           []any
		}{
			{"set_masklen(INET '1.2.3.4',33)", "INET", "invalid mask length: 33", nil},
			{"set_masklen($1::inet,$2::integer)", "INET", "invalid mask length: 33", []any{"1.2.3.4", 33}},
			{"inet_merge(INET '1.2.3.4',INET '::1')", "CIDR", "cannot merge addresses from different families", nil},
			{"inet_merge($1::inet,$2::inet)", "CIDR", "cannot merge addresses from different families", []any{"1.2.3.4", "::1"}},
		} {
			for _, tail := range []string{" WHERE FALSE", " FROM nm_demand", " LIMIT 0"} {
				t.Run(tc.expr+tail, func(t *testing.T) { errorBoth(t, "SELECT "+tc.expr+tail, inetMaskProvider(tc.msg), tc.args...) })
			}
		}
		run(fixture.Exec("INSERT INTO nm_demand VALUES('1.2.3.4',33)", 1))
		for _, tail := range []string{" WHERE FALSE", " WHERE n<0", " LIMIT 0"} {
			inetFormatBoth(t, p, b, fixture.Select("SELECT set_masklen(ip,n) AS v FROM nm_demand"+tail, []string{"v"}, []string{"INET"}, nil))
		}
		run(fixture.Exec("CREATE TABLE nm_atomic(id integer,ip inet,n integer)", 0))
		run(fixture.Exec("INSERT INTO nm_atomic VALUES(99,'1.2.3.4',24)", 1))
		for _, q := range []string{
			"INSERT INTO nm_atomic VALUES(1,set_masklen(INET '1.2.3.4',24),24),(2,set_masklen(INET '1.2.3.4',33),24)",
			"UPDATE nm_atomic SET ip=set_masklen(ip,33)",
			"INSERT INTO nm_atomic VALUES(1,inet_merge(INET '1.2.3.4',INET '1.2.3.5'),24),(2,inet_merge(INET '1.2.3.4',INET '::1'),24)",
		} {
			msg := "invalid mask length: 33"
			if strings.Contains(q, "inet_merge") {
				msg = "cannot merge addresses from different families"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, inetMaskProvider(msg))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				inetFormatBoth(t, p, b, fixture.Select("SELECT id,ip,n FROM nm_atomic", []string{"id", "ip", "n"}, []string{"INT4", "INET", "INT4"}, [][]any{{int64(99), "1.2.3.4", int64(24)}}))
			}
		}
	})
}
