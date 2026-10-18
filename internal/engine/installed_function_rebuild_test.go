package engine

import (
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
		return literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, s)}
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
