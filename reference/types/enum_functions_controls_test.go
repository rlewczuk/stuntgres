package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func enumFunctionsControls(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE enum_defaults(lo mood DEFAULT enum_first(NULL::mood),hi mood DEFAULT enum_last(NULL::mood))",
		"CREATE VIEW enum_ranges AS SELECT enum_range(lo,hi) AS v FROM enum_defaults",
	} {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO enum_defaults DEFAULT VALUES", 1))
	check := func() {
		t.Helper()
		fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT lo,hi FROM enum_defaults", []string{"lo", "hi"}, []string{"", ""}, [][]any{{"z", "m"}}))
		s := fixture.Select("SELECT v FROM enum_ranges", []string{"v"}, []string{enumFunctionsTypeName(b.PG, "_MOOD")}, [][]any{{"{z,a,m}"}})
		s.RawTypes = []string{"_ENUM"}
		s.CheckMetadata = enumFunctionsMetadataNullable(b.PG, true, true)
		fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, s)
	}
	check()
	if b.PG {
		return
	}
	var held []*sql.Stmt
	for _, q := range []string{"SELECT enum_first($1::mood) AS v", "SELECT enum_last(NULL::mood) AS v", "SELECT enum_range(NULL::mood) AS v"} {
		s, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		held = append(held, s)
	}
	rawConn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rawConn.Close()
	err = rawConn.Raw(func(raw any) error {
		eager, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT enum_first(NULL::mood) AS first,enum_range(NULL::mood) AS all_values", nil)
		if err != nil {
			return err
		}
		defer eager.Close()
		metadata := eager.(driver.RowsColumnTypeDatabaseTypeName)
		assertMeta := func() {
			if metadata.ColumnTypeDatabaseTypeName(0) != "" || metadata.ColumnTypeDatabaseTypeName(1) != "_MOOD" || eager.(driver.RowsColumnTypeScanType).ColumnTypeScanType(1) != reflect.TypeFor[string]() {
				t.Fatal("retained enum function metadata changed")
			}
		}
		assertMeta()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		bad := "CREATE TYPE replacement AS ENUM ('ok'); CREATE TABLE incoming(v replacement); INSERT INTO incoming VALUES ('nope');"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid staged replacement succeeded")
		}
		check()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if rows, err := conn.QueryContext(canceled, "SELECT enum_range(NULL::mood)"); !errors.Is(err, context.Canceled) || rows != nil {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("canceled enum query", err)
		}
		check()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		// Force fresh execution of both stored defaults and the range view.
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DELETE FROM enum_defaults", 1))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO enum_defaults DEFAULT VALUES", 1))
		check()
		for i, s := range held {
			var args []any
			message := "cached plan must not change result type"
			if i == 0 {
				args = []any{nil}
				message = "cached plan must not change parameter type"
			}
			rows, err := s.QueryContext(p.Ctx, args...)
			if rows != nil {
				rows.Close()
				t.Fatal("old enum contract exposed cursor")
			}
			fixture.EnumReferenceAssertDiagnostic(t, err, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: message})
		}
		enumFunctionsSelect(t, p, b, conn, "SELECT enum_range($1::mood) AS v", "{z,a,m}", true, true, nil)
		assertMeta()
		dst := make([]driver.Value, 2)
		if err := eager.Next(dst); err != nil || dst[0] != "z" || dst[1] != "{z,a,m}" {
			t.Fatal("retained values", dst, err)
		}
		assertMeta()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReferenceEnumFunctionsControls(t *testing.T) {
	enumFunctionsScenario(t, enumFunctionsControls)
}
