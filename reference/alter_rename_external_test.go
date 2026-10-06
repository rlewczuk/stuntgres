//go:build reference

package reference_test

import "testing"

func TestReferenceAlterRenameExternal(t *testing.T) {
	p := newReferencePair(t)
	record := &referenceRecorder{}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, record, s) }
	for _, q := range []string{"CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,CONSTRAINT fixed_pk PRIMARY KEY(a))", "CREATE TABLE child(a INT CONSTRAINT fixed_fk REFERENCES t(a))", "CREATE VIEW v AS SELECT a FROM t", "INSERT INTO t VALUES(7)", "ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		n := int64(0)
		if q == "INSERT INTO t VALUES(7)" {
			n = 1
		}
		run(referenceExec(q, n))
	}
	run(referenceSelect("SELECT * FROM v", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	run(referenceExec("INSERT INTO child VALUES(7)", 1))
	run(referenceImplicitError("INSERT INTO child VALUES(99)", referenceDiagnostic{Code: "23503", Message: `insert or update on table "child" violates foreign key constraint "fixed_fk"`, Detail: `Key (a)=(99) is not present in table "newtable".`, Schema: "public", Table: "child", Constraint: "fixed_fk"}))
	run(referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='newtable'::regclass ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"fixed_pk"}, {"nn"}}))
	run(referenceExec("CREATE TABLE inherited() INHERITS(newtable)", 0))
	run(referenceExec("ALTER TABLE newtable RENAME renamed TO inherited_name", 0))
	run(referenceSelect("SELECT attname FROM pg_attribute WHERE attrelid='inherited'::regclass AND attnum>0", []string{"attname"}, []string{"NAME"}, [][]any{{"inherited_name"}}))
	tx, err := p.postgres.BeginTx(p.ctx, nil)
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
	for _, q := range []string{"CREATE VIEW v AS SELECT a FROM t", "CREATE TABLE pk(a INT PRIMARY KEY)", "CREATE TABLE fk(a INT REFERENCES t(a))"} {
		_, err := p.memory.ExecContext(p.ctx, q)
		referenceState(t, err, "0A000")
	}
}
