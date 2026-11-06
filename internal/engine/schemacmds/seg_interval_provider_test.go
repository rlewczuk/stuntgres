package schemacmds_test

import (
	"context"
	"errors"
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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type segIntervalConflictStage struct {
	extension.Stage
	name     string
	operator bool
	owner    authorization.PrincipalRef
}

func (s segIntervalConflictStage) Create(d catalog.Descriptor) error {
	if err := s.Stage.Create(d); err != nil {
		return err
	}
	array, ok := d.(*catalog.Type)
	if !ok || !array.SQLType.IsArray() || !array.SQLType.Element().IsSeg() {
		return nil
	}
	oid, err := s.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := s.Revision()
	if err != nil {
		return err
	}
	typ := array.SQLType.Element()
	if s.operator {
		return s.Stage.Create(&catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: oid}, ObjectNamespace: array.Namespace(), ObjectRevision: revision, ObjectName: s.name, Left: ids.OID(typ.ID()), Right: ids.OID(typ.ID()), Owner: s.owner})
	}
	return s.Stage.Create(&catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: array.Namespace(), ObjectRevision: revision, ObjectName: s.name, Signature: extension.SegBinarySignature(typ.ID()), Owner: s.owner})
}

func segIntervalLateConflicts(t *testing.T) {
	t.Helper()
	for _, operator := range []bool{false, true} {
		for i, name := range []string{"seg_left", "seg_right", "seg_over_left", "seg_over_right", "seg_same", "seg_overlap", "seg_contains", "seg_contained"} {
			if operator {
				name = []string{"<<", ">>", "&<", "&>", "=", "&&", "@>", "<@"}[i]
			}
			t.Run("late_conflict_"+name, func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				before := e.Catalog.State
				e.Mu.Lock()
				err := func() error {
					auth, err := e.AuthorizationLocked(op)
					if err != nil {
						return err
					}
					stage := e.BeginCatalogStage(auth.Catalog)
					defer e.DiscardCatalogStage(stage)
					stage.AuthorizeLocked(auth, op.Session)
					oid, err := e.Catalog.AllocateOIDLocked()
					if err != nil {
						return err
					}
					revision, err := e.Catalog.RevisionLocked()
					if err != nil {
						return err
					}
					ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectRevision: revision, ObjectName: "seg", Version: "1.4", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: auth.State.CurrentUser}
					if err := stage.Create(ext); err != nil {
						return err
					}
					host := &schemacmds.ExtensionHost{Engine: e, Auth: auth, Stage: stage}
					return extension.StockSeg.Stage(segIntervalConflictStage{Stage: host, name: name, operator: operator, owner: ext.Owner}, ext, ext.Owner)
				}()
				e.Mu.Unlock()
				var diagnostic *sqlstate.Error
				code := "42723"
				if operator {
					code = "42710"
				}
				if !errors.As(err, &diagnostic) || diagnostic.Code != code || e.Catalog.State != before {
					t.Fatal("late conflict published prefix", name, err)
				}
			})
		}
	}
}

