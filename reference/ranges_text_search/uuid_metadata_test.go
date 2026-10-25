package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

type uuidReferenceValuer string

func (v uuidReferenceValuer) Value() (driver.Value, error) { return string(v), nil }

func uuidReferenceColumns(t *testing.T, types []*sql.ColumnType, pg bool, nullable []bool) {
	t.Helper()
	if len(types) != len(nullable) {
		t.Fatal(types, nullable)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if typ.Name() != "v" || typ.DatabaseTypeName() != "UUID" || typ.ScanType() != scan {
			t.Fatal(typ)
		}
		if _, ok := typ.Length(); ok {
			t.Fatal("UUID length")
		}
		if _, _, ok := typ.DecimalSize(); ok {
			t.Fatal("UUID precision")
		}
		n, known := typ.Nullable()
		if pg {
			if n || known {
				t.Fatal(n, known)
			}
		} else if !known || n != nullable[i] {
			t.Fatal(n, known)
		}
	}
}

func TestReferenceUUIDScanBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE u(v UUID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO u VALUES($1),($2),(NULL)", 3, uuidA, uuidF))
	for _, backend := range p.Backends() {
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
		var text string
		var buf []byte
		var nullable sql.NullString
		q := "SELECT v FROM u WHERE v=$1"
		// Keep the original non-Context typed scanners alongside the separate
		// Context consumers below; only the latter carry the recorder key.
		if err := backend.DB.QueryRow(q, uuidA).Scan(&text); err != nil || text != uuidA {
			t.Fatal(text, err)
		}
		if err := backend.DB.QueryRow(q, uuidA).Scan(&buf); err != nil || string(buf) != uuidA {
			t.Fatal(buf, err)
		}
		buf[0] = '0'
		if err := backend.DB.QueryRow("SELECT v FROM u ORDER BY v DESC").Scan(&nullable); err != nil || nullable.Valid {
			t.Fatal(nullable, err)
		}
		nonContextErr := backend.DB.QueryRow("SELECT v FROM u ORDER BY v DESC").Scan(&text)
		var nonContextState interface{ SQLState() string }
		if nonContextErr == nil || errors.As(nonContextErr, &nonContextState) {
			t.Fatal("ordinary non-Context NULL-to-string scan error", nonContextErr)
		}
		if err := backend.DB.QueryRowContext(ctx, q, uuidA).Scan(&text); err != nil || text != uuidA {
			t.Fatal(text, err)
		}
		if err := backend.DB.QueryRowContext(ctx, q, uuidA).Scan(&buf); err != nil || string(buf) != uuidA {
			t.Fatal(buf, err)
		}
		buf[0] = '0'
		if err := backend.DB.QueryRowContext(ctx, "SELECT v FROM u ORDER BY v DESC").Scan(&nullable); err != nil || nullable.Valid {
			t.Fatal(nullable, err)
		}
		err := backend.DB.QueryRowContext(ctx, "SELECT v FROM u ORDER BY v DESC").Scan(&text)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("ordinary NULL-to-string scan error", err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"UUID"}, [][]any{{uuidA}}, uuidA))
	}
	p.Step(t, fixture.Exec("CREATE TABLE required(v UUID NOT NULL)", 0))
	for _, backend := range p.Backends() {
		rows, err := backend.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), "SELECT v,v FROM required")
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		uuidReferenceColumns(t, types, backend.PG, []bool{false, false})
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("required table not empty", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v,v FROM required", []string{"v", "v"}, []string{"UUID", "UUID"}, nil))
	}
}
