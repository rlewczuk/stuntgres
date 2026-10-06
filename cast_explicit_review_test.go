package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestCastExplicitEqualityDiagnosticOwnership(t *testing.T) {
	_, db := castExplicitSetup(t)
	for _, tc := range []struct {
		q string
		d stuntgres.Error
	}{
		{"SELECT id FROM t WHERE i=int8(TRUE)", stuntgres.Error{Code: "42883", Message: "function int8(boolean) does not exist", Position: "26", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(int8(TRUE) AS text)", stuntgres.Error{Code: "42883", Message: "function int8(boolean) does not exist", Position: "31", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=int8(TRUE)::text", stuntgres.Error{Code: "42883", Message: "function int8(boolean) does not exist", Position: "26", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(TRUE AS bigint)", stuntgres.Error{Code: "42846", Message: "cannot cast type boolean to bigint", Position: "26"}},
		{"SELECT id FROM t WHERE i=TRUE::int8", stuntgres.Error{Code: "42846", Message: "cannot cast type boolean to bigint", Position: "30"}},
		{"SELECT id FROM t WHERE i=TRUE::bool", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(TRUE AS bool)", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=bool(TRUE)", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(bool(TRUE) AS bool)", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.q)
				if rows != nil {
					rows.Close()
					t.Fatal("analysis failure exposed cursor")
				}
				typeCoreDiagnostic(t, err, tc.d)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("analysis failure exposed statement")
				}
				typeCoreDiagnostic(t, err, tc.d)
			})
		})
	}
}

func TestCastExplicitEmptyImmutableExecution(t *testing.T) {
	_, db := castExplicitSetup(t)
	execSQL(t, db, "CREATE TABLE e(i INT4,s TEXT)")
	for _, tc := range []struct {
		q string
		d stuntgres.Error
	}{
		{"SELECT 2147483648::int4 FROM e", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
		{"SELECT CAST(2147483648 AS int4) FROM e", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
		{"SELECT int4(2147483648) FROM e", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
		{"SELECT CAST('oops'::text AS int4) FROM e", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"SELECT text(int4('oops'::text)) FROM e", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"SELECT i FROM e WHERE i=2147483648::int4", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
		{"SELECT i FROM e WHERE i=CAST('oops'::text AS int4)", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"SELECT i FROM e WHERE bool(int4('oops'::text))", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, prepareErr := db.Prepare(tc.q)
						if prepareErr != nil {
							t.Fatalf("execution-owned cast failed Prepare: %v", prepareErr)
						}
						defer stmt.Close()
						rows, err = stmt.Query()
					} else {
						rows, err = db.Query(tc.q)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("immutable invalid cast returned an empty successful cursor")
					}
					typeCoreDiagnostic(t, err, tc.d)
				})
			}
		})
	}
	// A conversion that needs a source row must not be speculatively evaluated.
	for _, q := range []string{"SELECT s::int4 FROM e", "SELECT int4(s)::text FROM e", "SELECT i FROM e WHERE s::bool"} {
		t.Run(q, func(t *testing.T) {
			wantTuples(t, db, q, [][]any{})
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.Query()
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if rows.Next() || rows.Err() != nil {
				t.Fatal("row-dependent empty query", rows.Err())
			}
		})
	}
	t.Run("held-row-dependent-and-rejected-rows", func(t *testing.T) {
		stmt, err := db.Prepare("SELECT s::int4 FROM e")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.Query()
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("empty held execution", rows.Err())
		}
		rows.Close()
		execSQL(t, db, "INSERT INTO e VALUES(1,'oops')")
		rows, err = stmt.Query()
		if rows != nil {
			rows.Close()
			t.Fatal("populated invalid row exposed cursor")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		// A rejected source row does not evaluate its projected conversion.
		wantTuples(t, db, "SELECT s::int4 FROM e WHERE i=0", [][]any{})
		filtered, err := db.Prepare("SELECT s::int4 FROM e WHERE i=0")
		if err != nil {
			t.Fatal(err)
		}
		defer filtered.Close()
		rows, err = filtered.Query()
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("rejected row conversion", rows.Err())
		}
		rows.Close()
		for _, prepared := range []bool{false, true} {
			q := "SELECT 2147483648::int4 FROM e WHERE i=0"
			if prepared {
				constant, prepareErr := db.Prepare(q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				defer constant.Close()
				rows, err = constant.Query()
			} else {
				rows, err = db.Query(q)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("rejected rows suppressed immutable conversion")
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
		}
	})
}
