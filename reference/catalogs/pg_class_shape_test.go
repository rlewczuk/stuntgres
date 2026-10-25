package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

var pgClassTypes = []string{"OID", "NAME", "OID", "OID", "OID", "OID", "OID", "OID", "OID", "INT4", "FLOAT4", "INT4", "INT4", "OID", "BOOL", "BOOL", "CHAR", "CHAR", "INT2", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "CHAR", "BOOL", "OID", "XID", "XID", "_ACLITEM", "_TEXT", "PG_NODE_TREE"}

func pgClassScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	pgClassBackends(t, fixture.NewScenario(t), f)
}

func pgClassBackends(t *testing.T, p *fixture.Pair, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

// Expectations are numeric OIDs supplied by callers, never learned from native
// output. XID deliberately has its own external decimal-byte contract.
func pgClassCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	want := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		want[i] = append([]any(nil), row...)
		for j, v := range row {
			if b.PG && s.Types[j] == "OID" && v != nil {
				want[i][j] = strconv.FormatInt(v.(int64), 10)
			}
		}
	}
	s.Rows = want
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if s.Types[col] != "XID" {
			return false
		}
		if _, ok := v.([]byte); !ok {
			t.Fatalf("XID raw got %T %#v, want decimal bytes", v, v)
		}
		if _, err := strconv.ParseUint(string(v.([]byte)), 10, 32); err != nil {
			t.Fatal("XID", v, err)
		}
		return true
	}
	fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
}

func pgClassMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, fixture.PGClassColumns) {
		t.Fatal("34 labels", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 34 {
		t.Fatal("34 descriptors", types, err)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PGClassColumns[i] || typ.DatabaseTypeName() != pgClassTypes[i] {
			t.Fatalf("descriptor %d: %s/%s want %s/%s", i+1, typ.Name(), typ.DatabaseTypeName(), fixture.PGClassColumns[i], pgClassTypes[i])
		}
		if nullable, known := typ.Nullable(); (!pg && (!known || nullable != (i >= 31))) || (pg && known) {
			t.Fatal("nullability", i, nullable, known)
		}
		scan := reflect.TypeFor[string]()
		switch pgClassTypes[i] {
		case "OID", "INT2", "INT4":
			scan = reflect.TypeFor[int64]()
		case "FLOAT4":
			scan = reflect.TypeFor[float64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "XID":
			scan = reflect.TypeFor[[]byte]()
		}
		if pg {
			switch pgClassTypes[i] {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "FLOAT4":
				scan = reflect.TypeFor[float32]()
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
		names[0] = "corrupted"
		again, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(again, fixture.PGClassColumns) {
			t.Fatal("detached labels", again, err)
		}
	}
}

func TestReferencePgClassShape(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_class WHERE FALSE", "SELECT * FROM pg_class WHERE oid=4294967295::oid"} {
			s := fixture.Select(q, fixture.PGClassColumns, pgClassTypes, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgClassMetadata(t, rows, b.PG) }
			for _, plain := range []bool{false, true} {
				s.NonContext = plain
				pgClassCheck(t, p, b, b.DB, s)
				pgClassCheck(t, p, b, b.DB, fixture.Prepared(s))
			}
			s.NonContext = false
			held := fixture.ViewHold(t, p, b, q)
			pgClassCheck(t, p, b, held, s)
			pgClassCheck(t, p, b, held, s)
		}
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc18_shape_heap(id integer)")
		q := "SELECT relacl,reloptions,relpartbound FROM pg_class WHERE oid='pc18_shape_heap'::regclass::oid"
		pgClassCheck(t, p, b, b.DB, fixture.Select(q, fixture.PGClassColumns[31:], pgClassTypes[31:], [][]any{{nil, nil, nil}}))
		// Fixed-field NULL detection is independent of physical counter values.
		for _, column := range fixture.PGClassColumns[:31] {
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT "+column+" IS NULL AS absent FROM pg_class WHERE oid='pc18_shape_heap'::regclass::oid", []string{"absent"}, []string{"BOOL"}, [][]any{{false}}))
		}
	})
}
