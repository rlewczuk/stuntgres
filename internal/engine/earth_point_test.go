package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestEarthPointProvider(t *testing.T) {
	e, ext, _ := earthUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.MustProcedure(ext.Members[extension.EarthPointProcedureSlot])
	op := view.MustOperator(ext.Members[extension.EarthPointOperatorSlot])
	if proc.Name() != "geo_distance" || proc.Signature != "600,600" || proc.Result != 701 || !proc.Strict || proc.Volatility != expr.Immutable || op.Name() != "<@>" || op.Left != 600 || op.Right != 600 || op.Result != 701 || op.Function != proc.Address().ObjectID || op.Commutator != op.Address().ObjectID {
		t.Fatal("point member declaration", proc, op)
	}
	if err := (routine.OperatorProvider{}).Validate(view, op); err != nil {
		t.Fatal(err)
	}
	if err := (extension.CatalogProvider{Class: ids.OperatorClassID}).Validate(view, op); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"earth", "sec_to_gc", "gc_to_sec", "ll_to_earth", "latitude", "longitude"} {
		if symbol, err := extension.ProcedureSymbol(view, view.MustProcedure(ext.Members[i+2])); err != nil || symbol != name {
			t.Fatal("incumbent slot", i, symbol, err)
		}
	}
	foreignOwner := New().Catalog.Owner.Principals.Principal(proc.Owner.ID())
	for _, corrupt := range []func(*catalog.Procedure, *catalog.Operator){
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Signature = "701,701" },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Result = 16 },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Strict = false },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Implementation = extension.StockCube },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Extension = ids.ObjectAddress{} },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Owner = foreignOwner },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Owner = foreignOwner },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Function = ext.Members[2].ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Commutator = 0 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Right = 701 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Result = 16 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Extension = ids.ObjectAddress{} },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Restriction = 139 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Join = 140 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Negator = o.Address().ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Hash = true },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Merge = true },
	} {
		next := catalog.CloneState(view.State())
		p, o := *proc, *op
		corrupt(&p, &o)
		next.SetDescriptor(p.Address(), &p)
		next.SetDescriptor(o.Address(), &o)
		if err := extension.StockEarthdistance.ValidateExtension(view.WithState(next), ext); err == nil {
			t.Fatal("corrupt point manifest admitted")
		}
		if err := (extension.CatalogProvider{Class: ids.OperatorClassID}).Validate(view.WithState(next), &o); err == nil {
			t.Fatal("corrupt operator provider admitted")
		}
	}
}

func TestEarthPointLateOperatorConflict(t *testing.T) {
	e, _, _ := cubeUnitInstalled(t)
	installer, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	revision, err := e.Catalog.RevisionLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	conflicting := &catalog.Operator{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: oid},
		ObjectNamespace: namespace, ObjectRevision: revision, ObjectName: "<@>",
		Left: 600, Right: 600, Result: 701, Function: 991, Commutator: oid,
		Owner: installer.RoleState().CurrentUser,
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Create(conflicting); err != nil {
		t.Fatal(err)
	}
	if err := (routine.OperatorProvider{}).Validate(e.StageRead(stage), conflicting); err != nil {
		t.Fatal("genuine loose POINT operator", err)
	}
	extensionUnitPublish(t, e, installer, stage)
	before := e.CatalogRead()
	original := before.MustOperator(conflicting.Address())
	backing := before.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 991})
	if backing.Name() != "point_distance" || backing.Signature != "600,600" || backing.Result != 701 {
		t.Fatal("genuine existing backing routine", backing)
	}
	originalValue := *original
	e.Mu.Lock()
	failed, err := stageExtensionLocked(e, &StatementScope{Op: installer}, &parser.CreateExtension{Name: parser.Identifier{Text: "earthdistance"}})
	e.Mu.Unlock()
	if failed != nil {
		e.DiscardCatalogStage(failed)
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Message != "operator <@> already exists" || failed != nil {
		t.Fatal("actual provider late operator collision", failed, err)
	}
	after := e.CatalogRead()
	if after.State() != before.State() || after.MustOperator(original.Address()) != original || *original != originalValue || after.MustProcedure(backing.Address()) != backing {
		t.Fatal("late collision changed committed root or original conflict authority")
	}
	if after.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "earthdistance"}) != (ids.ObjectAddress{}) {
		t.Fatal("failed installation published extension")
	}
	for _, name := range []string{"earth", "_earth"} {
		if after.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: name}) != (ids.ObjectAddress{}) {
			t.Fatal("failed installation published type prefix", name)
		}
	}
	for _, name := range []string{"earth", "sec_to_gc", "gc_to_sec", "ll_to_earth", "latitude", "longitude", "geo_distance"} {
		if len(after.State().ProceduresNamed(namespace, name)) != 0 {
			t.Fatal("failed installation published procedure prefix", name)
		}
	}
	if after.NameAddress(catalog.DescriptorNameKey(original)) != original.Address() {
		t.Fatal("failed installation displaced original POINT operator")
	}
}

