package plan

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestPGRewriteBoundActionNodes(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	if integer == nil {
		t.Fatal("INT4")
	}
	info := expr.Info{Usage: integer.Usage()}
	column := ColumnExpr{Info: info, Origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: 1259, ObjectID: 900, SubID: 2}}, Scope: 7, Frame: 3}
	base := ReturningColumnExpr{ColumnExpr: column}
	output := func(node expr.Expr) string {
		t.Helper()
		s, err := (BoundTree{Expression: node}).StructuralOutput(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	original := output(base)
	for _, mutate := range []func(*ReturningColumnExpr){func(n *ReturningColumnExpr) { n.Old = true }, func(n *ReturningColumnExpr) { n.Absent = true }, func(n *ReturningColumnExpr) { n.Merge = true }, func(n *ReturningColumnExpr) { n.Side = 1 }, func(n *ReturningColumnExpr) { n.Scope++ }, func(n *ReturningColumnExpr) { n.Frame++ }, func(n *ReturningColumnExpr) { n.Origin.Address.ObjectID++ }, func(n *ReturningColumnExpr) { n.Value = column }} {
		n := base
		mutate(&n)
		if output(n) == original {
			t.Fatal("returning distinction collapsed", n)
		}
	}
	for _, w := range []string{"ReturningColumn old false absent false merge false side 0 origin 1259/900/2 scope 7 frame 3", "Var attnum 2"} {
		if !strings.Contains(original, w) {
			t.Fatal(original)
		}
	}
	row := ReturningWholeRowExpr{Info: info, Members: []expr.Expr{column}, Scope: 7, Frame: 3}
	rowOriginal := output(row)
	for _, mutate := range []func(*ReturningWholeRowExpr){func(n *ReturningWholeRowExpr) { n.Old = true }, func(n *ReturningWholeRowExpr) { n.Merge = true }, func(n *ReturningWholeRowExpr) { n.Side = 1 }, func(n *ReturningWholeRowExpr) { n.Scope++ }, func(n *ReturningWholeRowExpr) { n.Frame++ }, func(n *ReturningWholeRowExpr) { n.Members = []expr.Expr{column, column} }} {
		n := row
		mutate(&n)
		if output(n) == rowOriginal {
			t.Fatal("whole-row returning distinction collapsed", n)
		}
	}
	if output(UpdateOutputExpr{Info: info, Index: 1, Group: 2}) == output(UpdateOutputExpr{Info: info, Index: 2, Group: 2}) || output(UpdateOutputExpr{Info: info, Index: 1, Group: 2}) == output(UpdateOutputExpr{Info: info, Index: 1, Group: 3}) {
		t.Fatal("assignment group/index collapsed")
	}
	if output(CachedOrderExpr{Info: info, Slot: 1}) == output(CachedOrderExpr{Info: info, Slot: 2}) {
		t.Fatal("cache-read occurrence collapsed")
	}
	if output(&WindowExpr{Info: info, Frame: 3, Slot: 1, Call: 0}) == output(&WindowExpr{Info: info, Frame: 3, Slot: 1, Call: 1}) {
		t.Fatal("window call association collapsed")
	}
}

func TestPGRewriteQuerySelections(t *testing.T) {
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
			break
		}
	}
	if text == nil {
		t.Fatal("TEXT")
	}
	usage := text.Usage()
	first, err := sqltype.BindGrouping(usage)
	if err != nil {
		t.Fatal(err)
	}
	usage.Collation.ID = 950
	second, err := sqltype.BindGrouping(usage)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(node Node) string {
		t.Helper()
		var out strings.Builder
		if err := AppendQueryTree(t.Context(), &out, node); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	a := &DistinctNode{Grouping: []Grouping{{Grouping: first}}}
	b := &DistinctNode{Grouping: []Grouping{{Grouping: second}}}
	if encode(a) == encode(b) {
		t.Fatal("bound equality collation selection collapsed")
	}
	set := &SetNode{Operation: "UNION", All: true, Left: &ProjectNode{Child: &SingletonNode{}}, Right: &ProjectNode{Child: &SingletonNode{}}}
	other := *set
	other.Operation = "INTERSECT"
	if encode(set) == encode(&other) {
		t.Fatal("bound set operation collapsed")
	}
	sort := &SortNode{Keys: []SortKey{{TargetIdentity: 1, Slot: 2, Cache: 3}}}
	for _, mutate := range []func(*SortKey){func(k *SortKey) { k.TargetIdentity++ }, func(k *SortKey) { k.Slot++ }, func(k *SortKey) { k.Cache++ }, func(k *SortKey) { k.Desc = true }, func(k *SortKey) { k.NullsFirst = true }} {
		copy := *sort
		copy.Keys = append([]SortKey(nil), sort.Keys...)
		mutate(&copy.Keys[0])
		if encode(sort) == encode(&copy) {
			t.Fatal("resolved ordering/cache-read association collapsed", copy.Keys)
		}
	}
}

func TestPGRewriteQueryNilOwners(t *testing.T) {
	for _, node := range []Node{
		(*SourceNode)(nil), (*ProjectNode)(nil), (*ValuesNode)(nil),
		(*SingletonNode)(nil), (*FilterNode)(nil), (*SortNode)(nil),
		(*AggregateNode)(nil), (*WindowNode)(nil), (*DistinctNode)(nil),
		(*DistinctOnNode)(nil), (*SetNode)(nil), (*RecursiveUnionNode)(nil),
		(*DerivedNode)(nil), (*WorktableScanNode)(nil), (*JoinNode)(nil),
		(*FunctionFromNode)(nil), (*LimitNode)(nil), (*RowLockNode)(nil),
		(*EvaluationNode)(nil), (*SetProjectNode)(nil),
	} {
		var out strings.Builder
		if err := AppendQueryTree(t.Context(), &out, node); err == nil {
			t.Fatalf("typed-nil query owner accepted: %T", node)
		}
	}
	var out strings.Builder
	if err := AppendQueryTree(t.Context(), &out, &SetNode{}); err == nil {
		t.Fatal("missing required set arms accepted")
	}
}
