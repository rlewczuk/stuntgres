package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

// Expectations are the PostgreSQL three-valued table, not evaluator output.
func TestLogicalTruthSelectWhere(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "logical-truth")
	execSQL(t, db, "CREATE TABLE t(id INT,a BOOL,b BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,TRUE,TRUE),(2,TRUE,FALSE),(3,TRUE,NULL),(4,FALSE,TRUE),(5,FALSE,FALSE),(6,FALSE,NULL),(7,NULL,TRUE),(8,NULL,FALSE),(9,NULL,NULL)")
	execSQL(t, db, "CREATE TABLE one(id INT)")
	execSQL(t, db, "INSERT INTO one VALUES(1)")
	pairs := []struct {
		a, b    string
		and, or any
	}{
		{"TRUE", "TRUE", true, true}, {"TRUE", "FALSE", false, true}, {"TRUE", "NULL", nil, true},
		{"FALSE", "TRUE", false, true}, {"FALSE", "FALSE", false, false}, {"FALSE", "NULL", false, nil},
		{"NULL", "TRUE", nil, true}, {"NULL", "FALSE", false, nil}, {"NULL", "NULL", nil, nil},
	}
	check := func(q string, want [][]any, typ string) {
		t.Helper()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(q, e)
				}
				defer stmt.Close()
				rows, err = stmt.Query()
			} else {
				rows, err = db.Query(q)
			}
			if err != nil {
				t.Fatal(q, err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(meta) != 1 || meta[0].DatabaseTypeName() != typ {
				t.Fatal(q, meta)
			}
			if typ == "BOOL" {
				if meta[0].Name() != "?column?" || meta[0].ScanType() != reflect.TypeFor[bool]() {
					t.Fatal(meta[0])
				}
				if _, known := meta[0].Nullable(); known {
					t.Fatal("computed nullability invented")
				}
			}
			got := [][]any{}
			for rows.Next() {
				var v any
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				got = append(got, []any{v})
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if !reflect.DeepEqual(got, want) {
				t.Fatal(q, got, want)
			}
			if meta[0].DatabaseTypeName() != typ {
				t.Fatal("metadata lost after close")
			}
		}
	}
	for _, p := range pairs {
		for _, op := range []struct {
			name  string
			value any
		}{{"AND", p.and}, {"OR", p.or}} {
			expr := p.a + " " + op.name + " " + p.b
			check("SELECT "+expr+" FROM one", [][]any{{op.value}}, "BOOL")
			want := [][]any{}
			if op.value == true {
				want = [][]any{{int64(1)}}
			}
			check("SELECT id FROM one WHERE "+expr, want, "INT4")
		}
	}
	for _, p := range []struct {
		input string
		value any
	}{{"TRUE", false}, {"FALSE", true}, {"NULL", nil}} {
		check("SELECT NOT "+p.input+" FROM one", [][]any{{p.value}}, "BOOL")
		want := [][]any{}
		if p.value == true {
			want = [][]any{{int64(1)}}
		}
		check("SELECT id FROM one WHERE NOT "+p.input, want, "INT4")
	}
	wantTuples(t, db, "SELECT id,a AND b,a OR b,NOT a FROM t ORDER BY id", [][]any{
		{int64(1), true, true, false}, {int64(2), false, true, false}, {int64(3), nil, true, false},
		{int64(4), false, true, true}, {int64(5), false, false, true}, {int64(6), false, nil, true},
		{int64(7), nil, true, nil}, {int64(8), false, nil, nil}, {int64(9), nil, nil, nil},
	})
	wantTuples(t, db, "SELECT id FROM t WHERE a AND b ORDER BY id", [][]any{{int64(1)}})
	wantTuples(t, db, "SELECT id FROM t WHERE a OR b ORDER BY id", [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(7)}})
	wantTuples(t, db, "SELECT id FROM t WHERE NOT a ORDER BY id", [][]any{{int64(4)}, {int64(5)}, {int64(6)}})
}
