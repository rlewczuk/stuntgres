package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgConstraintTypes = []string{"OID", "NAME", "OID", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "OID", "OID", "OID", "OID", "OID", "CHAR", "CHAR", "CHAR", "BOOL", "INT2", "BOOL", "BOOL", "_INT2", "_INT2", "_OID", "_OID", "_OID", "_INT2", "_OID", "PG_NODE_TREE"}

func pgConstraintScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func pgConstraintCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
}

func pgConstraintMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, fixture.PGConstraintColumns) {
		t.Fatal("28 labels", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 28 {
		t.Fatal("28 descriptors", types, err)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PGConstraintColumns[i] || typ.DatabaseTypeName() != pgConstraintTypes[i] {
			t.Fatalf("descriptor %d: %s/%s", i+1, typ.Name(), typ.DatabaseTypeName())
		}
		if nullable, known := typ.Nullable(); (!pg && (!known || nullable != (i >= 20))) || (pg && known) {
			t.Fatal("nullability", i, nullable, known)
		}
		scan := reflect.TypeFor[string]()
		switch pgConstraintTypes[i] {
		case "OID", "INT2":
			scan = reflect.TypeFor[int64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if pg {
			switch pgConstraintTypes[i] {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "BOOL":
			default:
				scan = reflect.TypeFor[any]()
			}
		}
		if typ.ScanType() != scan {
			t.Fatal("ScanType", i, typ.ScanType(), scan)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal("decimal", i, n, scale, known)
		}
	}
	if !pg {
		names[0] = "corrupt"
		again, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(again, fixture.PGConstraintColumns) {
			t.Fatal("detached labels", again, err)
		}
	}
}

func pgConstraintID(t *testing.T, p *fixture.Pair, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid=$1::regclass::oid AND conname=$2", table, name).Scan(&id); err != nil || id <= 0 {
		t.Fatal("constraint identity", id, err)
	}
	return id
}
