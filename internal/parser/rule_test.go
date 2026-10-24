package parser

import (
	"strings"
	"testing"
)

func TestRuleGrammar(t *testing.T) {
	statements, err := Parse("CREATE OR REPLACE RULE audit AS ON INSERT TO public.src WHERE NEW.id>0 DO ALSO (INSERT INTO sink VALUES(NEW.id); UPDATE sink SET id=id+1; DELETE FROM sink WHERE id=0; SELECT OLD.id); SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 {
		t.Fatalf("outer statements=%d want2", len(statements))
	}
	rule, ok := statements[0].(*CreateRule)
	if !ok || !rule.Replace || rule.Instead || !strings.EqualFold(rule.Event, "insert") || rule.Name.Text != "audit" || rule.Condition == nil || len(rule.Actions) != 4 {
		t.Fatalf("rule AST=%#v", statements[0])
	}
	if _, ok := rule.Actions[0].(*Insert); !ok {
		t.Fatal("first action is not INSERT")
	}
	if _, ok := rule.Actions[1].(*Update); !ok {
		t.Fatal("second action is not UPDATE")
	}
	if _, ok := rule.Actions[2].(*Delete); !ok {
		t.Fatal("third action is not DELETE")
	}
	if _, ok := rule.Actions[3].(*Select); !ok {
		t.Fatal("fourth action is not SELECT")
	}
	t.Run("qualified_mutation_actions", func(t *testing.T) {
		statements, err := Parse("CREATE RULE r AS ON UPDATE TO src DO ALSO (INSERT INTO sink VALUES(NEW.id,OLD.v),(OLD.id,NEW.v); DELETE FROM sink WHERE sink.id=OLD.id)")
		if err != nil {
			t.Fatal(err)
		}
		rule := statements[0].(*CreateRule)
		insert := rule.Actions[0].(*Insert)
		if len(insert.Rows) != 2 || len(insert.Rows[0]) != 2 || len(insert.Rows[1]) != 2 {
			t.Fatal("lost VALUES action rows")
		}
		for row, expected := range [][]string{{"new.id", "old.v"}, {"old.id", "new.v"}} {
			for slot, name := range expected {
				column, ok := insert.Rows[row][slot].(ColumnExpr)
				if !ok || len(column.Name.Parts) != 2 || column.Name.Parts[0].Text+"."+column.Name.Parts[1].Text != name {
					t.Fatalf("VALUES action row %d column %d lost qualification %s", row, slot, name)
				}
			}
		}
	})
	t.Run("owned_with_mutation_actions", func(t *testing.T) {
		statements, err := Parse("CREATE RULE r AS ON UPDATE TO src DO (WITH q AS (SELECT 1 AS id) INSERT INTO sink SELECT id FROM q; WITH q AS (SELECT 1 AS id) UPDATE sink SET id=q.id FROM q; WITH q AS (SELECT 1 AS id) DELETE FROM sink USING q WHERE sink.id=q.id); SELECT 1")
		if err != nil {
			t.Fatal(err)
		}
		rule := statements[0].(*CreateRule)
		if len(statements) != 2 || len(rule.Actions) != 3 {
			t.Fatal("WITH action escaped original rule statement")
		}
		for _, action := range rule.Actions {
			if len(StatementWith(action)) != 1 || StatementWith(action)[0].Name.Text != "q" {
				t.Fatal("lost action-owned WITH namespace")
			}
		}
		if _, ok := rule.Actions[0].(*Insert); !ok {
			t.Fatal("lost INSERT action")
		}
		if _, ok := rule.Actions[1].(*Update); !ok {
			t.Fatal("lost UPDATE action")
		}
		if _, ok := rule.Actions[2].(*Delete); !ok {
			t.Fatal("lost DELETE action")
		}
	})
	for _, query := range []string{"CREATE RULE r AS ON UPDATE TO src DO INSTEAD NOTHING", "CREATE RULE r AS ON DELETE TO src DO NOTHING"} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		rule := statements[0].(*CreateRule)
		if len(rule.Actions) != 0 {
			t.Fatal("NOTHING has executable actions")
		}
		if rule.Instead != strings.Contains(query, "INSTEAD") {
			t.Fatal("lost action mode")
		}
	}
	statements, err = Parse("ALTER RULE r ON src RENAME TO renamed; DROP RULE IF EXISTS renamed ON src CASCADE")
	if err != nil {
		t.Fatal(err)
	}
	alter, ok := statements[0].(*AlterRule)
	if !ok || alter.Rename.Text != "renamed" {
		t.Fatal("lost rule rename")
	}
	drop, ok := statements[1].(*DropRule)
	if !ok || !drop.IfExists || drop.Mode != DropCascade {
		t.Fatal("lost rule DROP options")
	}
	if _, err := Parse("CREATE TABLE rule(also INT,instead INT,notify INT)"); err != nil {
		t.Fatal("rule keywords lost unreserved identifier admission:", err)
	}
	if _, err := Parse("CREATE RULE r AS ON INSERT TO src DO NOTHING garbage"); err == nil {
		t.Fatal("accepted unconsumed rule suffix")
	}
}
