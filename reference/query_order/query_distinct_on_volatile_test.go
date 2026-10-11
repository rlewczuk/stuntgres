package reference_test

import (
	"database/sql"
	"sort"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctOnVolatile(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE random_keys(id INT)", 0))
		run(fixture.Exec("INSERT INTO random_keys VALUES(1),(2),(3),(4)", 4))
		seed := func() {
			var v any
			if err := c.QueryRowContext(p.Ctx, "SELECT setseed(.25)").Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
		draw := func() float64 {
			var v float64
			if err := c.QueryRowContext(p.Ctx, "SELECT random()").Scan(&v); err != nil {
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
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if next := draw(); next != pairs[0][0] {
				stmt.Close()
				t.Fatal("Prepare evaluated volatile key", next, pairs[0][0])
			}
			for _, route := range []string{"direct", "prepared", "held", "held repeat"} {
				seed()
				s := fixture.Select(q, []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, want)
				switch route {
				case "direct":
					run(s)
				case "prepared":
					run(fixture.Prepared(s))
				default:
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
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
		s := fixture.Select("SELECT DISTINCT ON(1,2) random() AS x,random() AS y FROM random_keys ORDER BY 1,2", []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, byFirst)
		fixture.QueryDistinctBoth(func(s fixture.Step) {
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
		fixture.QueryDistinctBoth(func(s fixture.Step) {
			seed()
			run(s)
			if next := draw(); next != hiddenContinuation {
				t.Fatal("hidden key evaluated twice", next, hiddenContinuation)
			}
		}, fixture.Select("SELECT DISTINCT ON(random(),random()) id FROM random_keys ORDER BY random(),id", []string{"id"}, []string{"INT4"}, ids))
		seed()
		run(fixture.Exec("CREATE VIEW on_random AS SELECT DISTINCT ON(y) random() AS x,random() AS y FROM random_keys ORDER BY y,id", 0))
		if next := draw(); next != pairs[0][0] {
			t.Fatal("CREATE VIEW evaluated random", next, pairs[0][0])
		}
		q := "SELECT x,y FROM on_random"
		viewStmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer viewStmt.Close()
		for range 2 {
			seed()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, viewStmt, q}, b.Recorder, fixture.Select(q, []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, want))
			if next := draw(); next != sharedContinuation {
				t.Fatal("stored shared occurrence added a draw", next, sharedContinuation)
			}
		}
	})
}

func TestReferenceQueryDistinctOnLoserPayload(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE payloads(id INT,k INT)", 0))
		run(fixture.Exec("INSERT INTO payloads VALUES(1,1),(2,1)", 2))
		// The pinned oracle demands cheap immutable payloads below sort/ON,
		// including losing rows. Both expressions independently fail22012.
		for _, q := range []string{
			"SELECT DISTINCT ON(k) id,1/(id-1) AS payload FROM payloads ORDER BY k,id DESC",
			"SELECT DISTINCT ON(k) id,1/(id-2) AS payload FROM payloads ORDER BY k,id DESC",
		} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{"direct", "prepared", "held"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.SelectRows = true
				s.ErrorStage = "execute"
				switch route {
				case "direct":
					run(s)
				case "prepared":
					run(fixture.Prepared(s))
				default:
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
				}
			}
			stmt.Close()
		}
		// This unrelated volatile payload is genuinely postponable; unlike
		// random() x it cannot reuse a structurally equal demanded key.
		run(fixture.Exec("CREATE SEQUENCE on_payload_effect START WITH 1", 0))
		q := "SELECT DISTINCT ON(k) id,1/(id-1)+0*nextval('on_payload_effect') AS payload FROM payloads ORDER BY k,id DESC"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, route := range []string{"direct", "prepared", "held", "held repeat"} {
			run(fixture.Exec("ALTER SEQUENCE on_payload_effect RESTART WITH 1", 0))
			s := fixture.Select(q, []string{"id", "payload"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}})
			switch route {
			case "direct":
				run(s)
			case "prepared":
				run(fixture.Prepared(s))
			default:
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
			}
			var next int64
			if err := c.QueryRowContext(p.Ctx, "SELECT nextval('on_payload_effect')").Scan(&next); err != nil || next != 2 {
				t.Fatal("loser volatile payload evaluated", next, err)
			}
		}
	})
}
