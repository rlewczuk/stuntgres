package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRestrictRelationKindForeignLifecycle(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		read := func(restricted bool) {
			if restricted {
				run(rkForeignDenied(rkForeignRead, false))
			} else {
				run(fdwRows(rkForeignRead, rkForeignBaseline()))
			}
		}
		txRead := func(restricted bool) {
			run(fixture.Exec("SAVEPOINT rk_foreign_error", 0))
			read(restricted)
			run(fixture.Exec("ROLLBACK TO rk_foreign_error", 0))
			run(fixture.Exec("RELEASE rk_foreign_error", 0))
		}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		rkForeignSet(run, "foreign-table")
		read(true)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fdwRows(rkForeignRead, rkForeignBaseline()))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SHOW "+rkForeignSetting, []string{rkForeignSetting}, []string{"TEXT"}, [][]any{{""}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL "+rkForeignSetting+"=''", 0))
		read(false)
		run(fixture.Exec("COMMIT", 0))
		read(true)
		rkForeignSet(run, "")
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET LOCAL "+rkForeignSetting+"='foreign-table'", 0))
			txRead(true)
			run(fixture.Exec("SAVEPOINT rk_write", 0))
			run(rkForeignDenied("INSERT INTO fdw_data VALUES(4,'lost',true)", false))
			run(fixture.Exec("ROLLBACK TO rk_write", 0))
			run(fixture.Exec(end, 0))
			read(false)
			fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", rkForeignBaseline()))
		}
		run(fixture.Exec("BEGIN", 0))
		rkForeignSet(run, "foreign-table")
		run(fixture.Exec("SAVEPOINT rk_foreign_save", 0))
		run(fixture.Exec("SET LOCAL "+rkForeignSetting+"=''", 0))
		read(false)
		run(fixture.Exec("ROLLBACK TO rk_foreign_save", 0))
		txRead(true)
		run(fixture.Exec("ROLLBACK", 0))
		read(false)
		run(fixture.Exec("BEGIN", 0))
		rkForeignSet(run, "foreign-table")
		run(fixture.Exec("COMMIT", 0))
		read(true)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT rk_foreign_invalid", 0))
		invalid := fdwDiagnostic("22023", `invalid value for parameter "restrict_nonsystem_relation_kind": "bogus,"`)
		invalid.Detail = "List syntax is invalid."
		run(fixture.ErrorDiagnostic("SET "+rkForeignSetting+"='bogus,'", invalid))
		run(fixture.Exec("ROLLBACK TO rk_foreign_invalid", 0))
		txRead(true)
		run(fixture.Exec("SAVEPOINT rk_foreign_release", 0))
		run(fixture.Exec("SET LOCAL "+rkForeignSetting+"=''", 0))
		run(fixture.Exec("RELEASE rk_foreign_release", 0))
		read(false)
		run(fixture.Exec("COMMIT", 0))
		read(true)
		for _, reset := range []string{"SET " + rkForeignSetting + "=DEFAULT", "RESET " + rkForeignSetting, "RESET ALL"} {
			rkForeignSet(run, "foreign-table")
			run(fixture.Exec(reset, 0))
			read(false)
		}
	})
}

func TestReferenceRestrictRelationKindForeignPreparedLifetime(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		heldRead, err := c.PrepareContext(p.Ctx, rkForeignRead)
		if err != nil {
			t.Fatal(err)
		}
		defer heldRead.Close()
		read := fixture.HeldStatement{Executor: c, Statement: heldRead, SQL: rkForeignRead}
		fixture.RunStep(t, p.Ctx, read, b.Recorder, fdwRows(rkForeignRead, rkForeignBaseline()))
		q := "INSERT INTO fdw_data VALUES(4,'never-planned',true)"
		heldWrite, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer heldWrite.Close()
		var captured *sql.Rows
		if !b.PG {
			captured, err = c.QueryContext(p.Ctx, rkForeignRead)
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
		}
		rkForeignSet(run, "foreign-table")
		if captured != nil {
			rkForeignCapturedRows(t, captured)
		}
		// Independently specified lifetime boundary: PG's executed read has a
		// cached plan; native retains syntax and rechecks on every use.
		want := fdwRows(rkForeignRead, rkForeignBaseline())
		if !b.PG {
			want = rkForeignDenied(rkForeignRead, false)
		}
		fixture.RunStep(t, p.Ctx, read, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: heldWrite, SQL: q}, b.Recorder, rkForeignDenied(q, false))
		// Fresh foreign Prepare must succeed without opening its handler.
		fresh, err := c.PrepareContext(p.Ctx, rkForeignRead)
		if err != nil {
			t.Fatalf("metadata-only foreign Prepare: %v", err)
		}
		defer fresh.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: rkForeignRead}, b.Recorder, rkForeignDenied(rkForeignRead, false))
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", rkForeignBaseline()))
		run(fixture.Exec("RESET "+rkForeignSetting, 0))
		fixture.RunStep(t, p.Ctx, read, b.Recorder, fdwRows(rkForeignRead, rkForeignBaseline()))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: heldWrite, SQL: q}, b.Recorder, fixture.Exec(q, 1))
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", append(rkForeignBaseline(), []any{int64(4), "never-planned", true})))
		run(fixture.Exec("DELETE FROM fdw_data WHERE id=4", 1))
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", rkForeignBaseline()))
	})
}

func rkForeignCapturedRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id", "label", "flag"}) {
		t.Fatal("captured foreign columns changed", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal("captured foreign types changed", types, err)
	}
	for i, want := range []string{"INT4", "TEXT", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatal("captured foreign type changed", i, types[i].DatabaseTypeName(), want)
		}
	}
	var got [][]any
	for rows.Next() {
		var id, label, flag any
		if err := rows.Scan(&id, &label, &flag); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{id, label, flag})
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, rkForeignBaseline()) {
		t.Fatal("captured foreign data changed", got, rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
