package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestUUIDOsspExpressionIdentity(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_generate_v4"}}}}
	node, err := binder.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.InstalledUUIDOsspExpr)
	if carrier, err := exec.ExpressionRoutineDescriptor(n); err != nil || carrier != n.Procedure {
		t.Fatal("missing common original routine carrier", carrier, err)
	}
	if n.Ref.Address != ext.Members[0] || n.ResultUsage().Type.ID() != sqltype.UUID || exec.PlannedVolatility(n) != expr.Volatile || !analyze.ValidInstalledUUIDOsspShape(n) || !analyze.ClosedUUIDDefault(n) || plan.GenerationImmutable(n) {
		t.Fatal("installed UUID shape/properties", n)
	}
	if plan.GenerationHasTableOID(n) {
		t.Fatal("zero-child UUID leaf acquired a tableoid dependency")
	}
	if err := validateGenerationPlan(view, nil, n); err != nil {
		t.Fatal("installed UUID plan validation", err)
	}
	found := false
	for _, d := range binder.Dependencies {
		if d.Ref == n.Ref && d.Revision == n.Procedure.Revision() {
			found = true
		}
	}
	if !found {
		t.Fatal("missing genuine procedure dependency")
	}
	visits := 0
	plan.ExpressionChildren(n, func(expr.Expr) { visits++ })
	if visits != 0 || !analyze.SameGroupExpression(n, n) {
		t.Fatal("not a zero-child identity leaf")
	}
	refs, err := ExpressionDependRefs(context.Background(), view, n)
	if err != nil || !slices.Contains(refs, n.Ref.Address) {
		t.Fatal(refs, err)
	}
	retagged, _, err := RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateInstalledUUIDOssp(retagged.(plan.InstalledUUIDOsspExpr), view); err != nil {
		t.Fatal(err)
	}
	rendered, err := (scalarRenderer{}).SQL(context.Background(), view, retagged)
	if err != nil || !strings.Contains(rendered, "uuid_generate_v4") || !strings.Contains(rendered, "public") || strings.Contains(rendered, "pg_catalog") {
		t.Fatal("installed renderer", rendered, err)
	}
	mapped, err := RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch++; return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := mapped.(plan.InstalledUUIDOsspExpr)
	if m.Ref.Epoch != n.Ref.Epoch+1 || m.Procedure != n.Procedure || m.Name != "uuidv4" || exec.PlannedVolatility(m) != expr.Volatile {
		t.Fatal("remap lost captured provider", m)
	}
	proc := extension.RemapDescriptor(n.Procedure, map[ids.ObjectAddress]ids.ObjectAddress{n.Procedure.Address(): {ClassID: ids.ProcedureClassID, ObjectID: 999999}}).(*catalog.Procedure)
	if extension.UUIDOsspProviderOf(proc) != extension.StockUUIDOssp || proc.Signature != "" || proc.Result != 2950 || proc.Address() == n.Procedure.Address() {
		t.Fatal(proc)
	}
	for _, mutate := range []func(*plan.InstalledUUIDOsspExpr){
		func(x *plan.InstalledUUIDOsspExpr) { x.Name = "gen_random_uuid" },
		func(x *plan.InstalledUUIDOsspExpr) { x.RoutineReference = nil },
		func(x *plan.InstalledUUIDOsspExpr) {
			x.RoutineReference = view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3432})
		},
		func(x *plan.InstalledUUIDOsspExpr) { x.Ref.Epoch = 0 },
		func(x *plan.InstalledUUIDOsspExpr) { copy := *x.Procedure; x.Procedure = &copy },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.Volatility = expr.Immutable },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.RowDependent = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.ParameterDependent = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.DomainDependent = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.RuntimeDomainConversion = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Timestamp = x.Usage.Type },
	} {
		bad := n
		mutate(&bad)
		if err := ValidateInstalledUUIDOssp(bad, view); err == nil {
			t.Fatal("invalid stored identity accepted", bad)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled leaf demanded session/entropy", err)
	}
}

