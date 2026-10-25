package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgDatabaseTypes = []string{"OID", "NAME", "OID", "INT4", "CHAR", "BOOL", "BOOL", "BOOL", "INT4", "XID", "XID", "OID", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_ACLITEM"}
var pgDatabaseNullable = []bool{false, false, false, false, false, false, false, false, false, true, true, true, false, false, true, true, true, true}

func pgDatabaseScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	pgDatabaseBackends(t, fixture.NewScenario(t), f)
}

func pgDatabaseBackends(t *testing.T, p *fixture.Pair, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func pgDatabaseMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, fixture.PGDatabaseColumns) {
		t.Fatal("18 labels", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 18 {
		t.Fatal("18 types", types, err)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PGDatabaseColumns[i] || typ.DatabaseTypeName() != pgDatabaseTypes[i] {
			t.Fatalf("column %d: %s/%s", i+1, typ.Name(), typ.DatabaseTypeName())
		}
		if nullable, known := typ.Nullable(); (pg && known) || (!pg && (!known || nullable != pgDatabaseNullable[i])) {
			t.Fatal("nullable", i, nullable, known)
		}
		scan := reflect.TypeFor[string]()
		switch pgDatabaseTypes[i] {
		case "OID", "INT4":
			scan = reflect.TypeFor[int64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "XID":
			scan = reflect.TypeFor[[]byte]()
		}
		if pg {
			switch pgDatabaseTypes[i] {
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "BOOL", "TEXT":
			default:
				scan = reflect.TypeFor[any]()
			}
		}
		if typ.ScanType() != scan {
			t.Fatal("scan type", i, typ.ScanType(), scan)
		}
		wantLength, wantLengthKnown := int64(0), false
		if pgDatabaseTypes[i] == "TEXT" {
			wantLength, wantLengthKnown = math.MaxInt64, true
		}
		if n, known := typ.Length(); n != wantLength || known != wantLengthKnown {
			t.Fatal("length", i, n, known, wantLength, wantLengthKnown)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal("decimal", i, n, scale, known)
		}
	}
	if !pg {
		names[0] = "corrupted"
		again, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(again, fixture.PGDatabaseColumns) {
			t.Fatal("owned labels", again, err)
		}
	}
}

func TestReferencePGDatabaseShape(t *testing.T) {
	pgDatabaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			q    string
			args []any
		}{
			{"SELECT * FROM pg_catalog.pg_database WHERE FALSE", nil},
			{"SELECT * FROM pg_database WHERE FALSE", nil},
			{"SELECT * FROM pg_database WHERE datname=$1", []any{"pgdb18_absent"}},
			{"SELECT * FROM pg_database WHERE datname=$1", []any{nil}},
			{"SELECT * FROM pg_database WHERE oid=$1", []any{int64(4294967295)}},
			{"SELECT * FROM pg_database WHERE oid=$1", []any{nil}},
		} {
			s := fixture.Select(tc.q, fixture.PGDatabaseColumns, pgDatabaseTypes, nil, tc.args...)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgDatabaseMetadata(t, rows, b.PG) }
			for _, plain := range []bool{false, true} {
				s.NonContext = plain
				fixture.PGDatabaseCheck(t, p, b, b.DB, s)
				fixture.PGDatabaseCheck(t, p, b, b.DB, fixture.Prepared(s))
			}
			s.NonContext = false
			held := fixture.ViewHold(t, p, b, tc.q)
			fixture.PGDatabaseCheck(t, p, b, held, s)
			fixture.PGDatabaseCheck(t, p, b, held, s)
		}
	})
}
