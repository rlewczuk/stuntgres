package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func inetSubnetAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, d fixture.Diagnostic) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, d))
	s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
	s.ErrorStage = "prepare"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func TestReferenceINETSubnetDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, op := range inetSubnetOps {
				for _, input := range []string{"bad", "[::1]", "fe80::1%eth0", "1.2.3.4/33", "::/129"} {
					q := prefix + "SELECT INET '" + input + "' " + op + " INET '::/0'"
					d := inetDiagnostic("inet", input, false)
					d.Position = fixture.Position(13 + utf8.RuneCountInString(prefix))
					inetSubnetAnalysisError(t, p, b, q, d)
				}
				q := prefix + "SELECT CIDR '192.168.0.1/24' " + op + " INET '::/0'"
				d := inetDiagnostic("cidr", "192.168.0.1/24", true)
				d.Position = fixture.Position(13 + utf8.RuneCountInString(prefix))
				inetSubnetAnalysisError(t, p, b, q, d)
				for _, bad := range []struct{ expression, typ string }{{"1", "integer"}, {"'::1'::text", "text"}, {"ARRAY[1]", "integer[]"}, {"NULL::text", "text"}} {
					q := prefix + "SELECT INET '::1' " + op + " " + bad.expression
					d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: inet "+op+" "+bad.typ)
					d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, " "+op+" ")]) + 2)
					inetSubnetAnalysisError(t, p, b, q, d)
				}
			}
			for _, op := range []string{"<<", ">>", "&&"} {
				q := prefix + "SELECT '::1' " + op + " '::/0'"
				d := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown "+op+" unknown")
				d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
				d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, " "+op+" ")]) + 2)
				inetSubnetAnalysisError(t, p, b, q, d)
			}
			for _, op := range []string{"<<=", ">>="} {
				q := prefix + "SELECT 'bad' " + op + " '::/0'"
				d := inetDiagnostic("inet", "bad", false)
				d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
				inetSubnetAnalysisError(t, p, b, q, d)
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE subnet_infer(h INET)", 0))
		for _, op := range inetSubnetOps {
			q := "SELECT $1 FROM subnet_infer WHERE h " + op + " $1"
			d := fixture.ControlsReferenceDiagnostic("42P08", "inconsistent types deduced for parameter $1")
			d.Detail = "inet versus text"
			d.Position = "8"
			s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE subnet_lazy(h TEXT)", 0))
		for _, tail := range []string{"", " WHERE FALSE", " LIMIT 0"} {
			q := "SELECT h::inet << INET '::/0' AS v FROM subnet_lazy" + tail
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, nil))
			q = "SELECT INET 'bad' << NULL::inet FROM subnet_lazy" + tail
			d := inetDiagnostic("inet", "bad", false)
			d.Position = "13"
			inetSubnetAnalysisError(t, p, b, q, d)
		}
		for _, op := range inetSubnetOps {
			q := "SELECT CAST('bad'::text AS inet) " + op + " NULL::inet AS v"
			d := inetDiagnostic("inet", "bad", false)
			s := fixture.ErrorDiagnostic(q, d)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		// Ordinary context cancellation remains a native adapter contract.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := b.DB.QueryContext(ctx, inetSubnetQuery("$1::inet", "$2::inet"), "::1", "::/0")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query returned rows")
			}
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal(err)
			}
		}
	}
}

func TestReferenceINETSubnetAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE subnet_atomic(id INTEGER,h TEXT,v BOOL)", 0))
		run(fixture.Exec("INSERT INTO subnet_atomic VALUES(1,'::1',TRUE),(2,'bad',FALSE)", 2))
		run(fixture.Exec("CREATE TABLE subnet_target(id INTEGER,v BOOL)", 0))
		run(fixture.Exec("INSERT INTO subnet_target VALUES(9,TRUE)", 1))
		saved := fixture.Select("SELECT id,v FROM subnet_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(9), true}})
		for _, q := range []string{
			"INSERT INTO subnet_target SELECT id,h::inet << INET '::/0' FROM subnet_atomic ORDER BY id RETURNING id,v",
			"UPDATE subnet_atomic SET v=h::inet <<= INET '::/0' RETURNING id,v",
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, inetDiagnostic("inet", "bad", false))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				s.SelectRows = true
				run(s)
				run(saved)
				run(fixture.Select("SELECT id,v FROM subnet_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
			}
		}
		q := "INSERT INTO subnet_target VALUES(10,$1::inet && $2::inet)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		d := inetDiagnostic("inet", "bad", false)
		d.Context = "unnamed portal parameter $2 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, "::1", "bad"))
		run(saved)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, "::1", "::/0"))
		run(fixture.Select("SELECT id,v FROM subnet_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(9), true}, {int64(10), true}}))
	}
}
