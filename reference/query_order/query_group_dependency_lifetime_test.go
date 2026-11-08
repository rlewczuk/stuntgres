package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func groupDependencyEdge(t *testing.T, p *fixture.Pair, b fixture.Backend, view, table, key string) {
	t.Helper()
	q := `SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid JOIN pg_class v ON v.oid=r.ev_class JOIN pg_constraint c ON c.oid=d.refobjid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_constraint'::regclass AND d.deptype='n' AND d.objsubid=0 AND d.refobjsubid=0 AND r.rulename='_RETURN' AND v.oid='` + view + `'::regclass AND c.conrelid='` + table + `'::regclass AND c.conname='` + key + `' AND c.contype='p'`
	groupDependencyCheck(t, p, b, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func groupDependencyRestrict(t *testing.T, p *fixture.Pair, b fixture.Backend, table, key, view string) {
	t.Helper()
	s := fixture.ImplicitError("ALTER TABLE "+table+" DROP CONSTRAINT "+key, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop constraint " + key + " on table " + table + " because other objects depend on it", Detail: "view " + view + " depends on constraint " + key + " on table " + table, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	groupDependencyCheck(t, p, b, s)
}

func groupDependencyLifetime(t *testing.T, p *fixture.Pair, b fixture.Backend, rich bool) {
	t.Helper()
	groupDependencyBase(t, p, b)
	groupDependencyEdge(t, p, b, "grin_gb_v", "grin_gb_t", "grin_gb_t_pkey")
	groupDependencyRestrict(t, p, b, "grin_gb_t", "grin_gb_t_pkey", "grin_gb_v")
	groupDependencyCheck(t, p, b, groupDependencyRows("SELECT * FROM grin_gb_v ORDER BY id"))
	if rich {
		groupDependencyExec(t, p, b, "CREATE VIEW gb_nested AS SELECT * FROM grin_gb_v", "CREATE TABLE gb_other(id int PRIMARY KEY)", "CREATE VIEW gb_unrelated AS SELECT count(*) FROM gb_other", "ALTER TABLE gb_other DROP CONSTRAINT gb_other_pkey", "ALTER TABLE grin_gb_t RENAME CONSTRAINT grin_gb_t_pkey TO gb_renamed_pk", "ALTER VIEW grin_gb_v RENAME TO gb_renamed_view")
		groupDependencyEdge(t, p, b, "gb_renamed_view", "grin_gb_t", "gb_renamed_pk")
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			groupDependencyEdge(t, p, b, "gb_renamed_view", "grin_gb_t", "gb_renamed_pk")
			// Remove the transitive view so the complete RESTRICT diagnostic is singular.
		}
		groupDependencyExec(t, p, b, "DROP VIEW gb_nested")
		groupDependencyRestrict(t, p, b, "grin_gb_t", "gb_renamed_pk", "gb_renamed_view")
		groupDependencyExec(t, p, b, "CREATE VIEW gb_nested AS SELECT * FROM gb_renamed_view", "ALTER TABLE grin_gb_t DROP CONSTRAINT gb_renamed_pk CASCADE")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT to_regclass('gb_renamed_view') IS NULL AS removed,to_regclass('gb_nested') IS NULL AS nested,to_regclass('gb_unrelated') IS NOT NULL AS preserved", []string{"removed", "nested", "preserved"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	} else {
		groupDependencyExec(t, p, b, "ALTER TABLE grin_gb_t DROP CONSTRAINT grin_gb_t_pkey CASCADE")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT to_regclass('grin_gb_v') IS NULL AS removed", []string{"removed"}, []string{"BOOL"}, [][]any{{true}}))
	}
	groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM grin_gb_t ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}}))
}

func TestReferenceQueryGroupDependencyViewLifecycle(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { groupDependencyLifetime(t, p, b, true) })
}

