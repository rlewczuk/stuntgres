package reference_test

import (
	"database/sql"
	"sort"
	"testing"
)

func TestReferenceQueryDistinctOnVolatile(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE random_keys(id INT)", 0))
		run(referenceExec("INSERT INTO random_keys VALUES(1),(2),(3),(4)", 4))
		seed := func() {
			var v any
			if err := c.QueryRowContext(p.ctx, "SELECT setseed(.25)").Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
		draw := func() float64 {
			var v float64
			if err := c.QueryRowContext(p.ctx, "SELECT random()").Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		seed()
		pairs := make([][]any, 4)
		for i := range pairs {
			pairs[i] = []any{draw(), draw()}
		}
		continuation := draw()
		// Oracle18.0 shares the required y with structurally identical late x.
		// Analysis identities remain distinct; ON(1,2) below demands both.
		seed()
		want := make([][]any, 4)
		for i := range want {
			v := draw()
			want[i] = []any{v, v}
		}
		sharedContinuation := draw()
		sort.Slice(want, func(i, j int) bool { return want[i][1].(float64) < want[j][1].(float64) })
		for _, on := range []string{"y", "2", "y,y"} {
			q := "SELECT DISTINCT ON(" + on + ") random() AS x,random() AS y FROM random_keys ORDER BY y,id"
			seed()
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if next := draw(); next != pairs[0][0] {
				stmt.Close()
				t.Fatal("Prepare evaluated volatile key", next, pairs[0][0])
			}
			for _, route := range []string{"direct", "prepared", "held", "held repeat"} {
				seed()
				s := referenceSelect(q, []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, want)
				switch route {
				case "direct":
					run(s)
				case "prepared":
					run(referencePrepared(s))
				default:
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
				}
				if next := draw(); next != sharedContinuation {
					stmt.Close()
					t.Fatal("ON/order/shared payload reevaluated occurrence", on, route, next, sharedContinuation)
				}
			}
			stmt.Close()
		}
		// Both explicit output slots must retain identity, despite matching ASTs.
		seed()
		byFirst := append([][]any(nil), pairs...)
		sort.Slice(byFirst, func(i, j int) bool { return byFirst[i][0].(float64) < byFirst[j][0].(float64) })
		s := referenceSelect("SELECT DISTINCT ON(1,2) random() AS x,random() AS y FROM random_keys ORDER BY 1,2", []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, byFirst)
		queryDistinctBoth(func(s referenceStep) {
			seed()
			run(s)
			if next := draw(); next != continuation {
				t.Fatal("two output occurrences collapsed", next, continuation)
			}
		}, s)
		// Hidden repeated SQL99 keys share one materialization per input row.
		seed()
		hidden := make([][]any, 4)
		for i := range hidden {
			hidden[i] = []any{int64(i + 1), draw()}
		}
		hiddenContinuation := draw()
		sort.Slice(hidden, func(i, j int) bool { return hidden[i][1].(float64) < hidden[j][1].(float64) })
		ids := make([][]any, 4)
		for i := range ids {
			ids[i] = hidden[i][:1]
		}
		queryDistinctBoth(func(s referenceStep) {
			seed()
			run(s)
			if next := draw(); next != hiddenContinuation {
				t.Fatal("hidden key evaluated twice", next, hiddenContinuation)
			}
		}, referenceSelect("SELECT DISTINCT ON(random(),random()) id FROM random_keys ORDER BY random(),id", []string{"id"}, []string{"INT4"}, ids))
		seed()
		run(referenceExec("CREATE VIEW on_random AS SELECT DISTINCT ON(y) random() AS x,random() AS y FROM random_keys ORDER BY y,id", 0))
		if next := draw(); next != pairs[0][0] {
			t.Fatal("CREATE VIEW evaluated random", next, pairs[0][0])
		}
		q := "SELECT x,y FROM on_random"
		viewStmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer viewStmt.Close()
		for range 2 {
			seed()
			referenceRunStep(t, p.ctx, referenceHeldStatement{c, viewStmt, q}, b.recorder, referenceSelect(q, []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, want))
			if next := draw(); next != sharedContinuation {
				t.Fatal("stored shared occurrence added a draw", next, sharedContinuation)
			}
		}
	})
}

func TestReferenceQueryDistinctOnLoserPayload(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE payloads(id INT,k INT)", 0))
		run(referenceExec("INSERT INTO payloads VALUES(1,1),(2,1)", 2))
		// The pinned oracle demands cheap immutable payloads below sort/ON,
		// including losing rows. Both expressions independently fail22012.
		for _, q := range []string{
			"SELECT DISTINCT ON(k) id,1/(id-1) AS payload FROM payloads ORDER BY k,id DESC",
			"SELECT DISTINCT ON(k) id,1/(id-2) AS payload FROM payloads ORDER BY k,id DESC",
		} {
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{"direct", "prepared", "held"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
				s.selectRows = true
				s.errorStage = "execute"
				switch route {
				case "direct":
					run(s)
				case "prepared":
					run(referencePrepared(s))
				default:
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
				}
			}
			stmt.Close()
		}
		// This unrelated volatile payload is genuinely postponable; unlike
		// random() x it cannot reuse a structurally equal demanded key.
		run(referenceExec("CREATE SEQUENCE on_payload_effect START WITH 1", 0))
		q := "SELECT DISTINCT ON(k) id,1/(id-1)+0*nextval('on_payload_effect') AS payload FROM payloads ORDER BY k,id DESC"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, route := range []string{"direct", "prepared", "held", "held repeat"} {
			run(referenceExec("ALTER SEQUENCE on_payload_effect RESTART WITH 1", 0))
			s := referenceSelect(q, []string{"id", "payload"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}})
			switch route {
			case "direct":
				run(s)
			case "prepared":
				run(referencePrepared(s))
			default:
				referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
			}
			var next int64
			if err := c.QueryRowContext(p.ctx, "SELECT nextval('on_payload_effect')").Scan(&next); err != nil || next != 2 {
				t.Fatal("loser volatile payload evaluated", next, err)
			}
		}
	})
}
