package analyze

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// This host supplies the real immutable catalog to the isolated namespace
// binder; no parser, SQL executor or synthesized binding result is substituted.
type tableOIDCatalogHost struct {
	Host
	read catalog.Read
}

func (h tableOIDCatalogHost) CurrentCatalog() catalog.Read                         { return h.read }
func (h tableOIDCatalogHost) CatalogOwner() *catalog.Owner                         { return h.read.Owner() }
func (h tableOIDCatalogHost) BindingDefinition(Authority) catalog.DefinitionReader { return nil }

func tableOIDBinder(t *testing.T) (*Binder, *QueryNamespace) {
	t.Helper()
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	read := catalog.NewRead(&catalog.Owner{Domain: new(ids.Domain)}, state)
	b := &Binder{Ctx: context.Background(), Host: tableOIDCatalogHost{read: read}}
	var ns QueryNamespace
	for i, name := range []string{"a", "b"} {
		r := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(20000 + i)}, ObjectRevision: 1, ObjectName: name}
		state.SetDescriptor(r.Address(), r)
		ref := read.Domain().Ref(read.Epoch(), r.Address())
		b.Query.Sources = append(b.Query.Sources, exec.Leaf{Ref: ref, Relation: r})
		ns.Qualifiers = append(ns.Qualifiers, QueryQualifier{Name: name, Ref: ref, TableOIDOccurrence: i + 1})
	}
	b.Query.Namespace = &ns
	return b, &ns
}

func TestQueryTableOIDBinding(t *testing.T) {
	b, ns := tableOIDBinder(t)
	pos := parser.Position{Character: 8}
	// Zero ordinary width still has independently bound qualified identities.
	node, err := b.queryTableOID(ns, &ns.Qualifiers[1], nil, 1, pos)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.TableOIDExpr)
	if n.Owner != b.Query.Sources[1].Ref || n.QueryOccurrence != 2 || n.Scope != 1 || n.Frame != 1 || !b.Query.Sources[1].TableOIDRead || len(b.Query.Sources[1].Uses) != 0 || len(ns.Frame) != 0 {
		t.Fatal("source coordinates/ordinary width", n)
	}
	if _, err := b.queryTableOID(ns, nil, nil, 1, pos); err == nil || err.(*sqlstate.Error).Code != "42702" {
		t.Fatal("comma source ambiguity", err)
	}
	ns.Qualifiers[0].Columns = []QueryColumn{{Descriptor: catalog.Column{Name: "tableoid"}}}
	ordinary := ns.Qualifiers[0].Columns[0]
	if _, err := b.queryTableOID(ns, nil, &ordinary, 1, pos); err == nil || err.(*sqlstate.Error).Code != "42702" {
		t.Fatal("mixed comma ambiguity", err)
	}
	for i := range ns.Qualifiers {
		ns.Qualifiers[i].SystemColumnsHidden = true
	}
	if node, err := b.queryTableOID(ns, nil, &ordinary, 1, pos); node != nil || err != nil {
		t.Fatal("joined ordinary output must win without implicit operand candidate", node, err)
	}
	if node, err := b.queryTableOID(ns, &ns.Qualifiers[1], nil, 1, pos); node == nil || err != nil {
		t.Fatal("qualified JOIN operand lost identity", err)
	}
	b.Consumer.CheckConsumer = true
	if node, err := b.queryTableOID(ns, &ns.Qualifiers[1], nil, 1, pos); node != nil || err != nil {
		t.Fatal("CHECK acquired ordinary system namespace")
	}
	t.Run("nearest_qualifier_owns_missing_member", func(t *testing.T) {
		inner, outer := tableOIDBinder(t)
		outer.Qualifiers = outer.Qualifiers[:1]
		outer.Qualifiers[0].Name = "q"
		inner.Query.Scopes = []queryBindingScope{{namespace: outer, frame: 1, allowed: true}}
		inner.Query.Frame = 2
		inner.Query.Namespace = &QueryNamespace{Qualifiers: []QueryQualifier{{Name: "q"}}}
		raw := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "q"}, {Text: "tableoid"}}}, Pos: pos}
		if node, err := inner.queryColumn(raw); node != nil || err == nil || err.(*sqlstate.Error).Code != "42703" {
			t.Fatal("nearest derived miss retried outer source", node, err)
		}
	})
}