func TestReferenceQueryGroupDependencySystemAttributeView(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyExec(t, p, b, "CREATE TABLE t(id int PRIMARY KEY)", "INSERT INTO t VALUES(1),(2)")
		q := "SELECT id FROM t GROUP BY id HAVING tableoid IS NOT NULL AND tableoid IS NOT NULL"
		columns := []string{"id"}
		types := []string{"INT4"}
		rows := [][]any{{int64(1)}, {int64(2)}}
		review := fixture.Select(q, columns, types, rows)
		review.Bag = true
		groupDependencyCheck(t, p, b, review)
		groupDependencyExec(t, p, b, "CREATE VIEW gb_system_review AS "+q)
		groupDependencyCheck(t, p, b, fixture.Select("SELECT * FROM gb_system_review ORDER BY id", columns, types, rows))
		groupDependencyExec(t, p, b, "DROP VIEW gb_system_review")
		q += " ORDER BY tableoid IS NOT NULL,tableoid IS NOT NULL,id"
		groupDependencyCheck(t, p, b, fixture.Select(q, columns, types, rows))
		groupDependencyExec(t, p, b, "CREATE VIEW gb_system_view AS "+q)
		check := func(view, table, key string) {
			groupDependencyCheck(t, p, b, fixture.Select("SELECT * FROM "+view+" ORDER BY id", columns, types, rows))
			groupDependencyCheck(t, p, b, fixture.Select("SELECT id,tableoid='"+table+"'::regclass AS owner FROM "+table+" GROUP BY id HAVING tableoid IS NOT NULL AND tableoid IS NOT NULL ORDER BY tableoid IS NOT NULL,tableoid IS NOT NULL,id", []string{"id", "owner"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), true}}))
			groupDependencyEdge(t, p, b, view, table, key)
			groupDependencyRestrict(t, p, b, table, key, view)
		}
		check("gb_system_view", "t", "t_pkey")
		groupDependencyExec(t, p, b, "ALTER TABLE t RENAME TO gb_system_table", "ALTER TABLE gb_system_table RENAME CONSTRAINT t_pkey TO gb_system_pk", "ALTER VIEW gb_system_view RENAME TO gb_system_renamed")
		check("gb_system_renamed", "gb_system_table", "gb_system_pk")
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check("gb_system_renamed", "gb_system_table", "gb_system_pk")
		}
		groupDependencyExec(t, p, b, "ALTER TABLE gb_system_table DROP CONSTRAINT gb_system_pk CASCADE")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT to_regclass('gb_system_renamed') IS NULL AS removed", []string{"removed"}, []string{"BOOL"}, [][]any{{true}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id FROM gb_system_table ORDER BY id", columns, types, rows))
	})
}

