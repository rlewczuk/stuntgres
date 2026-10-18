package engine

import (
	"fmt"
	"reflect"
	"testing"

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
	textOf := func(node BoundExpr) string {
		s, _ := node.(literalExpression).value.Text()
		return s
	}
	literal := func(s string) BoundExpr {
		return literalExpression{Info: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, s)}
	}
	info := expressionInfo{Usage: text.Usage()}
	call, err := functions.NewSoundex(info, "soundex", []BoundExpr{literal("a")})
	if err != nil {
		t.Fatal(err)
	}
	procedure := &procedureDescriptor{}
	ref := ids.ObjectRef{Address: ids.ObjectAddress{ObjectID: 7}}
	node := installedSoundexFunctionExpression{Soundex: call, procedure: procedure, ref: ref}
	rebuilt, err := node.Rebuild(info, []BoundExpr{literal("b")})
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
	info := expressionInfo{Usage: text.Usage()}
	leaf := literalExpression{Info: info, value: sqltype.String(text, "x")}
	inner, err := functions.NewSoundex(info, "soundex", []BoundExpr{leaf})
	if err != nil {
		t.Fatal(err)
	}
	wrapper := installedSoundexFunctionExpression{Soundex: inner}
	outer, err := functions.NewSoundex(info, "soundex", []BoundExpr{wrapper})
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	walkBoundExpression(outer, func(node BoundExpr) { visited = append(visited, fmt.Sprintf("%T", node)) })
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
	info := expressionInfo{Usage: uuid.Usage(), Property: ExprProperties{Volatility: Volatile}}
	call, err := functions.NewUUIDGeneration(info, "uuidv4", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	procedure := &procedureDescriptor{}
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
	if _, err := node.Rebuild(info, []BoundExpr{literalExpression{}}); err == nil {
		t.Fatal("Rebuild accepted an extra child")
	}
}
