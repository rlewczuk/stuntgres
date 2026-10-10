package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryDistinctOnAnalysisBoundaries(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT,j JSON)", 0))
		for _, tc := range []struct{ marked, code, message string }{
			{"SELECT DISTINCT ON(|) id FROM t", "42601", "syntax error at or near \")\""},
			{"SELECT DISTINCT ON(id,|) id FROM t", "42601", "syntax error at or near \")\""},
			{"SELECT DISTINCT ON(id) id FROM t ORDER BY |0", "42P10", "ORDER BY position 0 is not in select list"},
			{"SELECT DISTINCT ON(id) id FROM t ORDER BY |absent", "42703", "column \"absent\" does not exist"},
		} {
			q, want := queryDistinctOnMarked(tc.marked, tc.code, tc.message)
			queryDistinctAnalysis(t, p, b, c, q, want)
		}
		q, want := queryDistinctOnMarked("SELECT DISTINCT ON(j) id FROM t ORDER BY |j", "42883", "could not identify an ordering operator for type json")
		want.Hint = "Use an explicit ordering operator or modify the query."
		queryDistinctAnalysis(t, p, b, c, q, want)
		// Analysis failures do not evaluate volatile keys or selected effects.
		run(referenceExec("CREATE SEQUENCE on_effect START WITH 1", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(referenceExec("INSERT INTO t VALUES(1,'{}'),(2,NULL)", 2))
			}
			for _, prepared := range []bool{false, true} {
				run(referenceExec("ALTER SEQUENCE on_effect RESTART WITH 1", 0))
				q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|setseed(0)) nextval('on_effect') FROM t", "42883", "could not identify an equality operator for type void")
				s := referenceImplicitError(q, want)
				s.selectRows = true
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				run(s)
				var next int64
				if err := c.QueryRowContext(p.ctx, "SELECT nextval('on_effect')").Scan(&next); err != nil || next != 1 {
					t.Fatal("analysis consumed sequence", next, err)
				}
			}
		}
		q, want = queryDistinctOnMarked("CREATE VIEW on_parameter AS SELECT DISTINCT ON(|$1) id FROM t", "42P02", "there is no parameter $1")
		s := referenceImplicitError(q, want)
		s.errorStage = "execute"
		run(s)
	})
}

func TestReferenceQueryDistinctOnKeyFailure(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE key_input(id INT)", 0))
		run(referenceExec("INSERT INTO key_input VALUES(1),(2),(3)", 3))
		run(referenceExec("CREATE TABLE key_dest(id INT)", 0))
		run(referenceExec("INSERT INTO key_dest VALUES(17)", 1))
		q := "INSERT INTO key_dest SELECT DISTINCT ON(1/(id-$1)) id FROM key_input ORDER BY 1/(id-$1),id DESC"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, route := range []string{"direct", "prepared", "held"} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"}, 3)
			s.errorStage = "execute"
			switch route {
			case "direct":
				run(s)
			case "prepared":
				run(referencePrepared(s))
			default:
				referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
			}
			run(referenceSelect("SELECT id FROM key_dest", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
		}
		// With argument4 the integer keys are 0,0,-1, retaining ids2 and3.
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, referenceExec(q, 2, 4))
		run(referenceSelect("SELECT id FROM key_dest ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(17)}}))
	})
}
