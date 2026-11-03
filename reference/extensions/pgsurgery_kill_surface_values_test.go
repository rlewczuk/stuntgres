package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillValues(t *testing.T) {
	t.Run("void_null_empty", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				q    string
				args []any
				rows [][]any
			}{
				{killSQL, []any{"ks_part.heap", "{}"}, [][]any{{""}}},
				{killSQL, []any{nil, "{NULL}"}, [][]any{{nil}}},
				{killSQL, []any{int64(0), nil}, [][]any{{nil}}},
				{killSQL, []any{nil, nil}, [][]any{{nil}}},
				{killSQL + " WHERE false", []any{"ks_part.heap", "{}"}, nil},
			} {
				s := fixture.Select(tc.q, []string{"killed"}, []string{"VOID"}, tc.rows, tc.args...)
				s.CheckMetadata = ksVoidMetadata(b.PG)
				freezeBoth(t, p, b, c, s)
			}
			// SELECT's command tag is not the number of killed tuples.
			freezeBoth(t, p, b, c, fixture.Exec(killSQL, 1, "ks_part.heap", "{}"))
			freezeBoth(t, p, b, c, fixture.Exec(killSQL+" WHERE false", 0, "ks_part.heap", "{}"))
		})
	})
	for _, call := range []string{
		"ks_part.heap_force_kill($1::regclass,$2::tid[])",
		"ks_part.heap_force_kill(reloid=>$1::regclass,tids=>$2::tid[])",
		"ks_part.heap_force_kill(tids=>$2::tid[],reloid=>$1::regclass)",
	} {
		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run(call+"/"+name, func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'removed') RETURNING k,v,ctid,xmin,xmax")
					z := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(2,'kept') RETURNING k,v,ctid,xmin,xmax")
					s := ksVoid("SELECT "+call+" AS killed", "ks_part.heap", freezeTIDs(a.tid))
					s.Prepared, s.CheckMetadata = prepared, ksVoidMetadata(b.PG)
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c, z)
				})
			})
		}
	}
	t.Run("named_formal_slot_children", func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run(name, func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					freezeRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE ks_part.eval_order", 0))
					a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'formal-slot') RETURNING k,v,ctid,xmin,xmax")
					q := "SELECT ks_part.heap_force_kill(tids=>ARRAY[CASE WHEN nextval('ks_part.eval_order')=2 THEN $1::tid ELSE '(0,0)'::tid END],reloid=>CASE WHEN nextval('ks_part.eval_order')=1 THEN 'ks_part.heap'::regclass ELSE 0::regclass END) AS killed"
					s := ksVoid(q, a.tid)
					s.Prepared = prepared
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c)
					freezeBoth(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM ks_part.eval_order", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
				})
			})
		}
	})
}

func ksVoidMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatalf("VOID metadata %v: %v", columns, err)
		}
		col := columns[0]
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if col.Name() != "killed" || col.DatabaseTypeName() != "VOID" || col.ScanType() != scan {
			t.Fatalf("VOID descriptor %q/%q/%v, want killed/VOID/%v", col.Name(), col.DatabaseTypeName(), col.ScanType(), scan)
		}
		if value, known := col.Nullable(); value || known {
			t.Fatalf("VOID nullable %t/%t", value, known)
		}
		if value, known := col.Length(); value != 0 || known {
			t.Fatalf("VOID length %d/%t", value, known)
		}
		if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("VOID decimal %d,%d/%t", precision, scale, known)
		}
	}
}
