package reference_test

import "testing"

func TestReferenceAlterTypeForeignKeyBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE parent(c INT PRIMARY KEY)", 0))
			if b.pg {
				run(referenceExec("CREATE TABLE child(c INT REFERENCES parent(c))", 0))
			} else {
				failure := referenceImplicitError("CREATE TABLE child(c INT REFERENCES parent(c))", referenceDiagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: "26"})
				failure.errorStage = "execute"
				run(failure)
			}
			run(referenceExec("INSERT INTO parent VALUES(1)", 1))
			if b.pg {
				run(referenceExec("INSERT INTO child VALUES(1)", 1))
			}
			ids := make(map[string]int64)
			names := []string{"parent_pkey"}
			if b.pg {
				names = append(names, "child_c_fkey")
			}
			for _, name := range names {
				var id int64
				if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname=$1", name).Scan(&id); err != nil || id <= 0 {
					t.Fatal(name, id, err)
				}
				ids[name] = id
			}
			run(referenceExec("ALTER TABLE parent ALTER c TYPE BIGINT", 0))
			for name, oldID := range ids {
				var newID int64
				if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname=$1", name).Scan(&newID); err != nil || newID <= 0 || newID == oldID {
					t.Fatal("successful TYPE must rebuild dependency identity", name, oldID, newID, err)
				}
				run(referenceSelect("SELECT count(*)::int FROM pg_constraint WHERE oid=$1", []string{"count"}, []string{"INT4"}, [][]any{{int64(0)}}, oldID))
				if b.pg {
					run(referenceSelect("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}, newID))
				}
			}
			if b.pg {
				run(referenceSelect("SELECT fk.conindid=pk.conindid AS linked FROM pg_constraint fk,pg_constraint pk WHERE fk.conname='child_c_fkey' AND pk.conname='parent_pkey'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
				run(referenceSelect("SELECT indisvalid FROM pg_index WHERE indexrelid=(SELECT conindid FROM pg_constraint WHERE conname='parent_pkey')", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
			}
			run(referenceSelect("SELECT c FROM parent", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(referenceSelect("SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='parent' AND c.conname='parent_pkey'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (c)"}}))
			if b.pg {
				failure := referenceImplicitError("INSERT INTO child VALUES(2)", referenceDiagnostic{Code: "23503", Message: `insert or update on table "child" violates foreign key constraint "child_c_fkey"`, Detail: `Key (c)=(2) is not present in table "parent".`, Schema: "public", Table: "child", Constraint: "child_c_fkey"})
				failure.errorStage = "execute"
				run(failure)
				run(referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='child'::regclass AND conname='child_c_fkey'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (c) REFERENCES parent(c)"}}))
				run(referenceExec("INSERT INTO child VALUES(1)", 1))
				run(referenceSelect("SELECT c FROM child ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
			}
		})
	}
}
