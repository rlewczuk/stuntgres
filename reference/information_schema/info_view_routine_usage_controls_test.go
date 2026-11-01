package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func vruRetained(t *testing.T, rows *sql.Rows, columns []*sql.ColumnType, want [][]any) {
	t.Helper()
	defer rows.Close()
	var got [][]any
	for rows.Next() {
		var cells [6]any
		if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5]); err != nil {
			t.Fatal(err)
		}
		for i, value := range cells {
			if _, ok := value.(string); !ok {
				t.Fatalf("retained column %d=%T(%v)", i, value, value)
			}
		}
		got = append(got, append([]any(nil), cells[:]...))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	vruCheckBag(t, got, want)
	vruMetadata(t, columns, false)
}

// Only native controls are conditional. All callers first assert the same
// independently prescribed SQL scenario on both public backends.
func vruRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, where string, want [][]any, mutate, check func()) {
	t.Helper()
	if b.PG {
		return
	}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	capture := func() (*sql.Rows, []*sql.ColumnType) {
		rows, err := c.QueryContext(p.Ctx, vruProjection(false, where, nil).Query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rows.Close() })
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		vruMetadata(t, columns, false)
		return rows, columns
	}
	rows, columns := capture()
	stale := vruHeld(t, p, c, r, vruProjection(false, where, nil).Query)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CREATE VIEW information_schema.view_routine_usage", "INSERT INTO information_schema.view_routine_usage", `CREATE VIEW "information_schema"."view_routine_usage"`, `INSERT INTO "information_schema"."view_routine_usage"`} {
		if strings.Contains(dump.String(), forbidden) {
			t.Fatalf("dump materialized provider: %s", forbidden)
		}
	}
	if mutate != nil {
		mutate()
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(vruProjection(false, where, nil))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	vruRetained(t, rows, columns, want)
	fence := fixture.ErrorDiagnostic(vruProjection(false, where, nil).Query, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
	fence.SelectRows, fence.ErrorStage = true, "execute"
	stale(fence)
	check()
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	bad := "CREATE FUNCTION vru_staged_f() RETURNS integer RETURN 1; CREATE VIEW vru_staged_v AS SELECT vru_staged_f() AS x; CREATE VIEW vru_bad AS SELECT missing FROM vru_absent;"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	unchanged := func() {
		check()
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed/canceled restore changed stored definitions or owners")
		}
	}
	unchanged()
	run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('vru_staged_v','vru_bad')", []string{"relname"}, []string{"NAME"}, nil))
	run(fixture.Select("SELECT proname FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proname='vru_staged_f'", []string{"proname"}, []string{"NAME"}, nil))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore=%v", err)
	}
	unchanged()
	canceledRows, err := c.QueryContext(ctx, vruProjection(false, where, nil).Query)
	if canceledRows != nil {
		_ = canceledRows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query=%v", err)
	}
	unchanged()
}

func TestReferenceInfoViewRoutineUsageControls(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vruSeed(run)
		for _, q := range []string{"CREATE ROLE vru_b", "CREATE ROLE vru_reader", "ALTER FUNCTION vru_scope.f() OWNER TO vru_b"} {
			run(fixture.Exec(q, 0))
		}
		where := "table_schema='vru_scope'"
		checkOn := func(conn *sql.Conn, recorder *fixture.Recorder, db string) {
			consume := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			want := vruSeedRows(t, p, conn, db)
			s := vruProjection(b.PG, where, want)
			consume(s)
			consume(fixture.Prepared(s))
			vruHeld(t, p, conn, recorder, s.Query)(s)
			for _, actor := range []struct {
				role string
				rows [][]any
			}{{"vru_b", want}, {"vru_reader", nil}} {
				consume(fixture.Exec("SET SESSION AUTHORIZATION "+actor.role, 0))
				consume(vruProjection(b.PG, where, actor.rows))
				consume(fixture.Prepared(vruProjection(b.PG, where, actor.rows)))
				consume(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		check := func() { checkOn(c, r, database) }
		check()
		mutate := func() {
			run(fixture.Exec("CREATE OR REPLACE VIEW vru_scope.v AS SELECT vru_scope.g() AS x", 0))
			run(fixture.Exec("ALTER FUNCTION vru_scope.f() OWNER TO vru_reader", 0))
			run(vruProjection(false, where, [][]any{vruRow(database, "vru_scope", "v", "vru_scope", rsuIdentity(t, p, c, "vru_scope", "g", ""))}))
		}
		vruRoundTrip(t, p, b, c, r, where, vruSeedRows(t, p, c, database), mutate, check)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		destination, err := stuntgres.NewDriver().OpenConnector("vru-independent-destination")
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(destination)
		defer db.Close()
		recorded, recorder := fixture.RecordedNativePool(t, destination)
		// Database dumps retain owner names, not cluster-wide role creation.
		// An independent Driver therefore needs its own destination principals.
		for _, q := range []string{"CREATE ROLE vru_b", "CREATE ROLE vru_reader"} {
			fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec(q, 0))
		}
		if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		dest, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dest.Close()
		var destDatabase string
		if err := dest.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&destDatabase); err != nil {
			t.Fatal(err)
		}
		checkOn(dest, recorder, destDatabase)
		var second bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, db, &second); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, db); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, dest, recorder, vruProjection(false, where, nil))
		if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(second.Bytes())); err != nil {
			t.Fatal(err)
		}
		checkOn(dest, recorder, destDatabase)
	})
}
