package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestAlterTypeCheckDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, declaration, seed, action string
			names, types                    []string
			rows                            [][]any
			want                            stuntgres.Error
		}{
			{name: "widen", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1),(2),(NULL)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}, {int64(2)}, {nil}}},
			{name: "same-type-negative", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1),(2)", action: "ALTER c TYPE INT USING -c", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}, {int64(2)}}, want: stuntgres.Error{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"}},
			{name: "incompatible", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: stuntgres.Error{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "incompatible-empty", declaration: "c INT CONSTRAINT ck CHECK(c>0)", action: "ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{}, want: stuntgres.Error{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "incompatible-null", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(NULL)", action: "ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{nil}}, want: stuntgres.Error{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "default-first", declaration: "c TEXT DEFAULT '7' CONSTRAINT ck CHECK(c>'0')", seed: "('7')", action: "ALTER c TYPE INT USING c::int", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"7"}}, want: stuntgres.Error{Code: "42804", Message: `default for column "c" cannot be cast automatically to type integer`}},
			{name: "final-tuple", declaration: "a INT,b INT,CONSTRAINT ck CHECK(a<b)", seed: "(2,7)", action: "ALTER a TYPE BIGINT USING b, ALTER b TYPE BIGINT USING a*10", names: []string{"a", "b"}, types: []string{"INT8", "INT8"}, rows: [][]any{{int64(7), int64(20)}}},
			{name: "drop-remap", declaration: "p INT,c INT CONSTRAINT ck CHECK(c>0)", seed: "(9,1)", action: "ALTER c TYPE BIGINT, DROP p", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}}},
			{name: "early-check-before-later-conversion", declaration: "c TEXT CHECK(c<>'0')", seed: "('1'),('bad')", action: "ALTER c TYPE TEXT USING (c::int-1)::text", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}, {"bad"}}, want: stuntgres.Error{Code: "23514", Message: `check constraint "t_c_check" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "t_c_check"}},
			{name: "early-nn-before-later-conversion", declaration: "c TEXT CONSTRAINT nn NOT NULL", seed: "('1'),('bad')", action: "ALTER c TYPE INT USING CASE WHEN c='1' THEN NULL ELSE c::int END", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}, {"bad"}}, want: stuntgres.Error{Code: "23502", Message: `column "c" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "c"}},
			{name: "compatible-empty", declaration: "c INT CONSTRAINT ck CHECK(c>0)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{}},
			{name: "compatible-null", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(NULL)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{nil}}},
			{name: "late-false", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(3),(2)", action: "ALTER c TYPE BIGINT USING c-2", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(2)}, {int64(3)}}, want: stuntgres.Error{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"}},
			{name: "check-evaluation", declaration: "c INT CONSTRAINT ck CHECK(10/c>0)", seed: "(1)", action: "ALTER c TYPE BIGINT USING c-1", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: stuntgres.Error{Code: "22012", Message: "division by zero"}},
			{name: "nn-before-check", declaration: "c INT NOT NULL CONSTRAINT ck CHECK(c IS NOT NULL)", seed: "(1)", action: "ALTER c TYPE BIGINT USING NULL", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: stuntgres.Error{Code: "23502", Message: `column "c" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "c"}},
			{name: "drop-remap-leading", declaration: "p INT,c INT CONSTRAINT ck CHECK(c>0)", seed: "(9,1)", action: "DROP p, ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}}},
			{name: "drop-check-leading", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "DROP CONSTRAINT ck, ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}}},
			{name: "drop-check-trailing", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "ALTER c TYPE TEXT USING c::text, DROP CONSTRAINT ck", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}}},
			{name: "drop-checked-column", declaration: "p INT CONSTRAINT independent CHECK(p>0),c INT CONSTRAINT ck CHECK(c>0)", seed: "(9,1)", action: "DROP c, ALTER p TYPE BIGINT", names: []string{"p"}, types: []string{"INT8"}, rows: [][]any{{int64(9)}}},
			{name: "binding-before-conversion", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "ALTER c TYPE TEXT USING (10/(c-c))::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: stuntgres.Error{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "using-increment", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1),(2),(NULL)", action: "ALTER c TYPE BIGINT USING c+1", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(2)}, {int64(3)}, {nil}}},
			{name: "explicit-cast", declaration: "c INT CONSTRAINT ck CHECK(c::text<>'0') NO INHERIT", seed: "(1),(NULL)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}, {nil}}},
			{name: "explicit-literal-cast", declaration: "c INT CONSTRAINT ck CHECK(c::text<>1::text) NO INHERIT", seed: "(2),(NULL)", action: "ALTER c TYPE BIGINT USING c+1", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(3)}, {nil}}},
			{name: "floating-resolved-literal", declaration: "c REAL CONSTRAINT ck CHECK(c>'1.0000001' AND c<>NULL)", seed: "(2),(NULL)", action: "ALTER c TYPE DOUBLE PRECISION USING c", names: []string{"c"}, types: []string{"FLOAT8"}, rows: [][]any{{float64(2)}, {nil}}},
			{name: "uuid-resolved-literal", declaration: "c UUID CONSTRAINT ck CHECK(c>'00000000-0000-0000-0000-000000000000' AND c<>NULL)", seed: "('00000000-0000-0000-0000-000000000001'),(NULL)", action: "ALTER c TYPE UUID USING c", names: []string{"c"}, types: []string{"UUID"}, rows: [][]any{{"00000000-0000-0000-0000-000000000001"}, {nil}}},
			{name: "bytea-resolved-literal", declaration: `c BYTEA CONSTRAINT ck CHECK(c>'\x00' AND c<>NULL)`, seed: `('\x01'),(NULL)`, action: "ALTER c TYPE BYTEA USING c", names: []string{"c"}, types: []string{"BYTEA"}, rows: [][]any{{[]byte{1}}, {nil}}},
			{name: "later-set-nn", declaration: "c INT CONSTRAINT ck CHECK(c>0),n INT", seed: "(1,NULL)", action: "ALTER c TYPE BIGINT, ALTER n SET NOT NULL", names: []string{"c", "n"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), nil}}, want: stuntgres.Error{Code: "23502", Message: `column "n" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "n"}},
			{name: "later-add-nn", declaration: "c INT CONSTRAINT ck CHECK(c>0),n INT", seed: "(1,NULL)", action: "ALTER c TYPE BIGINT, ADD CONSTRAINT n_nn NOT NULL n", names: []string{"c", "n"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), nil}}, want: stuntgres.Error{Code: "23502", Message: `column "n" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "n"}},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t("+tc.declaration+")")
				if tc.seed != "" {
					execSQL(t, db, "INSERT INTO t VALUES"+tc.seed)
				}
				q := "ALTER TABLE t " + tc.action
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
				alterTypePublicRows(t, db, "SELECT * FROM t ORDER BY 1", tc.names, tc.types, tc.rows)
				if tc.name == "widen" {
					execSQL(t, db, "INSERT INTO t VALUES(3),(NULL)")
					_, err = db.Exec("INSERT INTO t VALUES(-1)")
					notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1).", Schema: "public", Table: "t", Constraint: "ck"})
					alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}, {nil}})
				}
				if tc.name == "floating-resolved-literal" {
					execSQL(t, db, "INSERT INTO t VALUES(2.5),(NULL)")
					_, err = db.Exec("INSERT INTO t VALUES(1.00000011)")
					notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (1.00000011).", Schema: "public", Table: "t", Constraint: "ck"})
					alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(2)}, {float64(2.5)}, {nil}, {nil}})
				}
			})
		}
	}
}

func TestAlterTypeCheckValidationOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ name, declaration, action, constraint string }{
			{"a-first", "a INT CONSTRAINT z CHECK(a>0),b INT CONSTRAINT aa CHECK(b>0)", "ALTER a TYPE INT USING -a, ALTER b TYPE INT USING -b", "z"},
			{"b-first", "a INT CONSTRAINT z CHECK(a>0),b INT CONSTRAINT aa CHECK(b>0)", "ALTER b TYPE INT USING -b, ALTER a TYPE INT USING -a", "aa"},
			{"declaration-z-first", "a INT CONSTRAINT z CHECK(a>0) CONSTRAINT aa CHECK(a>0),b INT", "ALTER a TYPE INT USING -a", "z"},
			{"declaration-aa-first", "a INT CONSTRAINT aa CHECK(a>0) CONSTRAINT z CHECK(a>0),b INT", "ALTER a TYPE INT USING -a", "aa"},
			{"multicolumn-a-first", "a INT,b INT,CONSTRAINT z CHECK(a>0),CONSTRAINT aa CHECK(b>0),CONSTRAINT middle CHECK(a>b)", "ALTER a TYPE INT USING -a, ALTER b TYPE INT USING -b", "z"},
			{"multicolumn-b-first", "a INT,b INT,CONSTRAINT z CHECK(a>0),CONSTRAINT aa CHECK(b>0),CONSTRAINT middle CHECK(a>b)", "ALTER b TYPE INT USING -b, ALTER a TYPE INT USING -a", "aa"},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t("+tc.declaration+")")
				execSQL(t, db, "INSERT INTO t VALUES(2,1)")
				q := "ALTER TABLE t " + tc.action
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`check constraint "%s" of relation "t" is violated by some row`, tc.constraint), Schema: "public", Table: "t", Constraint: tc.constraint})
				alterTypePublicRows(t, db, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}})
			})
		}
	}
}
