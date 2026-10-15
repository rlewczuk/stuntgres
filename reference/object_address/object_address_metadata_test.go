package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func objectAddressMetadata(t *testing.T, rows *sql.Rows, pg bool, names, types []string) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, names) {
		t.Fatal("column labels", cols, names, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal("metadata width", metadata, err)
	}
	for i, col := range metadata {
		scan := reflect.TypeFor[string]()
		switch types[i] {
		case "OID", "INT4", "INT8":
			scan = reflect.TypeFor[int64]()
		}
		if pg && types[i] != "TEXT" && types[i] != "INT4" && types[i] != "INT8" {
			scan = reflect.TypeFor[any]()
		}
		if pg && types[i] == "INT4" {
			scan = reflect.TypeFor[int32]()
		}
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan {
			t.Fatalf("descriptor %d %q/%q/%v want %q/%q/%v", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), names[i], types[i], scan)
		}
		// Follow existing scalar/composite expression metadata, not table NOT NULL.
		if n, k := col.Nullable(); n || k {
			t.Fatal("computed nullable must be unknown", i, n, k)
		}
		wantLength, wantKnown := int64(0), false
		if types[i] == "TEXT" {
			wantLength, wantKnown = math.MaxInt64, true
		}
		if n, k := col.Length(); n != wantLength || k != wantKnown {
			t.Fatal("length", i, n, k, wantLength, wantKnown)
		}
		if a, z, k := col.DecimalSize(); a != 0 || z != 0 || k {
			t.Fatal("decimal", i, a, z, k)
		}
	}
}

func TestReferenceObjectAddressMetadata(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		addressRow := []any{int64(1247), int64(23), int64(0)}
		if b.PG {
			addressRow = []any{"1247", "23", int64(0)}
		}
		for _, tc := range []struct {
			q            string
			names, types []string
			row          []any
		}{
			{"SELECT pg_identify_object(2615,'public'::regnamespace,0) AS v", []string{"v"}, []string{"RECORD"}, []any{"(schema,,public,public)"}},
			{"SELECT pg_identify_object_as_address(2615,'public'::regnamespace,0) AS v", []string{"v"}, []string{"RECORD"}, []any{"(schema,{public},{})"}},
			{"SELECT pg_get_object_address('type','{integer}','{}') AS v", []string{"v"}, []string{"RECORD"}, []any{"(1247,23,0)"}},
			{"SELECT pg_describe_object(2615,'public'::regnamespace,0) AS v", []string{"v"}, []string{"TEXT"}, []any{"schema public"}},
			{"SELECT * FROM pg_identify_object(2615,'public'::regnamespace,0)", []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, []any{"schema", nil, "public", "public"}},
			{"SELECT * FROM pg_identify_object_as_address(2615,'public'::regnamespace,0)", []string{"type", "object_names", "object_args"}, []string{"TEXT", "_TEXT", "_TEXT"}, []any{"schema", "{public}", "{}"}},
			{"SELECT * FROM pg_get_object_address('type','{integer}','{}')", []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}, addressRow},
			{"SELECT * FROM pg_identify_object(NULL,2200,0) AS x(kind,namespace,relation,identity)", []string{"kind", "namespace", "relation", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, []any{nil, nil, nil, nil}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				for _, limit := range []bool{false, true} {
					q := tc.q
					want := [][]any{tc.row}
					if limit {
						q += " LIMIT 0"
						want = nil
					}
					s := fixture.Select(q, tc.names, tc.types, want)
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) { objectAddressMetadata(t, r, b.PG, tc.names, tc.types) }
					objectAddressRun(t, p, b, c, s)
				}
			})
		}
	})
}
