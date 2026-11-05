package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func xpathUnitExpression(t *testing.T, e *core.Engine, name, memberType string) plan.InstalledXML2XPathTableExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
	for _, text := range []string{"id", "doc", "xt_owner_source", "/doc/a", "true"} {
		raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}})
	}
	n, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n, err = b.CompleteFunctionDefinitions(raw, n, []parser.FunctionColumnDefinition{
		{Name: parser.Identifier{Text: "id"}, Type: parser.TypeName{Name: "integer"}},
		{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: memberType}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return n.(plan.InstalledXML2XPathTableExpr)
}

func TestXML2XPathTableExpressionIdentity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION xml2")
	n := xpathUnitExpression(t, e, "xpath_table", "integer")
	view := e.CatalogRead()
	if err := stored.ValidateInstalledXML2XPathTable(n, view); err != nil {
		t.Fatal(err)
	}
	if n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || !n.Properties().StageDependent || len(n.Args) != 5 || n.Procedure.Result != 2249 || !n.Procedure.Installed.ReturnsSet || n.Procedure.Installed.Rows != 1000 {
		t.Fatal("released stable SRF properties", n)
	}
	original := append([]expr.Expr(nil), n.Args...)
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		children := append([]expr.Expr(nil), n.Args...)
		x, err := rebuild(n.Info, children)
		if err != nil {
			t.Fatal(err)
		}
		m := x.(plan.InstalledXML2XPathTableExpr)
		if m.Ref != n.Ref || m.Procedure != n.Procedure || m.RoutineReference != n.Procedure || m.Occurrence != n.Occurrence || m.Source != n.Source || m.Usage != n.Usage || &m.Args[0] == &children[0] || &m.Args[0] == &n.Args[0] || !m.SameSourceOccurrence(n) || !m.SameGroup(n, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
			t.Fatal("reconstruction lost owned original capture")
		}
		children[0] = nil
		if !reflect.DeepEqual(m.Args, original) || !reflect.DeepEqual(n.Args, original) {
			t.Fatal("reconstruction borrowed caller backing")
		}
	}
	sameText := func(a, b expr.Expr) bool {
		x, xok := a.(plan.LiteralExpr)
		y, yok := b.(plan.LiteralExpr)
		if !xok || !yok || x.Usage != y.Usage || x.Value.IsNull() != y.Value.IsNull() {
			return false
		}
		if x.Value.IsNull() {
			return true
		}
		left, leftErr := x.Value.Text()
		right, rightErr := y.Value.Text()
		return leftErr == nil && rightErr == nil && left == right
	}
	t.Run("equivalent_anonymous_shape", func(t *testing.T) {
		sameShape := *n.Usage.Type
		equivalent := n
		equivalent.Usage.Type = &sameShape
		if !n.SameSourceOccurrence(equivalent) || !n.SameGroup(equivalent, sameText, false) {
			t.Fatal("structurally equivalent anonymous shape lost its occurrence")
		}
		if err := stored.ValidateInstalledXML2XPathTable(equivalent, view); err != nil {
			t.Fatal("anonymous shape incorrectly requires catalog descriptor identity", err)
		}
	})
	t.Run("selected_reference", func(t *testing.T) {
		other := n
		other.Ref.Epoch++
		if n.SameSourceOccurrence(other) || n.SameGroup(other, sameText, false) {
			t.Fatal("different selected reference matched")
		}
	})
	t.Run("different_record_shape", func(t *testing.T) {
		members := []sqltype.CompositeMember{n.Usage.Type.CompositeMember(0), n.Usage.Type.CompositeMember(1)}
		members[1].Name = "other_a"
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			t.Fatal(err)
		}
		other := n
		other.Usage.Type = shape
		if n.SameSourceOccurrence(other) || n.SameGroup(other, sameText, false) {
			t.Fatal("genuinely different record shape matched")
		}
	})
	t.Run("operand_provenance", func(t *testing.T) {
		other := n
		literal := n.Args[0].(plan.LiteralExpr)
		literal.Occurrence++
		other.Args = append([]expr.Expr(nil), n.Args...)
		other.Args[0] = literal
		if n.SameSourceOccurrence(other) || !n.SameGroup(other, sameText, false) {
			t.Fatal("occurrence identity was conflated with supplied semantic grouping")
		}
	})
	t.Run("operand_value", func(t *testing.T) {
		other := n
		literal := n.Args[0].(plan.LiteralExpr)
		value, err := literal.Usage.Type.Input(context.Background(), "other_id", -1)
		if err != nil {
			t.Fatal(err)
		}
		literal.Value = value
		other.Args = append([]expr.Expr(nil), n.Args...)
		other.Args[0] = literal
		if !n.SameSourceOccurrence(other) || n.SameGroup(other, sameText, false) {
			t.Fatal("unchanged occurrence stamp hid different operand semantics")
		}
	})
	foreign := engine.New()
	enginetest.Run(t, foreign, "CREATE EXTENSION xml2")
	if stored.ValidateInstalledXML2XPathTable(n, foreign.CatalogRead()) == nil {
		t.Fatal("foreign same-number capture accepted")
	}
	t.Run("selected_procedure", func(t *testing.T) {
		other := xpathUnitExpression(t, foreign, "xpath_table", "integer")
		if n.SameSourceOccurrence(other) || n.SameGroup(other, sameText, false) {
			t.Fatal("foreign genuine selected procedure matched")
		}
	})
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Signature = "25" },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Volatility = expr.Volatile },
		func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
		func(p *catalog.Procedure) { p.Installed.Rows++ },
		func(p *catalog.Procedure) { p.Display.Source = "xpath_string" },
		func(p *catalog.Procedure) { p.Origin.Declaration = 0 },
		func(p *catalog.Procedure) { p.Owner = foreign.CatalogRead().MustProcedure(n.Ref.Address).Owner },
	} {
		p := *n.Procedure
		origin := *p.Origin
		p.Origin = &origin
		mutate(&p)
		bad := n
		bad.Procedure, bad.RoutineReference = &p, &p
		if stored.ValidateInstalledXML2XPathTable(bad, view) == nil {
			t.Fatal("forged table declaration accepted")
		}
	}
	bad := n
	bad.Ref.Epoch++
	if stored.ValidateInstalledXML2XPathTable(bad, view) == nil {
		t.Fatal("stale capture accepted")
	}
	info := n.Info
	info.RoutineReference = nil
	if _, err := n.Rebuild(info, n.Args); err == nil {
		t.Fatal("missing routine carrier rebuilt")
	}
	integer, _ := view.LookupType(sqltype.Int4)
	copyType := *integer
	// A valid shape uses one coherent generation for every repeated type ID.
	// The foreign leaf must be rejected by installed ownership validation,
	// not earlier by the record constructor's mixed-generation invariant.
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: copyType.Usage()}, {Attribute: 2, Name: "a", Usage: copyType.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	bad = n
	bad.Usage = shape.Usage()
	if stored.ValidateInstalledXML2XPathTable(bad, view) == nil {
		t.Fatal("foreign record member generation accepted")
	}
}