func groupDependencyRoutineLifetime(t *testing.T, p *fixture.Pair, b fixture.Backend, nested bool) {
	t.Helper()
	body := "SELECT s FROM gb_routine_t GROUP BY id"
	if nested {
		body = "WITH grouped AS MATERIALIZED (SELECT s FROM (SELECT s FROM gb_routine_t GROUP BY id) d) SELECT s FROM grouped"
	}
	groupDependencyExec(t, p, b,
		"CREATE TABLE gb_routine_t(id int PRIMARY KEY,s text)",
		"INSERT INTO gb_routine_t VALUES(1,'one')",
		"CREATE FUNCTION gb_routine() RETURNS text LANGUAGE SQL BEGIN ATOMIC "+body+"; END")
	check := func(routine, table, key string) {
		groupDependencyCheck(t, p, b, fixture.Select("SELECT "+routine+"() AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"one"}}))
		q := `SELECT count(*) FROM pg_depend d JOIN pg_proc r ON r.oid=d.objid JOIN pg_constraint c ON c.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_constraint'::regclass AND d.deptype='n' AND d.objsubid=0 AND d.refobjsubid=0 AND r.oid='` + routine + `()'::regprocedure AND c.conrelid='` + table + `'::regclass AND c.conname='` + key + `' AND c.contype='p'`
		groupDependencyCheck(t, p, b, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s := fixture.ImplicitError("ALTER TABLE "+table+" DROP CONSTRAINT "+key+" RESTRICT", fixture.Diagnostic{
			Code:    "2BP01",
			Message: "cannot drop constraint " + key + " on table " + table + " because other objects depend on it",
			Detail:  "function " + routine + "() depends on constraint " + key + " on table " + table,
			Hint:    "Use DROP ... CASCADE to drop the dependent objects too.",
		})
		groupDependencyCheck(t, p, b, s)
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM "+table, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	}
	check("gb_routine", "gb_routine_t", "gb_routine_t_pkey")
	groupDependencyExec(t, p, b,
		"ALTER TABLE gb_routine_t RENAME TO gb_routine_renamed_t",
		"ALTER TABLE gb_routine_renamed_t RENAME CONSTRAINT gb_routine_t_pkey TO gb_routine_renamed_pk",
		"ALTER FUNCTION gb_routine() RENAME TO gb_routine_renamed")
	check("gb_routine_renamed", "gb_routine_renamed_t", "gb_routine_renamed_pk")
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check("gb_routine_renamed", "gb_routine_renamed_t", "gb_routine_renamed_pk")
	}
	groupDependencyExec(t, p, b, "ALTER TABLE gb_routine_renamed_t DROP CONSTRAINT gb_routine_renamed_pk CASCADE")
	groupDependencyCheck(t, p, b, fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='gb_routine_renamed'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	groupDependencyCheck(t, p, b, fixture.Select("SELECT to_regclass('gb_routine_renamed_t') IS NOT NULL AS preserved", []string{"preserved"}, []string{"BOOL"}, [][]any{{true}}))
	groupDependencyCheck(t, p, b, fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='gb_routine_renamed_t'::regclass AND conname='gb_routine_renamed_pk'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s FROM gb_routine_renamed_t", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
}

func TestReferenceQueryGroupDependencyRoutineLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		nested bool
	}{{"direct", false}, {"nested", true}} {
		t.Run(tc.name, func(t *testing.T) {
			groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				groupDependencyRoutineLifetime(t, p, b, tc.nested)
			})
		})
	}
}

func groupDependencyHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	groupDependencyExec(t, p, b, "CREATE TABLE gb_held(id int PRIMARY KEY,s text)", "INSERT INTO gb_held VALUES(1,'one')")
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := "SELECT id,s,count(*) FROM gb_held GROUP BY id ORDER BY id"
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	check := func(rows [][]any) {
		s := fixture.Select(q, []string{"id", "s", "count"}, []string{"INT4", "TEXT", "INT8"}, rows)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	check([][]any{{int64(1), "one", int64(1)}})
	groupDependencyExec(t, p, b, "INSERT INTO gb_held VALUES(2,'two')")
	check([][]any{{int64(1), "one", int64(1)}, {int64(2), "two", int64(1)}})
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check([][]any{{int64(1), "one", int64(1)}, {int64(2), "two", int64(1)}})
	}
	if _, err := conn.ExecContext(p.Ctx, "ALTER TABLE gb_held DROP CONSTRAINT gb_held_pkey"); err != nil {
		t.Fatal(err)
	}
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42803", Message: `column "gb_held.s" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "11"})
	s.SelectRows = true
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	if _, err := conn.ExecContext(p.Ctx, "ALTER TABLE gb_held ADD PRIMARY KEY(id)"); err != nil {
		t.Fatal(err)
	}
	groupDependencyExec(t, p, b, "INSERT INTO gb_held VALUES(3,'three')")
	check([][]any{{int64(1), "one", int64(1)}, {int64(2), "two", int64(1)}, {int64(3), "three", int64(1)}})
	if _, err := conn.ExecContext(p.Ctx, "PREPARE gb_sql_held AS "+q); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(p.Ctx, "DEALLOCATE gb_sql_held")
	execute := fixture.Select("EXECUTE gb_sql_held", []string{"id", "s", "count"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "one", int64(1)}, {int64(2), "two", int64(1)}, {int64(3), "three", int64(1)}})
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, execute)
	if _, err := conn.ExecContext(p.Ctx, "ALTER TABLE gb_held DROP CONSTRAINT gb_held_pkey"); err != nil {
		t.Fatal(err)
	}
	invalid := fixture.ImplicitError("EXECUTE gb_sql_held", fixture.Diagnostic{Code: "42803", Message: `column "gb_held.s" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "34"})
	invalid.SelectRows = true
	invalid.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, invalid)
	if _, err := conn.ExecContext(p.Ctx, "ALTER TABLE gb_held ADD PRIMARY KEY(id)"); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, execute)
}

func TestReferenceQueryGroupDependencyPrepared(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { groupDependencyHeld(t, p, b) })
}

func TestReferenceQueryGroupDependencyFeatureSmoke(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyRoutineLifetime(t, p, b, false)
		groupDependencyLifetime(t, p, b, false)
		groupDependencyExec(t, p, b, "ALTER TABLE grin_gb_t ADD PRIMARY KEY(id)")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s,count(*) FROM grin_gb_t GROUP BY id HAVING s='one' ORDER BY s", []string{"id", "s", "count"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "one", int64(1)}}))
		groupDependencyCheck(t, p, b, groupDependencyError("SELECT s,count(*) FROM grin_gb_t", "grin_gb_t.s"))
		groupDependencyExec(t, p, b, "CREATE TABLE gb_smoke_unique(id int UNIQUE NOT NULL,s text)")
		groupDependencyCheck(t, p, b, groupDependencyError("SELECT s,count(*) FROM gb_smoke_unique GROUP BY id", "gb_smoke_unique.s"))
		groupDependencyHeld(t, p, b)
		groupDependencyMigration(t, p, b)
	})
}
