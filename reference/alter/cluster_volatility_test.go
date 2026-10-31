package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterVolatilityMetadata(kind string, clustered bool) fixture.Step {
	return fixture.Select("SELECT c.relname,i.indisclustered,i.indisvalid,i.indisready,i.indislive,i.indexprs IS NOT NULL AS has_expression,i.indpred IS NOT NULL AS has_predicate FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='t'::regclass ORDER BY c.relname", []string{"relname", "indisclustered", "indisvalid", "indisready", "indislive", "has_expression", "has_predicate"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
		{"a", clustered && kind != "selected_expression", true, true, true, false, false},
		{"b", false, true, true, true, true, false},
		{"u", false, true, true, true, false, false},
		{"v", clustered && kind == "selected_expression", true, true, true, kind != "sibling_predicate", kind == "sibling_predicate"},
	})
}

func clusterVolatilityValues(kind string) fixture.Step {
	if kind == "sibling_predicate" {
		return fixture.Select("SELECT id,k FROM t WHERE public.cluster_volatility_key(k) ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(3), int64(2)}, {int64(5), int64(3)}})
	}
	return fixture.Select("SELECT id,public.cluster_volatility_key(k) AS key FROM t ORDER BY id", []string{"id", "key"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}, {int64(2), int64(11)}, {int64(3), int64(12)}, {int64(4), nil}, {int64(5), int64(13)}})
}

func TestReferenceClusterExistingIndexVolatility(t *testing.T) {
	t.Run("changed_body_membership", clusterChangedBodyMembership)
	for _, volatility := range []string{"STABLE", "VOLATILE"} {
		for _, kind := range []string{"selected_expression", "sibling_expression", "sibling_predicate"} {
			for _, route := range []string{"direct", "fresh", "held"} {
				for _, driver := range []bool{false, true} {
					for _, commit := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/%s/driver=%t/commit=%t", volatility, kind, route, driver, commit), func(t *testing.T) {
							persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
								clusterCommandSeed(t, p, b, c)
								result, body := "integer", "SELECT 0; SELECT $1+10"
								indexSuffix := "ON t(public.cluster_volatility_key(k))"
								message := "functions in index expression must be marked IMMUTABLE"
								if kind == "sibling_predicate" {
									result, body = "boolean", "SELECT 0; SELECT $1>1"
									indexSuffix = "ON t(k) WHERE public.cluster_volatility_key(k)"
									message = "functions in index predicate must be marked IMMUTABLE"
								}
								// Two commands prevent SQL inlining. The body stays deterministic
								// and unchanged: only the existing declaration's volatility changes.
								persistenceExec(t, p, b, c, "CREATE FUNCTION public.cluster_volatility_key(integer) RETURNS "+result+" LANGUAGE SQL IMMUTABLE AS $$"+body+"$$")
								persistenceExec(t, p, b, c, "CREATE INDEX v "+indexSuffix)
								q := "CLUSTER t USING a"
								if kind == "selected_expression" {
									q = "CLUSTER t USING v"
								}
								var held *sql.Stmt
								if route == "held" {
									held = clusterCommandHeld(t, p, b, c, q)
								}
								var routineID int64
								if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_proc WHERE proname='cluster_volatility_key'").Scan(&routineID); err != nil {
									t.Fatal(err)
								}
								persistenceExec(t, p, b, c, "ALTER FUNCTION public.cluster_volatility_key(integer) "+volatility)
								code := "s"
								if volatility == "VOLATILE" {
									code = "v"
								}
								persistenceRun(t, p, b, c, fixture.Select("SELECT oid::bigint AS routine_id,provolatile::text AS volatility FROM pg_catalog.pg_proc WHERE proname='cluster_volatility_key'", []string{"routine_id", "volatility"}, []string{"INT8", "TEXT"}, [][]any{{routineID, code}}))
								// Existing-index maintenance is not new-index declaration admission.
								// Full diagnostics are independently source-derived, pending PG18.6.
								create := "CREATE INDEX rejected_idx " + indexSuffix
								for _, prepared := range []bool{false, true} {
									step := fixture.ErrorDiagnostic(create, persistenceDiagnostic("42P17", message))
									step.ErrorStage = "execute"
									if prepared {
										step = fixture.Prepared(step)
									}
									persistenceRun(t, p, b, c, step)
								}
								persistenceRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_class WHERE relname='rejected_idx'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
								persistenceRun(t, p, b, c, clusterVolatilityMetadata(kind, false))
								var executor fixture.Executor = c
								var tx *sql.Tx
								if driver {
									var err error
									tx, err = c.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									executor = tx
								} else {
									persistenceExec(t, p, b, c, "BEGIN")
									defer c.ExecContext(p.Ctx, "ROLLBACK")
								}
								step := fixture.Exec(q, 0)
								if route == "fresh" {
									step = fixture.Prepared(step)
								}
								if held != nil {
									statement := held
									if tx != nil {
										statement = tx.StmtContext(p.Ctx, held)
										defer statement.Close()
									}
									persistenceRun(t, p, b, fixture.HeldStatement{Executor: executor, Statement: statement, SQL: q}, step)
								} else {
									persistenceRun(t, p, b, executor, step)
								}
								persistenceRun(t, p, b, executor, clusterCommandRows())
								persistenceRun(t, p, b, executor, clusterVolatilityValues(kind))
								persistenceRun(t, p, b, executor, clusterVolatilityMetadata(kind, true))
								if tx != nil {
									var err error
									if commit {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								} else {
									end := "ROLLBACK"
									if commit {
										end = "COMMIT"
									}
									persistenceExec(t, p, b, c, end)
								}
								persistenceRun(t, p, b, c, fixture.Prepared(clusterCommandRows()))
								persistenceRun(t, p, b, c, clusterVolatilityMetadata(kind, commit))
								if !commit {
									persistenceExec(t, p, b, c, q)
								}
								persistenceExec(t, p, b, c, "CLUSTER t")
								persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO t VALUES(6,4,'d')", 1))
								persistenceRun(t, p, b, c, fixture.Exec("UPDATE t SET k=5 WHERE id=6", 1))
								persistenceRun(t, p, b, c, fixture.Select("SELECT id,k,payload FROM t WHERE id=6", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(6), int64(5), "d"}}))
								persistenceRun(t, p, b, c, fixture.Exec("DELETE FROM t WHERE id=6", 1))
								persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,9,'duplicate')", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "u"}))
								persistenceRun(t, p, b, c, clusterCommandRows())
								persistenceRun(t, p, b, c, fixture.Prepared(clusterVolatilityValues(kind)))
								persistenceRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
								persistenceRun(t, p, b, c, clusterVolatilityMetadata(kind, true))
								if !b.PG && driver && route == "held" && commit {
									// Dump replay creates declarations anew. Restore IMMUTABLE and
									// refresh before testing that separate native control lifecycle.
									persistenceExec(t, p, b, c, "ALTER FUNCTION public.cluster_volatility_key(integer) IMMUTABLE")
									persistenceExec(t, p, b, c, q)
									clusterRemedyNativeRoundtrip(t, p, b, c, clusterCommandRows().Rows, clusterVolatilityMetadata(kind, true))
								}
							})
						})
					}
				}
			}
		}
	}
}

