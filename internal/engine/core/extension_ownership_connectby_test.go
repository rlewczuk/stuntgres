package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ownershipConnectbyIdentity(t *testing.T) {
	for _, variant := range []struct {
		name, args, signature string
		declaration           int
	}{
		{"branch", "'missing','k','p','root',0,'~'", "text,text,text,text,integer,text", 1},
		{"plain", "'missing','k','p','root',0", "text,text,text,text,integer", 2},
		{"ordered_branch", "'missing','k','p','ord','root',0,'~'", "text,text,text,text,text,integer,text", 3},
		{"ordered", "'missing','k','p','ord','root',0", "text,text,text,text,text,integer", 4},
	} {
		t.Run(variant.name, func(t *testing.T) {
			e, ext := ownershipUnitInstall(t, "tablefunc")
			before := e.CatalogRead()
			recipe := ext.Installation
			original := before.MustProcedure(recipe.Members[variant.declaration])
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			raw := enginetest.Statement(t, "SELECT connectby("+variant.args+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
			bound, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			n := bound.(plan.InstalledConnectbyExpr)
			if n.Procedure != original || n.RoutineReference != original {
				t.Fatal("wrong released overload carrier")
			}
			defs := []parser.FunctionColumnDefinition{
				{Name: parser.Identifier{Text: "node"}, Type: parser.TypeName{Name: "text"}},
				{Name: parser.Identifier{Text: "parent"}, Type: parser.TypeName{Name: "text"}},
				{Name: parser.Identifier{Text: "depth"}, Type: parser.TypeName{Name: "integer"}},
			}
			if n.Branch {
				defs = append(defs, parser.FunctionColumnDefinition{Name: parser.Identifier{Text: "branch"}, Type: parser.TypeName{Name: "text"}})
			}
			if n.Ordered {
				defs = append(defs, parser.FunctionColumnDefinition{Name: parser.Identifier{Text: "serial"}, Type: parser.TypeName{Name: "integer"}})
			}
			completed, err := b.CompleteFunctionDefinitions(raw, n, defs)
			if err != nil {
				t.Fatal(err)
			}
			n = completed.(plan.InstalledConnectbyExpr)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, `CREATE ROLE eo_connectby_owner`)
			enginetest.RoleExecute(t, e, op, `CREATE EXTENSION "uuid-ossp"`)
			enginetest.RoleExecute(t, e, op, `CREATE FUNCTION eo_extra(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`)
			enginetest.RoleExecute(t, e, op, `ALTER EXTENSION tablefunc ADD FUNCTION eo_extra(integer)`)
			populated := e.CatalogRead()
			currentExtension := populated.MustExtension(ext.Address())
			if len(currentExtension.Members) != 17 || len(recipe.Declarations) != 16 {
				t.Fatal("ordinary membership rewrote release recipe")
			}
			if err := extension.StockTablefunc.ValidateExtension(populated, currentExtension); err != nil {
				t.Fatal("ordinary member extended current inventory without changing the sixteen-declaration recipe", err)
			}
			emptyState := catalog.CloneState(populated.State())
			emptyExtension := *currentExtension
			emptyExtension.Members = nil
			emptyState.SetDescriptor(ext.Address(), &emptyExtension)
			for _, address := range currentExtension.Members {
				d := populated.Get(address)
				detached, err := catalog.WithExtensionMembership(d, ids.ObjectAddress{}, d.Revision())
				if err != nil {
					t.Fatal(err)
				}
				emptyState.SetDescriptor(address, detached)
			}
			empty := populated.WithState(emptyState)
			if err := extension.StockTablefunc.ValidateExtension(empty, &emptyExtension); err != nil {
				t.Fatal("empty reciprocal inventory rejected", err)
			}
			if err := stored.ValidateInstalledConnectby(n, empty); err != nil {
				t.Fatal("empty current inventory erased genuine capability", err)
			}
			sig := "(" + variant.signature + ")"
			for _, q := range []string{
				`ALTER EXTENSION tablefunc DROP FUNCTION public.connectby` + sig,
				`ALTER FUNCTION public.connectby` + sig + ` RENAME TO eo_walk`,
				`ALTER FUNCTION public.eo_walk` + sig + ` OWNER TO eo_connectby_owner`,
				`REVOKE EXECUTE ON FUNCTION public.eo_walk` + sig + ` FROM PUBLIC`,
				`ALTER EXTENSION "uuid-ossp" ADD FUNCTION public.eo_walk` + sig,
				`DROP EXTENSION tablefunc`,
				`ALTER EXTENSION "uuid-ossp" DROP FUNCTION public.eo_walk` + sig,
			} {
				enginetest.RoleExecute(t, e, op, q)
				view := e.CatalogRead()
				p := view.MustProcedure(original.Address())
				if p.Origin.Installation != recipe || p.Origin.Declaration != variant.declaration || !p.Origin.Valid(p.Address()) || p.Installed != original.Installed || p.Display != original.Display || p.Implementation != original.Implementation {
					t.Fatal("membership rewrote released declaration", q)
				}
				if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != "connectby" {
					t.Fatal("lost immutable connectby symbol", q, symbol, err)
				}
				if current, err := exec.ValidateStoredRoutineDescriptor(view, original); err != nil || current != p {
					t.Fatal("retained carrier lost current authority", q, err)
				}
				if err := stored.ValidateInstalledConnectby(n, view); err != nil {
					t.Fatal("typed carrier rejected", q, err)
				}
				if current, ok := view.Extension(ext.Address()); ok {
					if err := extension.StockTablefunc.ValidateExtension(view, current); err != nil {
						t.Fatal("mutable member bag rejected", q, err)
					}
				}
			}
			view := e.CatalogRead()
			p := view.MustProcedure(original.Address())
			recipient, ok := e.Roles().Lookup("eo_connectby_owner")
			if !ok || p.Owner != recipient.Ref || !p.ACL.Explicit || reflect.DeepEqual(p.ACL, original.ACL) || view.Get(ext.Address()) != nil || p.Extension != (ids.ObjectAddress{}) || before.MustProcedure(original.Address()) != original || original.Extension != ext.Address() {
				t.Fatal("current owner/ACL, absence, or immutable predecessor lost")
			}
			for _, corrupt := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Origin = nil },
				func(p *catalog.Procedure) {
					origin := *p.Origin
					copied := *recipe
					origin.Installation = &copied
					p.Origin = &origin
				},
				func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin },
				func(p *catalog.Procedure) { p.Signature = "23" },
				func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
				func(p *catalog.Procedure) { p.Display.Source = "normal_rand" },
			} {
				forged := *p
				corrupt(&forged)
				candidate := catalog.CloneState(view.State())
				candidate.SetDescriptor(p.Address(), &forged)
				invalid := view.WithState(candidate)
				if err := extension.StockTablefunc.ValidateProcedure(invalid, &forged); err == nil {
					t.Fatal("forged current declaration accepted")
				}
				if err := stored.ValidateInstalledConnectby(n, invalid); err == nil {
					t.Fatal("typed carrier accepted forged current authority")
				}
				bad := n
				bad.Procedure, bad.RoutineReference = &forged, &forged
				if err := stored.ValidateInstalledConnectby(bad, view); err == nil {
					t.Fatal("forged retained carrier accepted")
				}
			}
			stale := n
			stale.Ref.Epoch++
			if err := stored.ValidateInstalledConnectby(stale, view); err == nil {
				t.Fatal("stale epoch carrier accepted")
			}
			foreign := n
			foreign.Ref = (&ids.Domain{}).Ref(view.Epoch(), original.Address())
			if err := stored.ValidateInstalledConnectby(foreign, view); err == nil {
				t.Fatal("foreign domain carrier accepted")
			}
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{
				ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 960000},
				p.Address():   {ClassID: ids.ProcedureClassID, ObjectID: 960001},
			}
			candidate := catalog.CloneState(view.State())
			candidate.UnbindName(catalog.DescriptorNameKey(p))
			candidate.RemoveDescriptor(p.Address())
			remapDescriptor := extension.DescriptorRemapper(mapping)
			mapped := remapDescriptor(p).(*catalog.Procedure)
			candidate.SetDescriptor(mapped.Address(), mapped)
			candidate.BindName(catalog.DescriptorNameKey(mapped), mapped.Address())
			candidate.SetEpoch(view.Epoch() + 1)
			destination := view.WithState(candidate)
			ref := func(r ids.ObjectRef) ids.ObjectRef {
				r.Epoch = destination.Epoch()
				if a, ok := mapping[r.Address]; ok {
					r.Address = a
				}
				return r
			}
			mapper := stored.ViewQueryRemapper{Catalog: destination, Ref: ref, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(remapDescriptor, nil, nil)}
			query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "walk", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
			remapped, err := mapper.Query(query)
			if err != nil {
				t.Fatal("absent-origin retained carrier remap", err)
			}
			m := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledConnectbyExpr)
			if m.Ref != ref(n.Ref) || m.RoutineReference != m.Procedure || m.Procedure == mapped || !exec.SameRoutineDeclaration(m.Procedure, mapped) || !sqltype.SameRecordUsage(m.Usage, n.Usage) || m.Ordered != n.Ordered || m.Branch != n.Branch || !m.Defined || destination.Get(mapping[ext.Address()]) != nil {
				t.Fatal("remap lost retained origin, current authority, record shape or flags")
			}
		})
	}
}

