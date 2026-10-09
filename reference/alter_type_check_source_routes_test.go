package reference_test

import (
	"fmt"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeCheckDependenciesSourceConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range alterTypeCheckDependencyCases {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
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
							if tc.name == "widen" {
								run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}, {nil}}))
							}
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

func TestReferenceAlterTypeCheckValidationOrderSourceConsumerRoutes(t *testing.T) {
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
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
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
