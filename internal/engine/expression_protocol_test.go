package engine

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
		s, _ := node.(literalExpression).value.Text()
		return s
	}
	literal := func(s string) expr.Expr {
		return literalExpression{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, s)}
	}
	info := expr.Info{Usage: text.Usage()}
	call, err := functions.NewSoundex(info, "soundex", []expr.Expr{literal("a")})
	if err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{}
	ref := ids.ObjectRef{Address: ids.ObjectAddress{ObjectID: 7}}
	node := installedSoundexFunctionExpression{Soundex: call, procedure: procedure, ref: ref}
	rebuilt, err := node.Rebuild(info, []expr.Expr{literal("b")})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := rebuilt.(installedSoundexFunctionExpression)
	if !ok || got.procedure != procedure || got.ref != ref || textOf(got.Args[0]) != "b" {
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
	leaf := literalExpression{Info: info, value: sqltype.String(text, "x")}
	inner, err := functions.NewSoundex(info, "soundex", []expr.Expr{leaf})
	if err != nil {
		t.Fatal(err)
	}
	wrapper := installedSoundexFunctionExpression{Soundex: inner}
	outer, err := functions.NewSoundex(info, "soundex", []expr.Expr{wrapper})
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	walkBoundExpression(outer, func(node expr.Expr) { visited = append(visited, fmt.Sprintf("%T", node)) })
	want := []string{"functions.Soundex", "engine.installedSoundexFunctionExpression", "engine.literalExpression"}
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
	node := installedUUIDOsspFunctionExpression{UUIDGeneration: call, procedure: procedure, ref: ref}
	next := info
	next.Pos.Offset = 3
	rebuilt, err := node.Rebuild(next, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := rebuilt.(installedUUIDOsspFunctionExpression)
	if !ok || got.procedure != procedure || got.ref != ref || got.Pos.Offset != 3 || node.Pos.Offset != 0 {
		t.Fatalf("rebuilt %#v", rebuilt)
	}
	if _, err := node.Rebuild(info, []expr.Expr{literalExpression{}}); err == nil {
		t.Fatal("Rebuild accepted an extra child")
	}
}