func TestUUIDOsspControls(t *testing.T) {
	record := extension.OwnerRecord{Name: "uuid-ossp", Schema: "public", Version: "1.1", Owner: "installer", Members: []extension.MemberOwner{{Kind: "procedure", Name: "uuid_generate_v4", Signature: "", Owner: "postgres"}}}
	for _, name := range []string{"uuid_nil", "uuid_ns_dns", "uuid_ns_url", "uuid_ns_oid", "uuid_ns_x500"} {
		record.Members = append(record.Members, extension.MemberOwner{Kind: "procedure", Name: name, Signature: "", Owner: "postgres"})
	}
	encode := func(r extension.OwnerRecord) string {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
	}
	got, recognized, err := extension.DecodeOwners(encode(record))
	if err != nil || !recognized || !reflect.DeepEqual(got, record) {
		t.Fatal("zero-signature owner roundtrip", got, recognized, err)
	}
	for _, mutate := range []func(*extension.OwnerRecord){
		func(r *extension.OwnerRecord) { r.Version = "missing" },
		func(r *extension.OwnerRecord) { r.Members = nil },
		func(r *extension.OwnerRecord) { r.Members = append(r.Members, r.Members[0]) },
		func(r *extension.OwnerRecord) { r.Members[0].Name = "uuidv4" },
		func(r *extension.OwnerRecord) { r.Members[0].Signature = "25" },
		func(r *extension.OwnerRecord) { r.Members[0].Kind = "dictionary" },
		func(r *extension.OwnerRecord) { r.Members[0].Owner = "" },
	} {
		bad := record
		bad.Members = slices.Clone(record.Members)
		mutate(&bad)
		if _, recognized, err := extension.DecodeOwners(encode(bad)); !recognized || err == nil {
			t.Fatal("invalid owner metadata accepted", bad, err)
		}
	}
}

func TestUUIDOsspGroupedExpressionIdentity(t *testing.T) {
	e, _ := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_generate_v4"}}}}
	node, err := binder.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	grouped, err := binder.RewriteGroup(node, nil)
	if err != nil {
		t.Fatal("actual grouping rewrite", err)
	}
	n := grouped.(plan.InstalledUUIDOsspExpr)
	if !n.Properties().StageDependent || n.Properties().Volatility != expr.Volatile || n.Procedure != node.(plan.InstalledUUIDOsspExpr).Procedure {
		t.Fatal("grouping lost staged volatility/installed identity", n)
	}
	if analyze.ClosedUUIDDefault(grouped) {
		t.Fatal("grouped execution leaf admitted as a closed DEFAULT")
	}
	if err := ValidateInstalledUUIDOssp(n, view); err != nil {
		t.Fatal("legitimate grouped installed identity rejected", err)
	}
	for _, mutate := range []func(*plan.InstalledUUIDOsspExpr){
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.RowDependent = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.ParameterDependent = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.DomainDependent = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.RuntimeDomainConversion = true },
		func(x *plan.InstalledUUIDOsspExpr) { x.Property.Volatility = expr.Stable },
	} {
		bad := n
		mutate(&bad)
		if err := ValidateInstalledUUIDOssp(bad, view); err == nil || analyze.ClosedUUIDDefault(bad) {
			t.Fatal("group stage masked invalid leaf properties or relaxed DEFAULT", bad)
		}
	}
	retagged, _, err := RetagTypes(view, grouped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal("retagging legitimate grouped leaf", err)
	}
	mapped, err := RemapExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal("remapping legitimate grouped leaf", err)
	}
	rebound, _, err := RetagTypes(view, mapped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal("retagging remapped grouped leaf", err)
	}
	got := rebound.(plan.InstalledUUIDOsspExpr)
	if got.Ref != n.Ref || got.Procedure != n.Procedure || !got.Properties().StageDependent || exec.PlannedVolatility(got) != expr.Volatile || analyze.ClosedUUIDDefault(got) {
		t.Fatal("grouped remap lost identity/stage or relaxed DEFAULT", got)
	}
}

