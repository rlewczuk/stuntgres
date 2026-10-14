package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCTEDMLControls(t *testing.T) {
	cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE dst(id INT,v INT)", 0), fixture.Exec("INSERT INTO src VALUES(1,10),(2,NULL),(2,NULL)", 3)} {
			cteDMLRun(t, p, b, false, s)
		}
		bag := [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}
		q := "WITH moved AS (DELETE FROM src RETURNING id,v), copied AS (INSERT INTO dst SELECT * FROM moved RETURNING id,v) SELECT * FROM copied"
		cteDMLRun(t, p, b, prepared, cteDMLBag(q, []string{"id", "v"}, []string{"INT4", "INT4"}, bag))
		cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM dst", []string{"id", "v"}, []string{"INT4", "INT4"}, bag))
		cteDMLRun(t, p, b, false, fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('moved','copied')", []string{"relname"}, []string{"NAME"}, [][]any{}))
		typeQuery := "SELECT typname FROM pg_type WHERE typname IN ('moved','copied')"
		typeStep := fixture.Select(typeQuery, []string{"typname"}, []string{"NAME"}, [][]any{})
		if !b.PG {
			typeStep = fixture.ImplicitError(typeQuery, fixture.Diagnostic{Code: "42P01", Message: `relation "pg_type" does not exist`, Position: "21"})
			typeStep.SelectRows = true
		}
		cteDMLRun(t, p, b, false, typeStep)
		if b.PG {
			return
		} // No invented PostgreSQL native-control API.
		cteDMLRun(t, p, b, false, fixture.Exec("CREATE SEQUENCE controls_s", 0))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "moved") || strings.Contains(dump.String(), "copied") || strings.Contains(dump.String(), "RETURNING") {
			t.Fatal("transient output serialized", dump.String())
		}
		bad := "WITH first AS (INSERT INTO src VALUES(9,90) RETURNING id), second AS (UPDATE dst SET v=99 FROM first WHERE dst.id=first.id RETURNING dst.id) SELECT 1/(id-id) FROM first"
		s := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.SelectRows, s.ErrorStage = true, "execute"
		cteDMLRun(t, p, b, prepared, s)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
			t.Fatal("failed statement changed Dump", err)
		}
		borrower, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer borrower.Close()
		err = borrower.Raw(func(raw any) error {
			retained, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "WITH d AS (DELETE FROM dst RETURNING id,v) SELECT * FROM d ORDER BY id", nil)
			if err != nil {
				return err
			}
			defer retained.Close()
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				return err
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				return err
			}
			if !reflect.DeepEqual(retained.Columns(), []string{"id", "v"}) {
				t.Fatal(retained.Columns())
			}
			meta := retained.(driver.RowsColumnTypeDatabaseTypeName)
			if meta.ColumnTypeDatabaseTypeName(0) != "INT4" || meta.ColumnTypeDatabaseTypeName(1) != "INT4" {
				t.Fatal("retained type metadata changed")
			}
			dst := make([]driver.Value, 2)
			for _, want := range [][]driver.Value{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}} {
				if err := retained.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
					t.Fatal(dst, want, err)
				}
			}
			if err := retained.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM dst", []string{"id", "v"}, []string{"INT4", "INT4"}, bag))
		cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM src", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
		for _, replay := range []struct{ sql, message string }{
			{q + ";", "restore accepts only generated sequence state"},
			{"WITH d AS (DELETE FROM dst) INSERT INTO src VALUES(9,90);", "restore INSERT WITH clauses are unsupported"},
			{"WITH d AS (DELETE FROM dst) SELECT setval('controls_s',99,true);", "restore accepts only generated sequence state"},
		} {
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(replay.sql))
			want := fixture.ControlsReferenceDiagnostic("0A000", replay.message)
			fixture.AssertDiagnostic(t, err, &want)
			after.Reset()
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				t.Fatal("failed Restore changed Dump", err)
			}
		}
	})
}
