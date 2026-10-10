package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceMergeInsertDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, q, token string
		d              referenceDiagnostic
	}{
		{"missingTarget", "MERGE INTO absent USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)", "absent", referenceDiagnostic{Code: "42P01", Message: `relation "absent" does not exist`}},
		{"missingSource", "MERGE INTO t USING absent ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)", "absent", referenceDiagnostic{Code: "42P01", Message: `relation "absent" does not exist`}},
		{"ambiguous", "MERGE INTO t USING s ON id=s.id WHEN NOT MATCHED THEN INSERT VALUES(1)", "id=s", referenceDiagnostic{Code: "42702", Message: `column reference "id" is ambiguous`}},
		{"collision", "MERGE INTO t USING t ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)", "", referenceDiagnostic{Code: "42712", Message: `name "t" specified more than once`, Detail: "The name is used both as MERGE target table and data source."}},
		{"hiddenCondition", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND t.id=1 THEN INSERT VALUES(1)", "t.id", referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`}},
		{"hiddenValue", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(t.id)", "t.id", referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`}},
		{"whenType", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND 1 THEN INSERT VALUES(1)", "1 THEN", referenceDiagnostic{Code: "42804", Message: "argument of WHEN must be type boolean, not type integer"}},
		{"whenInput", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND 'oops' THEN INSERT VALUES(1)", "'oops'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "oops"`}},
		{"missingColumn", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(missing) VALUES(1)", "missing", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
		{"duplicateColumn", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(id,id) VALUES(1,2)", "id) VALUES", referenceDiagnostic{Code: "42701", Message: `column "id" specified more than once`}},
		{"overflow", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1,2,3)", "3)", referenceDiagnostic{Code: "42601", Message: "INSERT has more expressions than target columns"}},
		{"underfill", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(id,v) VALUES(1)", "v) VALUES", referenceDiagnostic{Code: "42601", Message: "INSERT has more target columns than expressions"}},
		{"assignment", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(TRUE)", "TRUE", referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
		{"sourceTyping", "MERGE INTO t USING (VALUES('2')) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", "s.id)", referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression."}},
		{"unreachable", "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1) WHEN NOT MATCHED AND FALSE THEN INSERT VALUES(2)", "", referenceDiagnostic{Code: "42601", Message: "unreachable WHEN clause specified after unconditional WHEN clause"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
				run := mergeInsertRun(t, p, b, prepared)
				run(referenceExec("CREATE TABLE t(id INT,v TEXT)", 0))
				run(referenceExec("CREATE TABLE s(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(99,'sentinel')", 1))
				q := "/* é */ " + tc.q
				d := tc.d
				if tc.token != "" {
					d.Position = referencePosition(utf8.RuneCountInString(q[:strings.LastIndex(q, tc.token)]) + 1)
				}
				s := referenceImplicitError(q, d)
				s.prepared = prepared
				s.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
				run(referenceSelect("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}}))
			})
		})
	}
}

func TestReferenceMergeInsertAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, q string
		d            referenceDiagnostic
	}{
		{"notNull", "CREATE TABLE t(id INT NOT NULL,v INT)", "MERGE INTO t USING (VALUES(1),(NULL)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", referenceDiagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "t", Column: "id"}},
		{"check", "CREATE TABLE t(id INT CONSTRAINT positive CHECK(id>0),v INT)", "MERGE INTO t USING (VALUES(1),(-1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (-1, null).", Schema: "public", Table: "t", Constraint: "positive"}},
		{"range", "CREATE TABLE t(id INT,v INT)", "MERGE INTO t USING (VALUES(1::bigint),(2147483648::bigint)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
		{"condition", "CREATE TABLE t(id INT,v INT)", "MERGE INTO t USING (VALUES(1),(0)) s(id) ON FALSE WHEN NOT MATCHED AND 1/s.id=1 THEN INSERT VALUES(s.id)", referenceDiagnostic{Code: "22012", Message: "division by zero"}},
		{"assignment", "CREATE TABLE t(id INT,v INT)", "MERGE INTO t USING (VALUES(1),(0)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1/s.id)", referenceDiagnostic{Code: "22012", Message: "division by zero"}},
		{"crossArmUnique", "CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE,v INT)", "MERGE INTO t USING (VALUES(1),(2)) s(id) ON FALSE WHEN NOT MATCHED AND s.id=1 THEN INSERT VALUES(7) WHEN NOT MATCHED THEN INSERT VALUES(7)", referenceDiagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_pkey"`, Detail: "Key (id)=(7) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
				run := mergeInsertRun(t, p, b, prepared)
				run(referenceExec(tc.ddl, 0))
				run(referenceExec("INSERT INTO t VALUES(99,99)", 1))
				s := referenceImplicitError(tc.q, tc.d)
				s.errorStage = "execute"
				run(s)
				run(referenceSelect("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
			})
		})
	}
}
