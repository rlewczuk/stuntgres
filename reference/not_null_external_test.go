//go:build reference

package reference_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceNotNullUpdate(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'keep'),(2,'other')", 2))
	run := func(s referenceStep) { p.step(t, s) }
	diagnostic := referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, keep).", Schema: "public", Table: "t", Column: "a"}
	run(referenceImplicitError("UPDATE t SET a=NULL WHERE a=1", diagnostic))
	run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}, {int64(2), "other"}}))
	run(referenceExec("UPDATE t SET a=3 WHERE a=1", 1))
	run(referencePrepared(referenceImplicitError("UPDATE t SET a=$1 WHERE a=3", diagnostic, nil)))
	run(referencePrepared(referenceExec("UPDATE t SET a=$1 WHERE a=3", 1, 4)))
	run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "other"}, {int64(4), "keep"}}))
}

func TestReferenceNotNullExternalInheritance(t *testing.T) {
	p := newReferencePair(t)
	var notices []referenceDiagnostic
	_, conn, record := noticeReferencePool(t, p, true, &notices)
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
	run(referenceExec("CREATE TABLE nn_parent(a INT NOT NULL)", 0))
	notices = nil
	run(referenceExec("CREATE TABLE nn_child(a INT NULL) INHERITS(nn_parent)", 0))
	want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `merging column "a" with inherited definition`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices, want)
	}
	run(referenceImplicitError("INSERT INTO nn_child VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "nn_child" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn_child", Column: "a"}))
	run(referenceExec("INSERT INTO nn_child VALUES(7)", 1))
	run(referenceSelect("SELECT * FROM ONLY nn_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	run(referenceImplicitError("ALTER TABLE nn_child ALTER a DROP NOT NULL", referenceDiagnostic{Code: "42P16", Message: `cannot drop inherited constraint "nn_parent_a_not_null" of relation "nn_child"`}))
	run(referenceSelect("SELECT attnotnull FROM pg_attribute WHERE attrelid='nn_child'::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
	run(referenceSelect("SELECT conname,conislocal FROM pg_constraint WHERE conrelid='nn_child'::regclass AND contype='n'", []string{"conname", "conislocal"}, []string{"NAME", "BOOL"}, [][]any{{"nn_parent_a_not_null", false}}))
	run(referenceSelect("SELECT a.attname,c.conname FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] WHERE c.conrelid='nn_child'::regclass AND c.contype='n'", []string{"attname", "conname"}, []string{"NAME", "NAME"}, [][]any{{"a", "nn_parent_a_not_null"}}))
	run(referenceExec("CREATE TABLE nullable_parent(a INT)", 0))
	run(referenceExec("CREATE TABLE merged() INHERITS(nullable_parent,nn_parent)", 0))
	run(referenceSelect("SELECT attnotnull FROM pg_attribute WHERE attrelid='merged'::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
	run(referenceImplicitError("INSERT INTO merged VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "merged" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "merged", Column: "a"}))
	run(referenceExec("INSERT INTO merged VALUES(9)", 1))
	run(referenceSelect("SELECT * FROM ONLY merged", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
	run(referenceExec("CREATE TABLE noinherit_parent(a INT CONSTRAINT nn NOT NULL NO INHERIT)", 0))
	run(referenceExec("CREATE TABLE noinherit_child() INHERITS(noinherit_parent)", 0))
	run(referenceExec("INSERT INTO noinherit_child VALUES(NULL)", 1))
	run(referenceSelect("SELECT * FROM noinherit_child", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
	run(referenceImplicitError("INSERT INTO noinherit_parent VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "noinherit_parent" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "noinherit_parent", Column: "a"}))
	run(referenceExec("CREATE TABLE check_unknown(a INT CHECK(a>0))", 0))
	run(referenceExec("INSERT INTO check_unknown VALUES(NULL)", 1))
	run(referenceSelect("SELECT * FROM check_unknown", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
	run(referenceExec("CREATE TABLE excluded(LIKE nn_parent EXCLUDING CONSTRAINTS)", 0))
	run(referenceImplicitError("INSERT INTO excluded VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "excluded" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "excluded", Column: "a"}))
	// Native exclusions are separately asserted, not counted as engine hierarchy coverage.
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE nn_parent(a INT NOT NULL)", 0))
	for _, tc := range []struct{ q, token, message string }{
		{"CREATE TABLE nn_child(a INT NULL) INHERITS(nn_parent)", "INHERITS", "table inheritance is unsupported"},
		{"CREATE TABLE noinherit_parent(a INT CONSTRAINT nn NOT NULL NO INHERIT)", "NO INHERIT", "NOT NULL modifiers are unsupported"},
		{"CREATE TABLE excluded(LIKE nn_parent EXCLUDING CONSTRAINTS)", "EXCLUDING", "LIKE options are unsupported"},
	} {
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
			step.prepared = prepared
			if prepared {
				step.errorStage = "prepare"
			}
			if strings.Contains(tc.q, "LIKE") {
				step.errorStage = ""
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
		}
	}
	for _, name := range []string{"nn_child", "noinherit_parent", "excluded"} {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceImplicitError("SELECT * FROM "+name, referenceDiagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"}))
	}
	// CHECK's UNKNOWN result is supported independently of hierarchy support.
	// Exercise both direct and prepared inputs on the native backend.
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE check_unknown(a INT CHECK(a>0))", 0))
	for _, prepared := range []bool{false, true} {
		step := referenceExec("INSERT INTO check_unknown VALUES($1)", 1, nil)
		step.prepared = prepared
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
		step = referenceImplicitError("INSERT INTO check_unknown VALUES($1)", referenceDiagnostic{Code: "23514", Message: `new row for relation "check_unknown" violates check constraint "check_unknown_a_check"`, Detail: "Failing row contains (0).", Schema: "public", Table: "check_unknown", Constraint: "check_unknown_a_check"}, 0)
		step.prepared = prepared
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT * FROM check_unknown", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}}))
}

func TestReferenceNotNullExternalDeferredValidation(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE nn_valid(a INT)", 0))
	p.step(t, referenceExec("INSERT INTO nn_valid VALUES(NULL)", 1))
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
	run(referenceExec("ALTER TABLE nn_valid ADD CONSTRAINT nn NOT NULL a NOT VALID", 0))
	run(referenceSelect("SELECT convalidated FROM pg_constraint WHERE conrelid='nn_valid'::regclass AND contype='n'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
	run(referenceImplicitError("INSERT INTO nn_valid VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "nn_valid" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn_valid", Column: "a"}))
	run(referenceExec("INSERT INTO nn_valid VALUES(7)", 1))
	run(referenceImplicitError("ALTER TABLE nn_valid VALIDATE CONSTRAINT nn", referenceDiagnostic{Code: "23502", Message: `column "a" of relation "nn_valid" contains null values`, Schema: "public", Table: "nn_valid", Column: "a"}))
	run(referenceSelect("SELECT convalidated FROM pg_constraint WHERE conrelid='nn_valid'::regclass AND contype='n'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
	run(referenceExec("UPDATE nn_valid SET a=8 WHERE a IS NULL", 1))
	run(referenceExec("ALTER TABLE nn_valid VALIDATE CONSTRAINT nn", 0))
	run(referenceSelect("SELECT convalidated FROM pg_constraint WHERE conrelid='nn_valid'::regclass AND contype='n'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}))
	run(referenceSelect("SELECT * FROM nn_valid ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
	for _, q := range []string{"ALTER TABLE nn_valid ADD CONSTRAINT nn NOT NULL a NOT VALID", "ALTER TABLE nn_valid VALIDATE CONSTRAINT nn"} {
		token, message := "NOT VALID", "NOT NULL modifiers are unsupported"
		if strings.Contains(q, "VALIDATE") {
			token, message = "VALIDATE", "ALTER TABLE action is unsupported"
		}
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: message, Position: strconv.Itoa(strings.Index(q, token) + 1)})
			step.prepared = prepared
			if prepared {
				step.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
		}
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT * FROM nn_valid", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
}
