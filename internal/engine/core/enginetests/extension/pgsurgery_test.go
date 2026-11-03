package extension_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
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

func surgeryInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pg_surgery")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_surgery"}))
}
func surgeryExpression(t *testing.T, e *core.Engine, operation functions.PgsurgeryOperation) plan.InstalledPgsurgeryExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	node, err := binder.BindFunctionSource(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: operation.Symbol()}}}, Args: []parser.Expr{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "-"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "{}"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledPgsurgeryExpr)
}
func TestPgsurgeryFreezeProvider(t *testing.T) {
	e := engine.New()
	ext := surgeryInstalled(t, e)
	view := e.CatalogRead()
	control := extension.StockPgsurgery.Control()
	if control.Version != "1.0" || !control.Superuser || control.Trusted || !control.Relocatable || len(ext.Members) != 2 {
		t.Fatal("finite freeze-first surgery cohort", control, ext)
	}
	if err := extension.StockPgsurgery.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	proc := view.MustProcedure(ext.Members[0])
	if proc.Name() != "heap_force_freeze" || proc.Signature != "2205,1010" || proc.Result != 2278 || !proc.Strict || proc.Volatility != expr.Volatile || proc.Installed.Parallel != 'u' || proc.Installed.ReturnsSet || proc.Installed.Outputs != nil || proc.Installed.ArgumentNames != ([4]string{"reloid", "tids"}) || proc.SecurityDefiner || proc.Display.Language != catalog.CRoutineLanguage || proc.Display.Source != "heap_force_freeze" || proc.Display.Binary != "$libdir/pg_surgery" || !proc.Display.HasBinary {
		t.Fatal("released freeze declaration", proc)
	}
	initial := view.State().InitialPrivilege(proc.Address())
	if initial == nil || initial.PrivType != 'e' || !reflect.DeepEqual(initial.ACL, proc.ACL) {
		t.Fatal("missing genuine revoke history", initial)
	}
	for _, entry := range proc.ACL.Entries {
		if entry.Grantee.Public && entry.Privileges&authorization.ExecutePrivilege != 0 {
			t.Fatal("PUBLIC retained execute")
		}
		if entry.Grantee.Role.ID() == authorization.StatScanTablesRoleID {
			t.Fatal("invented scan-role grant")
		}
	}
	for name, mutate := range map[string]func(*catalog.Procedure){
		"foreign_provider": func(p *catalog.Procedure) { p.Implementation = new(extension.PgsurgeryProvider) },
		"symbol":           func(p *catalog.Procedure) { p.Display.Source = "heap_force_kill" },
		"signature":        func(p *catalog.Procedure) { p.Signature = "23,1010" },
		"strict":           func(p *catalog.Procedure) { p.Strict = false },
		"parallel":         func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
		"names":            func(p *catalog.Procedure) { p.Installed.ArgumentNames[0] = "target" },
		"origin_absent":    func(p *catalog.Procedure) { p.Origin = nil },
		"origin_unsealed": func(p *catalog.Procedure) {
			installation := *p.Origin.Installation
			origin := *p.Origin
			origin.Installation = &installation
			p.Origin = &origin
		},
		"slot": func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration++; p.Origin = &origin },
	} {
		t.Run(name, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			bad := *proc
			mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if extension.StockPgsurgery.ValidateProcedure(e.Catalog.Read(state), &bad) == nil {
				t.Fatal("forged declaration admitted")
			}
		})
	}
	surgeryExpressionOwnership(t, e, ext, functions.PgsurgeryFreeze)
}
func surgeryExpressionOwnership(t *testing.T, e *core.Engine, ext *catalog.Extension, operation functions.PgsurgeryOperation) {
	t.Helper()
	view := e.CatalogRead()
	n := surgeryExpression(t, e, operation)
	if n.Operation != operation || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent || n.CallRoutineReference() != n.Procedure || exec.PlannedVolatility(n) != expr.Volatile || plan.GenerationImmutable(n) {
		t.Fatal("lost volatile installed authority")
	}
	for _, rewrite := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		value, err := rewrite(expr.InfoOf(n), n.Children())
		if err != nil {
			t.Fatal(err)
		}
		copy := value.(plan.InstalledPgsurgeryExpr)
		if copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.Operation != n.Operation || copy.Slots != n.Slots || &copy.Args[0] == &n.Args[0] {
			t.Fatal("rebuild borrowed children or changed symbolic identity")
		}
	}
	for _, mutate := range []func(*plan.InstalledPgsurgeryExpr){
		func(n *plan.InstalledPgsurgeryExpr) { n.RoutineReference = nil },
		func(n *plan.InstalledPgsurgeryExpr) { n.Ref.Epoch++ },
		func(n *plan.InstalledPgsurgeryExpr) { n.Ref.Epoch = 0 },
		func(n *plan.InstalledPgsurgeryExpr) { n.Ref = new(ids.Domain).Ref(n.Ref.Epoch, n.Ref.Address) },
		func(n *plan.InstalledPgsurgeryExpr) { n.Slots[1] = 0 },
		func(n *plan.InstalledPgsurgeryExpr) { n.Operation = 0 },
		func(n *plan.InstalledPgsurgeryExpr) {
			if n.Operation == functions.PgsurgeryKill {
				n.Operation = functions.PgsurgeryFreeze
			} else {
				n.Operation = functions.PgsurgeryKill
			}
		},
		func(n *plan.InstalledPgsurgeryExpr) { n.Usage = n.Args[0].ResultUsage() },
	} {
		bad := n
		mutate(&bad)
		if stored.ValidateInstalledPgsurgery(bad, view) == nil {
			t.Fatal("forged carrier admitted")
		}
	}
	if !analyze.ClosedPgstattupleDefault(n, view) {
		t.Fatal("authenticated scalar closure rejected")
	}
	text, err := (stored.Deparser{View: view}).SQL(context.Background(), n, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "reloid =>") || !strings.Contains(text, "tids =>") {
		t.Fatal("formal rendering", text, err)
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address():  {ClassID: ids.ExtensionClassID, ObjectID: 930000},
		ext.Members[0]: {ClassID: ids.ProcedureClassID, ObjectID: 930001},
		ext.Members[1]: {ClassID: ids.ProcedureClassID, ObjectID: 930002},
	}
	state := catalog.CloneState(view.State())
	descriptor := extension.DescriptorRemapper(mapping)
	for old := range mapping {
		state.UnbindName(catalog.DescriptorNameKey(view.Get(old)))
		state.RemoveDescriptor(old)
	}
	for old, address := range mapping {
		next := descriptor(view.Get(old))
		state.SetDescriptor(address, next)
		state.BindName(catalog.DescriptorNameKey(next), address)
	}
	state.SetEpoch(state.Epoch() + 1)
	remap := func(ref ids.ObjectRef) ids.ObjectRef {
		ref.Epoch = state.Epoch()
		if address, ok := mapping[ref.Address]; ok {
			ref.Address = address
		}
		return ref
	}
	mapper := stored.ViewQueryRemapper{Catalog: e.Catalog.Read(state), Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(descriptor, nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "s", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	mappedQuery, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	mapped := mappedQuery.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgsurgeryExpr)
	if mapped.Ref != remap(n.Ref) || mapped.CallRoutineReference() != mapped.Procedure || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || n.Procedure != view.Get(n.Ref.Address) || &mapped.Args[0] == &n.Args[0] {
		t.Fatal("remap changed old immutable backing or lost destination identity")
	}
	if err := stored.ValidateInstalledPgsurgery(mapped, e.Catalog.Read(state)); err != nil {
		t.Fatal(err)
	}
}