func TestUUIDOsspRoutineOriginalAuthority(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	other, _ := uuidOsspUnitInstalled(t)
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	roleExecute(t, e, op, "CREATE TABLE strict_null_rows(v integer)")
	roleExecute(t, e, op, "CREATE VIEW uuid_authority_recipe AS SELECT uuid_generate_v4()")
	recipe := e.CatalogRead().MustRelation(roleTableRef(t, e, "uuid_authority_recipe").Address)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "uuid_generate_v4()")
	n := node.(plan.InstalledUUIDOsspExpr)
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured != n.Procedure || captured.Address() != ext.Members[0] || captured.RoutineKind != catalog.FunctionRoutine || captured.Result != 2950 || captured.Signature != "" || !captured.Strict || captured.Volatility != expr.Volatile || len(captured.Body) != 0 || n.Ref.Domain() != e.catalog.Domain {
		t.Fatal("genuine zero-arity original declaration", captured, err)
	}
	original := slices.Clone(binder.Dependencies)
	routines := 0
	for _, dep := range original {
		if dep.Ref.Address.ClassID == ids.ProcedureClassID {
			routines++
			if dep.Ref != n.Ref || dep.Revision != captured.Revision() {
				t.Fatal("invented helper edge or incorrect original manifest", dep)
			}
		}
	}
	if routines != 1 {
		t.Fatal("zero-arity call lost its own manifest", original)
	}
	if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
		t.Fatal("original scalar admission", err)
	}
	for _, mode := range []string{"nil", "empty", "missing", "epoch", "revision", "foreign_ref"} {
		t.Run(mode, func(t *testing.T) {
			probe := evaluation
			deps := slices.Clone(original)
			switch mode {
			case "nil":
				deps = nil
			case "empty":
				deps = []catalog.ExpressionDependency{}
			case "missing":
				deps = slices.DeleteFunc(deps, func(d catalog.ExpressionDependency) bool { return d.Ref.Address == n.Ref.Address })
			default:
				for i := range deps {
					if deps[i].Ref.Address != n.Ref.Address {
						continue
					}
					switch mode {
					case "epoch":
						deps[i].Ref.Epoch++
					case "revision":
						deps[i].Revision++
					case "foreign_ref":
						deps[i].Ref = other.catalog.Domain.Ref(deps[i].Ref.Epoch, deps[i].Ref.Address)
					}
				}
			}
			probe.RoutineChecks = deps
			if err := exec.AdmitRoutineCall(&probe, node, true, false); err == nil {
				t.Fatal("absent/stale original authority repaired")
			}
		})
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(d *catalog.Procedure) { d.Implementation = nil },
		func(d *catalog.Procedure) {
			d.Implementation = new(*extension.StockUUIDOssp)
		},
		func(d *catalog.Procedure) { d.RoutineKind = catalog.ProcedureRoutine },
	} {
		auth := *routineAuthorityView(&evaluation)
		auth.Catalog = auth.Catalog.WithState(catalog.CloneState(auth.Catalog.State()))
		bad := *captured
		mutate(&bad)
		auth.Catalog.State().SetDescriptor(n.Ref.Address, &bad)
		probe := evaluation
		probe.RoutineAuthority = &auth
		if exec.SameRoutineDeclaration(&bad, captured) || exec.AdmitRoutineCall(&probe, node, true, false) == nil {
			t.Fatal("incompatible current provider/kind admitted")
		}
	}
	grafted, _, err := MapExpression(evaluation.Catalog, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, recipe, nil)
	if err != nil {
		t.Fatal("graft genuine recipe", err)
	}
	probe := evaluation
	probe.RoutineChecks = nil
	if err := exec.AdmitRoutineCall(&probe, grafted, true, false); err != nil {
		t.Fatal("graft lost actual owning manifest", err)
	}
	if expr.InfoOf(grafted).RoutineRecipeReference != recipe || expr.InfoOf(node).RoutineRecipeReference != nil {
		t.Fatal("graft lost ancestry or mutated original")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000},
		n.Ref.Address: {ClassID: ids.ProcedureClassID, ObjectID: 900001},
	}
	next := mapping[n.Ref.Address].ObjectID + 1
	for _, address := range ext.Members {
		if address != n.Ref.Address {
			mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: next}
			next++
		}
	}
	state := catalog.CloneState(evaluation.Catalog.State())
	for address := range mapping {
		state.UnbindName(catalog.DescriptorNameKey(state.Get(address)))
		state.RemoveDescriptor(address)
	}
	for address, dest := range mapping {
		d := extension.RemapDescriptor(evaluation.Catalog.Get(address), mapping)
		state.SetDescriptor(dest, d)
		state.BindName(catalog.DescriptorNameKey(d), dest)
	}
	state.SetEpoch(state.Epoch() + 1)
	view := e.catalog.read(state)
	remap := func(ref ids.ObjectRef) ids.ObjectRef {
		ref.Epoch = state.Epoch()
		if dest, ok := mapping[ref.Address]; ok {
			ref.Address = dest
		}
		return ref
	}
	relocated, err := RemapExpression(node, remap, nil)
	if err != nil {
		t.Fatal(err)
	}
	relocated, _, err = RetagTypes(view, relocated, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal("retag must resolve selected remapped address", err)
	}
	current, err := exec.ExpressionRoutineDescriptor(relocated)
	if err != nil || current != state.Get(mapping[n.Ref.Address]) || !analyze.ValidInstalledUUIDOsspShape(relocated.(plan.InstalledUUIDOsspExpr)) {
		t.Fatal("remapped common and installed carriers diverged", err)
	}
	auth := catalogAuthorization(e, view)
	probe = evaluation
	probe.Catalog, probe.RoutineAuthority = view, &auth
	if err := exec.AdmitRoutineCall(&probe, relocated, true, false); err == nil {
		t.Fatal("retag manufactured original authority")
	}
	probe.RoutineChecks = slices.Clone(original)
	for i := range probe.RoutineChecks {
		probe.RoutineChecks[i].Ref = remap(probe.RoutineChecks[i].Ref)
	}
	if err := exec.AdmitRoutineCall(&probe, relocated, true, false); err != nil {
		t.Fatal("transferred original authority rejected", err)
	}
	foreignView := other.CatalogRead()
	foreignNode, err := RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef {
		ref = other.catalog.Domain.Ref(foreignView.Epoch(), ref.Address)
		return ref
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	foreignNode, _, err = RetagTypes(foreignView, foreignNode, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		typ, _ := foreignView.LookupType(d.ID())
		return typ
	})
	if err != nil {
		t.Fatal("foreign structural retag", err)
	}
	foreignAuth := catalogAuthorization(other, foreignView)
	probe = evaluation
	probe.RoutineAuthority = &foreignAuth
	if err := exec.AdmitRoutineCall(&probe, foreignNode, true, false); err == nil {
		t.Fatal("foreign same-OID retag acquired original authority")
	}
	if !reflect.DeepEqual(binder.Dependencies, original) || expr.InfoOf(node).RoutineReference != captured || n.Procedure != captured || n.Ref.Address != ext.Members[0] {
		t.Fatal("authority probes mutated original")
	}
	if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
		t.Fatal("source authority lost", err)
	}
}

