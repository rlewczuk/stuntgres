package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func groupDependencyRoutineEdgeCount(t *testing.T, p *fixture.Pair, b fixture.Backend, routine string, want int64) {
	t.Helper()
	q := `SELECT count(*) FROM pg_depend d JOIN pg_proc r ON r.oid=d.objid JOIN pg_constraint c ON c.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_constraint'::regclass AND d.deptype='n' AND d.objsubid=0 AND d.refobjsubid=0 AND r.oid='` + routine + `()'::regprocedure AND c.conrelid='gb_body_t'::regclass AND c.conname='gb_body_pk' AND c.contype='p'`
	groupDependencyCheck(t, p, b, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{want}}))
}

func groupDependencyRoutineValue(t *testing.T, p *fixture.Pair, b fixture.Backend, routine, want string) {
	t.Helper()
	groupDependencyCheck(t, p, b, fixture.Select("SELECT "+routine+"() AS s", []string{"s"}, []string{"TEXT"}, [][]any{{want}}))
}

func groupDependencyRoutineSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	groupDependencyExec(t, p, b,
		"CREATE TABLE gb_body_t(id int,s text,CONSTRAINT gb_body_pk PRIMARY KEY(id))",
		"INSERT INTO gb_body_t VALUES(1,'one')")
}

func TestReferenceQueryGroupDependencyRoutineCascade(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyRoutineSetup(t, p, b)
		groupDependencyExec(t, p, b,
			"CREATE TABLE gb_body_unrelated(id int PRIMARY KEY,s text)",
			"INSERT INTO gb_body_unrelated VALUES(9,'kept')",
			"CREATE FUNCTION gb_body() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT s FROM gb_body_t GROUP BY id; END",
			"CREATE FUNCTION gb_body_dependent() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT gb_body(); END",
			"CREATE FUNCTION gb_body_unrelated_fn() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT s FROM gb_body_unrelated WHERE id=9; END")
		groupDependencyRoutineValue(t, p, b, "gb_body_dependent", "one")
		groupDependencyRoutineEdgeCount(t, p, b, "gb_body", 1)
		groupDependencyExec(t, p, b, "ALTER TABLE gb_body_t DROP CONSTRAINT gb_body_pk CASCADE")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('gb_body','gb_body_dependent')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		groupDependencyRoutineValue(t, p, b, "gb_body_unrelated_fn", "kept")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM gb_body_unrelated", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "kept"}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM gb_body_t", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	})
}

func TestReferenceQueryGroupDependencyRoutineReplacement(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyRoutineSetup(t, p, b)
		grouped := "CREATE OR REPLACE FUNCTION gb_body() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT s FROM gb_body_t GROUP BY id; SELECT s FROM gb_body_t GROUP BY id; END"
		groupDependencyExec(t, p, b, grouped)
		groupDependencyRoutineValue(t, p, b, "gb_body", "one")
		groupDependencyRoutineEdgeCount(t, p, b, "gb_body", 1)
		groupDependencyExec(t, p, b, "CREATE OR REPLACE FUNCTION gb_body() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT 'plain'::text; END")
		groupDependencyRoutineValue(t, p, b, "gb_body", "plain")
		groupDependencyRoutineEdgeCount(t, p, b, "gb_body", 0)
		groupDependencyExec(t, p, b, "ALTER TABLE gb_body_t DROP CONSTRAINT gb_body_pk RESTRICT", "ALTER TABLE gb_body_t ADD CONSTRAINT gb_body_pk PRIMARY KEY(id)", grouped)
		groupDependencyRoutineValue(t, p, b, "gb_body", "one")
		groupDependencyRoutineEdgeCount(t, p, b, "gb_body", 1)
		groupDependencyCheck(t, p, b, fixture.ImplicitError("ALTER TABLE gb_body_t DROP CONSTRAINT gb_body_pk RESTRICT", fixture.Diagnostic{
			Code:    "2BP01",
			Message: "cannot drop constraint gb_body_pk on table gb_body_t because other objects depend on it",
			Detail:  "function gb_body() depends on constraint gb_body_pk on table gb_body_t",
			Hint:    "Use DROP ... CASCADE to drop the dependent objects too.",
		}))
		groupDependencyRoutineValue(t, p, b, "gb_body", "one")
	})
}

