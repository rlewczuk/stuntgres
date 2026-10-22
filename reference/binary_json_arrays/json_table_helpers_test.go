package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTableScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func jsonTableMetadata(s fixture.Step, pg bool, nativeNullable ...bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cts, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cts) != len(s.Types) {
			t.Fatalf("metadata width %d want %d", len(cts), len(s.Types))
		}
		if len(nativeNullable) != 0 && len(nativeNullable) != len(cts) {
			t.Fatalf("native nullability width %d want %d", len(nativeNullable), len(cts))
		}
		for i, ct := range cts {
			if ct.Name() != s.Columns[i] || ct.DatabaseTypeName() != s.Types[i] {
				t.Errorf("column %d: %s %s", i, ct.Name(), ct.DatabaseTypeName())
			}
			want := reflect.TypeFor[string]()
			switch s.Types[i] {
			case "INT4":
				want = reflect.TypeFor[int64]()
				if pg {
					want = reflect.TypeFor[int32]()
				}
			case "BOOL":
				want = reflect.TypeFor[bool]()
			case "JSON", "JSONB":
				if pg {
					want = reflect.TypeFor[any]()
				}
			}
			if ct.ScanType() != want {
				t.Errorf("%s ScanType=%v want=%v", ct.Name(), ct.ScanType(), want)
			}
			// pq does not expose nullability. Native preserves known source and
			// outer-join proofs when this scenario supplies them explicitly.
			nullable, known := false, false
			if !pg && len(nativeNullable) != 0 {
				nullable, known = nativeNullable[i], true
			}
			if n, ok := ct.Nullable(); n != nullable || ok != known {
				t.Errorf("%s Nullable=%v,%v want %v,%v", ct.Name(), n, ok, nullable, known)
			}
			length, hasLength := int64(0), false
			if s.Types[i] == "TEXT" {
				length, hasLength = math.MaxInt64, true
			}
			if n, ok := ct.Length(); n != length || ok != hasLength {
				t.Errorf("%s Length=%d,%v want %d,%v", ct.Name(), n, ok, length, hasLength)
			}
			if precision, scale, ok := ct.DecimalSize(); precision != 0 || scale != 0 || ok {
				t.Errorf("%s DecimalSize=%d,%d,%v want 0,0,false", ct.Name(), precision, scale, ok)
			}
		}
	}
	return s
}

const jsonTableSiblingDocument = `[{"id":7,"a":[10,20],"b":["x"]},{"id":8,"a":[],"b":[]},{"id":9,"b":["z"]}]`
const jsonTableSiblingColumns = ` COLUMNS(r FOR ORDINALITY,id int,NESTED PATH '$.a[*]' COLUMNS(ao FOR ORDINALITY,av int PATH '$' DEFAULT 99 ON EMPTY),NESTED PATH '$.b[*]' COLUMNS(bo FOR ORDINALITY,bv text PATH '$'))`

func jsonTableSiblingStep(q string, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"r", "id", "ao", "av", "bo", "bv"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{
		{int64(1), int64(7), int64(1), int64(10), nil, nil},
		{int64(1), int64(7), int64(2), int64(20), nil, nil},
		{int64(1), int64(7), nil, nil, int64(1), "x"},
		{int64(2), int64(8), nil, nil, nil, nil},
		{int64(3), int64(9), nil, nil, int64(1), "z"},
	}, args...)
	s.Bag = true
	return s
}
