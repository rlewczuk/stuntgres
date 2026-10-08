package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

func TestBooleanAggregateRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE t(k TEXT,b BOOL,v INT,txt TEXT)",
		"INSERT INTO t VALUES('T',TRUE,1,'true'),('T',TRUE,1,'true'),('T',NULL,1,'true'),('F',FALSE,1,'true'),('F',FALSE,1,'true'),('F',NULL,1,'true'),('M',TRUE,1,'true'),('M',FALSE,1,'true'),('M',NULL,1,'true'),('N',NULL,1,'true'),('N',NULL,1,'true')",
		"CREATE TABLE empty(k TEXT,b BOOL,v INT,txt TEXT)",
		"CREATE TABLE m(b BOOL)",
		"INSERT INTO m VALUES(TRUE),(FALSE),(NULL)",
		"CREATE TABLE nn(b BOOL NOT NULL)",
	} {
		execSQL(t, db, query)
	}
	for _, tc := range []struct {
		query        string
		args         []any
		names, types []string
		want         [][]any
	}{
		{"SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='T'", nil, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}},
		{"SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='F'", nil, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false}}},
		{"SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='M'", nil, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false}}},
		{"SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='N'", nil, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil}}},
		{"SELECT bool_and(b),bool_or(b),every(b) FROM empty", nil, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil}}},
		{"SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE FALSE", nil, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil}}},
		{"SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE NULL", nil, []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil}}},
		{"SELECT k,bool_and(b),bool_or(b),every(b) FROM t GROUP BY k", nil, []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"T", true, true, true}, {"F", false, false, false}, {"M", false, true, false}, {"N", nil, nil, nil}}},
		{"SELECT k,bool_and(b),bool_or(b),every(b) FROM empty GROUP BY k", nil, []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{}},
		{"SELECT count(b),bool_and(b),bool_or(b),every(b),bool_and(b) FROM m", nil, []string{"count", "bool_and", "bool_or", "every", "bool_and"}, []string{"INT8", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(2), false, true, false, false}}},
		{"SELECT bool_and(b),bool_or(b),every(b),count(b) FROM nn", nil, []string{"bool_and", "bool_or", "every", "count"}, []string{"BOOL", "BOOL", "BOOL", "INT8"}, [][]any{{nil, nil, nil, int64(0)}}},
		{"SELECT bool_and('true'),bool_or(NULL) FROM m", nil, []string{"bool_and", "bool_or"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}}},
		{"SELECT every($1) FROM m", []any{true}, []string{"every"}, []string{"BOOL"}, [][]any{{true}}},
		{"SELECT every($1) FROM m", []any{false}, []string{"every"}, []string{"BOOL"}, [][]any{{false}}},
		{"SELECT every($1) FROM m", []any{nil}, []string{"every"}, []string{"BOOL"}, [][]any{{nil}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					query := db.Query
					if prepared {
						stmt, err := db.Prepare(tc.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						query = func(_ string, args ...any) (*sql.Rows, error) { return stmt.Query(args...) }
					}
					rows, err := query(tc.query, tc.args...)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(types) != len(tc.types) {
						t.Fatalf("column count = %d, want %d", len(types), len(tc.types))
					}
					for i, typ := range types {
						if tc.types[i] == "BOOL" {
							if typ.ScanType() != reflect.TypeOf(false) {
								t.Errorf("column %d ScanType = %v, want bool", i, typ.ScanType())
							}
							if nullable, known := typ.Nullable(); !known || !nullable {
								t.Errorf("column %d Nullable = %v,%v, want true,true", i, nullable, known)
							}
						} else if tc.names[i] == "count" {
							if nullable, known := typ.Nullable(); !known || nullable {
								t.Errorf("count Nullable = %v,%v, want false,true", nullable, known)
							}
						}
					}
					queryCoreRows(t, rows, nil, tc.names, tc.types, tc.want, true)
				})
			}
		})
	}
	// Source-grounded expectations; the root observes the pinned oracle before implementation.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range []string{"bool_and", "bool_or", "every"} {
		for _, tc := range []struct {
			query, signature, position string
		}{
			{"SELECT " + name + "(1) FROM t", name + "(integer)", "8"},
			{"SELECT " + name + "('true'::text) FROM t", name + "(text)", "8"},
			{"SELECT " + name + "(txt) FROM t", name + "(text)", "8"},
			{"SELECT /*é*/ " + name + "(1) FROM empty WHERE FALSE", name + "(integer)", "14"},
			{"SELECT pg_catalog." + name + "(1) FROM t", "pg_catalog." + name + "(integer)", "8"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := stuntgres.Error{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: tc.position}
				t.Run("direct", func(t *testing.T) {
					rows, err := db.Query(tc.query)
					if rows != nil {
						rows.Close()
						t.Fatal("failed Query exposed rows")
					}
					typeCoreDiagnostic(t, err, want)
				})
				t.Run("prepared", func(t *testing.T) {
					stmt, err := db.Prepare(tc.query)
					if stmt != nil {
						stmt.Close()
						t.Fatal("invalid signature must fail at Prepare without a statement")
					}
					typeCoreDiagnostic(t, err, want)
				})
			})
		}
	}
}
