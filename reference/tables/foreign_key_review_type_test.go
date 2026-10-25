package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignKeyTypeAtomicity(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE type_parent(id INT PRIMARY KEY,note TEXT)", 0))
		run(fixture.Exec("CREATE TABLE type_child(pid INT CONSTRAINT type_fk REFERENCES type_parent,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO type_parent VALUES(1,'parent'),(2,'untouched')", 2))
		run(fixture.Exec("INSERT INTO type_child VALUES(1,'child'),(NULL,'nullable')", 2))
		identityQueries := []string{
			"SELECT oid::bigint FROM pg_class WHERE relname='type_parent'",
			"SELECT oid::bigint FROM pg_class WHERE relname='type_child'",
			"SELECT oid::bigint FROM pg_class WHERE relname='type_parent_pkey'",
			"SELECT oid::bigint FROM pg_constraint WHERE conname='type_parent_pkey'",
			"SELECT oid::bigint FROM pg_constraint WHERE conname='type_fk'",
		}
		capture := func() []int64 {
			values := make([]int64, len(identityQueries))
			for i, q := range identityQueries {
				if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&values[i]); err != nil || values[i] <= 0 {
					t.Fatal(q, values[i], err)
				}
			}
			return values
		}
		original := capture()
		unchanged := func() {
			current := capture()
			for i, old := range original {
				if current[i] != old {
					t.Fatal("failed TYPE changed identity", identityQueries[i], old, current[i])
				}
			}
			run(fixture.Select("SELECT id,note FROM type_parent ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "parent"}, {int64(2), "untouched"}}))
			run(fixture.Select("SELECT pid,note FROM type_child ORDER BY note", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "child"}, {nil, "nullable"}}))
			run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='type_fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (pid) REFERENCES type_parent(id)"}}))
			run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='type_parent_pkey'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (id)"}}))
			if b.PG {
				run(fixture.Select("SELECT fk.conindid=pk.conindid AS linked FROM pg_constraint fk,pg_constraint pk WHERE fk.conname='type_fk' AND pk.conname='type_parent_pkey'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
				run(fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid=(SELECT conindid FROM pg_constraint WHERE conname='type_parent_pkey')", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
			}
		}
		for _, tc := range []struct {
			query string
			error fixture.Diagnostic
		}{
			{"ALTER TABLE type_parent ALTER id TYPE BIGINT USING id+10", fixture.ForeignKeyChild("type_child", "type_fk", `Key (pid)=(1) is not present in table "type_parent".`)},
			{"ALTER TABLE type_child ALTER pid TYPE BIGINT USING pid+10", fixture.ForeignKeyChild("type_child", "type_fk", `Key (pid)=(11) is not present in table "type_parent".`)},
			{"ALTER TABLE type_parent ALTER id TYPE TEXT USING id::text", fixture.ForeignKeyError("42804", `foreign key constraint "type_fk" cannot be implemented`, `Key columns "pid" of the referencing table and "id" of the referenced table are of incompatible types: integer and text.`, "", "")},
			{"ALTER TABLE type_child ALTER pid TYPE TEXT USING pid::text", fixture.ForeignKeyError("42804", `foreign key constraint "type_fk" cannot be implemented`, `Key columns "pid" of the referencing table and "id" of the referenced table are of incompatible types: text and integer.`, "", "")},
			{"ALTER TABLE type_child ALTER pid TYPE NUMERIC USING pid::numeric", fixture.ForeignKeyError("42804", `foreign key constraint "type_fk" cannot be implemented`, `Key columns "pid" of the referencing table and "id" of the referenced table are of incompatible types: numeric and integer.`, "", "")},
		} {
			for _, prepared := range []bool{false, true} {
				run(fixture.ForeignKeyFail(tc.query, tc.error, prepared))
				unchanged()
				run(fixture.Prepared(fixture.Exec("INSERT INTO type_child VALUES(2,'probe')", 1)))
				run(fixture.ForeignKeyFail("INSERT INTO type_child VALUES(9,'missing')", fixture.ForeignKeyChild("type_child", "type_fk", `Key (pid)=(9) is not present in table "type_parent".`), true))
				run(fixture.Prepared(fixture.Exec("DELETE FROM type_child WHERE note='probe'", 1)))
				unchanged()
			}
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, "INSERT INTO type_child VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Prepared(fixture.Exec("ALTER TABLE type_child ALTER pid TYPE BIGINT USING pid", 0)))
		current := capture()
		for i := range len(original) - 1 {
			if current[i] != original[i] {
				t.Fatal("child TYPE rebuilt unchanged parent/table/index identity", identityQueries[i], original[i], current[i])
			}
		}
		if current[4] == original[4] {
			t.Fatal("child TYPE did not rebuild FK identity", original[4], current[4])
		}
		run(fixture.Select("SELECT count(*)::int FROM pg_constraint WHERE oid=$1", []string{"count"}, []string{"INT4"}, [][]any{{int64(0)}}, original[4]))
		if b.PG {
			run(fixture.Select("SELECT fk.conindid=pk.conindid AS linked FROM pg_constraint fk,pg_constraint pk WHERE fk.conname='type_fk' AND pk.conname='type_parent_pkey'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conname='type_fk'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}))
		}
		runHeld := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: "INSERT INTO type_child VALUES($1,$2)"}, b.Recorder, s)
		}
		runHeld(fixture.Exec("INSERT INTO type_child VALUES($1,$2)", 1, int64(2), "recovery"))
		// The retained INSERT above keeps its original inferred INT parameter
		// signature. A fresh BIGINT signature exercises FK lookup, not int4 input.
		s := fixture.ForeignKeyFail("INSERT INTO type_child VALUES($1::BIGINT,$2)", fixture.ForeignKeyChild("type_child", "type_fk", `Key (pid)=(2147483648) is not present in table "type_parent".`), true)
		s.Args = []any{int64(2147483648), "outside_parent_width"}
		run(s)
		run(fixture.ForeignKeyFail("INSERT INTO type_child VALUES(9,'missing')", fixture.ForeignKeyChild("type_child", "type_fk", `Key (pid)=(9) is not present in table "type_parent".`), true))
		run(fixture.Select("SELECT pid,note FROM type_child ORDER BY note", []string{"pid", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "child"}, {nil, "nullable"}, {int64(2), "recovery"}}))
		run(fixture.ForeignKeyFail("DELETE FROM type_parent WHERE id=2", fixture.ForeignKeyParent("type_parent", "type_child", "type_fk", `Key (id)=(2) is still referenced from table "type_child".`), true))
		run(fixture.Select("SELECT id,note FROM type_parent ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "parent"}, {int64(2), "untouched"}}))
	})
}