func TestUUIDOsspRoutineDefaultACL(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	roleExecute(t, e, op, "CREATE ROLE uuid_acl_reader LOGIN")
	reader, readerDone := roleOperation(t, e, "uuid_acl_reader")
	t.Cleanup(readerDone)
	address := ext.Members[0]
	original := e.CatalogRead().MustProcedure(address)
	if original.ACL.Explicit || original.ACL.Entries != nil {
		t.Fatal("installed raw ACL must be NULL/default", original.ACL)
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_generate_v4"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sql      string
		explicit bool
		execute  bool
	}{
		{"", false, true},
		{"REVOKE EXECUTE ON FUNCTION uuid_generate_v4() FROM PUBLIC", true, false},
		{"GRANT EXECUTE ON FUNCTION uuid_generate_v4() TO uuid_acl_reader", true, true},
		{"REVOKE EXECUTE ON FUNCTION uuid_generate_v4() FROM uuid_acl_reader", true, false},
	} {
		if tc.sql != "" {
			roleExecute(t, e, op, tc.sql)
		}
		auth, err := operationAuthorization(reader)
		if err != nil {
			t.Fatal(err)
		}
		current := auth.Catalog.MustProcedure(address)
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), address)
		rights, err := auth.Effective(ref, false)
		if err != nil || current.ACL.Explicit != tc.explicit || (rights&authorization.ExecutePrivilege != 0) != tc.execute || !exec.SameRoutineDeclaration(current, original) {
			t.Fatal("default/explicit current EXECUTE", tc.sql, current.ACL, rights, err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies}}
		err = exec.AdmitRoutineCall(&evaluation, node, true, false)
		if tc.execute {
			if err != nil {
				t.Fatal("original carrier lost current grant", err)
			}
		} else if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "42501" {
			t.Fatal("original carrier bypassed current revoke", err)
		}
		if original.ACL.Explicit || original.ACL.Entries != nil {
			t.Fatal("COW changed original raw NULL ACL")
		}
	}
}
