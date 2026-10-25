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

func ftsTypesNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, want [][]any) {
	t.Helper()
	query := "SELECT id,v,q FROM " + table + " ORDER BY id"
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		rows, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, query, nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		assertMetadata := func() {
			if !reflect.DeepEqual(rows.Columns(), []string{"id", "v", "q"}) {
				t.Fatal("retained columns", rows.Columns())
			}
			for i, typ := range []string{"INT4", "TSVECTOR", "TSQUERY"} {
				if rows.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != typ {
					t.Fatal("retained type", i)
				}
				if i > 0 && rows.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[string]() {
					t.Fatal("retained FTS ScanType", i)
				}
			}
		}
		assertMetadata()
		var dump, redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			return err
		}
		if dump.String() != redump.String() {
			t.Fatalf("FTS redump differs:\nbefore:\n%s\nafter:\n%s", dump.String(), redump.String())
		}
		if table == "fts_saved" {
			if !strings.Contains(dump.String(), "( ''a'' & ( ''b'' & ''c'' ) )") || !strings.Contains(dump.String(), "| ( ''x\\\\y'':* | ''É'' )") {
				t.Fatal("dump erased right-associated query structure")
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(query, []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, want))
		if p.Postgres != nil {
			tx, err := p.Postgres.BeginTx(p.Ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			replayed, err := tx.QueryContext(p.Ctx, query)
			if err != nil {
				return err
			}
			columns, err := replayed.ColumnTypes()
			if err != nil {
				return err
			}
			for i, typ := range []string{"INT4", "TSVECTOR", "TSQUERY"} {
				if columns[i].DatabaseTypeName() != typ {
					t.Fatal("replay metadata", columns[i])
				}
			}
			for _, expected := range want {
				if !replayed.Next() {
					t.Fatal("missing replay row", replayed.Err())
				}
				var id, v, q any
				if err := replayed.Scan(&id, &v, &q); err != nil {
					return err
				}
				fixture.AssertRawValue(t, true, "TSVECTOR", v)
				fixture.AssertRawValue(t, true, "TSQUERY", q)
				if !reflect.DeepEqual([]any{fixture.Normalize(id), fixture.Normalize(v), fixture.Normalize(q)}, expected) {
					t.Fatal("actual dump replay", id, v, q, expected)
				}
			}
			if replayed.Next() || replayed.Err() != nil {
				t.Fatal("replay extra row", replayed.Err())
			}
			if err := replayed.Close(); err != nil {
				return err
			}
			if err := tx.Rollback(); err != nil {
				return err
			}
		}
		if table == "fts_saved" {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v,q FROM fts_control_view ORDER BY id", []string{"v", "q"}, []string{"TSVECTOR", "TSQUERY"}, [][]any{{"'view'", "'a' <-> 'b'"}, {"'view'", "'a' <-> 'b'"}, {"'view'", "'a' <-> 'b'"}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO fts_saved(id) VALUES(9)", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v,q FROM fts_saved WHERE id=9", []string{"v", "q"}, []string{"TSVECTOR", "TSQUERY"}, [][]any{{"'a':1", "'a' & 'b' & 'c'"}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM fts_saved WHERE id=9", 1))
		}
		script := "CREATE TABLE fts_rejected(v tsvector); INSERT INTO fts_rejected VALUES('ok'); INSERT INTO fts_rejected VALUES('a:0')"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d := fixture.ControlsReferenceDiagnostic("42601", "wrong position info in tsvector: \"a:0\"")
		d.Position = fixture.Position(strings.Index(script, "'a:0'") + 1)
		fixture.AssertDiagnostic(t, err, &d)
		script = "CREATE TABLE fts_rejected(v tsquery NOT NULL); INSERT INTO fts_rejected VALUES('ok'); INSERT INTO fts_rejected VALUES(NULL)"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d = fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "fts_rejected" violates not-null constraint`)
		d.Detail = "Failing row contains (null)."
		d.Schema = "public"
		d.Table = "fts_rejected"
		d.Column = "v"
		fixture.AssertDiagnostic(t, err, &d)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, strings.NewReader("CREATE TABLE lost(v tsvector)")); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		var ignored bytes.Buffer
		if err := stuntgres.Dump(canceled, p.Native, &ignored); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Dump", err)
		}
		if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Reset", err)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(query, []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, want))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE fts_success(v text); INSERT INTO fts_success VALUES('replacement')")); err != nil {
			return err
		}
		assertMetadata()
		dest := make([]driver.Value, 3)
		for _, expected := range want {
			if err := rows.Next(dest); err != nil {
				return err
			}
			for i, value := range expected {
				if dest[i] != value {
					t.Fatal("retained value", i, dest, expected)
				}
			}
		}
		if err := rows.Next(dest); err != io.EOF {
			t.Fatal("retained EOF", err)
		}
		assertMetadata()
		return rows.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM fts_success", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
}

func TestReferenceFTSTypesControls(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_saved(id integer,v tsvector DEFAULT 'a:1'::tsvector,q tsquery DEFAULT 'a & (b & c)'::tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_saved(id) VALUES(1)", 1))
		vector := "'space word':3A 'é' 'a''b' 'x\\\\y'"
		query := "'É space' & ('a''b' | ('x\\\\y':* | 'É'))"
		run(fixture.Exec("INSERT INTO fts_saved VALUES(2,$1,$2),(3,NULL,NULL)", 2, vector, query))
		run(fixture.Exec("CREATE VIEW fts_control_view AS SELECT id,TSVECTOR 'view' AS v,TSQUERY 'a <1> b' AS q FROM fts_saved", 0))
		want := [][]any{{int64(1), "'a':1", "'a' & 'b' & 'c'"}, {int64(2), "'a''b' 'space word':3A 'x\\\\y' 'é'", "'É space' & ( 'a''b' | 'x\\\\y':* | 'É' )"}, {int64(3), nil, nil}}
		run(fixture.Select("SELECT id,v,q FROM fts_saved ORDER BY id", []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, want))
		if !b.PG {
			ftsTypesNativeControls(t, p, b, "fts_saved", want)
		}
	})
}