func TestReferenceQueryGroupDependencyRoutineRoots(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		modifier bool
	}{
		{"scalar", "SELECT (SELECT s FROM gb_body_t GROUP BY id)", false},
		{"derived", "SELECT s FROM (SELECT s FROM gb_body_t GROUP BY id) d", false},
		{"cte", "WITH grouped AS MATERIALIZED (SELECT s FROM gb_body_t GROUP BY id) SELECT s FROM grouped", false},
		{"modifier", "WITH written AS (INSERT INTO gb_body_sink SELECT id,s FROM gb_body_t GROUP BY id RETURNING s) SELECT s FROM written", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				groupDependencyRoutineSetup(t, p, b)
				groupDependencyExec(t, p, b, "CREATE TABLE gb_body_sink(id int,s text)", "CREATE FUNCTION gb_body() RETURNS text LANGUAGE SQL BEGIN ATOMIC "+tc.body+"; END")
				groupDependencyRoutineValue(t, p, b, "gb_body", "one")
				groupDependencyRoutineEdgeCount(t, p, b, "gb_body", 1)
				if tc.modifier {
					groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM gb_body_sink ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(1), "one"}}))
				}
				groupDependencyExec(t, p, b, "ALTER TABLE gb_body_t DROP CONSTRAINT gb_body_pk CASCADE")
				groupDependencyCheck(t, p, b, fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='gb_body'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		})
	}
}

func TestReferenceQueryGroupDependencyRoutineQuotedSource(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyRoutineSetup(t, p, b)
		groupDependencyExec(t, p, b, "CREATE FUNCTION gb_body() RETURNS text LANGUAGE SQL AS 'SELECT s FROM gb_body_t GROUP BY id'")
		groupDependencyRoutineValue(t, p, b, "gb_body", "one")
		groupDependencyRoutineEdgeCount(t, p, b, "gb_body", 0)
		groupDependencyExec(t, p, b, "ALTER TABLE gb_body_t DROP CONSTRAINT gb_body_pk RESTRICT")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='gb_body'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM gb_body_t", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	})
}

func TestReferenceQueryGroupDependencyRoutineSeparateKeyReplay(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Replacement retains a routine created before the separately replayed PK.
		groupDependencyExec(t, p, b, "CREATE FUNCTION gb_body() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT 'plain'::text; END")
		var routineID int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'gb_body()'::regprocedure::oid::bigint").Scan(&routineID); err != nil || routineID <= 0 {
			t.Fatal("original routine identity", routineID, err)
		}
		groupDependencyRoutineValue(t, p, b, "gb_body", "plain")
		groupDependencyExec(t, p, b,
			"CREATE TABLE gb_body_t(id int NOT NULL,s text)",
			"CREATE UNIQUE INDEX gb_body_pk ON gb_body_t(id) INCLUDE(s)",
			"ALTER TABLE gb_body_t ADD CONSTRAINT gb_body_pk PRIMARY KEY USING INDEX gb_body_pk",
			"INSERT INTO gb_body_t VALUES(1,'one')",
			"CREATE OR REPLACE FUNCTION gb_body() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT s FROM gb_body_t GROUP BY id; END")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT 'gb_body()'::regprocedure::oid::bigint=$1 AS same", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, routineID))

		state := func() {
			t.Helper()
			groupDependencyRoutineValue(t, p, b, "gb_body", "one")
			groupDependencyRoutineEdgeCount(t, p, b, "gb_body", 1)
			// Resolve index ownership through destination metadata, never saved OIDs.
			q := `SELECT count(*) FROM pg_depend d JOIN pg_class i ON i.oid=d.objid JOIN pg_constraint c ON c.oid=d.refobjid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_constraint'::regclass AND d.deptype='i' AND d.objsubid=0 AND d.refobjsubid=0 AND i.oid='gb_body_pk'::regclass AND i.relkind='i' AND c.conrelid='gb_body_t'::regclass AND c.conname='gb_body_pk' AND c.contype='p'`
			groupDependencyCheck(t, p, b, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
			groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM gb_body_t", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
		}
		check := func() {
			t.Helper()
			state()
			groupDependencyCheck(t, p, b, fixture.ImplicitError("ALTER TABLE gb_body_t DROP CONSTRAINT gb_body_pk RESTRICT", fixture.Diagnostic{
				Code:    "2BP01",
				Message: "cannot drop constraint gb_body_pk on table gb_body_t because other objects depend on it",
				Detail:  "function gb_body() depends on constraint gb_body_pk on table gb_body_t",
				Hint:    "Use DROP ... CASCADE to drop the dependent objects too.",
			}))
			state()
		}
		check()
		if !b.PG {
			// PostgreSQL shares the SQL lifecycle, not native persistence controls.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
		}
		groupDependencyExec(t, p, b, "ALTER TABLE gb_body_t DROP CONSTRAINT gb_body_pk CASCADE")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='gb_body'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='gb_body_t'::regclass AND conname='gb_body_pk'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT to_regclass('gb_body_t') IS NOT NULL AS preserved,to_regclass('gb_body_pk') IS NULL AS index_removed", []string{"preserved", "index_removed"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM gb_body_t", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	})
}