func TestPgsurgeryKillProvider(t *testing.T) {
	e := engine.New()
	ext := surgeryInstalled(t, e)
	view := e.CatalogRead()
	if len(ext.Members) != 2 {
		t.Fatal("incomplete finite surgery recipe")
	}
	proc := view.MustProcedure(ext.Members[1])
	symbol, err := extension.ProcedureSymbol(view, proc)
	if err != nil || symbol != "heap_force_kill" || proc.Display.Source != symbol || proc.Display.Binary != "$libdir/pg_surgery" || !proc.Display.HasBinary || !proc.Strict || proc.Volatility != expr.Volatile || proc.Result != 2278 || proc.Signature != "2205,1010" || proc.Installed.Parallel != 'u' || proc.SecurityDefiner || proc.Installed.ArgumentNames != ([4]string{"reloid", "tids"}) {
		t.Fatal("kill lost its sealed C declaration", proc, err)
	}
	initial := view.State().InitialPrivilege(proc.Address())
	if initial == nil || initial.PrivType != 'e' || !reflect.DeepEqual(initial.ACL, proc.ACL) || initial == view.State().InitialPrivilege(ext.Members[0]) {
		t.Fatal("kill did not acquire independent initial privileges")
	}
	for _, entry := range proc.ACL.Entries {
		if entry.Grantee.Public && entry.Privileges&authorization.ExecutePrivilege != 0 {
			t.Fatal("kill inherited PUBLIC EXECUTE")
		}
	}
	surgeryExpressionOwnership(t, e, ext, functions.PgsurgeryKill)
}

