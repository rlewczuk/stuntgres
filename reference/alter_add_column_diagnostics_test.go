package reference_test

import "testing"

func TestReferenceAlterAddColumnAtomicFailures(t *testing.T) {
	cases := []struct {
		name, definition string
		want             referenceDiagnostic
	}{
		{"not_null", "b INT NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "b" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "b"}},
		{"null_default", "b INT DEFAULT NULL NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "b" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "b"}},
		{"check", "b INT DEFAULT 7 CONSTRAINT c CHECK(b>id)", referenceDiagnostic{Code: "23514", Message: `check constraint "c" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "c"}},
		{"bad_input", "b INT DEFAULT 'oops'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"overflow", "b INT DEFAULT 2147483648", referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
		{"division", "b INT DEFAULT (1/0)", referenceDiagnostic{Code: "22012", Message: "division by zero"}},
		{"assignment_length", "b VARCHAR(2) DEFAULT 'long'::TEXT", referenceDiagnostic{Code: "22001", Message: "value too long for type character varying(2)"}},
		{"wrong_type", "b INT DEFAULT TRUE", referenceDiagnostic{Code: "42804", Message: `column "b" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
		{"column_default", "b INT DEFAULT id", referenceDiagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression"}},
		{"parameter_default", "b INT DEFAULT $1", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1"}},
		{"integer_check", "b INT CHECK(1)", referenceDiagnostic{Code: "42804", Message: "argument of CHECK must be type boolean, not type integer"}},
		{"missing_check_column", "b INT CHECK(missing>0)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`}},
		{"parameter_check", "b INT CHECK($1>0)", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT NOT NULL CONSTRAINT old_check CHECK(id>0), old INT DEFAULT 3)", 0))
				run(referenceExec("INSERT INTO t(id) VALUES(1),(9)", 2))
				run(referenceExec("COMMENT ON COLUMN t.old IS 'retained'", 0))
				failure := referenceImplicitError("ALTER TABLE t ADD "+tc.definition, tc.want)
				failure.errorStage = "execute"
				run(failure)
				run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "old"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(9), int64(3)}}))
				run(referenceSelect("SELECT col_description('t'::regclass,2)", []string{"col_description"}, []string{"TEXT"}, [][]any{{"retained"}}))
				run(referenceExec("INSERT INTO t(id) VALUES(2)", 1))
				run(referenceExec("ALTER TABLE t ADD b INT DEFAULT 4", 0))
				run(referenceSelect("SELECT b FROM t ORDER BY id", []string{"b"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(4)}, {int64(4)}}))
			})
		})
	}
}

func TestReferenceAlterAddColumnChecks(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		run(referenceExec("ALTER TABLE t ADD b INT CONSTRAINT nullable_positive CHECK(b>0) NO INHERIT", 0))
		run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(2), nil}}))
		run(referenceImplicitError("INSERT INTO t VALUES(3,0)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "nullable_positive"`, Detail: "Failing row contains (3, 0).", Schema: "public", Table: "t", Constraint: "nullable_positive"}))
		run(referenceExec("CREATE TABLE empty_t(id INT)", 0))
		run(referenceExec("ALTER TABLE empty_t ADD b INT CONSTRAINT never CHECK(FALSE)", 0))
		run(referenceImplicitError("INSERT INTO empty_t(id) VALUES(1)", referenceDiagnostic{Code: "23514", Message: `new row for relation "empty_t" violates check constraint "never"`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "empty_t", Constraint: "never"}))
		run(referenceExec("ALTER TABLE t ADD c INT DEFAULT 7 CONSTRAINT forward CHECK(c<d AND c>public.t.id), ADD d INT DEFAULT 8", 0))
		run(referenceSelect("SELECT c,d FROM t ORDER BY id", []string{"c", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}, {int64(7), int64(8)}}))
		run(referenceImplicitError("ALTER TABLE t ADD e INT DEFAULT 0 CONSTRAINT z_first CHECK(e>0) CONSTRAINT a_second CHECK(e<0)", referenceDiagnostic{Code: "23514", Message: `check constraint "z_first" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "z_first"}))
	})
}
