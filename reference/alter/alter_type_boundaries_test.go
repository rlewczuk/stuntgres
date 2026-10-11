package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeForeignKeyBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE parent(c INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE child(c INT REFERENCES parent(c))", 0))
			run(fixture.Exec("INSERT INTO parent VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO child VALUES(1)", 1))
			ids := make(map[string]int64)
			names := []string{"parent_pkey", "child_c_fkey"}
			for _, name := range names {
				var id int64
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname=$1", name).Scan(&id); err != nil || id <= 0 {
					t.Fatal(name, id, err)
				}
				ids[name] = id
			}
			run(fixture.Exec("ALTER TABLE parent ALTER c TYPE BIGINT", 0))
			for name, oldID := range ids {
				var newID int64
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname=$1", name).Scan(&newID); err != nil || newID <= 0 || newID == oldID {
					t.Fatal("successful TYPE must rebuild dependency identity", name, oldID, newID, err)
				}
				run(fixture.Select("SELECT count(*)::int FROM pg_constraint WHERE oid=$1", []string{"count"}, []string{"INT4"}, [][]any{{int64(0)}}, oldID))
				if b.PG {
					run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}, newID))
				}
			}
			if b.PG {
				run(fixture.Select("SELECT fk.conindid=pk.conindid AS linked FROM pg_constraint fk,pg_constraint pk WHERE fk.conname='child_c_fkey' AND pk.conname='parent_pkey'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
				run(fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid=(SELECT conindid FROM pg_constraint WHERE conname='parent_pkey')", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
			}
			run(fixture.Select("SELECT c FROM parent", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='parent' AND c.conname='parent_pkey'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (c)"}}))
			failure := fixture.ImplicitError("INSERT INTO child VALUES(2)", fixture.Diagnostic{Code: "23503", Message: `insert or update on table "child" violates foreign key constraint "child_c_fkey"`, Detail: `Key (c)=(2) is not present in table "parent".`, Schema: "public", Table: "child", Constraint: "child_c_fkey"})
			failure.ErrorStage = "execute"
			run(failure)
			run(fixture.Select("SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='child' AND c.conname='child_c_fkey'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (c) REFERENCES parent(c)"}}))
			run(fixture.Exec("INSERT INTO child VALUES(1)", 1))
			run(fixture.Select("SELECT c FROM child ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		})
	}
}