func TestEarthPointOwners(t *testing.T) {
	e, ext, _ := earthUnitInstalled(t)
	view := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	metadata, err := extension.OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !found {
		t.Fatal("owner codec", err)
	}
	for slot, kind := range map[int]string{extension.EarthPointProcedureSlot: "procedure", extension.EarthPointOperatorSlot: "operator"} {
		member := record.Members[slot]
		if member.Kind != kind || member.Signature != "600,600" {
			t.Fatal("point owner signature", member)
		}
	}
	encode := func(r extension.OwnerRecord) string {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
	}
	for _, mutate := range []func(*extension.OwnerRecord){
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointProcedureSlot].Signature = "earth,earth" },
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointProcedureSlot].Kind = "operator" },
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointOperatorSlot].Signature = "cube,cube" },
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointOperatorSlot].Kind = "procedure" },
		func(r *extension.OwnerRecord) {
			r.Members[extension.EarthPointOperatorSlot].Slot = extension.EarthPointProcedureSlot
		},
	} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		mutate(&bad)
		if _, _, err := extension.DecodeOwners(encode(bad)); err == nil {
			t.Fatal("forged point owner record admitted")
		}
		candidate := catalog.CloneState(view.State())
		if _, err := extension.ImportOwners(context.Background(), view.WithState(candidate), roles, []extension.OwnerRecord{bad}); err == nil {
			t.Fatal("direct import bypassed point owner roles")
		}
	}
	for _, slot := range []int{extension.EarthPointProcedureSlot, extension.EarthPointOperatorSlot} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		bad.Members[slot].Name = "foreign_label"
		candidate := catalog.CloneState(view.State())
		if _, err := extension.ImportOwners(context.Background(), view.WithState(candidate), roles, []extension.OwnerRecord{bad}); err == nil {
			t.Fatal("foreign slot declaration imported")
		}
	}
	candidate := catalog.CloneState(view.State())
	if _, err := extension.ImportOwners(context.Background(), view.WithState(candidate), roles, []extension.OwnerRecord{record}); err != nil {
		t.Fatal("genuine point owners", err)
	}
}