// Stale partial roots would hide newly qualifying rows after this body change.
func clusterChangedBodyMembership(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterCommandSeed(t, p, b, c)
				for _, q := range []string{
					"CREATE FUNCTION public.cluster_membership(integer) RETURNS boolean LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1>2$$",
					"CREATE INDEX membership_idx ON t(k) WHERE public.cluster_membership(k)",
				} {
					persistenceExec(t, p, b, c, q)
				}
				const command = "CLUSTER t USING a"
				run := clusterUnpopulatedExecutor(t, p, b, c, route, command)
				persistenceExec(t, p, b, c, "CREATE OR REPLACE FUNCTION public.cluster_membership(integer) RETURNS boolean LANGUAGE SQL STABLE AS $$SELECT 0; SELECT $1>1$$")
				run(fixture.Exec(command, 0))
				values := func(rows [][]any) fixture.Step {
					return fixture.Select("SELECT id FROM t WHERE public.cluster_membership(k) ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
				}
				persistenceRun(t, p, b, c, values([][]any{{int64(1)}, {int64(3)}, {int64(5)}}))
				persistenceRun(t, p, b, c, fixture.Prepared(values([][]any{{int64(1)}, {int64(3)}, {int64(5)}})))
				persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO t VALUES(6,2,'new')", 1))
				persistenceRun(t, p, b, c, fixture.Exec("UPDATE t SET k=1 WHERE id=3", 1))
				persistenceRun(t, p, b, c, fixture.Exec("DELETE FROM t WHERE id=5", 1))
				persistenceRun(t, p, b, c, values([][]any{{int64(1)}, {int64(6)}}))
				persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", true}, {"b", false}, {"membership_idx", false}, {"u", false}}))
				persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,9,'duplicate')", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "u"}))
			})
		})
	}
}
