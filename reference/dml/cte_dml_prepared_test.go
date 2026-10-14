package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCTEDMLPrepared(t *testing.T) {
	cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		cteDMLSeed(t, p, b)
		cteDMLRun(t, p, b, false, fixture.Exec("CREATE SEQUENCE s", 0))
		q := "WITH u AS (UPDATE t SET v=v+$1 WHERE id=1 RETURNING id,v,nextval('s') AS token), unused AS (INSERT INTO t VALUES($2,$3) RETURNING $4::int4 AS out) SELECT a.v,b.v,a.token,$5::int4 AS main FROM u a JOIN u b ON a.id=b.id"
		var held *sql.Stmt
		var x fixture.Executor = b.DB
		if prepared {
			var err error
			held, err = b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			x = fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: q}
		}
		cteDMLRun(t, p, b, false, fixture.Select("SELECT last_value,is_called FROM s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		cteDMLRun(t, p, b, false, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		for i, delta := range []int64{1, 2} {
			value := int64(11)
			if i == 1 {
				value = 13
			}
			s := fixture.Select(q, []string{"v", "v", "token", "main"}, []string{"INT4", "INT4", "INT8", "INT4"}, [][]any{{value, value, int64(i + 1), int64(90 + i)}}, delta, int64(3+i), int64(30+i), int64(80+i), int64(90+i))
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		}
		cteDMLRun(t, p, b, false, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(13)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(4), int64(31)}}))
	})
	t.Run("held_shape", func(t *testing.T) {
		cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ bool) {
			cteDMLSeed(t, p, b)
			q := "WITH u AS (UPDATE t SET v=v+1 RETURNING id,v) SELECT * FROM t"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			cteDMLRun(t, p, b, false, fixture.Exec("ALTER TABLE t ADD COLUMN extra INT", 0))
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
			cteDMLRun(t, p, b, false, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		})
	})
}

func TestReferenceCTEDMLAuthority(t *testing.T) {
	for _, kind := range []string{"insert", "update", "delete", "merge", "output"} {
		t.Run(kind, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"cte_dml_reader"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
							if b.PG {
								run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
							}
							for _, s := range []fixture.Step{fixture.Exec("CREATE USER cte_dml_reader", 0), fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("CREATE TABLE labels(label TEXT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1), fixture.Exec("INSERT INTO labels VALUES('label')", 1), fixture.Exec("GRANT SELECT ON t TO cte_dml_reader", 0)} {
								run(s)
							}
							body := "INSERT INTO t VALUES(2,20)"
							grant, table := "INSERT", "t"
							switch kind {
							case "update":
								body = "UPDATE t SET v=v+1"
								grant = "UPDATE"
							case "delete":
								body = "DELETE FROM t"
								grant = "DELETE"
							case "merge":
								body = "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+1"
								grant = "UPDATE"
							case "output":
								body = "UPDATE t SET v=v+1 RETURNING (SELECT label FROM labels)"
								grant = "SELECT"
								table = "labels"
								run(fixture.Exec("GRANT UPDATE ON t TO cte_dml_reader", 0))
							}
							q := "WITH unused AS (" + body + ") SELECT 7 AS n"
							run(fixture.Exec("SET SESSION AUTHORIZATION cte_dml_reader", 0))
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
							s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
							run(s)
							// Unused SELECT still has no executable privilege demand.
							run(fixture.Select("WITH unused AS (SELECT label FROM labels) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
							run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
							run(fixture.Exec("GRANT "+grant+" ON "+table+" TO cte_dml_reader", 0))
							run(fixture.Exec("SET SESSION AUTHORIZATION cte_dml_reader", 0))
							stmt, err := a.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
							revoke := grant
							run(fixture.Exec("REVOKE "+revoke+" ON "+table+" FROM cte_dml_reader", 0))
							run(fixture.Exec("SET SESSION AUTHORIZATION cte_dml_reader", 0))
							s.Prepared = false
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}, b.Recorder, s)
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
							run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
						})
					}
				})
			}
		})
	}
	for _, primary := range []string{"SELECT 7 AS n", "INSERT INTO dst VALUES(1)", "UPDATE dst SET id=2", "DELETE FROM dst", "MERGE INTO dst USING (VALUES(1)) s(id) ON dst.id=s.id WHEN MATCHED THEN DELETE"} {
		t.Run("read_only/"+primary, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE dst(id INT)", 0))
				tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				q := "WITH unused AS (DELETE FROM t) " + primary
				tag := stringsCTEDMLTag(primary)
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tag + " in a read-only transaction"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
}

func stringsCTEDMLTag(q string) string {
	for i := range q {
		if q[i] == ' ' {
			return q[:i]
		}
	}
	return q
}