func earthPointUnitCall(t *testing.T, e *Engine, ext *catalog.Extension, nullable bool) plan.InstalledEarthPointExpr {
	t.Helper()
	view := e.CatalogRead()
	point, _ := view.LookupType(sqltype.Point)
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	var args [2]expr.Expr
	for i, text := range []string{"(0,60)", "(1,60)"} {
		v, err := point.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		if nullable && i == 0 {
			v = sqltype.Null(point)
		}
		args[i] = binder.Literal(v, parser.Position{})
	}
	bound, err := binder.BindSelectedOperatorRoutine(view.MustProcedure(ext.Members[extension.EarthPointProcedureSlot]), args[:], parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	call, ok := bound.(plan.InstalledEarthPointExpr)
	if !ok {
		t.Fatalf("installed point carrier %T", bound)
	}
	return call
}

func TestEarthPointExpression(t *testing.T) {
	ctx := context.Background()
	source, ext, _ := earthUnitInstalled(t)
	before := source.CatalogRead()
	target := New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	defer closeOwner()
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
		t.Fatal(err)
	}
	after := target.CatalogRead()
	imported := after.MustExtension(after.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "earthdistance"}))
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): imported.Address()}
	for i, a := range ext.Members {
		mapping[a] = imported.Members[i]
	}
	mapRef := func(r ids.ObjectRef) ids.ObjectRef {
		a := r.Address
		if next, ok := mapping[a]; ok {
			a = next
		}
		return target.Catalog.Domain.Ref(after.Epoch(), a)
	}
	mapType := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		n, _ := after.LookupType(d.ID())
		return n
	}
	mapperProc := extension.ProcedureRemapper(mapping, replacementProcedurePolicyFixture(source, target), nil)
	for _, nullable := range []bool{false, true} {
		call := earthPointUnitCall(t, source, ext, nullable)
		proc := call.Procedure
		if call.Ref.Address != proc.Address() || call.CallRoutineReference() != proc {
			t.Fatal("callable capture")
		}
		for _, rewrite := range []func() (expr.Expr, error){func() (expr.Expr, error) { return call.Rebuild(expr.InfoOf(call), call.Children()) }, func() (expr.Expr, error) { return call.Substitute(expr.InfoOf(call), call.Children()) }} {
			n, err := rewrite()
			retained, ok := n.(plan.InstalledEarthPointExpr)
			if err != nil || !ok || retained.Ref != call.Ref || retained.Procedure != proc || !analyze.SameGroupExpression(call, retained) {
				t.Fatal("reconstruction shed point authority", err)
			}
		}
		if err := stored.ValidateInstalledEarthPoint(call, before); err != nil {
			t.Fatal(err)
		}
		text, err := (stored.ScalarRenderer{}).SQL(ctx, before, call)
		if err != nil || !strings.Contains(text, "geo_distance") {
			t.Fatal("point rendering", text, err)
		}
		v, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: before}})
		if err != nil || v.IsNull() != nullable {
			t.Fatal("point evaluation", v, err)
		}
		if !nullable {
			if got, err := v.Float(); err != nil || math.Abs(got-34.54626) > 0.000005 {
				t.Fatal("mile value", got, err)
			}
		}
		bad := call
		copy := *proc
		bad.Procedure = &copy
		if err := stored.ValidateInstalledEarthPoint(bad, before); err == nil {
			t.Fatal("counterfeit carrier admitted")
		}
		bad = call
		bad.Ref.Address = ext.Address()
		if err := stored.ValidateInstalledEarthPoint(bad, before); err == nil {
			t.Fatal("foreign reference admitted")
		}
		for _, fresh := range sqltype.Builtins() {
			if fresh.ID() != sqltype.Point && fresh.ID() != sqltype.Float8 {
				continue
			}
			bad = call
			if fresh.ID() == sqltype.Float8 {
				bad.Usage = fresh.Usage()
			} else {
				bad.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: fresh.Usage()}, Value: sqltype.Null(fresh)}
			}
			if err := stored.ValidateInstalledEarthPoint(bad, before); err == nil {
				t.Fatal("same-OID foreign generation admitted before NULL")
			}
			if _, err := bad.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: before}}); err == nil {
				t.Fatal("foreign generation executed before NULL")
			}
		}
		var foreignPoint, foreignFloat *sqltype.Descriptor
		for _, fresh := range sqltype.Builtins() {
			switch fresh.ID() {
			case sqltype.Point:
				foreignPoint = fresh
			case sqltype.Float8:
				foreignFloat = fresh
			}
		}
		foreignInfo := expr.InfoOf(call)
		foreignInfo.Usage = foreignFloat.Usage()
		foreignArg := plan.LiteralExpr{Info: expr.Info{Usage: foreignPoint.Usage()}, Value: sqltype.Null(foreignPoint)}
		foreignKernel, err := functions.NewEarthPoint(foreignInfo, [2]expr.Expr{foreignArg, foreignArg})
		if err != nil {
			t.Fatal("coherent foreign test generation", err)
		}
		bad = call
		bad.EarthPoint = foreignKernel
		if err := stored.ValidateInstalledEarthPoint(bad, before); err == nil {
			t.Fatal("coherent foreign NULL kernel admitted")
		}
		if _, err := bad.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: before}}); err == nil {
			t.Fatal("coherent foreign NULL kernel executed")
		}
		op := before.MustOperator(ext.Members[extension.EarthPointOperatorSlot])
		info := expr.InfoOf(call)
		info.RoutineReference = nil
		selected := plan.SelectedOperatorExpr{Info: info, Operator: op, Ref: source.Catalog.Domain.Ref(before.Epoch(), op.Address()), Provider: call}
		query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "miles", Usage: call.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{selected}}
		mapper := stored.ViewQueryRemapper{Catalog: after, Ref: mapRef, Type: mapType, Procedure: mapperProc}
		remapped, err := mapper.Query(query)
		if err != nil {
			t.Fatal("point operator remap", err)
		}
		actual := remapped.(*plan.ProjectNode).Expressions[0].(plan.SelectedOperatorExpr)
		n := actual.Provider.(plan.InstalledEarthPointExpr)
		if actual.Ref != mapRef(selected.Ref) || actual.Operator.Address() != mapping[op.Address()] || n.Ref != mapRef(call.Ref) || !exec.SameRoutineDeclaration(after.MustProcedure(n.Ref.Address), n.Procedure) {
			t.Fatal("remap shed operator/callable identity")
		}
		if err := stored.ValidateInstalledEarthPoint(n, after); err != nil {
			t.Fatal(err)
		}
		if _, _, err := stored.RetagTypes(after, n, mapType); err != nil {
			t.Fatal("destination point retag", err)
		}
		stale := n
		stale.Procedure = proc
		if err := stored.ValidateInstalledEarthPoint(stale, after); err == nil {
			t.Fatal("source procedure admitted after remap")
		}
		if call.Procedure != proc || selected.Operator != op {
			t.Fatal("remap mutated source")
		}
	}
}
