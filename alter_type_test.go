package stuntgres_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
)

func TestAlterTypeAssignmentAndUsing(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, decl, seed, action, typ string
			rows                          [][]any
			want                          stuntgres.Error
		}{
			{name: "assignment", decl: "INT", seed: "(1,1),(2,NULL),(3,-2)", action: "TYPE BIGINT", typ: "INT8", rows: [][]any{{int64(1)}, {nil}, {int64(-2)}}},
			{name: "using", decl: "TEXT", seed: "(1,'41'),(2,NULL),(3,'7')", action: "TYPE INT USING c::integer", typ: "INT4", rows: [][]any{{int64(41)}, {nil}, {int64(7)}}},
			{name: "late-invalid", decl: "TEXT", seed: "(1,'41'),(2,'oops')", action: "TYPE INT USING c::integer", typ: "TEXT", rows: [][]any{{"41"}, {"oops"}}, want: stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
			{name: "narrowing", decl: "BIGINT", seed: "(1,7),(2,2147483648)", action: "TYPE INT", typ: "INT8", rows: [][]any{{int64(7)}, {int64(2147483648)}}, want: stuntgres.Error{Code: "22003", Message: "integer out of range"}},
			{name: "empty-incompatible", decl: "TEXT", action: "TYPE INT", typ: "TEXT", rows: [][]any{}, want: stuntgres.Error{Code: "42804", Message: `column "c" cannot be cast automatically to type integer`, Hint: `You might need to specify "USING c::integer".`}},
			{name: "using-incompatible", decl: "TEXT", seed: "(1,NULL)", action: "TYPE INT USING c", typ: "TEXT", rows: [][]any{{nil}}, want: stuntgres.Error{Code: "42804", Message: `result of USING clause for column "c" cannot be cast automatically to type integer`, Hint: "You might need to add an explicit cast."}},
			{name: "varchar-shrink", decl: "VARCHAR(5)", seed: "(1,'abcd')", action: "TYPE VARCHAR(2)", typ: "VARCHAR", rows: [][]any{{"abcd"}}, want: stuntgres.Error{Code: "22001", Message: "value too long for type character varying(2)"}},
			{name: "explicit-shrink", decl: "VARCHAR(5)", seed: "(1,'é🙂x')", action: "TYPE VARCHAR(2) USING c::varchar(2)", typ: "VARCHAR", rows: [][]any{{"é🙂"}}},
			{name: "default-incompatible", decl: "TEXT DEFAULT '7'", seed: "(1,'8')", action: "TYPE INT USING c::int", typ: "TEXT", rows: [][]any{{"8"}}, want: stuntgres.Error{Code: "42804", Message: `default for column "c" cannot be cast automatically to type integer`}},
			{name: "not-null", decl: "INT CONSTRAINT nn NOT NULL", seed: "(1,7)", action: "TYPE INT USING NULL", typ: "INT4", rows: [][]any{{int64(7)}}, want: stuntgres.Error{Code: "23502", Message: `column "c" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "c"}},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(id INT,c "+tc.decl+")")
				if tc.seed != "" {
					execSQL(t, db, "INSERT INTO t VALUES"+tc.seed)
				}
				q := "ALTER TABLE t ALTER COLUMN c " + tc.action
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("effect-free Prepare", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				if tc.want.Code != "" {
					notNullDiagnostic(t, err, tc.want)
				} else if err != nil {
					t.Fatal(q, err)
				}
				alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY id", []string{"c"}, []string{tc.typ}, tc.rows)
			})
		}
	}
}

func alterTypePublicRows(t *testing.T, db interface {
	Query(string, ...any) (*sql.Rows, error)
}, q string, names, types []string, want [][]any) {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatal(labels, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, c := range metadata {
		if c.DatabaseTypeName() != types[i] {
			t.Fatal(c, types[i])
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}
