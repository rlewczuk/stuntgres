package analyze

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestQueryCTEOnlyActiveMarksFenceFolding(t *testing.T) {
	root := &plan.ProjectNode{Child: &plan.RowLockNode{Child: &plan.SingletonNode{}, Marks: []plan.RowMark{{}}}}
	if CTEVolatile(root) {
		t.Fatal("ignored virtual row mark fenced folding")
	}
	root.Child.(*plan.RowLockNode).Marks[0].Mode = RowLockMode("FOR UPDATE")
	if !CTEVolatile(root) {
		t.Fatal("active physical row mark omitted")
	}
}

func TestQueryCTEExplicitLockTargetKind(t *testing.T) {
	virtual := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "c"}}}}
	physical := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}}
	from := &parser.JoinReference{
		Left:  &parser.AliasReference{Input: virtual, Name: parser.Identifier{Text: "z"}},
		Right: physical,
	}
	classification := map[*parser.TableReference]bool{virtual: true}
	for _, mode := range []string{"FOR UPDATE", "FOR NO KEY UPDATE", "FOR SHARE", "FOR KEY SHARE"} {
		target := parser.ObjectName{Parts: []parser.Identifier{{Text: "z", Pos: parser.Position{Character: 73}}}}
		_, err := RowLockTargets(from, []parser.RowLockClause{{Mode: mode, Targets: []parser.ObjectName{target}}}, classification)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != mode+" cannot be applied to a WITH query" || diagnostic.Position != "73" {
			t.Fatal(mode, err)
		}
		marks, err := RowLockTargets(from, []parser.RowLockClause{{Mode: mode}}, classification)
		if err != nil || len(marks) != 2 || marks[0].Mode != 0 || marks[1].Mode != RowLockMode(mode) {
			t.Fatal("untargeted virtual entry affected physical marks", mode, marks, err)
		}
		target.Parts[0].Text = "t"
		marks, err = RowLockTargets(from, []parser.RowLockClause{{Mode: mode, Targets: []parser.ObjectName{target}}}, classification)
		if err != nil || marks[0].Mode != 0 || marks[1].Mode != RowLockMode(mode) {
			t.Fatal("physical target lost", mode, marks, err)
		}
	}
}
