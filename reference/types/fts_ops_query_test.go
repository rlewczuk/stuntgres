package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsOpsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func ftsOpsValue(t *testing.T, p *fixture.Pair, b fixture.Backend, expression, typ string, want any, args ...any) {
	t.Helper()
	s := fixture.Select("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{want}}, args...)
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func ftsOpsDistanceError() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22023", "distance in phrase operator must be an integer value between zero and 16384 inclusive")
}

func TestReferenceFTSOpsQueryAlgebra(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ expression, want string }{
			{"'fat | rat'::tsquery && 'cat'::tsquery", "( 'fat' | 'rat' ) & 'cat'"},
			{"'fat | rat'::tsquery || 'cat'::tsquery", "'fat' | 'rat' | 'cat'"},
			{"!!'cat'::tsquery", "!'cat'"},
			{"'foo & bar'::tsquery || !!'asd & fg'::tsquery", "'foo' & 'bar' | !( 'asd' & 'fg' )"},
			{"'a'::tsquery && 'a'::tsquery", "'a' & 'a'"},
			{"!!'!a'::tsquery", "!!'a'"},
			{"'a:*A'::tsquery && 'b:DC'::tsquery", "'a':*A & 'b':CD"},
			{"'a'::tsquery && ('b'::tsquery || 'c'::tsquery)", "'a' & ( 'b' | 'c' )"},
		} {
			t.Run(tc.expression, func(t *testing.T) { ftsOpsValue(t, p, b, tc.expression, "TSQUERY", tc.want) })
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_ops_algebra(id integer,a tsquery,b tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_ops_algebra VALUES(1,'a | b','c'),(2,'a','b & c')", 2))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,a && b AS conjunction,a || !!b AS disjunction FROM fts_ops_algebra ORDER BY id", []string{"id", "conjunction", "disjunction"}, []string{"INT4", "TSQUERY", "TSQUERY"}, [][]any{{int64(1), "( 'a' | 'b' ) & 'c'", "'a' | 'b' | !'c'"}, {int64(2), "'a' & 'b' & 'c'", "'a' | !( 'b' & 'c' )"}})
			s.Prepared = prepared
			run(s)
		}
	})
}

func TestReferenceFTSOpsPhrase(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ expression, want string }{
			{"'a'::tsquery <-> 'b & d'::tsquery", "'a' <-> ( 'b' & 'd' )"},
			{"tsquery_phrase('a <3> g','b & d',10)", "'a' <3> 'g' <10> ( 'b' & 'd' )"},
			{"tsquery_phrase('a','b')", "'a' <-> 'b'"},
			{"'a'::tsquery <-> ('b'::tsquery <-> 'c'::tsquery)", "'a' <-> ( 'b' <-> 'c' )"},
		} {
			ftsOpsValue(t, p, b, tc.expression, "TSQUERY", tc.want)
		}
		query := "SELECT tsquery_phrase($1::tsquery,$2::tsquery,$3::int4) AS result"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		for _, distance := range []int{0, 1, 16383, 16384} {
			op := fmt.Sprintf("<%d>", distance)
			if distance == 1 {
				op = "<->"
			}
			s := fixture.Select(query, []string{"result"}, []string{"TSQUERY"}, [][]any{{"'a' " + op + " 'b'"}}, "a", "b", distance)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			ftsOpsValue(t, p, b, "tsquery_phrase('a','b',"+fmt.Sprint(distance)+")", "TSQUERY", "'a' "+op+" 'b'")
		}
		for _, distance := range []int64{-1, 16385, -2147483648, 2147483647} {
			for _, left := range []any{"a", "", nil} {
				if left == nil {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"result"}, []string{"TSQUERY"}, [][]any{{nil}}, left, "b", distance))
				} else {
					s := fixture.ErrorDiagnostic(query, ftsOpsDistanceError(), left, "b", distance)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
		}
	})
}

func TestReferenceFTSOpsContainment(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			left, right         string
			contains, contained bool
		}{
			{"a | !b <2> a", "b & a", true, true}, {"a:*A", "a:D", true, true},
			{"a:*", "abc", false, false}, {"a", "A", false, false}, {"a & a", "a", true, true},
			{"a | b", "a", true, false}, {"", "", true, true}, {"a", "", true, false}, {"", "a", false, true},
		} {
			ftsOpsValue(t, p, b, "$1::tsquery @> $2::tsquery", "BOOL", tc.contains, tc.left, tc.right)
			ftsOpsValue(t, p, b, "$1::tsquery <@ $2::tsquery", "BOOL", tc.contained, tc.left, tc.right)
		}
	})
}