func TestQueryTableOIDFrameSemantics(t *testing.T) {
	b, ns := tableOIDBinder(t)
	node, err := b.queryTableOID(ns, &ns.Qualifiers[0], nil, 4, parser.Position{Character: 17})
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.TableOIDExpr)
	if !n.Properties().RowDependent || n.ResultUsage().Type.ID() != sqltype.Oid || !ExpressionUsesFrame(n, 4) || ExpressionUsesFrame(n, 1) || FirstQueryBoundFrameVariable(n, 4).Character != 17 {
		t.Fatal("tableoid treated as constant", n)
	}
	for _, change := range []func(*plan.TableOIDExpr){func(x *plan.TableOIDExpr) { x.Frame++ }, func(x *plan.TableOIDExpr) { x.QueryOccurrence++ }, func(x *plan.TableOIDExpr) { x.Owner = new(ids.Domain).Ref(x.Owner.Epoch, x.Owner.Address) }} {
		other := n
		change(&other)
		if b.SameGroupExpression(n, other) {
			t.Fatal("distinct source/frame/owner collapsed")
		}
	}
	generated := n
	generated.Scope, generated.Frame, generated.QueryOccurrence = 0, 0, 0
	if ExpressionUsesFrame(generated, 4) || !b.SameGroupExpression(generated, generated) {
		t.Fatal("generated mode changed")
	}
	b.Query.Frame = 4
	owner, _ := b.argumentQueryOwner(func(visit func(expr.Expr)) { visit(n) })
	if owner != 4 {
		t.Fatal("aggregate owner", owner)
	}
}

func TestQueryTableOIDFunctionCorrelation(t *testing.T) {
	b, ns := tableOIDBinder(t)
	node, err := b.queryTableOID(ns, &ns.Qualifiers[1], nil, 1, parser.Position{Offset: 20, Character: 21})
	if err != nil {
		t.Fatal(err)
	}
	system := node.(plan.TableOIDExpr)
	array, ok := b.BindingCatalog().LookupArray(system.ResultUsage().Type)
	if !ok {
		t.Fatal("missing OID array")
	}
	call := plan.UnnestExpr{Info: expr.Info{Usage: system.ResultUsage()}, Args: []expr.Expr{plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: []expr.Expr{system}}}}
	captures := b.functionCaptures([]expr.Expr{call})
	if len(captures) != 1 || captures[0].Frame != 1 || captures[0].QueryOccurrence != 2 || captures[0].Slot != 0 || captures[0].Name != "b" || captures[0].Pos != system.Pos {
		t.Fatal("identity capture became an ordinary slot", captures)
	}
	local := QueryNamespace{Qualifiers: []QueryQualifier{ns.Qualifiers[1]}}
	ancestor := QueryNamespace{Qualifiers: []QueryQualifier{ns.Qualifiers[0]}}
	if len(local.Frame) != 0 || !queryNamespaceHasTableOID(&local, 2) || queryNamespaceHasTableOID(&ancestor, 2) || !queryNamespaceHasTableOID(&ancestor, 1) {
		t.Fatal("zero-column membership used tuple width")
	}
	root := &plan.FunctionFromNode{Calls: []expr.Expr{call}, Frame: 1, Correlated: true, Captures: captures}
	rebased, err := RebaseFunctionCorrelation(b.Host, b.BindingCatalog(), root, 1, 9)
	function, ok := rebased.(*plan.FunctionFromNode)
	if err != nil || !ok || function.Captures[0].QueryOccurrence != 2 || function.Captures[0].Slot != 0 {
		t.Fatal("identity-only arguments rebased as ordinary slots", rebased, err)
	}
	nested := &plan.JoinNode{Left: &plan.SingletonNode{}, Right: root, Frame: 1, CorrelatedRight: true, CorrelationPrefix: 9, CorrelationIdentityPrefix: true, Captures: captures}
	rebased, err = RebaseFunctionCorrelation(b.Host, b.BindingCatalog(), nested, 1, 9)
	join, ok := rebased.(*plan.JoinNode)
	if err != nil || !ok || join.CorrelationPrefix != 0 || !join.CorrelationIdentityPrefix || join.Captures[0].QueryOccurrence != 2 {
		t.Fatal("identity-only exported capture prevented nested prefix normalization", rebased, err)
	}
}
