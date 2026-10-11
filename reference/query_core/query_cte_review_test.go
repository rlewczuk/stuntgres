package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryCTERowLocks(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE TABLE c(id INT)", 0))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		for _, mode := range []string{"FOR UPDATE", "FOR NO KEY UPDATE", "FOR SHARE", "FOR KEY SHARE"} {
			for _, occurrence := range []struct{ from, target string }{
				{"c", "c"},
				{"c AS z", "z"},
				{`c AS "é"`, `"é"`},
			} {
				q := "WITH c AS (SELECT 1 AS n) SELECT n FROM " + occurrence.from + " " + mode + " OF " + occurrence.target
				want := fixture.ControlsReferenceDiagnostic("0A000", mode+" cannot be applied to a WITH query")
				at := strings.LastIndex(q, occurrence.target)
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:at]) + 1)
				subqueryAnalysisFailure(run, q, want)
			}
			// Untargeted locks ignore virtual entries, while physical siblings
			// and qualified physical same-name references remain targetable.
			subqueryBoth(run, cteBag("WITH c AS (SELECT 1 AS n) SELECT n FROM c "+mode, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			subqueryBoth(run, cteBag("WITH c AS (SELECT 1 AS n) SELECT p.id,c.n FROM t p CROSS JOIN c "+mode+" OF p", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
			subqueryBoth(run, cteBag("WITH c AS (SELECT 1 AS n) SELECT c.id FROM public.c "+mode+" OF c", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		}
	})
}

func TestReferenceQueryCTEDeadDemand(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		raw := subqueryRun(t, p, b, c)
		raw(fixture.Exec("CREATE TABLE t(id INT)", 0))
		raw(fixture.Exec("INSERT INTO t VALUES(0)", 1))
		for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			for _, condition := range []struct {
				name, sql string
				args      []any
			}{
				{"false", "FALSE", nil},
				{"null", "NULL::boolean", nil},
				{"bound false", "$1::boolean", []any{false}},
				{"bound null", "$1::boolean", []any{nil}},
			} {
				t.Run(hint+"/"+condition.name, func(t *testing.T) {
					run := subqueryRun(t, p, b, c)
					run(fixture.Exec("CREATE SEQUENCE dead_s", 0))
					defer func() {
						if _, err := c.ExecContext(p.Ctx, "DROP SEQUENCE dead_s"); err != nil {
							t.Error(err)
						}
					}()
					q := "WITH c AS " + hint + " (SELECT nextval('dead_s') AS n) SELECT n FROM c WHERE " + condition.sql
					subqueryBoth(run, cteBag(q, []string{"n"}, []string{"INT8"}, nil, condition.args...))
					run(cteBag("SELECT nextval('dead_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					q = "WITH c AS " + hint + " (SELECT 10/id AS v FROM t) SELECT v FROM c WHERE " + condition.sql
					subqueryBoth(run, cteBag(q, []string{"v"}, []string{"INT4"}, nil, condition.args...))
				})
			}
			cteExecutionFailure(t, p, b, c, "WITH c AS "+hint+" (SELECT 10/id AS v FROM t) SELECT v FROM c WHERE TRUE", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		}
		// A held parameterized statement must gate each invocation separately,
		// not retain a false/null gate from Prepare or a previous Execute.
		raw(fixture.Exec("CREATE SEQUENCE held_dead_s", 0))
		q := "WITH c AS MATERIALIZED (SELECT nextval('held_dead_s') AS n) SELECT n FROM c WHERE $1::boolean"
		statement, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
		for _, step := range []struct {
			arg  any
			rows [][]any
		}{
			{false, nil}, {nil, nil}, {true, [][]any{{int64(1)}}}, {false, nil}, {true, [][]any{{int64(2)}}},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"n"}, []string{"INT8"}, step.rows, step.arg))
		}
		raw(cteBag("SELECT nextval('held_dead_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceQueryCTEPhysicalFrontier(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			for _, scenario := range []struct {
				name, body, selectSQL, column, typ string
				nativeFrontier                     bool
			}{
				{"opaque direct locks", "SELECT id FROM t FOR UPDATE", "SELECT id FROM c WHERE FALSE", "id", "INT4", false},
				{"opaque stored aggregate", "SELECT n FROM grouped", "SELECT n FROM c WHERE FALSE", "n", "INT8", false},
				{"independent physical sibling", "SELECT 1 AS k", "SELECT g.n FROM c CROSS JOIN grouped g WHERE FALSE", "n", "INT8", true},
			} {
				name := map[bool]string{false: "direct", true: "prepared"}[prepared] + "/" + hint + "/" + scenario.name
				t.Run(name, func(t *testing.T) {
					cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						raw := subqueryRun(t, p, b, c)
						cteFixture(raw)
						raw(fixture.Exec("CREATE VIEW stored AS SELECT id FROM t FOR UPDATE", 0))
						raw(fixture.Exec("CREATE VIEW grouped AS SELECT id,count(*) AS n FROM stored GROUP BY id", 0))
						holderConnection, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						holder, err := holderConnection.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						q := "WITH c AS " + hint + " (" + scenario.body + ") " + scenario.selectSQL
						step := cteBag(q, []string{scenario.column}, []string{scenario.typ}, nil)
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, holder, b.Recorder, step)
						if scenario.nativeFrontier && !b.PG {
							// Preserve the existing native eager aggregate frontier.
							// PostgreSQL deliberately has no constant-gated row locks.
							fixture.ViewNativeBlocked(t, p.Ctx, peer, "DELETE FROM t WHERE id=1", prepared)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							fixture.RowAffected(t, p.Ctx, peer, "DELETE FROM t WHERE id=1", prepared, 1)
						} else {
							// An opaque dead CTE body has no physical lock demand,
							// even while its consuming transaction remains open.
							fixture.RowAffected(t, p.Ctx, peer, "DELETE FROM t WHERE id=1", prepared, 1)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
						}
					})
				})
			}
		}
	}
}