func TestPgsurgeryFreezeProviderMembership(t *testing.T) {
	for _, membership := range []string{"detached", "foreign", "origin_retired"} {
		t.Run(membership, func(t *testing.T) {
			e := engine.New()
			ext := surgeryInstalled(t, e)
			before := e.CatalogRead().MustProcedure(ext.Members[0])
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			execute := func(q string) { enginetest.RoleExecute(t, e, op, q) }
			execute("ALTER EXTENSION pg_surgery DROP FUNCTION heap_force_freeze(regclass,tid[])")
			if membership == "foreign" {
				execute(`CREATE EXTENSION "uuid-ossp"`)
				execute(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION heap_force_freeze(regclass,tid[])`)
			}
			execute("ALTER FUNCTION heap_force_freeze(regclass,tid[]) RENAME TO genuine_freeze")
			if membership == "origin_retired" {
				execute("DROP EXTENSION pg_surgery")
			}
			view := e.CatalogRead()
			current := view.MustProcedure(before.Address())
			ref := e.Catalog.Domain.Ref(view.Epoch(), current.Address())
			if current == before || current.Name() != "genuine_freeze" || current.Origin != before.Origin || before.Name() != "heap_force_freeze" {
				t.Fatal("rename mutated captured declaration or origin")
			}
			if err := extension.ValidatePgsurgeryIdentity(view, current, ref); err != nil {
				t.Fatal("authentic origin lost", err)
			}
			symbol, err := extension.ProcedureSymbol(view, current)
			if err != nil || symbol != "heap_force_freeze" {
				t.Fatal(symbol, err)
			}
			if err := extension.ValidatePgsurgeryIdentity(view, before, ref); err == nil {
				t.Fatal("captured descriptor retained live authority")
			}
		})
	}
}

func TestPgsurgeryKillStrictNullProvenance(t *testing.T) {
	e := engine.New()
	surgeryInstalled(t, e)
	for _, operation := range []functions.PgsurgeryOperation{functions.PgsurgeryFreeze, functions.PgsurgeryKill} {
		original := surgeryExpression(t, e, operation)
		info := expr.InfoOf(original)
		info.Occurrence = 9
		info.Source = expr.FoldSourceOccurrence{Frame: 7, Position: parser.Position{Offset: 13, Character: 13}, Input: parser.Position{Offset: 17, Character: 17}}
		child := plan.LiteralExpr{Info: expr.Info{Usage: original.Args[1].ResultUsage()}, Value: sqltype.Null(original.Args[1].ResultUsage().Type)}
		rebuilt, err := original.Rebuild(info, []expr.Expr{original.Args[0], child})
		if err != nil {
			t.Fatal(err)
		}
		call := rebuilt.(plan.InstalledPgsurgeryExpr)
		folded, changed, err := exec.FoldStrictParameterNull(&exec.Context{}, call)
		if err != nil || !changed {
			t.Fatal("strict canonical fold rejected", changed, err)
		}
		literal, ok := folded.(plan.LiteralExpr)
		if !ok || !literal.Value.IsNull() || literal.ResultUsage() != call.ResultUsage() || literal.CallRoutineReference() != nil || literal.Source != info.Source || literal.Occurrence != info.Occurrence || literal.Pos != info.Pos {
			t.Fatal("strict NULL lost owned occurrence/result or retained callable authority")
		}
		if original.Operation != operation || call.CallRoutineReference() != original.Procedure || &call.Args[0] == &original.Args[0] || original.Args[1].ResultUsage() != child.ResultUsage() {
			t.Fatal("NULL planning mutated original authenticated definition/backing")
		}
	}
}
