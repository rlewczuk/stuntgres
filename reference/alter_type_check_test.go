//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterTypeCheckDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, declaration, seed, action string
			names, types                    []string
			rows                            [][]any
			want                            referenceDiagnostic
		}{
			{name: "widen", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1),(2),(NULL)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}, {int64(2)}, {nil}}},
			{name: "same-type-negative", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1),(2)", action: "ALTER c TYPE INT USING -c", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}, {int64(2)}}, want: referenceDiagnostic{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"}},
			{name: "incompatible", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "incompatible-empty", declaration: "c INT CONSTRAINT ck CHECK(c>0)", action: "ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{}, want: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "incompatible-null", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(NULL)", action: "ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{nil}}, want: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "default-first", declaration: "c TEXT DEFAULT '7' CONSTRAINT ck CHECK(c>'0')", seed: "('7')", action: "ALTER c TYPE INT USING c::int", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"7"}}, want: referenceDiagnostic{Code: "42804", Message: `default for column "c" cannot be cast automatically to type integer`}},
			{name: "final-tuple", declaration: "a INT,b INT,CONSTRAINT ck CHECK(a<b)", seed: "(2,7)", action: "ALTER a TYPE BIGINT USING b, ALTER b TYPE BIGINT USING a*10", names: []string{"a", "b"}, types: []string{"INT8", "INT8"}, rows: [][]any{{int64(7), int64(20)}}},
			{name: "drop-remap", declaration: "p INT,c INT CONSTRAINT ck CHECK(c>0)", seed: "(9,1)", action: "ALTER c TYPE BIGINT, DROP p", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}}},
			{name: "early-check-before-later-conversion", declaration: "c TEXT CHECK(c<>'0')", seed: "('1'),('bad')", action: "ALTER c TYPE TEXT USING (c::int-1)::text", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}, {"bad"}}, want: referenceDiagnostic{Code: "23514", Message: `check constraint "t_c_check" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "t_c_check"}},
			{name: "early-nn-before-later-conversion", declaration: "c TEXT CONSTRAINT nn NOT NULL", seed: "('1'),('bad')", action: "ALTER c TYPE INT USING CASE WHEN c='1' THEN NULL ELSE c::int END", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}, {"bad"}}, want: referenceDiagnostic{Code: "23502", Message: `column "c" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "c"}},
			{name: "compatible-empty", declaration: "c INT CONSTRAINT ck CHECK(c>0)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{}},
			{name: "compatible-null", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(NULL)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{nil}}},
			{name: "late-false", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(3),(2)", action: "ALTER c TYPE BIGINT USING c-2", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(2)}, {int64(3)}}, want: referenceDiagnostic{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"}},
			{name: "check-evaluation", declaration: "c INT CONSTRAINT ck CHECK(10/c>0)", seed: "(1)", action: "ALTER c TYPE BIGINT USING c-1", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: referenceDiagnostic{Code: "22012", Message: "division by zero"}},
			{name: "nn-before-check", declaration: "c INT NOT NULL CONSTRAINT ck CHECK(c IS NOT NULL)", seed: "(1)", action: "ALTER c TYPE BIGINT USING NULL", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: referenceDiagnostic{Code: "23502", Message: `column "c" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "c"}},
			{name: "drop-remap-leading", declaration: "p INT,c INT CONSTRAINT ck CHECK(c>0)", seed: "(9,1)", action: "DROP p, ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}}},
			{name: "drop-check-leading", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "DROP CONSTRAINT ck, ALTER c TYPE TEXT USING c::text", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}}},
			{name: "drop-check-trailing", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "ALTER c TYPE TEXT USING c::text, DROP CONSTRAINT ck", names: []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"1"}}},
			{name: "drop-checked-column", declaration: "p INT CONSTRAINT independent CHECK(p>0),c INT CONSTRAINT ck CHECK(c>0)", seed: "(9,1)", action: "DROP c, ALTER p TYPE BIGINT", names: []string{"p"}, types: []string{"INT8"}, rows: [][]any{{int64(9)}}},
			{name: "binding-before-conversion", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1)", action: "ALTER c TYPE TEXT USING (10/(c-c))::text", names: []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, want: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{name: "using-increment", declaration: "c INT CONSTRAINT ck CHECK(c>0)", seed: "(1),(2),(NULL)", action: "ALTER c TYPE BIGINT USING c+1", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(2)}, {int64(3)}, {nil}}},
			{name: "explicit-cast", declaration: "c INT CONSTRAINT ck CHECK(c::text<>'0') NO INHERIT", seed: "(1),(NULL)", action: "ALTER c TYPE BIGINT", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}, {nil}}},
			{name: "explicit-literal-cast", declaration: "c INT CONSTRAINT ck CHECK(c::text<>1::text) NO INHERIT", seed: "(2),(NULL)", action: "ALTER c TYPE BIGINT USING c+1", names: []string{"c"}, types: []string{"INT8"}, rows: [][]any{{int64(3)}, {nil}}},
			{name: "floating-resolved-literal", declaration: "c REAL CONSTRAINT ck CHECK(c>'1.0000001' AND c<>NULL)", seed: "(2),(NULL)", action: "ALTER c TYPE DOUBLE PRECISION USING c", names: []string{"c"}, types: []string{"FLOAT8"}, rows: [][]any{{float64(2)}, {nil}}},
			{name: "uuid-resolved-literal", declaration: "c UUID CONSTRAINT ck CHECK(c>'00000000-0000-0000-0000-000000000000' AND c<>NULL)", seed: "('00000000-0000-0000-0000-000000000001'),(NULL)", action: "ALTER c TYPE UUID USING c", names: []string{"c"}, types: []string{"UUID"}, rows: [][]any{{"00000000-0000-0000-0000-000000000001"}, {nil}}},
			{name: "bytea-resolved-literal", declaration: `c BYTEA CONSTRAINT ck CHECK(c>'\x00' AND c<>NULL)`, seed: `('\x01'),(NULL)`, action: "ALTER c TYPE BYTEA USING c", names: []string{"c"}, types: []string{"BYTEA"}, rows: [][]any{{[]byte{1}}, {nil}}},
			{name: "later-set-nn", declaration: "c INT CONSTRAINT ck CHECK(c>0),n INT", seed: "(1,NULL)", action: "ALTER c TYPE BIGINT, ALTER n SET NOT NULL", names: []string{"c", "n"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), nil}}, want: referenceDiagnostic{Code: "23502", Message: `column "n" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "n"}},
			{name: "later-add-nn", declaration: "c INT CONSTRAINT ck CHECK(c>0),n INT", seed: "(1,NULL)", action: "ALTER c TYPE BIGINT, ADD CONSTRAINT n_nn NOT NULL n", names: []string{"c", "n"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), nil}}, want: referenceDiagnostic{Code: "23502", Message: `column "n" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "n"}},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferencePair(t)
				for _, b := range []struct {
					name   string
					db     *sql.DB
					record *referenceRecorder
				}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
						run(referenceExec("CREATE TABLE t("+tc.declaration+")", 0))
						if tc.seed != "" {
							run(referenceExec("INSERT INTO t VALUES"+tc.seed, int64(len(tc.rows))))
						}
						var checkID int64
						if b.name == "postgres" && tc.name == "same-type-negative" {
							if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid='t'::regclass AND conname='ck'").Scan(&checkID); err != nil || checkID <= 0 {
								t.Fatal(checkID, err)
							}
						}
						q := "ALTER TABLE t " + tc.action
						step := referenceExec(q, 0)
						if tc.want.Code != "" {
							step = referenceImplicitError(q, tc.want)
							step.errorStage = "execute"
						}
						step.prepared = prepared
						run(step)
						run(referenceSelect("SELECT * FROM t ORDER BY 1", tc.names, tc.types, tc.rows))
						if b.name == "postgres" && tc.name == "same-type-negative" {
							var retainedID int64
							if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid='t'::regclass AND conname='ck'").Scan(&retainedID); err != nil || retainedID != checkID {
								t.Fatal("failed TYPE replaced CHECK", checkID, retainedID, err)
							}
							run(referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass AND conname='ck'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK ((c > 0))"}}))
						}
						if tc.name == "widen" || tc.name == "same-type-negative" || tc.name == "incompatible" {
							run(referenceExec("INSERT INTO t VALUES(3),(NULL)", 2))
							failure := referenceImplicitError("INSERT INTO t VALUES(-1)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1).", Schema: "public", Table: "t", Constraint: "ck"})
							failure.errorStage = "execute"
							run(failure)
						}
						if tc.name == "floating-resolved-literal" {
							run(referenceExec("INSERT INTO t VALUES(2.5),(NULL)", 2))
							failure := referenceImplicitError("INSERT INTO t VALUES(1.00000011)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (1.00000011).", Schema: "public", Table: "t", Constraint: "ck"})
							failure.errorStage = "execute"
							run(failure)
							run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(2)}, {float64(2.5)}, {nil}, {nil}}))
						}
					})
				}
			})
		}
	}
}

func TestReferenceAlterTypeCheckValidationOrder(t *testing.T) {
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
				p := newReferencePair(t)
				for _, b := range []struct {
					name   string
					db     *sql.DB
					record *referenceRecorder
				}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
						run(referenceExec("CREATE TABLE t("+tc.declaration+")", 0))
						run(referenceExec("INSERT INTO t VALUES(2,1)", 1))
						failure := referenceImplicitError("ALTER TABLE t "+tc.action, referenceDiagnostic{Code: "23514", Message: fmt.Sprintf(`check constraint "%s" of relation "t" is violated by some row`, tc.constraint), Schema: "public", Table: "t", Constraint: tc.constraint})
						failure.prepared = prepared
						failure.errorStage = "execute"
						run(failure)
						run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}))
					})
				}
			})
		}
	}
}
