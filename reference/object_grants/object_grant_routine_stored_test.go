package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Pinned PostgreSQL 18 witnesses distinguish declaration from computation:
// REL_18_0 heap.c cookDefault/cookConstraint store expressions without executing
// them; index.c FormIndexDatum initializes an expression only when forming a key.
// execExpr.c ExecInitFunc checks the current user's EXECUTE, including evaluation
// used by clauses.c evaluate_function to fold an immutable constant. A stored
// recipe must not become a permanently authorized literal after its first use.
//
// json_array_length(json), released OID 3956, is a genuine immutable provider in
// all these existing consumers. abs(int4) deliberately retains its native DEFAULT
// exclusion; this test does not enable that or any additional recipe provider.
func TestReferenceObjectGrantRoutineStoredAdmission(t *testing.T) {
	objectGrantScenario(t, []string{"og_stored"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.json_array_length(json)"})
		run(fixture.Exec("CREATE SCHEMA og_stored", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_stored TO og_object_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC", 0))

		// Declaring a dependency is not invoking it. All four tables start empty;
		// the index has no key to compute. The constant DEFAULT remains a recipe.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, q := range []string{
				"CREATE TABLE og_stored.d(id INT,d INT DEFAULT pg_catalog.json_array_length('[1,2]'::json))",
				"CREATE TABLE og_stored.g(id INT,j JSON,g INT GENERATED ALWAYS AS (pg_catalog.json_array_length(j)) STORED)",
				"CREATE TABLE og_stored.c(id INT,j JSON,CHECK(pg_catalog.json_array_length(j)>0))",
				"CREATE TABLE og_stored.i(id INT,j JSON)",
				"CREATE INDEX og_stored_i ON og_stored.i((pg_catalog.json_array_length(j)))",
				"CREATE VIEW og_stored.v AS SELECT id,pg_catalog.json_array_length(j) AS n FROM og_stored.i",
			} {
				run(fixture.Exec(q, 0))
			}
		})

		// REL_18_0 nodeModifyTable.c ExecComputeStoredGenerated initializes
		// generated expressions from ExecInsert only for an actual input tuple;
		// execMain.c ExecRelCheck similarly prepares CHECK expressions on demand.
		// In contrast, the constant DEFAULT enters the INSERT target projection
		// and clauses.c evaluate_function may evaluate it during planning even
		// when the source has no rows. These are oracle questions, not observed
		// results: DEFAULT is expected to deny at execution, generated/CHECK to
		// affect zero rows without invoking json_array_length.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "INSERT INTO og_stored.d(id) SELECT 9 FROM og_stored.i WHERE FALSE", "function", "json_array_length")
			run(fixture.Exec("INSERT INTO og_stored.g(id,j) SELECT 9,'[1,2]'::json FROM og_stored.i WHERE FALSE", 0))
			run(fixture.Exec("INSERT INTO og_stored.c(id,j) SELECT 9,'[1,2]'::json FROM og_stored.i WHERE FALSE", 0))
			for _, table := range []string{"d", "g", "c", "i"} {
				run(fixture.Select("SELECT count(*) AS n FROM og_stored."+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		})

		// Each statement is prepared once while EXECUTE is denied, then reused
		// through deny/grant/revoke/regrant. The fixture HeldStatement preserves
		// its recorder/executor pair; no wrapper masks the prepared SQL route.
		type consumer struct {
			query string
			read  fixture.Step
			held  fixture.HeldStatement
		}
		consumers := []consumer{
			{query: "INSERT INTO og_stored.d(id) VALUES($1) RETURNING d AS n", read: fixture.Select("SELECT id,d AS n FROM og_stored.d ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}})},
			{query: "INSERT INTO og_stored.g(id,j) VALUES($1,'[1,2]'::json) RETURNING g AS n", read: fixture.Select("SELECT id,g AS n FROM og_stored.g ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}})},
			{query: "INSERT INTO og_stored.c(id,j) VALUES($1,'[1,2]'::json) RETURNING id AS n", read: fixture.Select("SELECT id FROM og_stored.c ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})},
			{query: "INSERT INTO og_stored.i(id,j) VALUES($1,'[1,2]'::json) RETURNING id AS n", read: fixture.Select("SELECT id FROM og_stored.i ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})},
		}
		for i := range consumers {
			stmt, err := c.PrepareContext(p.Ctx, consumers[i].query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			consumers[i].held = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: consumers[i].query}
		}
		denied := func(id int64) {
			t.Helper()
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				for _, tc := range consumers {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length"}, id)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, tc.held, b.Recorder, s)
					// Also exercise the scenario's ordinary direct/prepared route.
					run(s)
				}
			})
		}
		insert := func(id int64) {
			t.Helper()
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				for i, tc := range consumers {
					n := int64(2)
					if i >= 2 {
						n = id
					}
					s := fixture.Select(tc.query, []string{"n"}, []string{"INT4"}, [][]any{{n}}, id)
					fixture.RunStep(t, p.Ctx, tc.held, b.Recorder, s)
				}
			})
		}
		denied(1)
		for _, table := range []string{"d", "g", "c", "i"} {
			run(fixture.Select("SELECT count(*) AS n FROM og_stored."+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
		insert(1)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_reader", 0))
		denied(2)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			// Existing datums need no invocation: DEFAULT, STORED generation,
			// CHECK and index maintenance are not re-run by an ordinary read.
			run(fixture.Select("SELECT id,d AS n FROM og_stored.d", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
			run(fixture.Select("SELECT id,g AS n FROM og_stored.g", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
			for _, table := range []string{"c", "i"} {
				run(fixture.Select("SELECT id FROM og_stored."+table, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
			fixture.RoleGrantPermissionDenied(run, "SELECT id,n FROM og_stored.v", "function", "json_array_length")
			// Unlike its empty predecessor, a new index now has a real key to
			// compute. The table owner is still the denied reader.
			fixture.RoleGrantPermissionDenied(run, "CREATE INDEX og_stored_rebuild ON og_stored.i((pg_catalog.json_array_length(j)))", "function", "json_array_length")
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
		insert(2)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("CREATE INDEX og_stored_rebuild ON og_stored.i((pg_catalog.json_array_length(j)))", 0))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, tc := range consumers {
				run(tc.read)
			}
			run(fixture.Select("SELECT id,n FROM og_stored.v ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}}))
		})

		objectGrantStoredViewAdmission(t, p, b, c, run, prepared)
		// Explicitly remove schema/owned-role dependencies before role teardown.
		run(fixture.Exec("DROP VIEW public.og_stored_owner,public.og_stored_invoker", 0))
		run(fixture.Exec("DROP SCHEMA og_stored CASCADE", 0))
	})
}

// A definer view changes relation authority, not the invoker's function EXECUTE.
// REL_18_0 rewriteHandler.c checkAsUser affects RTEPermissionInfo; ExecInitFunc
// still uses GetUserId(). CREATE VIEW's Notes state the same distinction.
func objectGrantStoredViewAdmission(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
	t.Helper()
	run(fixture.Exec("GRANT USAGE ON SCHEMA og_stored TO og_object_other", 0))
	run(fixture.Exec("GRANT SELECT ON og_stored.i TO og_object_other", 0))
	run(fixture.Exec("CREATE VIEW public.og_stored_owner AS SELECT id,pg_catalog.json_array_length(j) AS n FROM og_stored.i", 0))
	run(fixture.Exec("CREATE VIEW public.og_stored_invoker WITH(security_invoker=true) AS SELECT id,pg_catalog.json_array_length(j) AS n FROM og_stored.i", 0))
	// Changing a view owner needs CREATE in its containing schema; this is
	// temporary authority, not a substitute for the reader's USAGE grants.
	run(fixture.Exec("GRANT CREATE ON SCHEMA public TO og_object_other", 0))
	run(fixture.Exec("ALTER VIEW public.og_stored_owner OWNER TO og_object_other", 0))
	run(fixture.Exec("ALTER VIEW public.og_stored_invoker OWNER TO og_object_other", 0))
	run(fixture.Exec("REVOKE CREATE ON SCHEMA public FROM og_object_other", 0))
	run(fixture.Exec("GRANT SELECT ON public.og_stored_owner,public.og_stored_invoker TO og_object_reader", 0))
	// The reader owns i, so move it to the view owner to separate base SELECT.
	run(fixture.Exec("ALTER TABLE og_stored.i OWNER TO og_object_other", 0))
	queries := []string{"SELECT id,n FROM public.og_stored_owner ORDER BY id", "SELECT id,n FROM public.og_stored_invoker ORDER BY id"}
	held := make([]fixture.HeldStatement, len(queries))
	for i, q := range queries {
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	}
	read := func(i int) {
		t.Helper()
		s := fixture.Select(queries[i], []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}})
		run(s)
		fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
	}
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		read(0) // View owner has base SELECT, but no EXECUTE; reader has X.
		fixture.RoleGrantPermissionDenied(run, queries[1], "table", "i")
	})
	run(fixture.Exec("GRANT SELECT ON og_stored.i TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() { read(1) })
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		for i, q := range queries {
			fixture.RoleGrantPermissionDenied(run, q, "function", "json_array_length")
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
		}
	})
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
	// A fresh qualified lookup needs schema USAGE independently of X. Stored
	// views already captured base identities and need only their own schema.
	run(fixture.Exec("REVOKE USAGE ON SCHEMA og_stored FROM og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		fixture.RoleGrantPermissionDenied(run, "SELECT id FROM og_stored.i", "schema", "og_stored", prepared)
		read(0)
		read(1)
	})
	run(fixture.Exec("GRANT USAGE ON SCHEMA og_stored TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		run(fixture.Select("SELECT id FROM og_stored.i ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		read(0)
		read(1)
	})
}
