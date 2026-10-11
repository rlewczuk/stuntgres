package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameExternal(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,CONSTRAINT fixed_pk PRIMARY KEY(a))", 0))
			if b.PG {
				run(fixture.Exec("CREATE TABLE child(a INT CONSTRAINT fixed_fk REFERENCES t(a))", 0))
			}
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE VIEW v AS SELECT a FROM t", 0),
				fixture.Exec("INSERT INTO t VALUES(7)", 1),
				fixture.Exec("ALTER TABLE t RENAME a TO renamed", 0),
				fixture.Exec("ALTER TABLE t RENAME TO newtable", 0),
				fixture.Select("SELECT * FROM v", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}),
				fixture.Select("SELECT c.conname FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='newtable' ORDER BY c.conname", []string{"conname"}, []string{"NAME"}, [][]any{{"fixed_pk"}, {"nn"}}),
			} {
				run(s)
			}
			if b.PG {
				run(fixture.Exec("INSERT INTO child VALUES(7)", 1))
				run(fixture.ImplicitError("INSERT INTO child VALUES(99)", fixture.Diagnostic{Code: "23503", Message: `insert or update on table "child" violates foreign key constraint "fixed_fk"`, Detail: `Key (a)=(99) is not present in table "newtable".`, Schema: "public", Table: "child", Constraint: "fixed_fk"}))
				run(fixture.Exec("CREATE TABLE inherited() INHERITS(newtable)", 0))
				run(fixture.Exec("ALTER TABLE newtable RENAME renamed TO inherited_name", 0))
				run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='inherited'::regclass AND attnum>0", []string{"attname"}, []string{"NAME"}, [][]any{{"inherited_name"}}))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(p.Ctx, "ALTER TABLE newtable RENAME TO temporary"); err != nil {
					t.Fatal(err)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT inherited_name FROM newtable", []string{"inherited_name"}, []string{"INT4"}, [][]any{{int64(7)}}))
			} else {
				q := "CREATE TABLE fk(a INT REFERENCES t(a))"
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: fixture.Position(strings.Index(q, "REFERENCES") + 1)}))
			}
		})
	}
}
