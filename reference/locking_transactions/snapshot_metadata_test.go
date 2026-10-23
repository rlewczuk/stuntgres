package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func snapshotMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows, kinds []string) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(kinds) {
		t.Fatalf("descriptors %v: %v", types, err)
	}
	for i, kind := range kinds {
		wantName := kind
		wantScan := reflect.TypeFor[[]byte]()
		if b.PG {
			wantName = ""
			wantScan = reflect.TypeFor[any]()
		}
		if kind == "BOOL" {
			wantName = "BOOL"
			wantScan = reflect.TypeFor[bool]()
		}
		if types[i].DatabaseTypeName() != wantName || types[i].ScanType() != wantScan {
			t.Fatalf("column %d name/scan %s/%v want %s/%v", i, types[i].DatabaseTypeName(), types[i].ScanType(), wantName, wantScan)
		}
		if v, known := types[i].Nullable(); v || known {
			t.Fatalf("computed column %d nullability %v/%v", i, v, known)
		}
		if v, known := types[i].Length(); v != 0 || known {
			t.Fatalf("column %d length %d/%v", i, v, known)
		}
		if precision, scale, known := types[i].DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("column %d decimal %d/%d/%v", i, precision, scale, known)
		}
	}
}
func snapshotTypedStep(b fixture.Backend, q string, names, kinds []string, rows [][]any, args ...any) fixture.Step {
	types := append([]string(nil), kinds...)
	for i, kind := range kinds {
		if kind != "BOOL" {
			types[i] = snapshotType(b, kind)
		}
	}
	s := fixture.Select(q, names, types, rows, args...)
	s.RawTypes = append([]string(nil), kinds...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { snapshotMetadata(t, b, r, kinds) }
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if kinds[col] == "BOOL" {
			return false
		}
		if v != nil {
			got, ok := v.([]byte)
			if !ok || string(got) != rows[row][col] {
				t.Fatalf("raw column %d row %d %T %v want %v", col, row, v, v, rows[row][col])
			}
		}
		return true
	}
	return s
}

func TestReferenceSnapshotMetadata(t *testing.T) {
	for _, kind := range []string{"value", "null", "empty", "current", "identity"} {
		t.Run(kind, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				switch kind {
				case "value":
					snapshotBoth(t, p, b, b.DB, snapshotTypedStep(b, "SELECT '10:20:10,14'::pg_snapshot AS s,pg_snapshot_xmin('10:20:10,14') AS lo,pg_snapshot_xmax('10:20:10,14') AS hi,pg_visible_in_snapshot('11','10:20:10,14') AS v", []string{"s", "lo", "hi", "v"}, []string{"PG_SNAPSHOT", "XID8", "XID8", "BOOL"}, [][]any{{"10:20:10,14", "10", "20", true}}))
				case "null":
					snapshotBoth(t, p, b, b.DB, snapshotTypedStep(b, "SELECT NULL::pg_snapshot AS s,pg_snapshot_xmin(NULL) AS lo,pg_visible_in_snapshot(NULL,NULL) AS v", []string{"s", "lo", "v"}, []string{"PG_SNAPSHOT", "XID8", "BOOL"}, [][]any{{nil, nil, nil}}))
				case "empty":
					for _, arg := range []string{"'10:20:'", "NULL::pg_snapshot"} {
						snapshotBoth(t, p, b, b.DB, snapshotTypedStep(b, "SELECT x FROM pg_snapshot_xip("+arg+") AS s(x)", []string{"x"}, []string{"XID8"}, nil))
					}
				case "current":
					c := snapshotConn(t, p, b)
					fixture.LockExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					s := snapshotRead(t, p, c, nil)
					snapshotBoth(t, p, b, c, snapshotTypedStep(b, "SELECT pg_current_snapshot() AS pg_current_snapshot", []string{"pg_current_snapshot"}, []string{"PG_SNAPSHOT"}, [][]any{{s}}))
					fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
				case "identity":
					snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT 'pg_snapshot'::regtype::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{int64(5038)}}))
				}
			})
		})
	}
}

func TestReferenceSnapshotHeld(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := snapshotConn(t, p, b)
			q := "SELECT x FROM pg_snapshot_xip($1) AS s(x) ORDER BY x"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			x := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
			for _, tc := range []struct {
				arg  any
				rows [][]any
			}{
				{"10:20:10,14,15", [][]any{{"10"}, {"14"}, {"15"}}},
				{"10:20:", nil}, {nil, nil}, {[]byte(nil), nil},
				{[]byte("10:20:10,14,15"), [][]any{{"10"}, {"14"}, {"15"}}},
			} {
				fixture.RunStep(t, p.Ctx, x, b.Recorder, snapshotTypedStep(b, q, []string{"x"}, []string{"XID8"}, tc.rows, tc.arg))
			}
			buf := []byte("10:20:10,14,15")
			rows, err := held.QueryContext(p.Ctx, buf)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for i := range buf {
				buf[i] = 'x'
			}
			var got []string
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				got = append(got, v)
			}
			if err := rows.Err(); err != nil || !reflect.DeepEqual(got, []string{"10", "14", "15"}) {
				t.Fatalf("caller buffer ownership %v: %v", got, err)
			}
		})
	})
	t.Run("scalar", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := snapshotConn(t, p, b)
			q := "SELECT pg_snapshot_xmin($1) AS lo,pg_snapshot_xmax($1) AS hi,pg_visible_in_snapshot($2::xid8,$1) AS v"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			x := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
			for _, tc := range []struct {
				arg  any
				id   string
				rows [][]any
			}{
				{"10:20:10,14", "11", [][]any{{"10", "20", true}}},
				{[]byte("1:18446744073709551615:3"), "18446744073709551615", [][]any{{"1", "18446744073709551615", false}}},
				{nil, "11", [][]any{{nil, nil, nil}}}, {[]byte(nil), "11", [][]any{{nil, nil, nil}}},
				{"10:20:10,14", "14", [][]any{{"10", "20", false}}},
			} {
				fixture.RunStep(t, p.Ctx, x, b.Recorder, snapshotTypedStep(b, q, []string{"lo", "hi", "v"}, []string{"XID8", "XID8", "BOOL"}, tc.rows, tc.arg, tc.id))
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type pg_snapshot: "bad"`, Context: "unnamed portal parameter $1 = '...'"}
			s := fixture.ErrorDiagnostic(q, want, "bad", "11")
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, x, b.Recorder, snapshotTypedStep(b, q, []string{"lo", "hi", "v"}, []string{"XID8", "XID8", "BOOL"}, [][]any{{"10", "20", true}}, "10:20:", "11"))
		})
	})
}
