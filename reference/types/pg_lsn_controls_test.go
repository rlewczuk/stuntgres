package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgLSNNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, want [][]driver.Value) {
	t.Helper()
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		rows, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v FROM "+table+" ORDER BY v", nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		metadata := rows.(driver.RowsColumnTypeDatabaseTypeName)
		scan := rows.(driver.RowsColumnTypeScanType)
		checkMetadata := func() {
			if metadata.ColumnTypeDatabaseTypeName(0) != "PG_LSN" || scan.ColumnTypeScanType(0) != reflect.TypeFor[string]() {
				t.Fatal("retained pg_lsn metadata changed")
			}
		}
		checkMetadata()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			return err
		}
		if dump.String() != redump.String() {
			t.Fatal("non-deterministic pg_lsn dump", dump.String(), redump.String())
		}
		var normalized [][]any
		for _, row := range want {
			normalized = append(normalized, []any{row[0]})
		}
		query := "SELECT v FROM " + table + " ORDER BY v"
		expect := fixture.Select(query, []string{"v"}, []string{"PG_LSN"}, normalized)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, expect)
		if p.Postgres != nil {
			var recorder *fixture.Recorder
			for _, backend := range p.Backends() {
				if backend.PG {
					recorder = backend.Recorder
				}
			}
			tx, err := p.Postgres.BeginTx(p.Ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			fixture.RunStep(t, p.Ctx, tx, recorder, expect)
			if table == "pglsn_saved" {
				fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Select("SELECT origin FROM pglsn_view ORDER BY id", []string{"origin"}, []string{"PG_LSN"}, [][]any{{"0/0"}, {"0/0"}, {"0/0"}, {"0/0"}, {"0/0"}}))
				fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Exec("INSERT INTO pglsn_saved(id) VALUES(5)", 1))
				fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Select("SELECT v FROM pglsn_saved WHERE id=5", []string{"v"}, []string{"PG_LSN"}, [][]any{{"0/1"}}))
			}
			if err := tx.Rollback(); err != nil {
				return err
			}
		}
		script := "CREATE TABLE pglsn_replacement(v pg_lsn); INSERT INTO pglsn_replacement VALUES('bad')"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d := pgLSNDiagnostic("bad")
		d.Position = fixture.Position(strings.Index(script, "'bad'") + 1)
		fixture.AssertDiagnostic(t, err, &d)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, expect)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_class WHERE relname='pglsn_replacement'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if table == "pglsn_saved" {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT origin FROM pglsn_view ORDER BY id", []string{"origin"}, []string{"PG_LSN"}, [][]any{{"0/0"}, {"0/0"}, {"0/0"}, {"0/0"}, {"0/0"}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pglsn_saved(id) VALUES(5)", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM pglsn_saved WHERE id=5", []string{"v"}, []string{"PG_LSN"}, [][]any{{"0/1"}}))
			check := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "pglsn_saved" violates check constraint "pglsn_saved_v_check"`)
			check.Detail = "Failing row contains (6, 0/2)."
			check.Schema = "public"
			check.Table = "pglsn_saved"
			check.Constraint = "pglsn_saved_v_check"
			rejected := fixture.ErrorDiagnostic("INSERT INTO pglsn_saved VALUES(6,'0/2')", check)
			rejected.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, rejected)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM pglsn_saved WHERE id=5", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, expect)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE pglsn_new(v text); INSERT INTO pglsn_new VALUES('new')")); err != nil {
			return err
		}
		checkMetadata()
		dst := make([]driver.Value, 1)
		for _, expected := range want {
			if err := rows.Next(dst); err != nil || !reflect.DeepEqual(dst, expected) {
				t.Fatal("retained pg_lsn", dst, expected, err)
			}
		}
		if err := rows.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		checkMetadata()
		if err := rows.Close(); err != nil {
			return err
		}
		checkMetadata()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM pglsn_new", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}

func TestReferencePGLSNControls(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pglsn_saved(id integer,v pg_lsn DEFAULT '0/1'::pg_lsn CHECK(v >= '0/0'::pg_lsn AND v <> '0/2'::pg_lsn))", 0))
		run(fixture.Exec("INSERT INTO pglsn_saved(id) VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO pglsn_saved VALUES(0,'0/0'),(2,'80000000/0'),(3,'FFFFFFFF/FFFFFFFF'),(4,NULL)", 4))
		run(fixture.Exec("CREATE VIEW pglsn_view AS SELECT id,v,'0/0'::pg_lsn AS origin FROM pglsn_saved", 0))
		unchanged := fixture.Select("SELECT id,v FROM pglsn_saved ORDER BY id", []string{"id", "v"}, []string{"INT4", "PG_LSN"}, [][]any{{int64(0), "0/0"}, {int64(1), "0/1"}, {int64(2), "80000000/0"}, {int64(3), "FFFFFFFF/FFFFFFFF"}, {int64(4), nil}})
		run(unchanged)
		run(fixture.Select("SELECT origin FROM pglsn_view ORDER BY id", []string{"origin"}, []string{"PG_LSN"}, [][]any{{"0/0"}, {"0/0"}, {"0/0"}, {"0/0"}, {"0/0"}}))
		check := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "pglsn_saved" violates check constraint "pglsn_saved_v_check"`)
		check.Detail = "Failing row contains (6, 0/2)."
		check.Schema = "public"
		check.Table = "pglsn_saved"
		check.Constraint = "pglsn_saved_v_check"
		rejected := fixture.ErrorDiagnostic("INSERT INTO pglsn_saved VALUES(6,'0/2')", check)
		rejected.ErrorStage = "execute"
		run(rejected)
		run(unchanged)
		if !b.PG {
			pgLSNNativeControls(t, p, b, "pglsn_saved", [][]driver.Value{{"0/0"}, {"0/1"}, {"80000000/0"}, {"FFFFFFFF/FFFFFFFF"}, {nil}})
		}
	})
}

