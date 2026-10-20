package engine

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Installed-routine wrappers embed a functions node; their Rebuild must keep
// the wrapper and its routine identity rather than the promoted bare node.
func TestInstalledFunctionRebuildKeepsWrapper(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	textOf := func(node expr.Expr) string {
		s, _ := node.(plan.LiteralExpr).Value.Text()
		return s
	}
	literal := func(s string) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, s)}
	}
	info := expr.Info{Usage: text.Usage()}
	call, err := functions.NewSoundex(info, "soundex", []expr.Expr{literal("a")})
	if err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{}
	ref := ids.ObjectRef{Address: ids.ObjectAddress{ObjectID: 7}}
	node := plan.InstalledSoundexExpr{Soundex: call, Procedure: procedure, Ref: ref}
	rebuilt, err := node.Rebuild(info, []expr.Expr{literal("b")})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := rebuilt.(plan.InstalledSoundexExpr)
	if !ok || got.Procedure != procedure || got.Ref != ref || textOf(got.Args[0]) != "b" {
		t.Fatalf("rebuilt %#v", rebuilt)
	}
	if textOf(node.Args[0]) != "a" {
		t.Fatal("Rebuild modified the source node")
	}
	if _, err := node.Rebuild(info, nil); err == nil {
		t.Fatal("Rebuild accepted a missing child")
	}
}

// The lexical walk reaches protocol nodes' children through the Composite
// branch, including through an installed-routine wrapper.
func TestWalkBoundExpressionVisitsCompositeChildren(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	info := expr.Info{Usage: text.Usage()}
	leaf := plan.LiteralExpr{Info: info, Value: sqltype.String(text, "x")}
	inner, err := functions.NewSoundex(info, "soundex", []expr.Expr{leaf})
	if err != nil {
		t.Fatal(err)
	}
	wrapper := plan.InstalledSoundexExpr{Soundex: inner}
	outer, err := functions.NewSoundex(info, "soundex", []expr.Expr{wrapper})
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	plan.WalkExpression(outer, func(node expr.Expr) { visited = append(visited, fmt.Sprintf("%T", node)) })
	want := []string{"functions.Soundex", "plan.InstalledSoundexExpr", "plan.LiteralExpr"}
	if !reflect.DeepEqual(visited, want) {
		t.Fatalf("walk visited %v, want %v", visited, want)
	}
}

// The uuid-ossp wrapper embeds a generation node with no children; Rebuild
// takes new metadata and keeps the installed identity.
func TestInstalledUUIDOsspRebuildKeepsWrapper(t *testing.T) {
	var uuid *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.UUID {
			uuid = d
		}
	}
	info := expr.Info{Usage: uuid.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}
	call, err := functions.NewUUIDGeneration(info, "uuidv4", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{}
	ref := ids.ObjectRef{Address: ids.ObjectAddress{ObjectID: 7}}
	node := plan.InstalledUUIDOsspExpr{UUIDGeneration: call, Procedure: procedure, Ref: ref}
	next := info
	next.Pos.Offset = 3
	rebuilt, err := node.Rebuild(next, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := rebuilt.(plan.InstalledUUIDOsspExpr)
	if !ok || got.Procedure != procedure || got.Ref != ref || got.Pos.Offset != 3 || node.Pos.Offset != 0 {
		t.Fatalf("rebuilt %#v", rebuilt)
	}
	if _, err := node.Rebuild(info, []expr.Expr{plan.LiteralExpr{}}); err == nil {
		t.Fatal("Rebuild accepted an extra child")
	}
}