func segIntervalUnitCall(t *testing.T, e *core.Engine, ext *catalog.Extension, base *catalog.Type, index int) plan.InstalledSegIntervalExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	var args [2]expr.Expr
	for i, text := range []string{"1.0 .. 1.00", "1.0"} {
		value, err := base.SQLType.Input(t.Context(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = binder.Literal(value, parser.Position{})
	}
	bound, err := binder.BindSelectedOperatorRoutine(e.CatalogRead().MustProcedure(ext.Installation.Members[3+index]), args[:], parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	call, ok := bound.(plan.InstalledSegIntervalExpr)
	if !ok {
		t.Fatal("wrong carrier", bound)
	}
	return call
}

func TestSegIntervalProviderIdentity(t *testing.T) {
	e, _, ext, base := segProviderInstalled(t)
	view := e.CatalogRead()
	if len(ext.Installation.Members) != 19 || len(ext.Installation.Declarations) != 19 || len(ext.Members) != 20 {
		t.Fatal("selected scalar recipe/current member count")
	}
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if array.Origin != nil || array.Extension != ext.Address() || array.SQLType.Element() != base.SQLType || !(base.Address().ObjectID < ext.Installation.Members[1].ObjectID && ext.Installation.Members[1].ObjectID < ext.Installation.Members[2].ObjectID && ext.Installation.Members[2].ObjectID < array.Address().ObjectID) {
		t.Fatal("genuine type/I/O/array construction boundary")
	}
	for i, name := range []string{"seg_left", "seg_right", "seg_over_left", "seg_over_right", "seg_same", "seg_overlap", "seg_contains", "seg_contained"} {
		proc := view.MustProcedure(ext.Installation.Members[3+i])
		op := view.MustOperator(ext.Installation.Members[11+i])
		if proc.Address().ObjectID <= array.Address().ObjectID || op.Address().ObjectID <= array.Address().ObjectID || proc.Origin == nil || proc.Origin.Installation != ext.Installation || proc.Origin.Declaration != 3+i || op.Origin == nil || op.Origin.Installation != ext.Installation || op.Origin.Declaration != 11+i {
			t.Fatal("selected allocation order or logical origin changed", i, proc, op)
		}
		if proc.Name() != name || proc.Signature != extension.SegBinarySignature(base.SQLType.ID()) || proc.Result != 16 || proc.Owner != ext.Owner || op.Function != proc.Address().ObjectID || op.Left != ids.OID(base.SQLType.ID()) || op.Right != op.Left || op.Result != 16 || op.Negator != 0 || op.Hash || op.Merge != (i == 4) {
			t.Fatal("selected role", i, proc, op)
		}
		if err := (routine.OperatorProvider{}).Validate(view, op); err != nil {
			t.Fatal(err)
		}
		call := segIntervalUnitCall(t, e, ext, base, i)
		if call.Procedure != proc || call.Ref.Address != proc.Address() || call.CallRoutineReference() != proc {
			t.Fatal("captured identity")
		}
		for _, rewrite := range []func() (expr.Expr, error){func() (expr.Expr, error) { return call.Rebuild(expr.InfoOf(call), call.Children()) }, func() (expr.Expr, error) { return call.Substitute(expr.InfoOf(call), call.Children()) }} {
			node, err := rewrite()
			retained, ok := node.(plan.InstalledSegIntervalExpr)
			if err != nil || !ok || retained.Ref != call.Ref || retained.Procedure != proc || retained.Plan != call.Plan || retained.Args != call.Args || expr.InfoOf(retained) != expr.InfoOf(call) {
				t.Fatal("rebuild lost authority", err)
			}
		}
		value, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: view}})
		got, conversion := value.Boolean()
		want := [8]bool{false, false, true, true, false, true, true, true}[i]
		if err != nil || conversion != nil || got != want {
			t.Fatal("metadata-sensitive evaluation", i, got, err, conversion)
		}
		// A checked kernel cannot be paired with another genuine routine.
		forged := call
		forged.Procedure = view.MustProcedure(ext.Installation.Members[3+(i+1)%8])
		forged.Ref.Address = forged.Procedure.Address()
		forged.RoutineReference = forged.Procedure
		if _, err := forged.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: view}}); err == nil {
			t.Fatal("wrong role pairing admitted")
		}
	}
	for _, mutate := range []func(*catalog.Procedure, *catalog.Operator){
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Signature = "23,23" },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Result = 25 },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.ObjectName = "seg_in" },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Display.Source = "seg_in" },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Implementation = &extension.SegProvider{} },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Owner = authorization.PrincipalRef{} },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Origin = nil },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Function = ext.Installation.Members[3].ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Commutator = ext.Installation.Members[11].ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Negator = 1 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Right = 23 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Merge = true },
	} {
		next := catalog.CloneState(view.State())
		p := *view.MustProcedure(ext.Installation.Members[5])
		o := *view.MustOperator(ext.Installation.Members[13])
		mutate(&p, &o)
		next.SetDescriptor(p.Address(), &p)
		next.SetDescriptor(o.Address(), &o)
		if err := extension.StockSeg.ValidateExtension(e.Catalog.Read(next), ext); err == nil {
			t.Fatal("corrupt role accepted")
		}
		if err := extension.StockSeg.ValidateExtension(view, ext); err != nil {
			t.Fatal("original authority mutated", err)
		}
	}
}

