package schemacmds_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func armorInstalledSource(t *testing.T, e *core.Engine) plan.InstalledPgpArmorHeadersExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pgp_armor_headers"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "-----BEGIN PGP MESSAGE-----\nfoo: bar\n-----END PGP MESSAGE-----\n"}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledPgpArmorHeadersExpr)
}

func TestPgcryptoArmorProviderAuthentication(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	view := e.CatalogRead()
	for i, want := range []struct {
		sql, symbol, signature string
		result                 ids.OID
		set                    bool
	}{
		{"armor", "pg_armor", "17", 25, false}, {"armor", "pg_armor", "17,1009,1009", 25, false},
		{"dearmor", "pg_dearmor", "25", 17, false}, {"pgp_armor_headers", "pgp_armor_headers", "25", 2249, true},
	} {
		p := view.MustProcedure(ext.Members[14+i])
		name, err := extension.ProcedureInstallationName(view, p)
		symbol, symbolErr := extension.ProcedureSymbol(view, p)
		if err != nil || symbolErr != nil || name != want.sql || symbol != want.symbol || string(p.Signature) != want.signature || p.Result != want.result || p.Installed.ReturnsSet != want.set || p.Installed.Parallel != 's' {
			t.Fatal(p, name, symbol, err, symbolErr)
		}
		if want.set {
			out := p.Installed.Outputs
			if out == nil || out.Inputs() != 1 || out.Len() != 2 || out.Name(0) != "" || out.Name(1) != "key" || out.Name(2) != "value" || out.Type(0) != 25 || out.Type(1) != 25 || p.Installed.Rows != 1000 {
				t.Fatal("declaration-owned OUT", out)
			}
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = false },
			func(p *catalog.Procedure) { p.Volatility = expr.Stable },
			func(p *catalog.Procedure) { p.Result = 16 },
			func(p *catalog.Procedure) { p.Signature = "23" },
			func(p *catalog.Procedure) { p.Installed.Parallel = 'u' },
			func(p *catalog.Procedure) { p.Installed.ReturnsSet = !p.Installed.ReturnsSet },
			func(p *catalog.Procedure) {
				p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25, 25}, "", "key", "value")
			},
			func(p *catalog.Procedure) {
				p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25, 17}, "", "key", "value")
			},
			func(p *catalog.Procedure) {
				p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25}, "", "wrong")
			},
			func(p *catalog.Procedure) {
				p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25, 25}, "", "value", "key")
			},
			func(p *catalog.Procedure) {
				p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25, 25}, "key", "value")
			},
			func(p *catalog.Procedure) { p.Installed.Rows++ },
			func(p *catalog.Procedure) { p.Installed.ArgumentNames[0] = "data" },
			func(p *catalog.Procedure) { p.Display.Source = "armor" },
			func(p *catalog.Procedure) { p.Display.Binary = "$libdir/fake" },
			func(p *catalog.Procedure) { p.Implementation = new(extension.PgcryptoProvider) },
			func(p *catalog.Procedure) { p.Origin = nil },
			func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin },
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(p.Address(), &bad)
			if _, err := extension.ProcedureSymbol(e.Catalog.Read(state), &bad); err == nil {
				t.Fatal("forged declaration admitted", i, bad)
			}
		}
	}
}