func TestReferencePGLSNFeatureSmoke(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pglsn_smoke(id integer,v pg_lsn)", 0))
		run(fixture.Exec("INSERT INTO pglsn_smoke VALUES(1,'0/0'),(2,'80000000/0'),(3,'FFFFFFFF/FFFFFFFF')", 3))
		want := fixture.Select("SELECT v AS position FROM pglsn_smoke ORDER BY id", []string{"position"}, []string{"PG_LSN"}, [][]any{{"0/0"}, {"80000000/0"}, {"FFFFFFFF/FFFFFFFF"}})
		want.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgLSNMetadata(t, rows, b.PG, true, "position") }
		run(want)
		q := "SELECT id FROM pglsn_smoke WHERE v=$1 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct {
			arg  any
			rows [][]any
		}{{"0/0", [][]any{{int64(1)}}}, {[]byte("80000000/0"), [][]any{{int64(2)}}}, {nil, nil}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, frame.rows, frame.arg))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT 'FFFFFFFF/FFFFFFFF'::pg_lsn - '0/0'::pg_lsn AS d,'0/FFFFFFFF'::pg_lsn + 1 AS carry,'0/1'::pg_lsn - 0.5::numeric AS rounded", []string{"d", "carry", "rounded"}, []string{"NUMERIC", "PG_LSN", "PG_LSN"}, [][]any{{"18446744073709551615", "1/0", "0/1"}}))
		d := pgLSNDiagnostic("bad")
		d.Context = "unnamed portal parameter $1 = '...'"
		malformed := fixture.ErrorDiagnostic("INSERT INTO pglsn_smoke VALUES(4,$1)", d, "bad")
		malformed.ErrorStage = "execute"
		run(malformed)
		overflow := fixture.ErrorDiagnostic("UPDATE pglsn_smoke SET v=v+1", fixture.ControlsReferenceDiagnostic("22023", "pg_lsn out of range"))
		overflow.ErrorStage = "execute"
		run(overflow)
		run(want)
		if !b.PG {
			pgLSNNativeControls(t, p, b, "pglsn_smoke", [][]driver.Value{{"0/0"}, {"80000000/0"}, {"FFFFFFFF/FFFFFFFF"}})
		}
	})
}