func TestXML2XPathTableStoredIdentity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION xml2")
	enginetest.Run(t, e, "CREATE DOMAIN xt_member AS integer")
	n := xpathUnitExpression(t, e, "xpath_table", "xt_member")
	oldType := n.Usage.Type.CompositeMember(1).Usage.Type
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oldType.ID())}
	revision := e.CatalogRead().Get(address).Revision()
	enginetest.Run(t, e, "ALTER DOMAIN xt_member RENAME TO xt_current_member")
	view := e.CatalogRead()
	current, _ := view.LookupType(oldType.ID())
	if current == oldType || view.Get(address).Revision() == revision {
		t.Fatal("actual member generation did not advance")
	}
	mapped, changed, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		if current, ok := view.LookupType(d.ID()); ok {
			return current
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal("record leaf retag", changed, err)
	}
	m := mapped.(plan.InstalledXML2XPathTableExpr)
	if m.Usage.Type.CompositeMember(1).Usage.Type != current || n.Usage.Type.CompositeMember(1).Usage.Type != oldType || m.Procedure != n.Procedure || m.RoutineReference != n.Procedure {
		t.Fatal("record generation or original capture mutated")
	}
	enginetest.Run(t, e, "ALTER FUNCTION xpath_table(text,text,text,text,text) RENAME TO xt_current_table")
	enginetest.Run(t, e, "ALTER EXTENSION xml2 DROP FUNCTION xt_current_table(text,text,text,text,text)")
	enginetest.Run(t, e, "DROP EXTENSION xml2")
	view = e.CatalogRead()
	if err := stored.ValidateInstalledXML2XPathTable(m, view); err != nil {
		t.Fatal("detached/headerless original identity", err)
	}
	text, err := (stored.Deparser{View: view}).SQL(context.Background(), m, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "xt_current_table") || strings.Contains(text, "xpath_table(") {
		t.Fatal("current-name renderer", text, err)
	}
	rebound := xpathUnitExpression(t, e, "xt_current_table", "xt_current_member")
	if rebound.Ref != n.Ref || !exec.SameRoutineDeclaration(rebound.Procedure, n.Procedure) {
		t.Fatal("detached source lost authentic routine")
	}
	remapped, err := stored.RemapExpression(m, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	retained := remapped.(plan.InstalledXML2XPathTableExpr)
	if retained.Ref != m.Ref || retained.Procedure != m.Procedure || retained.RoutineReference != m.RoutineReference || retained.Usage != m.Usage || &retained.Args[0] == &m.Args[0] || !reflect.DeepEqual(retained.Args, m.Args) {
		t.Fatal("structural remap lost owned operands or original capture")
	}
}