func TestPgcryptoArmorRetainedSourceIdentity(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	view := e.CatalogRead()
	n := armorInstalledSource(t, e)
	if err := stored.ValidateInstalledPgpArmorHeaders(n, view); err != nil {
		t.Fatal(err)
	}
	refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
	if err != nil || !slices.Contains(refs, n.Ref.Address) {
		t.Fatal(refs, err)
	}
	rebuilt, err := n.Rebuild(n.Info, n.Children())
	if err != nil || !n.SameSourceOccurrence(rebuilt) || !analyze.SameGroupExpression(n, rebuilt) {
		t.Fatal(rebuilt, err)
	}
	badInfo := n.Info
	badInfo.RoutineReference = nil
	if _, err := n.Rebuild(badInfo, n.Children()); err == nil {
		t.Fatal("rebuild dropped retained authority")
	}
	for _, mutate := range []func(*plan.InstalledPgpArmorHeadersExpr){
		func(n *plan.InstalledPgpArmorHeadersExpr) { n.Ref.Epoch++ },
		func(n *plan.InstalledPgpArmorHeadersExpr) {
			n.Ref = ids.ObjectRef{Address: n.Ref.Address, Epoch: n.Ref.Epoch}
		},
		func(n *plan.InstalledPgpArmorHeadersExpr) { n.Procedure = view.MustProcedure(ext.Members[16]) },
		func(n *plan.InstalledPgpArmorHeadersExpr) {
			foreign := sqltype.Builtins()
			var text *sqltype.Descriptor
			for _, d := range foreign {
				if d.ID() == sqltype.Text {
					text = d
				}
			}
			shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "key", Usage: text.Usage()}, {Attribute: 2, Name: "value", Usage: text.Usage()}})
			if err != nil {
				t.Fatal(err)
			}
			n.Usage = shape.Usage()
		},
	} {
		bad := n
		mutate(&bad)
		if err := stored.ValidateInstalledPgpArmorHeaders(bad, view); err == nil {
			t.Fatal("foreign source capture admitted", bad)
		}
	}
	// Remap the entire sealed installation, not just its OUT procedure. The old
	// carrier continues to own the original declaration and argument backing.
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.Members {
		mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + i)}
	}
	state := catalog.CloneState(view.State())
	for old := range mapping {
		state.UnbindName(catalog.DescriptorNameKey(view.Get(old)))
		state.RemoveDescriptor(old)
	}
	for old, destination := range mapping {
		d := extension.RemapDescriptor(view.Get(old), mapping)
		state.SetDescriptor(destination, d)
		state.BindName(catalog.DescriptorNameKey(d), destination)
	}
	state.SetEpoch(state.Epoch() + 1)
	destination := e.Catalog.Read(state)
	remap := func(ref ids.ObjectRef) ids.ObjectRef {
		ref.Epoch = state.Epoch()
		if address, ok := mapping[ref.Address]; ok {
			ref.Address = address
		}
		return ref
	}
	mapper := stored.ViewQueryRemapper{Catalog: destination, Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "headers", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	mapped, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	m := mapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgpArmorHeadersExpr)
	if m.Ref != remap(n.Ref) || !exec.SameRoutineDeclaration(destination.MustProcedure(m.Ref.Address), m.Procedure) || n.Procedure != view.Get(n.Ref.Address) || query.Expressions[0].(plan.InstalledPgpArmorHeadersExpr).Ref != n.Ref || &query.Expressions[0] == &mapped.(*plan.ProjectNode).Expressions[0] {
		t.Fatal("retained/remapped owner lost", m)
	}
	if err := stored.ValidateInstalledPgpArmorHeaders(m, destination); err != nil {
		t.Fatal(err)
	}
	retagged, _, err := stored.RetagTypes(destination, m, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	m = retagged.(plan.InstalledPgpArmorHeadersExpr)
	text, err := (stored.Deparser{View: destination}).SQL(context.Background(), m, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "pgp_armor_headers") {
		t.Fatal(text, err)
	}
	state.RemoveDescriptor(m.Ref.Address)
	if err := stored.ValidateInstalledPgpArmorHeaders(m, destination); err == nil {
		t.Fatal("removed declaration admitted")
	}
}

func TestPgcryptoArmorScalarStoredRecipes(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	for i, tc := range []struct {
		name      string
		args      []parser.Expr
		signature string
	}{
		{"armor", []parser.Expr{literal("test")}, "17"},
		{"armor", []parser.Expr{literal("test"), literal("{foo}"), literal("{bar}")}, "17,1009,1009"},
		{"dearmor", []parser.Expr{literal("-----BEGIN PGP MESSAGE-----\n\ndGVzdA==\n=+G7Q\n-----END PGP MESSAGE-----\n")}, "25"},
	} {
		node, err := b.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: tc.args})
		if err != nil {
			t.Fatal(err)
		}
		n := node.(plan.InstalledCryptoExpr)
		if n.Ref.Address != ext.Members[14+i] || analyze.CryptoExpressionSignature(n) != catalog.ProcedureSignature(tc.signature) || !analyze.ClosedPgcryptoDefault(n) || n.Validate() != nil {
			t.Fatal(n)
		}
		if err := stored.ValidateInstalledCrypto(n, e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		for _, arg := range n.Args {
			if arg == nil {
				t.Fatal("lost formal")
			}
		}
		if _, err := n.Rebuild(n.Info, n.Args); err != nil {
			t.Fatal(err)
		}
		if _, ok := functions.PgcryptoLookup(tc.name, tc.signature); !ok {
			t.Fatal("missing exact declaration")
		}
	}
}
