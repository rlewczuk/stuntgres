package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// LIKE constructs new indexes even when the source was successfully maintained.
// Deterministic two-statement bodies prevent inlining from hiding the routine.
func TestReferenceClusterDeclarationAdmission(t *testing.T) {
	for _, volatility := range []string{"STABLE", "VOLATILE"} {
		for _, kind := range []string{"expression", "unique_predicate", "predicate"} {
			t.Run(volatility+"/"+kind, func(t *testing.T) {
				persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(step fixture.Step) { persistenceRun(t, p, b, c, step) }
					for _, q := range []string{
						"CREATE TABLE declaration_source(k integer)",
						"CREATE INDEX declaration_plain ON declaration_source(k)",
					} {
						persistenceExec(t, p, b, c, q)
					}
					run(fixture.Exec("INSERT INTO declaration_source VALUES(2),(1),(3)", 3))
					result, body := "integer", "SELECT 0; SELECT $1+10"
					suffix := "ON declaration_source(public.declaration_key(k))"
					message := "functions in index expression must be marked IMMUTABLE"
					prefix := "CREATE INDEX "
					if kind == "unique_predicate" {
						prefix = "CREATE UNIQUE INDEX "
					}
					if kind == "predicate" || kind == "unique_predicate" {
						result, body = "boolean", "SELECT 0; SELECT $1>1"
						suffix = "ON declaration_source(k) WHERE public.declaration_key(k)"
						message = "functions in index predicate must be marked IMMUTABLE"
					}
					persistenceExec(t, p, b, c, "CREATE FUNCTION public.declaration_key(integer) RETURNS "+result+" LANGUAGE SQL IMMUTABLE AS $$"+body+"$$")
					persistenceExec(t, p, b, c, prefix+"declaration_existing "+suffix)
					var routineID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_proc WHERE proname='declaration_key'").Scan(&routineID); err != nil {
						t.Fatal(err)
					}
					code := "s"
					if volatility == "VOLATILE" {
						code = "v"
					}
					routine := func(code string) fixture.Step {
						return fixture.Select("SELECT oid::bigint AS routine_id,provolatile::text AS volatility FROM pg_catalog.pg_proc WHERE proname='declaration_key'", []string{"routine_id", "volatility"}, []string{"INT8", "TEXT"}, [][]any{{routineID, code}})
					}
					values := fixture.Select("SELECT k,public.declaration_key(k) AS result FROM declaration_source ORDER BY k", []string{"k", "result"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(12)}, {int64(3), int64(13)}})
					if kind != "expression" {
						values = fixture.Select("SELECT k,public.declaration_key(k) AS result FROM declaration_source ORDER BY k", []string{"k", "result"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), false}, {int64(2), true}, {int64(3), true}})
					}
					persistenceExec(t, p, b, c, "ALTER FUNCTION public.declaration_key(integer) "+volatility)
					persistenceExec(t, p, b, c, "CLUSTER declaration_source USING declaration_plain")
					run(routine(code))
					run(values)
					for _, prepared := range []bool{false, true} {
						for _, q := range []string{
							prefix + "declaration_rejected " + suffix,
							"CREATE TABLE declaration_copy(LIKE declaration_source INCLUDING INDEXES)",
						} {
							step := fixture.ErrorDiagnostic(q, persistenceDiagnostic("42P17", message))
							step.ErrorStage = "execute"
							if prepared {
								step = fixture.Prepared(step)
							}
							run(step)
							// The preceding plain clone, destination row type and all
							// later declarations belong to the failed statement too.
							run(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_class WHERE relname='declaration_rejected' OR relname LIKE 'declaration_copy%'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
							run(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_type WHERE typname LIKE '%declaration_copy%'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
					}
					run(fixture.Select("SELECT k FROM declaration_source ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					run(fixture.Select("SELECT c.relname,i.indisclustered,i.indisvalid,i.indisready,i.indislive FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='declaration_source'::regclass ORDER BY c.relname", []string{"relname", "indisclustered", "indisvalid", "indisready", "indislive"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"declaration_existing", false, true, true, true}, {"declaration_plain", true, true, true, true}}))
					persistenceExec(t, p, b, c, "ALTER FUNCTION public.declaration_key(integer) IMMUTABLE")
					persistenceExec(t, p, b, c, "CLUSTER declaration_source")
					run(routine("i"))
					run(fixture.Prepared(values))
					run(fixture.Prepared(fixture.Exec("CREATE TABLE declaration_copy(LIKE declaration_source INCLUDING INDEXES)", 0)))
					run(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_index WHERE indrelid='declaration_copy'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				})
			})
		}
	}
}

func TestReferenceClusterPartitionDeclarationAdmission(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(step fixture.Step) { persistenceRun(t, p, b, c, step) }
		for _, q := range []string{
			"CREATE FUNCTION public.partition_declaration_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1+10$$",
			"CREATE TABLE declaration_root(k integer) PARTITION BY RANGE(k)",
			"CREATE UNIQUE INDEX declaration_root_idx ON declaration_root(k)",
			"CREATE TABLE declaration_child PARTITION OF declaration_root FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE declaration_attached(k integer)",
			"CREATE UNIQUE INDEX declaration_reusable ON declaration_attached(k)",
		} {
			persistenceExec(t, p, b, c, q)
		}
		var reusable int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'declaration_reusable'::regclass::bigint").Scan(&reusable); err != nil {
			t.Fatal(err)
		}
		run(fixture.Prepared(fixture.Exec("ALTER TABLE declaration_root ATTACH PARTITION declaration_attached FOR VALUES FROM(10) TO(20)", 0)))
		run(fixture.Select("SELECT indexrelid::bigint AS index_id,indisvalid,indisready,indislive FROM pg_catalog.pg_index WHERE indrelid='declaration_attached'::regclass", []string{"index_id", "indisvalid", "indisready", "indislive"}, []string{"INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{reusable, true, true, true}}))
		run(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_inherits WHERE inhparent='declaration_root_idx'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec("INSERT INTO declaration_root VALUES(2),(12)", 2))
		run(fixture.Select("SELECT k FROM declaration_root ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(12)}}))
		// Explicit index ATTACH is compatibility of two existing declarations.
		for _, q := range []string{
			"CREATE UNIQUE INDEX declaration_attach_parent ON ONLY declaration_root(k)",
			"CREATE UNIQUE INDEX declaration_attach_child ON declaration_child(k)",
			"ALTER INDEX declaration_attach_parent ATTACH PARTITION declaration_attach_child",
			"CREATE TABLE declaration_unique(k integer PRIMARY KEY, v integer UNIQUE)",
			"CREATE TABLE declaration_unique_copy(LIKE declaration_unique INCLUDING INDEXES)",
		} {
			persistenceExec(t, p, b, c, q)
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_index WHERE indrelid='declaration_unique_copy'::regclass AND indisunique AND indisvalid AND indisready AND indislive", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		for _, volatility := range []string{"STABLE", "VOLATILE"} {
			persistenceExec(t, p, b, c, "ALTER FUNCTION public.partition_declaration_key(integer) "+volatility)
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic("CREATE INDEX declaration_rejected ON declaration_attached(public.partition_declaration_key(k))", persistenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE"))
				step.ErrorStage = "execute"
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				run(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_class WHERE relname='declaration_rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		}
	})
}
