package parser

import "testing"

func TestTablespaceLifecycleAST(t *testing.T) {
	statements, err := Parse(`ALTER TABLESPACE "Old Name" RENAME TO "New Name"; DROP TABLESPACE IF EXISTS "New Name"`)
	if err != nil || len(statements) != 2 {
		t.Fatal(statements, err)
	}
	rename, ok := statements[0].(*AlterTablespaceRename)
	if !ok || rename.Name.Text != "Old Name" || rename.NewName.Text != "New Name" || rename.Name.Pos.Character == 0 || rename.NewName.Pos.Character == 0 {
		t.Fatal("lost rename identity/position", statements[0])
	}
	drop, ok := statements[1].(*DropTablespace)
	if !ok || !drop.IfExists || drop.Name.Text != "New Name" || drop.Name.Pos.Character == 0 {
		t.Fatal("lost drop identity/position", statements[1])
	}
	for _, tc := range []struct{ input, name, identity string }{
		{"CURRENT_USER", "", "current"},
		{"CURRENT_ROLE", "", "current"},
		{"SESSION_USER", "", "session"},
		{`"current_user"`, "current_user", ""},
		{"ordinary", "ordinary", ""},
	} {
		statements, err := Parse("ALTER TABLESPACE pg_default OWNER TO " + tc.input)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc, statements, err)
		}
		owner, ok := statements[0].(*AlterOwner)
		if !ok || owner.Kind != "tablespace" || len(owner.Name.Parts) != 1 || owner.Name.Parts[0].Text != "pg_default" || owner.Target.Name != tc.name || owner.Target.Identity != tc.identity {
			t.Fatal("owner route lost typed recipient", tc, statements[0])
		}
	}
}
