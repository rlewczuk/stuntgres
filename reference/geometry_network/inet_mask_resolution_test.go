package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceINETMaskResolution(t *testing.T) {
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, tc := range []struct{ name, args, typ, w string }{
			{"hostmask", "CIDR '192.168.1.0/24'", "INET", "0.0.0.255"}, {"netmask", "CIDR '192.168.1.0/24'", "INET", "255.255.255.0"},
			{"network", "CIDR '192.168.1.0/24'", "CIDR", "192.168.1.0/24"}, {"inet_merge", "CIDR '192.168.1.0/24',INET '192.168.2.5/24'", "CIDR", "192.168.0.0/22"},
			{"set_masklen", "INET '192.168.1.5/24',16::smallint", "INET", "192.168.1.5/16"}, {"set_masklen", "CIDR '192.168.1.0/24',16::smallint", "CIDR", "192.168.0.0/16"},
		} {
			for _, name := range []string{tc.name, "pg_catalog." + tc.name, `"` + tc.name + `"`, `pg_catalog."` + tc.name + `"`} {
				inetFormatBoth(t, p, b, fixture.Select("SELECT "+name+"("+tc.args+") AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.w}}))
			}
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN nm_i AS inet", 0), fixture.Exec("CREATE DOMAIN nm_ii AS nm_i", 0), fixture.Exec("CREATE DOMAIN nm_c AS cidr", 0), fixture.Exec("CREATE DOMAIN nm_cc AS nm_c", 0),
			fixture.Exec("CREATE TABLE nm_names(hostmask integer,netmask integer,network integer,inet_merge integer,set_masklen integer)", 0), fixture.Exec("INSERT INTO nm_names VALUES(1,2,3,4,5)", 1),
		} {
			run(s)
		}
		inetFormatBoth(t, p, b, fixture.Select("SELECT hostmask,netmask,network,inet_merge,set_masklen FROM nm_names", []string{"hostmask", "netmask", "network", "inet_merge", "set_masklen"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3), int64(4), int64(5)}}))
		inetFormatBoth(t, p, b, fixture.Select("SELECT hostmask('192.168.1.5/24'::nm_ii) AS hm,netmask('192.168.1.0/24'::nm_cc) AS nm,network('192.168.1.5/24'::nm_ii) AS n,set_masklen('192.168.1.5/24'::nm_ii,16) AS si,set_masklen('192.168.1.0/24'::nm_cc,16) AS sc,inet_merge('192.168.1.0/24'::nm_cc,'192.168.2.5/24'::nm_ii) AS m", inetMaskNames, inetMaskTypes, [][]any{{"0.0.0.255", "255.255.255.0", "192.168.1.0/24", "192.168.1.5/16", "192.168.0.0/16", "192.168.0.0/22"}}))
		for _, tc := range []struct {
			q    string
			args []any
			w    any
		}{
			{"SELECT set_masklen('192.168.1.5/24',16) AS v", nil, "192.168.1.5/16"},
			{"SELECT set_masklen($1,$2) AS v", []any{"192.168.1.5/24", 16}, "192.168.1.5/16"},
			{"SELECT set_masklen(NULL,NULL) AS v", nil, nil},
		} {
			inetFormatBoth(t, p, b, fixture.Select(tc.q, []string{"v"}, []string{"INET"}, [][]any{{tc.w}}, tc.args...))
		}
		// Hold the genuinely unconstrained statement on both backends. The
		// INET result/retained host bits and -1 boundary prove its selected
		// overload; the INTEGER bind accepts textual16 and recovers after NULL.
		q := "SELECT set_masklen($1,$2) AS v"
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
		for _, tc := range []struct {
			args []any
			want any
		}{
			{[]any{"192.168.1.5/24", 16}, "192.168.1.5/16"},
			{[]any{"192.168.1.5/24", "16"}, "192.168.1.5/16"},
			{[]any{nil, nil}, nil},
			{[]any{"192.168.1.5/24", -1}, "192.168.1.5"},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INET"}, [][]any{{tc.want}}, tc.args...))
		}
		if b.PG {
			// Native does not expose pg_prepared_statements. This supplemental
			// oracle vector must share the actual SQL PREPARE physical session.
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			}()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE nm_infer AS SELECT set_masklen($1,$2)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT parameter_types::text AS p FROM pg_prepared_statements WHERE name='nm_infer'", []string{"p"}, []string{"TEXT"}, [][]any{{"{inet,integer}"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DEALLOCATE nm_infer", 0))
		}
		inetMaskSyntax(t, p, b)
	})
}

func inetMaskAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, q, code, message, hint, at string) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Hint = hint
	pos := strings.Index(q, at)
	if pos < 0 {
		t.Fatal("missing diagnostic anchor", q, at)
	}
	d.Position = fixture.Position(utf8.RuneCountInString(q[:pos]) + 1)
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d)
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func inetMaskSyntax(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ name, args, sig string }{
		{"hostmask", "1", "integer"}, {"netmask", "TRUE", "boolean"}, {"network", "'x'::text", "text"}, {"inet_merge", "INET '::1',TRUE", "inet, boolean"},
		{"set_masklen", "INET '::1',1::bigint", "inet, bigint"}, {"set_masklen", "CIDR '::/0',1::numeric", "cidr, numeric"}, {"set_masklen", "INET '::1','1'::text", "inet, text"},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			inetMaskAnalysisError(t, p, b, prefix+"SELECT "+tc.name+"("+tc.args+")", "42883", "function "+tc.name+"("+tc.sig+") does not exist", hint, tc.name)
		}
	}
	for _, tc := range []struct{ name, args, sig string }{{"hostmask", "INET '::1'", "inet"}, {"netmask", "INET '::1'", "inet"}, {"network", "INET '::1'", "inet"}, {"inet_merge", "INET '::1',INET '::2'", "inet, inet"}, {"set_masklen", "INET '::1',64", "inet, integer"}} {
		for _, name := range []string{"public." + tc.name, `"` + strings.ToUpper(tc.name) + `"`} {
			label := strings.Trim(name, `"`)
			inetMaskAnalysisError(t, p, b, "SELECT "+name+"("+tc.args+")", "42883", "function "+label+"("+tc.sig+") does not exist", hint, name)
		}
		for _, bad := range []struct{ args, sig string }{{"", ""}, {"NULL::inet,NULL::inet,NULL::inet", "inet, inet, inet"}, {"x => NULL::inet", "x => inet"}} {
			inetMaskAnalysisError(t, p, b, "SELECT "+tc.name+"("+bad.args+")", "42883", "function "+tc.name+"("+bad.sig+") does not exist", hint, tc.name)
		}
		inetMaskAnalysisError(t, p, b, "SELECT "+tc.name+"(x => NULL::inet,x => NULL::inet)", "42601", `argument name "x" used more than once`, "", "x => NULL::inet)")
		inetMaskAnalysisError(t, p, b, "SELECT "+tc.name+"(x => NULL::inet,NULL::inet)", "42601", "positional argument cannot follow named argument", "", "NULL::inet)")
		inetMaskAnalysisError(t, p, b, "SELECT "+tc.name+"("+strings.TrimSuffix(strings.Repeat("NULL::inet,", 101), ",")+")", "54023", "cannot pass more than 100 arguments to a function", "", tc.name)
		for _, dec := range []struct{ args, tail, label, kind string }{
			{"DISTINCT " + tc.args, "", "DISTINCT", "an aggregate function"}, {tc.args + " ORDER BY 1", "", "ORDER BY", "an aggregate function"}, {tc.args, " FILTER (WHERE TRUE)", "FILTER", "an aggregate function"}, {tc.args, " OVER ()", "OVER", "a window function nor an aggregate function"},
		} {
			inetMaskAnalysisError(t, p, b, "SELECT "+tc.name+"("+dec.args+")"+dec.tail, "42809", dec.label+" specified, but "+tc.name+" is not "+dec.kind, "", tc.name)
		}
	}
	// Parameter inference belongs to Prepare, not a simple query with unbound $1.
	q := "SELECT set_masklen($1,$2),set_masklen($2,$1)"
	d := fixture.ControlsReferenceDiagnostic("42883", "function set_masklen(integer, inet) does not exist")
	d.Hint = hint
	d.Position = fixture.Position(strings.Index(q, "set_masklen($2") + 1)
	s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
	s.SelectRows = true
	s.ErrorStage = "prepare"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}