func ownershipConnectbyCanonicalRemap(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "tablefunc")
	view := e.CatalogRead()
	recipe := ext.Installation
	if len(recipe.Members) != 16 || len(recipe.Declarations) != 16 {
		t.Fatal("complete tablefunc recipe lost normal_rand, connectby or crosstab declarations")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 950000}, ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 950001}}
	for i, a := range recipe.Members {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(950100 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	next := remap(ext).(*catalog.Extension).Installation
	if !next.Valid() || next == recipe || next.Cohort != mapping[ext.Address()] || next.Version != "1.0" {
		t.Fatal("remapped sealed release lost")
	}
	for i, a := range recipe.Members {
		original := view.Get(a)
		canonical := next.Declarations[i]
		if next.Members[i] != mapping[a] || canonical.Address() != mapping[a] || canonical.Namespace() != mapping[ext.TargetNamespace] || catalog.DescriptorOrigin(canonical) != nil {
			t.Fatal("remap split the complete canonical release", i)
		}
		if procedure, ok := original.(*catalog.Procedure); ok {
			p := remap(procedure).(*catalog.Procedure)
			declaration := canonical.(*catalog.Procedure)
			result := procedure.Result
			if mapped, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: result}]; ok {
				result = mapped.ObjectID
			}
			if p.Origin.Installation != next || p.Origin.Declaration != i || !p.Origin.Valid(mapping[a]) || declaration.Signature != procedure.Signature || declaration.Installed != procedure.Installed || declaration.Result != result || declaration.Display != procedure.Display || p.Result != result {
				t.Fatal("remap split or changed released procedure declaration", i)
			}
		}
		if recipe.Declarations[i].Address() != a || catalog.DescriptorOrigin(original).Installation != recipe {
			t.Fatal("remap mutated source release", i)
		}
	}
}
