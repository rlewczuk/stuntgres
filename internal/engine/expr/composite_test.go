package expr

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// testComposite is a protocol node; rebuilds counts Rebuild calls and failure
// makes Rebuild fail.
type testComposite struct {
	testNode
	name     string
	args     []Expr
	rebuilds *int
	failure  error
}

func (e testComposite) Children() []Expr { return e.args }
func (e testComposite) Rebuild(info Info, args []Expr) (Expr, error) {
	if e.rebuilds != nil {
		*e.rebuilds++
	}
	if e.failure != nil {
		return nil, e.failure
	}
	e.Info, e.args = info, args
	return e, nil
}

type testLeaf struct {
	testNode
	name string
}

func testChildren(node Expr, walk func(Expr)) {
	if c, ok := node.(Composite); ok {
		for _, child := range c.Children() {
			walk(child)
		}
	}
}

func nodeName(node Expr) string {
	switch n := node.(type) {
	case testComposite:
		return n.name
	case testLeaf:
		return n.name
	}
	return "?"
}

func TestWalkVisitsPreOrderAndSkipsNil(t *testing.T) {
	leaf := func(name string) Expr { return testLeaf{name: name} }
	tree := testComposite{name: "root", args: []Expr{
		testComposite{name: "left", args: []Expr{leaf("a"), nil, leaf("b")}},
		nil,
		leaf("c"),
	}}
	var got []string
	Walk(tree, testChildren, func(node Expr) { got = append(got, nodeName(node)) })
	want := []string{"root", "left", "a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("Walk visited %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Walk visited %v, want %v", got, want)
		}
	}
	visited := false
	Walk(nil, testChildren, func(Expr) { visited = true })
	if visited {
		t.Fatal("Walk visited a nil root")
	}
}

func TestMapUnchangedKeepsNode(t *testing.T) {
	rebuilds := 0
	args := []Expr{testLeaf{name: "a"}, nil}
	node := testComposite{name: "root", args: args, rebuilds: &rebuilds}
	var seen []Expr
	got, changed, err := Map(node, func(child Expr) (Expr, bool, error) {
		seen = append(seen, child)
		return child, false, nil
	})
	if err != nil || changed || rebuilds != 0 {
		t.Fatalf("Map = changed %v, err %v, rebuilds %d; want unchanged", changed, err, rebuilds)
	}
	if len(seen) != 1 || nodeName(seen[0]) != "a" {
		t.Fatalf("Map called child for %v, want only the non-nil child", seen)
	}
	if n, ok := got.(testComposite); !ok || n.name != "root" || &n.args[0] != &args[0] {
		t.Fatalf("Map returned %#v, want the original node", got)
	}
}

func TestMapChangedRebuildsCopy(t *testing.T) {
	rebuilds := 0
	info := Info{Usage: sqltype.Builtins()[sqltype.Int4].Usage(), Pos: parser.Position{Character: 7}, Property: Properties{Volatility: Stable}, RoutineReference: testRoutine(42)}
	args := []Expr{testLeaf{name: "a"}, nil, testLeaf{name: "b"}}
	node := testComposite{testNode: testNode{info}, name: "root", args: args, rebuilds: &rebuilds}
	got, changed, err := Map(node, func(child Expr) (Expr, bool, error) {
		if nodeName(child) == "b" {
			return testLeaf{name: "B"}, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed || rebuilds != 1 {
		t.Fatalf("Map = changed %v, err %v, rebuilds %d; want one rebuild", changed, err, rebuilds)
	}
	rebuilt := got.(testComposite)
	if InfoOf(rebuilt) != InfoOf(node) {
		t.Fatalf("rebuilt info %+v, want %+v", InfoOf(rebuilt), InfoOf(node))
	}
	if len(rebuilt.args) != 3 || nodeName(rebuilt.args[0]) != "a" || rebuilt.args[1] != nil || nodeName(rebuilt.args[2]) != "B" {
		t.Fatalf("rebuilt children %v", rebuilt.args)
	}
	if nodeName(args[2]) != "b" {
		t.Fatal("Map modified the original children")
	}
}

func TestMapPropagatesErrors(t *testing.T) {
	childFailure := errors.New("child")
	node := testComposite{name: "root", args: []Expr{testLeaf{name: "a"}}}
	if _, _, err := Map(node, func(Expr) (Expr, bool, error) { return nil, false, childFailure }); !errors.Is(err, childFailure) {
		t.Fatalf("Map child error = %v, want %v", err, childFailure)
	}
	rebuildFailure := errors.New("rebuild")
	node.failure = rebuildFailure
	if _, _, err := Map(node, func(Expr) (Expr, bool, error) { return testLeaf{name: "x"}, true, nil }); !errors.Is(err, rebuildFailure) {
		t.Fatalf("Map rebuild error = %v, want %v", err, rebuildFailure)
	}
}