func TestSegIntervalProviderPublication(t *testing.T) {
	segIntervalLateConflicts(t)
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead().State()
	stage := segProviderStage(t, e, op)
	view := e.StageRead(stage)
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"}))
	for _, address := range []ids.ObjectAddress{ext.Installation.Members[10], ext.Installation.Members[18]} {
		var duplicate catalog.Descriptor
		switch original := view.Get(address).(type) {
		case *catalog.Procedure:
			n := *original
			n.ObjectAddress.ObjectID = 900000
			duplicate = &n
		case *catalog.Operator:
			n := *original
			n.ObjectAddress.ObjectID = 900001
			duplicate = &n
		}
		var conflict catalog.Conflict
		if err := stage.Create(duplicate); !errors.As(err, &conflict) {
			t.Fatal("late staged conflict", err)
		}
		if e.CatalogRead().State() != before {
			t.Fatal("conflict published prefix")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication", err)
	}
	view.MustProcedure(ext.Installation.Members[10]).Display.Source = "seg_in"
	if _, err := e.ValidateCatalogStage(t.Context(), stage); err == nil {
		t.Fatal("late corrupt manifest admitted")
	}
	if e.CatalogRead().State() != before {
		t.Fatal("partial publication")
	}
}

func TestSegIntervalStoredIdentity(t *testing.T) {
	e, _, ext, base := segProviderInstalled(t)
	before := e.CatalogRead()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, "SELECT '1.0 .. 1.00'::seg = '1.0'::seg").(*parser.Select).Targets[0]
	bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	selected, ok := bound.(plan.SelectedOperatorExpr)
	if !ok {
		t.Fatal("missing authentic selected operator", bound)
	}
	call, ok := selected.Provider.(plan.InstalledSegIntervalExpr)
	if !ok {
		t.Fatal("missing genuine seg carrier", selected.Provider)
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.Members {
		mapping[address] = ids.ObjectAddress{ClassID: address.ClassID, ObjectID: ids.OID(900010 + i)}
	}
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)}
	newType, err := sqltype.Seg(sqltype.ID(mapping[base.Address()].ObjectID), "seg", "public.seg")
	if err != nil {
		t.Fatal(err)
	}
	array := before.MustType(arrayAddress)
	newArray := sqltype.LookupArray(sqltype.ID(mapping[arrayAddress].ObjectID), array.Name(), newType)
	types := map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: newType, array.SQLType: newArray}
	next := catalog.CloneState(before.State())
	for source := range mapping {
		next.UnbindName(catalog.DescriptorNameKey(before.Get(source)))
		next.RemoveDescriptor(source)
	}
	for source, destination := range mapping {
		descriptor := extension.RemapDescriptor(before.Get(source), mapping)
		if typ, ok := descriptor.(*catalog.Type); ok {
			n := *typ
			n.SQLType = types[typ.SQLType]
			if source == base.Address() {
				n.Array = newArray.ID()
			}
			descriptor = &n
		}
		next.SetDescriptor(destination, descriptor)
		next.BindName(catalog.DescriptorNameKey(descriptor), destination)
	}
	next.SetEpoch(next.Epoch() + 1)
	next.SetScalarImports(types)
	view := e.Catalog.Read(next)
	if err := extension.StockSeg.ValidateExtension(view, next.MustExtension(mapping[ext.Address()])); err != nil {
		t.Fatal(err)
	}
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) {
			return old
		}
		if destination, ok := mapping[old.Address]; ok {
			old.Address = destination
		}
		old.Epoch = next.Epoch()
		return old
	}
	typeMapper := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if target := types[old]; target != nil {
			return target
		}
		return old
	}
	procedure := extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), nil, nil)
	mapper := stored.ViewQueryRemapper{Catalog: view, Ref: ref, Type: typeMapper, Procedure: procedure}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "same", Usage: call.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{selected}}
	remapped, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	transported, ok := remapped.(*plan.ProjectNode).Expressions[0].(plan.SelectedOperatorExpr)
	if !ok || transported.Ref.Address != mapping[selected.Ref.Address] || transported.Operator.Address() != mapping[selected.Operator.Address()] || transported.Operator.Function != mapping[call.Ref.Address].ObjectID || transported.SourcePosition() != selected.SourcePosition() || transported.Properties() != selected.Properties() {
		t.Fatal("selected operator transport", transported)
	}
	actual, ok := transported.Provider.(plan.InstalledSegIntervalExpr)
	if !ok || actual.Ref.Address != mapping[call.Ref.Address] || !exec.SameRoutineDeclaration(next.MustProcedure(actual.Ref.Address), actual.Procedure) || actual.Procedure.Revision() != call.Procedure.Revision() || actual.Args[0].ResultUsage().Type != newType || actual.Plan.Operation() != sqltype.SegSame || actual.Properties() != call.Properties() {
		t.Fatal("remapped original authority", actual)
	}
	value, err := actual.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: view}})
	got, conversion := value.Boolean()
	if err != nil || conversion != nil || got {
		t.Fatal("hidden precision lost in remap", got, err, conversion)
	}
	if call.Procedure != before.Get(call.Ref.Address) || call.Args[0].ResultUsage().Type != base.SQLType {
		t.Fatal("source carrier mutated")
	}
	bad := actual
	bad.Ref = call.Ref
	if _, err := bad.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: view}}); err == nil {
		t.Fatal("stale reference admitted")
	}
	capturedProcedure, capturedRef := actual.Procedure, actual.Ref
	capturedRoutine, capturedRevision := actual.CallRoutineReference(), actual.Procedure.Revision()
	// A presentation revision retains the same executable declaration, but
	// validation must never graft its current descriptor onto the old carrier.
	current := *next.MustProcedure(actual.Ref.Address)
	current.ObjectRevision = current.Revision() + 1
	next.SetDescriptor(current.Address(), &current)
	if current.Revision() == actual.Procedure.Revision() {
		t.Fatal("routine revision probe did not advance its effective stamp")
	}
	if err := stored.ValidateInstalledSegInterval(actual, e.Catalog.Read(next)); err != nil {
		t.Fatal("presentation-only revision invalidated unchanged declaration", err)
	}
	value, err = actual.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: e.Catalog.Read(next)}})
	got, conversion = value.Boolean()
	if err != nil || conversion != nil || got {
		t.Fatal("presentation revision changed retained hidden-precision result", got, err, conversion)
	}
	semantic := current
	semantic.ObjectRevision = current.Revision() + 1
	semantic.Volatility = expr.Volatile
	next.SetDescriptor(semantic.Address(), &semantic)
	if err := stored.ValidateInstalledSegInterval(actual, e.Catalog.Read(next)); err == nil {
		t.Fatal("changed routine semantics resurrected captured call")
	}
	if _, err := actual.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: e.Catalog.Read(next)}}); err == nil {
		t.Fatal("changed routine semantics executed captured call")
	}
	if actual.Procedure != capturedProcedure || actual.Ref != capturedRef || actual.CallRoutineReference() != capturedRoutine || actual.Procedure.Revision() != capturedRevision || actual.Procedure == &current || actual.Procedure == &semantic || call.Procedure != before.Get(call.Ref.Address) {
		t.Fatal("validation rewrote authority")
	}
}
