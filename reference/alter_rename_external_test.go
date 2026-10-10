package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceAlterRenameExternal(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,CONSTRAINT fixed_pk PRIMARY KEY(a))", 0))
			if b.pg {
				run(referenceExec("CREATE TABLE child(a INT CONSTRAINT fixed_fk REFERENCES t(a))", 0))
			}
			for _, s := range []referenceStep{
				referenceExec("CREATE VIEW v AS SELECT a FROM t", 0),
				referenceExec("INSERT INTO t VALUES(7)", 1),
				referenceExec("ALTER TABLE t RENAME a TO renamed", 0),
				referenceExec("ALTER TABLE t RENAME TO newtable", 0),
				referenceSelect("SELECT * FROM v", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}),
				referenceSelect("SELECT c.conname FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='newtable' ORDER BY c.conname", []string{"conname"}, []string{"NAME"}, [][]any{{"fixed_pk"}, {"nn"}}),
			} {
				run(s)
			}
			if b.pg {
				run(referenceExec("INSERT INTO child VALUES(7)", 1))
				run(referenceImplicitError("INSERT INTO child VALUES(99)", referenceDiagnostic{Code: "23503", Message: `insert or update on table "child" violates foreign key constraint "fixed_fk"`, Detail: `Key (a)=(99) is not present in table "newtable".`, Schema: "public", Table: "child", Constraint: "fixed_fk"}))
				run(referenceExec("CREATE TABLE inherited() INHERITS(newtable)", 0))
				run(referenceExec("ALTER TABLE newtable RENAME renamed TO inherited_name", 0))
				run(referenceSelect("SELECT attname FROM pg_attribute WHERE attrelid='inherited'::regclass AND attnum>0", []string{"attname"}, []string{"NAME"}, [][]any{{"inherited_name"}}))
				tx, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(p.ctx, "ALTER TABLE newtable RENAME TO temporary"); err != nil {
					t.Fatal(err)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT inherited_name FROM newtable", []string{"inherited_name"}, []string{"INT4"}, [][]any{{int64(7)}}))
			} else {
				q := "CREATE TABLE fk(a INT REFERENCES t(a))"
				run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: referencePosition(strings.Index(q, "REFERENCES") + 1)}))
			}
		})
	}
}
