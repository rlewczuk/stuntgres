package parser

import "testing"

func TestAlterAddColumnDeclarations(t *testing.T) {
	for _, query := range []string{
		"ALTER TABLE t ADD b INT DEFAULT 7 NOT NULL CHECK(b>0)",
		"ALTER TABLE ONLY(t) ADD COLUMN IF NOT EXISTS b INT DEFAULT 7 NOT NULL CHECK(b>0)",
		"ALTER TABLE t* ADD if TEXT",
	} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		alter := statements[0].(*AlterTable)
		if len(alter.Actions) != 1 || alter.Actions[0].Action != AddColumn || alter.Actions[0].Definition == nil {
			t.Fatal(alter)
		}
		column := alter.Actions[0].Definition
		if column.Pos.Character == 0 || alter.Actions[0].Column.Text != column.Name {
			t.Fatal(column, alter.Actions[0])
		}
		if column.Name == "b" && len(column.Constraints) != 3 {
			t.Fatal(column.Constraints)
		}
	}
	for _, query := range []string{"ALTER TABLE t ADD", "ALTER TABLE t ADD COLUMN b", "ALTER TABLE t ADD b INT CHECK(", "ALTER TABLE t ADD b INT; ALTER TABLE t ADD c"} {
		if _, err := Parse(query); err == nil {
			t.Fatal("accepted incomplete full input", query)
		}
	}
}
