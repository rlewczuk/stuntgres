package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikePrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(s fixture.Step) {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
	}
	run(fixture.Exec("CREATE TABLE t(a TEXT,b TEXT,c TEXT)", 0))
	for _, symbol := range []string{"~~", "OPERATOR(pg_catalog.~~)"} {
		for _, tc := range []struct{ q, message string }{
			{"SELECT a LIKE b " + symbol + " c FROM t", "operator does not exist: text ~~ boolean"},
			{"SELECT a " + symbol + " b LIKE c FROM t", "operator does not exist: boolean ~~ text"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: tc.message, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: strconv.Itoa(strings.Index(tc.q, " LIKE ") + 2)})
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				s.ErrorStage = "prepare"
				run(fixture.Prepared(s))
			})
		}
	}
}

func TestReferenceLikeMixedPrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(t *testing.T, s fixture.Step) {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
	}
	run(t, fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
	run(t, fixture.Exec("INSERT INTO t VALUES(1,'ab'),(2,'b'),(3,NULL)", 3))
	for _, tc := range []struct {
		expression string
		rows       [][]any
	}{
		{"NOT v LIKE 'a%'", [][]any{{false}, {true}, {nil}}},
		{"v LIKE 'a%' = FALSE", [][]any{{false}, {true}, {nil}}},
		{"v LIKE 'a%' OR v LIKE 'b%' AND FALSE", [][]any{{true}, {false}, {nil}}},
		{"v LIKE 'a%' AND id BETWEEN 1 AND 2", [][]any{{true}, {false}, {false}}},
		{"(v LIKE 'a%') BETWEEN FALSE AND TRUE", [][]any{{true}, {true}, {nil}}},
		{"NOT v LIKE 'a%' AND TRUE OR v LIKE 'b%'", [][]any{{false}, {true}, {nil}}},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			s := fixture.Select("SELECT "+tc.expression+" AS matched FROM t ORDER BY id", []string{"matched"}, []string{"BOOL"}, tc.rows)
			run(t, s)
			run(t, fixture.Prepared(s))
		})
	}
	for _, tc := range []struct{ query, token, position string }{
		{"SELECT v LIKE 'a%' BETWEEN FALSE AND TRUE FROM t", "BETWEEN", "20"},
		{"SELECT v BETWEEN 'a' AND 'z' LIKE 'a%' FROM t", "LIKE", "30"},
		{"SELECT v BETWEEN 'a' AND 'z' LIKE FROM t", "LIKE", "30"},
		{"SELECT v BETWEEN 'a' AND 'z' ILIKE FROM t", "ILIKE", "30"},
		{"SELECT v='a' LIKE FROM t", "FROM", "19"},
		{"SELECT v LIKE 'a' LIKE FROM t", "LIKE", "19"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"" + tc.token + "\"", Position: tc.position})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(t, s)
			s.ErrorStage = "prepare"
			run(t, fixture.Prepared(s))
		})
	}
	s := fixture.Select("SELECT (v BETWEEN 'a' AND 'z') LIKE FROM t ORDER BY id", []string{"like"}, []string{"BOOL"}, [][]any{{true}, {true}, {nil}})
	run(t, s)
	run(t, fixture.Prepared(s))
}
