package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgAMConsume(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var oid int64
		var name, handler, kind string
		if err := rows.Scan(&oid, &name, &handler, &kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{oid, name, handler, kind})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained pg_am rows: got %v want %v", got, want)
	}
}

func pgAMHeldExecution(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	stmt, err := b.DB.PrepareContext(p.Ctx, pgAMProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAMProbe}
	old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_am ORDER BY oid")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgAMMetadata(t, old, b.PG)
	saved, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
	check := func() {
		for _, value := range []any{int64(2), int64(403), int64(405), int64(783), int64(0), nil} {
			pgAMRows(t, p, b, held, pgAMProbe, pgAMLookup(value, false), false, value)
		}
		pgAMInventory(t, p, b)
	}
	for _, q := range []string{
		"CREATE TABLE am_held(n INT)",
		"CREATE INDEX am_held_b ON am_held(n)",
		"CREATE INDEX am_held_h ON am_held USING hash(n)",
		"CREATE DOMAIN am_held_domain AS INT",
		"CREATE TYPE am_held_enum AS ENUM('a','b')",
	} {
		run(q)
		check()
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("CREATE INDEX am_held_bad ON am_held(missing)", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`})
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='public' AND c.relname='am_held_bad'", []string{"relname"}, []string{"NAME"}, nil))
		check()
	}
	// The scenario reset owns the remaining unrelated enum's cleanup.
	for _, q := range []string{"DROP INDEX am_held_b", "DROP INDEX am_held_h", "DROP TABLE am_held", "DROP DOMAIN am_held_domain"} {
		run(q)
		check()
	}
	pgAMMetadata(t, old, b.PG)
	pgAMConsume(t, old, pgAMFacts(b.PG, false))
	pgAMSavedMetadata(t, saved, b.PG, false)
}

func TestReferencePGAMHeldExecution(t *testing.T) { pgAMScenario(t, pgAMHeldExecution) }

func pgAMLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE am_life(n INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO am_life VALUES(7,'kept'),(8,'also')", 2))
	run(fixture.Exec("CREATE INDEX am_life_b ON am_life(n)", 0))
	run(fixture.Exec("CREATE INDEX am_life_h ON am_life USING hash(s)", 0))
	run(fixture.Exec("CREATE VIEW am_life_view AS SELECT * FROM pg_catalog.pg_am WHERE oid IN (2,403,405,783,3580)", 0))
	stmt, err := b.DB.PrepareContext(p.Ctx, pgAMProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAMProbe}
	const viewq = "SELECT * FROM am_life_view ORDER BY oid"
	viewstmt, err := b.DB.PrepareContext(p.Ctx, viewq)
	if err != nil {
		t.Fatal(err)
	}
	defer viewstmt.Close()
	viewheld := fixture.HeldStatement{Executor: b.DB, Statement: viewstmt, SQL: viewq}
	viewRows := func(e fixture.Executor, prepared bool) {
		s := pgAMStep(b, viewq, pgAMFacts(false, false))
		s.Prepared = prepared
		// User views retain nullable descriptors even over NOT NULL catalog
		// columns; base-catalog checks continue to require NOT NULL.
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			pgAMSavedMetadata(t, meta, b.PG, true)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	}
	check := func() {
		pgAMRows(t, p, b, held, pgAMProbe, pgAMLookup(int64(405), false), false, int64(405))
		for _, prepared := range []bool{false, true} {
			viewRows(b.DB, prepared)
		}
		viewRows(viewheld, false)
		run(fixture.Select("SELECT n,s FROM am_life ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), "also"}}))
	}
	check()
	old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_am ORDER BY oid")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgAMMetadata(t, old, b.PG)
	saved, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE VIEW") || !strings.Contains(dump.String(), "am_life_view") || !strings.Contains(dump.String(), "INSERT INTO") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_am") || strings.Contains(dump.String(), `INSERT INTO "pg_catalog"."pg_am"`) {
			t.Fatal("not a user-only logical dump", dump.String())
		}
		// Failed staged replay must preserve the existing inventory and user data.
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE am_partial(n INT); INSERT INTO am_missing VALUES(1);")); err == nil {
			t.Fatal("invalid restore succeeded")
		}
		check()
		pgAMInventory(t, p, b)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		check()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgAMInventory(t, p, b)
		pgAMRows(t, p, b, held, pgAMProbe, pgAMLookup(int64(2), false), false, int64(2))
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='public' AND c.relname IN ('am_life','am_life_view','am_life_b','am_life_h','am_partial')", []string{"relname"}, []string{"NAME"}, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		check()
		pgAMInventory(t, p, b)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			t.Fatal(err)
		}
		if again.String() != dump.String() {
			t.Fatalf("logical redump changed\nbefore:%s\nafter:%s", dump.String(), again.String())
		}
	}
	pgAMMetadata(t, old, b.PG)
	pgAMConsume(t, old, pgAMFacts(b.PG, false))
	pgAMSavedMetadata(t, saved, b.PG, false)
}

func TestReferencePGAMLifecycle(t *testing.T) { pgAMScenario(t, pgAMLifecycle) }

func TestReferencePGAMCancellation(t *testing.T) {
	pgAMScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgAMReads(t, p, b, b.DB)
		if b.PG {
			return // pq precancel transport scheduling is not a server SQL contract.
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, pgAMProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		ordinary := func(err error) {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary cancellation gained SQLSTATE", err)
			}
		}
		rows, err := b.DB.QueryContext(ctx, pgAMProbe, int64(2))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled direct query published rows")
		}
		ordinary(err)
		rows, err = stmt.QueryContext(ctx, int64(2))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held query published rows")
		}
		ordinary(err)
		prepared, err := b.DB.PrepareContext(ctx, pgAMProbe)
		if prepared != nil {
			prepared.Close()
			t.Fatal("canceled prepare published statement")
		}
		ordinary(err)
		pgAMRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAMProbe}, pgAMProbe, pgAMLookup(int64(403), false), false, int64(403))
	})
}

func TestReferencePGAMFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgam_reader"})
	pgAMScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgAMInventory(t, p, b)
		pgAMReads(t, p, b, b.DB)
		pgAMRows(t, p, b, b.DB, "SELECT * FROM pg_am WHERE FALSE", nil, false)
		pgAMLinkage(t, p, b)
		pgAMPermissions(t, p, b)
		pgAMLifecycle(t, p, b)
	})
}
