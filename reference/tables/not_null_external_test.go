package reference_test

import (
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNotNullUpdate(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,'keep'),(2,'other')", 2))
	run := func(s fixture.Step) { p.Step(t, s) }
	diagnostic := fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, keep).", Schema: "public", Table: "t", Column: "a"}
	run(fixture.ImplicitError("UPDATE t SET a=NULL WHERE a=1", diagnostic))
	run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}, {int64(2), "other"}}))
	run(fixture.Exec("UPDATE t SET a=3 WHERE a=1", 1))
	run(fixture.Prepared(fixture.ImplicitError("UPDATE t SET a=$1 WHERE a=3", diagnostic, nil)))
	run(fixture.Prepared(fixture.Exec("UPDATE t SET a=$1 WHERE a=3", 1, 4)))
	run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "other"}, {int64(4), "keep"}}))
}

func TestReferenceNotNullExternalInheritance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				var notices []fixture.Diagnostic
				_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, record, s)
				}
				run(fixture.Exec("CREATE TABLE nn_parent(a INT NOT NULL)", 0))
				notices = nil
				run(fixture.Exec("CREATE TABLE nn_child(a INT NULL) INHERITS(nn_parent)", 0))
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `merging column "a" with inherited definition`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices, want)
				}
				run(fixture.ImplicitError("INSERT INTO nn_child VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "nn_child" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn_child", Column: "a"}))
				run(fixture.Exec("INSERT INTO nn_child VALUES(7)", 1))
				run(fixture.Select("SELECT * FROM nn_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				step := fixture.Exec("INSERT INTO nn_child VALUES(8)", 1)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, tx, record, step)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT * FROM nn_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(fixture.ImplicitError("ALTER TABLE nn_child ALTER a DROP NOT NULL", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited constraint "nn_parent_a_not_null" of relation "nn_child"`}))
				run(fixture.Select("SELECT c.conname FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid WHERE t.relname='nn_child' AND c.contype='n'", []string{"conname"}, []string{"NAME"}, [][]any{{"nn_parent_a_not_null"}}))
				if backend.PG {
					// These catalog columns are not exposed by the native catalog.
					run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='nn_child'::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
					run(fixture.Select("SELECT conname,conislocal FROM pg_constraint WHERE conrelid='nn_child'::regclass AND contype='n'", []string{"conname", "conislocal"}, []string{"NAME", "BOOL"}, [][]any{{"nn_parent_a_not_null", false}}))
					run(fixture.Select("SELECT a.attname,c.conname FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] WHERE c.conrelid='nn_child'::regclass AND c.contype='n'", []string{"attname", "conname"}, []string{"NAME", "NAME"}, [][]any{{"a", "nn_parent_a_not_null"}}))
				}
				run(fixture.Exec("CREATE TABLE nullable_parent(a INT)", 0))
				run(fixture.Exec("CREATE TABLE merged() INHERITS(nullable_parent,nn_parent)", 0))
				if backend.PG {
					run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='merged'::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
				}
				run(fixture.ImplicitError("INSERT INTO merged VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "merged" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "merged", Column: "a"}))
				run(fixture.Exec("INSERT INTO merged VALUES(9)", 1))
				run(fixture.Select("SELECT * FROM merged", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
				q := "CREATE TABLE noinherit_parent(a INT CONSTRAINT nn NOT NULL NO INHERIT)"
				if backend.PG {
					run(fixture.Exec(q, 0))
					run(fixture.Exec("CREATE TABLE noinherit_child() INHERITS(noinherit_parent)", 0))
					run(fixture.Exec("INSERT INTO noinherit_child VALUES(NULL)", 1))
					run(fixture.Select("SELECT * FROM noinherit_child", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
					run(fixture.ImplicitError("INSERT INTO noinherit_parent VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "noinherit_parent" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "noinherit_parent", Column: "a"}))
				} else {
					step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "NOT NULL modifiers are unsupported", Position: strconv.Itoa(strings.Index(q, "NO INHERIT") + 1)})
					if prepared {
						step.ErrorStage = "prepare"
					}
					run(step)
					run(fixture.ImplicitError("SELECT * FROM noinherit_parent", fixture.Diagnostic{Code: "42P01", Message: `relation "noinherit_parent" does not exist`, Position: "15"}))
				}
				run(fixture.Exec("CREATE TABLE check_unknown(a INT CHECK(a>0))", 0))
				run(fixture.Exec("INSERT INTO check_unknown VALUES($1)", 1, nil))
				run(fixture.ImplicitError("INSERT INTO check_unknown VALUES($1)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "check_unknown" violates check constraint "check_unknown_a_check"`, Detail: "Failing row contains (0).", Schema: "public", Table: "check_unknown", Constraint: "check_unknown_a_check"}, 0))
				run(fixture.Select("SELECT * FROM check_unknown", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
			}
		})
	}
}

func TestReferenceNotNullExternalDeferredValidation(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE nn_valid(a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO nn_valid VALUES(NULL)", 1))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE nn_valid ADD CONSTRAINT nn NOT NULL a NOT VALID", 0)))
	p.Step(t, fixture.ImplicitError("INSERT INTO nn_valid VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "nn_valid" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn_valid", Column: "a"}))
	p.Step(t, fixture.Exec("INSERT INTO nn_valid VALUES(7)", 1))
	if p.Postgres != nil {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
		run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='nn_valid'::regclass AND contype='n'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.ImplicitError("ALTER TABLE nn_valid VALIDATE CONSTRAINT nn", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "nn_valid" contains null values`, Schema: "public", Table: "nn_valid", Column: "a"}))
		run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='nn_valid'::regclass AND contype='n'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("UPDATE nn_valid SET a=8 WHERE a IS NULL", 1))
		run(fixture.Exec("ALTER TABLE nn_valid VALIDATE CONSTRAINT nn", 0))
		run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='nn_valid'::regclass AND contype='n'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Select("SELECT * FROM nn_valid ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
	}
	for _, q := range []string{"ALTER TABLE nn_valid VALIDATE CONSTRAINT nn"} {
		token, message := "VALIDATE", "ALTER TABLE action is unsupported"
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: message, Position: strconv.Itoa(strings.Index(q, token) + 1)})
			step.Prepared = prepared
			if prepared {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
		}
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT * FROM nn_valid ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
}
