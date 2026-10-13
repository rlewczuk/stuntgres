package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgEnumColumns = []string{"oid", "enumtypid", "enumsortorder", "enumlabel"}
var pgEnumTypes = []string{"OID", "OID", "FLOAT4", "NAME"}

const pgEnumProbe = "SELECT * FROM pg_catalog.pg_enum WHERE enumtypid=$1::oid ORDER BY enumsortorder"

type pgEnumRow struct {
	ID, Owner int64
	Order     float64
	Label     string
}

func pgEnumScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgEnumExec(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
}

func pgEnumOwner(t *testing.T, p *fixture.Pair, x fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, "SELECT '"+name+"'::regtype::oid FROM pg_class WHERE oid=3501")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id int64
	if !rows.Next() {
		t.Fatal("owner missing", rows.Err())
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id <= 0 || id > math.MaxUint32 || rows.Next() || rows.Err() != nil {
		t.Fatal("owner identity", id, rows.Err())
	}
	return id
}

func pgEnumMetadata(t *testing.T, rows *sql.Rows, pg, view bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, pgEnumColumns) {
		t.Fatal("enum columns", cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 {
		t.Fatal("enum descriptors", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[float64]()
		}
		if i == 3 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
			if i == 2 {
				scan = reflect.TypeFor[float32]()
			}
		}
		if typ.Name() != pgEnumColumns[i] || typ.DatabaseTypeName() != pgEnumTypes[i] || typ.ScanType() != scan {
			t.Fatalf("descriptor %d: %s %s %v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType())
		}
		// Native stored views describe their ordinary nullable projection;
		// direct catalog origins retain the stronger NOT NULL contract.
		if nullable, known := typ.Nullable(); nullable != (view && !pg) || known == pg {
			t.Fatal("enum nullability", i, nullable, known)
		}
		if n, ok := typ.Length(); n != 0 || ok {
			t.Fatal("enum length", i, n, ok)
		}
		if p, s, ok := typ.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatal("enum precision", i, p, s, ok)
		}
	}
}

func pgEnumRead(t *testing.T, rows *sql.Rows, pg bool) []pgEnumRow {
	t.Helper()
	defer rows.Close()
	pgEnumMetadata(t, rows, pg, false)
	out := []pgEnumRow{}
	seen := map[int64]bool{}
	for rows.Next() {
		var raw [4]any
		if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3]); err != nil {
			t.Fatal(err)
		}
		fixture.OidReferenceValue(t, pg, "OID", raw[0])
		fixture.OidReferenceValue(t, pg, "OID", raw[1])
		if _, ok := raw[2].(float64); !ok {
			t.Fatalf("raw FLOAT4 %T", raw[2])
		}
		if pg {
			if _, ok := raw[3].([]byte); !ok {
				t.Fatalf("raw NAME %T", raw[3])
			}
		} else {
			if _, ok := raw[3].(string); !ok {
				t.Fatalf("raw NAME %T", raw[3])
			}
		}
		var r pgEnumRow
		if err := rows.Scan(&r.ID, &r.Owner, &r.Order, &r.Label); err != nil {
			t.Fatal(err)
		}
		if r.ID <= 0 || r.ID > math.MaxUint32 || seen[r.ID] || r.Owner <= 0 || r.Owner > math.MaxUint32 || math.IsNaN(r.Order) || math.IsInf(r.Order, 0) {
			t.Fatal("invalid member metadata", r)
		}
		// FLOAT4 is binary32. pq parses PostgreSQL's shortest decimal text
		// into float64, which need not equal the widened binary32 value.
		// Keep the raw-cell check above and compare at the SQL type boundary.
		r.Order = float64(float32(r.Order))
		if math.IsInf(r.Order, 0) {
			t.Fatal("FLOAT4 overflow", r)
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for i, a := range out {
		for _, b := range out[i+1:] {
			if a.Owner == b.Owner && a.ID%2 == 0 && b.ID%2 == 0 && ((a.ID < b.ID) != (a.Order < b.Order)) {
				t.Fatal("even OID ordering", a, b)
			}
		}
	}
	return out
}

func pgEnumQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, args ...any) []pgEnumRow {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	return pgEnumRead(t, rows, b.PG)
}

func pgEnumCheck(t *testing.T, got []pgEnumRow, owner int64, labels []string, orders []float64) {
	t.Helper()
	if len(got) != len(labels) {
		t.Fatal("enum count", got, "want", labels)
	}
	for i, r := range got {
		if r.Owner != owner || r.Label != labels[i] || (orders != nil && r.Order != orders[i]) {
			t.Fatalf("member %d: %+v; want owner=%d label=%q orders=%v", i, r, owner, labels[i], orders)
		}
		if i > 0 && got[i-1].Order >= r.Order {
			t.Fatal("non-increasing positions", got)
		}
	}
}

func pgEnumStable(t *testing.T, before, after []pgEnumRow, rename map[string]string, positions bool) {
	t.Helper()
	for _, old := range before {
		label := old.Label
		if s, ok := rename[label]; ok {
			label = s
		}
		found := false
		for _, r := range after {
			if r.Label == label {
				found = true
				if r.ID != old.ID || r.Owner != old.Owner || (positions && r.Order != old.Order) {
					t.Fatal("unstable member", old, r)
				}
			}
		}
		if !found {
			t.Fatal("lost member", old)
		}
	}
}

func pgEnumCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, name, labels string) int64 {
	t.Helper()
	pgEnumExec(t, p, b, x, fmt.Sprintf("CREATE TYPE %s AS ENUM (%s)", name, labels))
	return pgEnumOwner(t, p, x, name)
}
