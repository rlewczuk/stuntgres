package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

// Release authority: REL_18_6/src/include/catalog/pg_proc.h. Neither these
// expectations nor the user-declaration matrices consume native descriptors.
var pgProcColumns = []string{"oid", "proname", "pronamespace", "proowner", "prolang", "procost", "prorows", "provariadic", "prosupport", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "proargdefaults", "protrftypes", "prosrc", "probin", "prosqlbody", "proconfig", "proacl"}
var pgProcTypes = []string{"OID", "NAME", "OID", "OID", "OID", "FLOAT4", "FLOAT4", "OID", "REGPROC", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "CHAR", "CHAR", "INT2", "INT2", "OID", "OIDVECTOR", "_OID", "_CHAR", "_TEXT", "PG_NODE_TREE", "_OID", "TEXT", "TEXT", "PG_NODE_TREE", "_TEXT", "_ACLITEM"}
var pgProcTypeOIDs = []int64{26, 19, 26, 26, 26, 700, 700, 26, 24, 18, 16, 16, 16, 16, 18, 18, 21, 21, 26, 30, 1028, 1002, 1009, 194, 1028, 25, 25, 194, 1009, 1034}

func pgProcNullable(i int) bool { return i >= 20 && i != 25 }

func pgProcBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, columns, types []string, rows [][]any, args ...any) {
	t.Helper()
	s := fixture.Select(q, columns, types, rows, args...)
	fixture.PGAttributeCheck(t, p, b, x, s)
	fixture.PGAttributeCheck(t, p, b, x, fixture.Prepared(s))
}

func pgProcExec(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
	}
}

func pgProcShape(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 30 {
		t.Fatalf("pg_proc width %d, want 30", len(types))
	}
	for i, c := range types {
		if c.Name() != pgProcColumns[i] || c.DatabaseTypeName() != pgProcTypes[i] {
			t.Fatalf("column %d: %s/%s", i+1, c.Name(), c.DatabaseTypeName())
		}
		nullable, known := c.Nullable()
		if known != !b.PG || nullable != (!b.PG && pgProcNullable(i)) {
			t.Fatalf("column %s nullability %v/%v", c.Name(), nullable, known)
		}
		scan := reflect.TypeFor[string]()
		switch pgProcTypes[i] {
		case "OID", "INT2":
			scan = reflect.TypeFor[int64]()
		case "FLOAT4":
			scan = reflect.TypeFor[float64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if b.PG {
			switch pgProcTypes[i] {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "FLOAT4":
				scan = reflect.TypeFor[float32]()
			case "NAME", "CHAR", "OID", "REGPROC", "OIDVECTOR", "_OID", "_CHAR", "_TEXT", "PG_NODE_TREE", "_ACLITEM":
				scan = reflect.TypeFor[any]()
			}
		}
		if c.ScanType() != scan {
			t.Fatalf("column %s ScanType %v, want %v", c.Name(), c.ScanType(), scan)
		}
	}
}

func pgProcAbsRow() []any {
	return []any{int64(1397), "abs", int64(11), int64(10), int64(12), float64(1), float64(0), int64(0), "-", "f", false, false, true, false, "i", "s", int64(1), int64(0), int64(23), "23", nil, nil, nil, nil, nil, "int4abs", nil, nil, nil, nil}
}

func pgProcShapeStep(b fixture.Backend, q string, values [][]any, args ...any) fixture.Step {
	// Match the shared PGAttributeCheck adapter convention without changing SQL
	// types or suppressing fixture.AssertRawValue's OID byte/int64 checks.
	if b.PG {
		adapted := make([][]any, len(values))
		for i, row := range values {
			adapted[i] = append([]any(nil), row...)
			for j, value := range row {
				if pgProcTypes[j] == "OID" && value != nil {
					adapted[i][j] = strconv.FormatInt(value.(int64), 10)
				}
			}
		}
		values = adapted
	}
	s := fixture.Select(q, pgProcColumns, pgProcTypes, values, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgProcShape(t, b, rows) }
	return s
}
