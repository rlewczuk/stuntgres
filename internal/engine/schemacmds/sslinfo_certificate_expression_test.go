package schemacmds_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSSLInfoCertificateExpressionProtocol(t *testing.T) {
	e, ext := sslInfoUnitInstalled(t)
	enginetest.Run(t, e, "CREATE SEQUENCE ssl_certificate_default")
	view := e.CatalogRead()
	bind := func(t *testing.T, query string) (*analyze.Binder, parser.FunctionExpr, plan.InstalledSSLInfoExpr) {
		t.Helper()
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		raw := enginetest.Statement(t, query).(*parser.Select).Targets[0].(parser.FunctionExpr)
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		return b, raw, node.(plan.InstalledSSLInfoExpr)
	}
	for slot, name := range []string{"ssl_client_dn_field", "ssl_issuer_field", "ssl_extension_info"} {
		t.Run(name, func(t *testing.T) {
			query := "SELECT " + name + "()"
			if slot != 2 {
				query = "SELECT " + name + "('CN')"
			}
			b, raw, n := bind(t, query)
			procedure := view.MustProcedure(ext.Members[slot+7])
			canonical, ok := ext.Installation.Declarations[slot+7].(*catalog.Procedure)
			if !ok || canonical == procedure || canonical.Origin != nil || canonical.Address() != procedure.Address() || canonical.Signature != procedure.Signature || canonical.Result != procedure.Result || canonical.Installed != procedure.Installed || canonical.Display != procedure.Display || n.Procedure != procedure || n.RoutineReference != procedure || n.Ref.Address != procedure.Address() || n.Ref.Domain() != view.Domain() || n.Ref.Epoch != view.Epoch() || procedure.Origin.Installation != ext.Installation || procedure.Origin.Declaration != slot+7 || !analyze.ValidInstalledSSLInfoShape(n) {
				t.Fatal("lost authentic declaration backing", n)
			}
			found := false
			for _, dependency := range b.Dependencies {
				if dependency.Ref == n.Ref && dependency.Revision == procedure.Revision() {
					found = true
				}
			}
			if !found {
				t.Fatal("missing real routine revision dependency", b.Dependencies)
			}
			if n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || plan.GenerationImmutable(n) || exec.ConditionalConstantInputs(n) || exec.PlannedVolatility(n) != expr.Volatile {
				t.Fatal("fold/volatility contract", n)
			}
			admitted, set := n.SourceAdmission()
			if !admitted || set != (slot == 2) {
				t.Fatal("source cardinality contract", admitted, set)
			}
			if err := b.CaptureViewLiterals(raw, n, analyze.ViewTargetLiteral, slot); err != nil {
				t.Fatal("actual operand capture", err)
			}
			if err := stored.ValidateInstalledSSLInfo(n, view); err != nil {
				t.Fatal(err)
			}
			children := n.Children()
			if slot == 2 {
				if n.Arg != nil || len(children) != 0 || analyze.ClosedSSLInfoDefault(n) || n.Usage.Type.ID() != 2249 || !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() != 3 {
					t.Fatal("fixed record recipe", n)
				}
				if !analyze.SameQuerySetOccurrence(n, n) || analyze.SameQuerySetOccurrence(n, n.SSLInfo) {
					t.Fatal("installed source occurrence lost authority")
				}
				moved := n
				moved.Pos.Offset++
				foreign := n
				foreign.Ref.Epoch++
				if analyze.SameQuerySetOccurrence(n, moved) || analyze.SameQuerySetOccurrence(n, foreign) {
					t.Fatal("distinct source occurrence admitted")
				}
				for i, field := range []string{"name", "value", "critical"} {
					member := n.Usage.Type.CompositeMember(i)
					current, _ := view.LookupType([]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool}[i])
					if member.Name != field || member.Attribute != int32(i+1) || member.Usage != current.Usage() {
						t.Fatal("OUT member generation", i, member)
					}
				}
			} else {
				literal, ok := n.Arg.(plan.LiteralExpr)
				if !ok || len(children) != 1 || !reflect.DeepEqual(children[0], n.Arg) || literal.Value.IsNull() || !analyze.ClosedSSLInfoDefault(n) {
					t.Fatal("actual scalar argument lost", n.Arg)
				}
				if text, err := literal.Value.Text(); err != nil || text != "CN" {
					t.Fatal("original literal backing", text, err)
				}
				captured, accepted, err := n.Capture(expr.CaptureFunction)
				if err != nil || !accepted || captured.Count != 1 || !reflect.DeepEqual(captured.Inline[0], n.Arg) {
					t.Fatal("capture did not retain argument", captured, err)
				}
				_, _, other := bind(t, "SELECT "+name+"('O')")
				if analyze.SameGroupExpression(n, other) {
					t.Fatal("equal NULL outcomes hid unequal operands")
				}
				integer, err := sqltype.Integer(b.Type(sqltype.Int4), 1)
				if err != nil {
					t.Fatal(err)
				}
				for _, invalid := range [][]expr.Expr{nil, {nil}, {n.Arg, n.Arg}, {plan.LiteralExpr{Info: expr.Info{Usage: b.Type(sqltype.Int4).Usage()}, Value: integer}}} {
					if _, err := n.Rebuild(n.Info, invalid); err == nil {
						t.Fatal("invalid child reconstruction accepted", invalid)
					}
				}
			}
			info := n.Info
			info.Pos.Offset++
			for _, rebuild := range []func() (expr.Expr, error){
				func() (expr.Expr, error) { return n.Rebuild(info, children) },
				func() (expr.Expr, error) { return n.Substitute(info, children) },
				func() (expr.Expr, error) {
					next, _, err := n.Retag(info, children, true, expr.Retag{})
					return next, err
				},
			} {
				next, err := rebuild()
				if err != nil {
					t.Fatal(err)
				}
				got, ok := next.(plan.InstalledSSLInfoExpr)
				if !ok || got.Info != info || got.Procedure != n.Procedure || got.Ref != n.Ref || got.RoutineReference != n.RoutineReference || !reflect.DeepEqual(got.Arg, n.Arg) || !analyze.SameGroupExpression(n, got) {
					t.Fatal("reconstruction erased authority/operand", next)
				}
			}
			retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || !reflect.DeepEqual(retagged, n) {
				t.Fatal("identity retag changed recipe", retagged, err)
			}
			ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
			cursor, err := n.OpenSource(ctx)
			if err != nil {
				t.Fatal(err)
			}
			first, present, err := cursor.Next(context.Background())
			if err != nil || present != (slot != 2) || present && (!first.IsNull() || first.Type() != n.Usage.Type) {
				t.Fatal("scalar row versus empty record source", first, present, err)
			}
			if _, present, err := cursor.Next(context.Background()); err != nil || present {
				t.Fatal("source emitted extra row", present, err)
			}
			cursor.Close()
			if slot == 2 {
				if _, err := n.Eval(ctx); err == nil {
					t.Fatal("SRF accepted scalar invocation")
				}
			} else {
				grouped, err := b.RewriteGroup(n, nil)
				if err != nil {
					t.Fatal(err)
				}
				g := grouped.(plan.InstalledSSLInfoExpr)
				if g.Arg == nil || g.Procedure != n.Procedure || g.Ref != n.Ref || !g.Properties().StageDependent || !analyze.ValidInstalledSSLInfoShape(g) || analyze.ClosedSSLInfoDefault(g) {
					t.Fatal("group dropped argument/authority", g)
				}
			}
		})
	}
	t.Run("DemandedParameterArgument", func(t *testing.T) {
		_, _, n := bind(t, "SELECT ssl_client_dn_field((1 / $1::integer)::text)")
		if !n.Properties().ParameterDependent || !analyze.ValidInstalledSSLInfoShape(n) || analyze.ClosedSSLInfoDefault(n) {
			t.Fatal("parameter shape/default boundary", n)
		}
		parameters := 0
		plan.WalkExpression(n, func(child expr.Expr) {
			if p, ok := child.(plan.ParameterExpr); ok && p.Number == 1 {
				parameters++
			}
		})
		if parameters != 1 {
			t.Fatal("parameter subtree not walked", parameters)
		}
		capture, accepted, err := n.Capture(expr.CaptureFunction)
		if err != nil || !accepted || capture.Count != 1 || !reflect.DeepEqual(capture.Inline[0], n.Arg) {
			t.Fatal("typed parameter argument capture", capture, err)
		}
		retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || !reflect.DeepEqual(retagged, n) {
			t.Fatal("parameter/cast retag lost child", retagged, err)
		}
		_, _, literal := bind(t, "SELECT ssl_client_dn_field('O')")
		info := n.Info
		info.Property = expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, literal.Arg.Properties())
		substituted, err := n.Substitute(info, literal.Children())
		if err != nil {
			t.Fatal(err)
		}
		replaced := substituted.(plan.InstalledSSLInfoExpr)
		if replaced.Procedure != n.Procedure || replaced.Ref != n.Ref || replaced.RoutineReference != n.RoutineReference || !reflect.DeepEqual(replaced.Arg, literal.Arg) || replaced.Properties().ParameterDependent || analyze.SameGroupExpression(n, replaced) {
			t.Fatal("substitution lost changed argument or installed authority", replaced)
		}
		integer, _ := view.LookupType(sqltype.Int4)
		zero, err := sqltype.Integer(integer, 0)
		if err != nil {
			t.Fatal(err)
		}
		one, err := sqltype.Integer(integer, 1)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{zero}}}
		_, err = n.Eval(ctx)
		if failure, ok := err.(interface{ SQLState() string }); !ok || failure.SQLState() != "22012" {
			t.Fatal("known NULL output skipped actual division argument", err)
		}
		ctx.Parameters[0] = one
		value, err := n.Eval(ctx)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal("subsequent parameter execution", value, err)
		}
		ctx.Parameters[0] = sqltype.Null(integer)
		value, err = n.Eval(ctx)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal("strict NULL argument", value, err)
		}
	})
	t.Run("ClosedArgumentOwner", func(t *testing.T) {
		for _, test := range []struct {
			expression        string
			closed, delegated bool
		}{
			{"ssl_client_dn_field('CN')", true, true},
			{"ssl_issuer_field(nextval('ssl_certificate_default')::text)", true, false},
			{"ssl_issuer_field($1::text)", false, false},
			{"ssl_client_dn_field((random())::text)", false, false},
		} {
			_, _, n := bind(t, "SELECT "+test.expression)
			if analyze.ClosedSSLInfoDefault(n) != test.closed || analyze.ClosedSSLInfoDefault(n.SSLInfo) || analyze.ClosedPgstattupleDefault(n, view) != test.delegated {
				t.Fatal("closed-default argument owner bypass", test.expression, n)
			}
		}
	})
}
