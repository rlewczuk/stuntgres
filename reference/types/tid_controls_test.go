package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func tidNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, want [][]driver.Value) {
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
		if metadata.ColumnTypeDatabaseTypeName(0) != "TID" || rows.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
			t.Fatal("initial TID metadata")
		}
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
			t.Fatal("TID dump is not deterministic", dump.String(), redump.String())
		}
		if p.Postgres != nil {
			tx, err := p.Postgres.BeginTx(p.Ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			replayed, err := tx.QueryContext(p.Ctx, "SELECT v FROM "+table+" ORDER BY v")
			if err != nil {
				return err
			}
			for _, expected := range want {
				if !replayed.Next() {
					t.Fatal("missing replay row", replayed.Err())
				}
				var value any
				if err := replayed.Scan(&value); err != nil {
					return err
				}
				fixture.AssertRawValue(t, true, "TID", value)
				if fixture.Normalize(value) != expected[0] {
					t.Fatal("dump replay value", value, expected)
				}
			}
			if replayed.Next() || replayed.Err() != nil {
				t.Fatal("extra replay row", replayed.Err())
			}
			if err := replayed.Close(); err != nil {
				return err
			}
			if err := tx.Rollback(); err != nil {
				return err
			}
		}
		// Bad staged input must leave the actual saved table and values intact.
		script := "CREATE TABLE tid_replacement(v tid); INSERT INTO tid_replacement VALUES('bad')"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d := tidDiagnostic("bad")
		d.Position = fixture.Position(strings.Index(script, "'bad'") + 1)
		fixture.AssertDiagnostic(t, err, &d)
		var normalized [][]any
		for _, row := range want {
			normalized = append(normalized, []any{row[0]})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM "+table+" ORDER BY v", []string{"v"}, []string{"TID"}, normalized))
		if table == "tid_saved" {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT origin FROM tid_view ORDER BY id", []string{"origin"}, []string{"TID"}, [][]any{{"(0,0)"}, {"(0,0)"}, {"(0,0)"}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO tid_saved(id) VALUES(4)", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM tid_saved WHERE id=4", []string{"v"}, []string{"TID"}, [][]any{{"(1,2)"}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM tid_saved WHERE id=4", 1))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE tid_new(v text); INSERT INTO tid_new VALUES('new')")); err != nil {
			return err
		}
		if metadata.ColumnTypeDatabaseTypeName(0) != "TID" {
			t.Fatal("replacement changed held type")
		}
		dst := make([]driver.Value, 1)
		for _, expected := range want {
			if err := rows.Next(dst); err != nil || !reflect.DeepEqual(dst, expected) {
				t.Fatal("retained TID", dst, expected, err)
			}
		}
		if err := rows.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		if metadata.ColumnTypeDatabaseTypeName(0) != "TID" {
			t.Fatal("EOF changed held type")
		}
		return rows.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM tid_new", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}

func TestReferenceTIDControls(t *testing.T) {
	tidScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE tid_saved(id integer,v tid DEFAULT '(1,2)'::tid CHECK(v >= '(0,0)'::tid))", 0))
		run(fixture.Exec("INSERT INTO tid_saved(id) VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO tid_saved VALUES(2,'(-1,0)'),(3,NULL)", 2))
		run(fixture.Exec("CREATE INDEX tid_saved_btree ON tid_saved(v)", 0))
		run(fixture.Exec("CREATE INDEX tid_saved_hash ON tid_saved USING hash(v)", 0))
		run(fixture.Exec("CREATE VIEW tid_view AS SELECT id,v,'(,)'::tid AS origin FROM tid_saved", 0))
		run(fixture.Select("SELECT * FROM tid_saved ORDER BY id", []string{"id", "v"}, []string{"INT4", "TID"}, [][]any{{int64(1), "(1,2)"}, {int64(2), "(4294967295,0)"}, {int64(3), nil}}))
		run(fixture.Select("SELECT origin FROM tid_view ORDER BY id", []string{"origin"}, []string{"TID"}, [][]any{{"(0,0)"}, {"(0,0)"}, {"(0,0)"}}))
		if b.PG {
			run(fixture.Select("SELECT count(*) AS n FROM tid_saved WHERE ctid IS NOT NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		} else {
			d := fixture.ControlsReferenceDiagnostic("42703", `column "ctid" does not exist`)
			d.Position = "8"
			d.Hint = `Perhaps you meant to reference the column "tid_saved.id".`
			s := fixture.ErrorDiagnostic("SELECT ctid FROM tid_saved", d)
			s.SelectRows = true
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := b.DB.ExecContext(canceled, "INSERT INTO tid_saved VALUES(4,$1)", "(2,3)")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			tidNativeControls(t, p, b, "tid_saved", [][]driver.Value{{"(1,2)"}, {"(4294967295,0)"}, {nil}})
		}
	})
}

func TestReferenceTIDFeatureSmoke(t *testing.T) {
	tidScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE tid_smoke(id integer,v tid)", 0))
		run(fixture.Exec("INSERT INTO tid_smoke VALUES(1,'(2,0)'),(2,'(1,10)'),(3,'(-1,0)')", 3))
		want := fixture.Select("SELECT * FROM tid_smoke ORDER BY v", []string{"id", "v"}, []string{"INT4", "TID"}, [][]any{{int64(2), "(1,10)"}, {int64(1), "(2,0)"}, {int64(3), "(4294967295,0)"}})
		run(want)
		q := "SELECT id FROM tid_smoke WHERE v=$1 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct {
			arg  any
			rows [][]any
		}{{"(1,10)", [][]any{{int64(2)}}}, {[]byte("(-1,0)"), [][]any{{int64(3)}}}, {nil, nil}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, frame.rows, frame.arg))
		}
		run(fixture.Select("SELECT '(,)'::tid AS v", []string{"v"}, []string{"TID"}, [][]any{{"(0,0)"}}))
		d := tidDiagnostic("bad")
		d.Position = "8"
		run(fixture.ErrorDiagnostic("SELECT 'bad'::tid", d))
		q = "INSERT INTO tid_smoke VALUES(4,'(3,1)'),(5,'bad')"
		d.Position = fixture.Position(strings.Index(q, "'bad'") + 1)
		run(fixture.ErrorDiagnostic(q, d))
		run(want)
		if !b.PG {
			tidNativeControls(t, p, b, "tid_smoke", [][]driver.Value{{"(1,10)"}, {"(2,0)"}, {"(4294967295,0)"}})
		}
	})
}
