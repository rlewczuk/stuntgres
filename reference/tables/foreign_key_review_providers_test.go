package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceForeignKeySupportingFamilyAdmission(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE integer_parent(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO integer_parent VALUES(1)", 1))
		q := "CREATE TABLE numeric_child(pid NUMERIC CONSTRAINT numeric_fk REFERENCES integer_parent)"
		d := foreignKeyError("42804", `foreign key constraint "numeric_fk" cannot be implemented`, `Key columns "pid" of the referencing table and "id" of the referenced table are of incompatible types: numeric and integer.`, "", "")
		for _, prepared := range []bool{false, true} {
			run(foreignKeyFail(q, d, prepared))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='numeric_child'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='numeric_fk'", []string{"conname"}, []string{"NAME"}, nil))
			run(foreignKeyIntRows("SELECT id FROM integer_parent", "id", 1))
		}
		// The direction matters: int4 -> numeric is implicit, not assignment-only.
		run(fixture.Exec("CREATE TABLE numeric_parent(id NUMERIC PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE integer_child(pid INT CONSTRAINT integer_fk REFERENCES numeric_parent)", 0))
		run(fixture.Exec("INSERT INTO numeric_parent VALUES(1)", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO integer_child VALUES($1)", 1, 1)
			s.Prepared = prepared
			run(s)
			run(foreignKeyFail("INSERT INTO integer_child VALUES(2)", foreignKeyChild("integer_child", "integer_fk", `Key (pid)=(2) is not present in table "numeric_parent".`), prepared))
		}
		run(foreignKeyIntRows("SELECT pid FROM integer_child ORDER BY pid", "pid", 1, 1))
		run(foreignKeyFail("DELETE FROM numeric_parent", foreignKeyParent("numeric_parent", "integer_child", "integer_fk", `Key (id)=(1) is still referenced from table "integer_child".`), false))
		run(fixture.Select("SELECT id FROM numeric_parent", []string{"id"}, []string{"NUMERIC"}, [][]any{{"1"}}))
	})
}

func TestReferenceForeignKeySupportingFamilyCoercion(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE padded_parent(id CHAR(8) PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE text_child(pid TEXT CONSTRAINT padded_fk REFERENCES padded_parent)", 0))
		run(fixture.Exec("INSERT INTO padded_parent VALUES('x'),('abcdefgh')", 2))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO text_child VALUES($1)", 1, "x ")
			s.Prepared = prepared
			run(s)
			// Opclass input coercion is unsized: it must not truncate to CHAR(8).
			run(foreignKeyFail("INSERT INTO text_child VALUES('abcdefghz')", foreignKeyChild("text_child", "padded_fk", `Key (pid)=(abcdefghz) is not present in table "padded_parent".`), prepared))
		}
		run(fixture.Exec("INSERT INTO text_child VALUES('abcdefgh')", 1))
		run(fixture.Select("SELECT pid FROM text_child ORDER BY pid", []string{"pid"}, []string{"TEXT"}, [][]any{{"abcdefgh"}, {"x "}, {"x "}}))
		run(fixture.Exec("ALTER TABLE text_child ALTER pid TYPE VARCHAR(12)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO text_child VALUES($1)", 1, "x    ")
			s.Prepared = prepared
			run(s)
			run(foreignKeyFail("INSERT INTO text_child VALUES('abcdefghz')", foreignKeyChild("text_child", "padded_fk", `Key (pid)=(abcdefghz) is not present in table "padded_parent".`), prepared))
		}
		run(fixture.Select("SELECT pid::text AS pid FROM text_child ORDER BY pid", []string{"pid"}, []string{"TEXT"}, [][]any{{"abcdefgh"}, {"x "}, {"x "}, {"x    "}, {"x    "}}))
		run(foreignKeyFail("DELETE FROM padded_parent WHERE id='abcdefgh'", foreignKeyParent("padded_parent", "text_child", "padded_fk", `Key (id)=(abcdefgh) is still referenced from table "text_child".`), false))
		run(fixture.Select("SELECT id::text AS id FROM padded_parent ORDER BY id", []string{"id"}, []string{"TEXT"}, [][]any{{"abcdefgh"}, {"x"}}))
		if !b.PG {
			foreignKeyRoundtrip(t, p)
			run(fixture.Select("SELECT pid::text AS pid FROM text_child ORDER BY pid", []string{"pid"}, []string{"TEXT"}, [][]any{{"abcdefgh"}, {"x "}, {"x "}, {"x    "}, {"x    "}}))
			run(fixture.Exec("INSERT INTO text_child VALUES('x      ')", 1))
			run(foreignKeyFail("INSERT INTO text_child VALUES('abcdefghz')", foreignKeyChild("text_child", "padded_fk", `Key (pid)=(abcdefghz) is not present in table "padded_parent".`), true))
			run(foreignKeyFail("DELETE FROM padded_parent WHERE id='abcdefgh'", foreignKeyParent("padded_parent", "text_child", "padded_fk", `Key (id)=(abcdefgh) is still referenced from table "text_child".`), true))
			run(fixture.Select("SELECT pid::text AS pid FROM text_child ORDER BY pid", []string{"pid"}, []string{"TEXT"}, [][]any{{"abcdefgh"}, {"x "}, {"x "}, {"x    "}, {"x    "}, {"x      "}}))
			run(fixture.Select("SELECT id::text AS id FROM padded_parent ORDER BY id", []string{"id"}, []string{"TEXT"}, [][]any{{"abcdefgh"}, {"x"}}))
		}
	})
}

func TestReferenceForeignKeySupportingFamilyRestore(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE family_sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO family_sentinel VALUES(91)", 1))
		d := foreignKeyError("42804", `foreign key constraint "restore_family_fk" cannot be implemented`, `Key columns "pid" of the referencing table and "id" of the referenced table are of incompatible types: numeric and integer.`, "", "")
		if b.PG {
			run(fixture.Exec("CREATE TABLE restore_family_parent(id INT PRIMARY KEY)", 0))
			for _, prepared := range []bool{false, true} {
				run(foreignKeyFail("CREATE TABLE restore_family_child(pid NUMERIC CONSTRAINT restore_family_fk REFERENCES restore_family_parent)", d, prepared))
			}
		} else {
			var before, after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			var sentinelOID int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='family_sentinel'").Scan(&sentinelOID); err != nil {
				t.Fatal(err)
			}
			held, err := b.DB.QueryContext(p.Ctx, "SELECT v FROM family_sentinel")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			script := "CREATE TABLE restore_family_parent(id INT PRIMARY KEY);CREATE TABLE restore_family_child(pid NUMERIC CONSTRAINT restore_family_fk REFERENCES restore_family_parent);"
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
			fixture.TransactionValues(t, held, 91)
			var currentOID int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='family_sentinel'").Scan(&currentOID); err != nil || currentOID != sentinelOID {
				t.Fatal("failed family binding replaced sentinel", sentinelOID, currentOID, err)
			}
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed family binding changed dump", err)
			}
		}
		run(foreignKeyIntRows("SELECT v FROM family_sentinel", "v", 91))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='restore_family_child'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='restore_family_fk'", []string{"conname"}, []string{"NAME"}, nil))
	})
}
