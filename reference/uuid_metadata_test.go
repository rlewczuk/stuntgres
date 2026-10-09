package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
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
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE u(v UUID)", 0))
	p.step(t, referenceExec("INSERT INTO u VALUES($1),($2),(NULL)", 3, uuidA, uuidF))
	for _, backend := range p.backends() {
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
		var text string
		var buf []byte
		var nullable sql.NullString
		q := "SELECT v FROM u WHERE v=$1"
		// Keep the original non-Context typed scanners alongside the separate
		// Context consumers below; only the latter carry the recorder key.
		if err := backend.db.QueryRow(q, uuidA).Scan(&text); err != nil || text != uuidA {
			t.Fatal(text, err)
		}
		if err := backend.db.QueryRow(q, uuidA).Scan(&buf); err != nil || string(buf) != uuidA {
			t.Fatal(buf, err)
		}
		buf[0] = '0'
		if err := backend.db.QueryRow("SELECT v FROM u ORDER BY v DESC").Scan(&nullable); err != nil || nullable.Valid {
			t.Fatal(nullable, err)
		}
		nonContextErr := backend.db.QueryRow("SELECT v FROM u ORDER BY v DESC").Scan(&text)
		var nonContextState interface{ SQLState() string }
		if nonContextErr == nil || errors.As(nonContextErr, &nonContextState) {
			t.Fatal("ordinary non-Context NULL-to-string scan error", nonContextErr)
		}
		if err := backend.db.QueryRowContext(ctx, q, uuidA).Scan(&text); err != nil || text != uuidA {
			t.Fatal(text, err)
		}
		if err := backend.db.QueryRowContext(ctx, q, uuidA).Scan(&buf); err != nil || string(buf) != uuidA {
			t.Fatal(buf, err)
		}
		buf[0] = '0'
		if err := backend.db.QueryRowContext(ctx, "SELECT v FROM u ORDER BY v DESC").Scan(&nullable); err != nil || nullable.Valid {
			t.Fatal(nullable, err)
		}
		err := backend.db.QueryRowContext(ctx, "SELECT v FROM u ORDER BY v DESC").Scan(&text)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("ordinary NULL-to-string scan error", err)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect(q, []string{"v"}, []string{"UUID"}, [][]any{{uuidA}}, uuidA))
	}
	p.step(t, referenceExec("CREATE TABLE required(v UUID NOT NULL)", 0))
	for _, backend := range p.backends() {
		rows, err := backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), "SELECT v,v FROM required")
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		uuidReferenceColumns(t, types, backend.pg, []bool{false, false})
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("required table not empty", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v,v FROM required", []string{"v", "v"}, []string{"UUID", "UUID"}, nil))
	}
}
