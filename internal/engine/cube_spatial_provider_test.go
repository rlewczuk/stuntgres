package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func cubeSpatialUnitArguments(t *testing.T, e *Engine, base *catalog.Type) [2]expr.Expr {
	t.Helper()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	var args [2]expr.Expr
	for i, text := range []string{"(0,0),(1,1)", "(4,5),(6,7)"} {
		value, err := base.SQLType.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = binder.Literal(value, parser.Position{})
	}
	return args
}

// Spatial procedures precede the operator tail after the constructor prefix.
func cubeSpatialUnitMembers(ext *catalog.Extension) ([]ids.ObjectAddress, []ids.ObjectAddress) {
	firstOperator := len(ext.Members)
	for firstOperator > 0 && ext.Members[firstOperator-1].ClassID == ids.OperatorClassID {
		firstOperator--
	}
	count := len(ext.Members) - firstOperator
	return ext.Members[firstOperator-count : firstOperator], ext.Members[firstOperator:]
}

func TestCubeSpatialProviderIdentity(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	view := e.CatalogRead()
	procedures, operators := cubeSpatialUnitMembers(ext)
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := cubeSpatialUnitArguments(t, e, base)
	for i, spec := range []struct {
		name, operator string
		result         sqltype.ID
		want           any
	}{
		{"cube_overlap", "&&", sqltype.Bool, false},
		{"cube_contains", "@>", sqltype.Bool, false},
		{"cube_contained", "<@", sqltype.Bool, false},
		{"cube_distance", "<->", sqltype.Float8, float64(5)},
		{"distance_taxicab", "<#>", sqltype.Float8, float64(7)},
		{"distance_chebyshev", "<=>", sqltype.Float8, float64(4)},
	} {
		proc := view.MustProcedure(procedures[i])
		op := view.MustOperator(operators[i])
		if proc.Name() != spec.name || proc.Signature != extension.CubeBinarySignature(base.SQLType.ID()) || proc.Result != ids.OID(spec.result) || op.Name() != spec.operator || op.Function != proc.Address().ObjectID || op.Left != ids.OID(base.SQLType.ID()) || op.Right != op.Left || op.Result != proc.Result || op.Extension != ext.Address() {
			t.Fatal("selected authentic signature", proc, op)
		}
		if err := (routine.OperatorProvider{}).Validate(view, op); err != nil {
			t.Fatal(err)
		}
		bound, err := binder.BindSelectedOperatorRoutine(proc, args[:], parser.Position{})
		if err != nil {
			t.Fatal(err)
		}
		call, ok := bound.(plan.InstalledCubeSpatialExpr)
		if !ok || call.Procedure != proc || call.Ref.Address != proc.Address() || call.CallRoutineReference() != proc {
			t.Fatal("selected provider identity", bound)
		}
		for _, rewrite := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return call.Rebuild(expr.InfoOf(call), call.Children()) },
			func() (expr.Expr, error) { return call.Substitute(expr.InfoOf(call), call.Children()) },
		} {
			node, err := rewrite()
			retained, ok := node.(plan.InstalledCubeSpatialExpr)
			if err != nil || !ok || retained.Ref != call.Ref || retained.Procedure != proc {
				t.Fatal("wrapper lost on reconstruction", node, err)
			}
		}
		v, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}})
		if err != nil {
			t.Fatal(err)
		}
		if spec.result == sqltype.Bool {
			got, err := v.Boolean()
			if err != nil || got != spec.want.(bool) {
				t.Fatal(got, err)
			}
		} else {
			got, err := v.Float()
			if err != nil || got != spec.want.(float64) {
				t.Fatal(got, err)
			}
		}
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !found {
		t.Fatal("owner metadata", err)
	}
	seen := map[string]bool{}
	for _, member := range record.Members {
		_, spatial := sqltype.CubeSpatialKind(member.Name)
		if member.Kind == "operator" && member.Name != "->" && member.Name != "~>" || member.Kind == "procedure" && spatial {
			if member.Signature != "cube,cube" {
				t.Fatal("allocator identity leaked", member)
			}
			seen[member.Kind+":"+member.Name] = true
		}
	}
	for _, name := range []string{"procedure:cube_overlap", "procedure:cube_contains", "procedure:cube_contained", "procedure:cube_distance", "procedure:distance_taxicab", "procedure:distance_chebyshev", "operator:&&", "operator:@>", "operator:<@", "operator:<->", "operator:<#>", "operator:<=>"} {
		if !seen[name] {
			t.Fatal("missing symbolic member", name)
		}
	}
}

func TestCubeSpatialProviderConflicts(t *testing.T) {
	t.Run("authentic_staged_name_conflicts", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		before := e.Catalog.State
		stage := cubeUnitStage(t, e, op, "cube", "")
		candidate := stage.State()
		ext := candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
		procedures, operators := cubeSpatialUnitMembers(ext)
		for _, address := range []ids.ObjectAddress{procedures[0], operators[0]} {
			var duplicate catalog.Descriptor
			switch original := candidate.Get(address).(type) {
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
				t.Fatalf("staged spatial conflict %v", err)
			}
			if stage.Get(address) != candidate.Get(address) || e.Catalog.State != before {
				t.Fatal("staged conflict changed authority")
			}
		}
	})
	e, ext, _ := cubeUnitInstalled(t)
	procedures, operators := cubeSpatialUnitMembers(ext)
	for _, corrupt := range []func(*catalog.Procedure, *catalog.Operator){
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Signature = "23,23" },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Implementation = nil },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Result = 16 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Function = procedures[0].ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Commutator = operators[0].ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Extension = ids.ObjectAddress{} },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Right = 23 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Hash = true },
	} {
		next := catalog.CloneState(e.Catalog.State)
		p := *next.MustProcedure(procedures[3])
		o := *next.MustOperator(operators[3])
		corrupt(&p, &o)
		next.SetDescriptor(p.Address(), &p)
		next.SetDescriptor(o.Address(), &o)
		if err := extension.StockCube.ValidateExtension(e.Catalog.Read(next), ext); err == nil {
			t.Fatal("corrupt spatial manifest admitted")
		}
		if err := extension.StockCube.ValidateExtension(e.CatalogRead(), ext); err != nil {
			t.Fatal("negative validation mutated original", err)
		}
	}
}

func TestCubeSpatialProviderControls(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	before := e.CatalogRead()
	procedures, operators := cubeSpatialUnitMembers(ext)
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := cubeSpatialUnitArguments(t, e, base)
	bound, err := binder.BindSelectedOperatorRoutine(before.MustProcedure(procedures[3]), args[:], parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(plan.InstalledCubeSpatialExpr)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.Members {
		mapping[address] = ids.ObjectAddress{ClassID: address.ClassID, ObjectID: ids.OID(900010 + i)}
	}
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)}
	mapping[arrayAddress] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 900100}
	newType, err := sqltype.Cube(sqltype.ID(mapping[base.Address()].ObjectID), "cube", "public.cube")
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
	// The combined installation also owns internal B-tree strategy/support rows.
	// Keep their row identities while remapping every reference with the manifest.
	for address, descriptor := range before.Descriptors() {
		switch row := descriptor.(type) {
		case *catalog.OperatorFamilyOperator:
			if row.InternalOwner == ext.Members[17] {
				mapped := catalog.RemapOperatorFamily(row, mapping)
				next.UnbindName(catalog.DescriptorNameKey(row))
				next.SetDescriptor(address, mapped)
				next.BindName(catalog.DescriptorNameKey(mapped), address)
			}
		case *catalog.OperatorFamilySupport:
			if row.InternalOwner == ext.Members[17] {
				mapped := catalog.RemapOperatorFamily(row, mapping)
				next.UnbindName(catalog.DescriptorNameKey(row))
				next.SetDescriptor(address, mapped)
				next.BindName(catalog.DescriptorNameKey(mapped), address)
			}
		}
	}
	next.SetEpoch(next.Epoch() + 1)
	next.SetScalarImports(types)
	view := e.Catalog.Read(next)
	family := mapping[ext.Members[16]].ObjectID
	class := mapping[ext.Members[17]].ObjectID
	for strategy := int16(1); strategy <= 5; strategy++ {
		row, ok := view.FamilyOperator(family, ids.OID(newType.ID()), ids.OID(newType.ID()), strategy, 's')
		if !ok || row.Family != family || row.Left != ids.OID(newType.ID()) || row.Right != row.Left || row.InternalOwner != (ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: class}) {
			t.Fatal("missing mapped strategy lookup or class ownership", strategy, row)
		}
	}
	if row, ok := view.FamilySupport(family, ids.OID(newType.ID()), ids.OID(newType.ID()), 1); !ok || row.Family != family || row.Left != ids.OID(newType.ID()) || row.Right != row.Left || row.InternalOwner != (ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: class}) {
		t.Fatal("missing mapped support lookup or class ownership", row)
	}
	if err := extension.StockCube.ValidateExtension(view, next.MustExtension(mapping[ext.Address()])); err != nil {
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
	procedure := extension.ProcedureRemapper(mapping, nil, nil)
	mapper := stored.ViewQueryRemapper{Catalog: view, Ref: ref, Type: typeMapper, Procedure: procedure}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "distance", Usage: call.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{call}}
	remapped, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	mapped := remapped.(*plan.ProjectNode).Expressions[0]
	actual, ok := mapped.(plan.InstalledCubeSpatialExpr)
	if !ok || actual.Ref.Address != mapping[call.Ref.Address] || !exec.SameRoutineDeclaration(next.MustProcedure(actual.Ref.Address), actual.Procedure) || actual.Procedure.Revision() != call.Procedure.Revision() || actual.Args[0].ResultUsage().Type != newType {
		t.Fatal("remapped expression authority", mapped)
	}
	v, err := actual.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := v.Float(); err != nil || n != 5 {
		t.Fatal("remapped distance", n, err)
	}
	if call.Procedure != before.Get(call.Ref.Address) || call.Args[0].ResultUsage().Type != base.SQLType {
		t.Fatal("source expression mutated")
	}
	bad := actual
	bad.Ref = call.Ref
	if _, err := bad.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}); err == nil {
		t.Fatal("stale installed reference executed")
	}
	t.Run("cached_projection_identity", func(t *testing.T) {
		for _, mode := range []string{"order_key", "order_output", "bounded_output", "distinct_on"} {
			t.Run(mode, func(t *testing.T) {
				source := &plan.ValuesNode{Rows: [][]expr.Expr{{}}}
				projection := []expr.Expr{call}
				var producer plan.Node
				switch mode {
				case "order_key":
					producer, err = binder.FinishOrder(source, []plan.SortKey{{Expression: call, OutputOrdinal: 1, Cache: -1}}, projection)
				case "order_output":
					producer = binder.ScheduleQueryProjection(&plan.SortNode{Child: source}, projection, false)
				case "bounded_output":
					producer = binder.ScheduleQueryProjection(source, projection, true)
				case "distinct_on":
					grouping, failure := sqltype.BindGrouping(call.ResultUsage())
					if failure != nil {
						t.Fatal(failure)
					}
					producer, err = binder.FinishDistinctOn(source, nil, []plan.SortKey{{Expression: call, TargetIdentity: 1}}, []plan.Grouping{{Grouping: grouping}}, projection)
				}
				if err != nil {
					t.Fatal(err)
				}
				cached, ok := projection[0].(plan.CachedOrderExpr)
				if !ok || cached.CallRoutineReference() != nil || cached.CallRoutineRecipeReference() != nil || cached.ResultUsage() != call.ResultUsage() || cached.SourcePosition() != call.SourcePosition() {
					t.Fatal("cache slot acquired producer identity or lost value metadata", projection[0])
				}
				query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "distance", Usage: call.ResultUsage()}}, Child: producer, Expressions: projection}
				mapper := stored.ViewQueryRemapper{Catalog: view, Ref: ref, Type: typeMapper, Procedure: procedure}
				reowned, failure := mapper.Query(query)
				if failure != nil {
					t.Fatal("remint cached projection and its owning call", failure)
				}
				plan.VisitCTEQuery(reowned, func(plan.Node) {}, func(node expr.Expr) {
					plan.WalkExpression(node, func(node expr.Expr) {
						if cache, ok := node.(plan.CachedOrderExpr); ok && (cache.CallRoutineReference() != nil || cache.CallRoutineRecipeReference() != nil) {
							t.Fatal("remapped cache slot owns a callable", cache)
						}
						if owned, ok := node.(plan.InstalledCubeSpatialExpr); ok {
							if owned.Ref.Address != mapping[call.Ref.Address] || !exec.SameRoutineDeclaration(next.MustProcedure(owned.Ref.Address), owned.Procedure) || owned.Procedure.Revision() != call.Procedure.Revision() || owned.Args[0].ResultUsage().Type != newType {
								t.Fatal("producer lost reminted callable authority", owned)
							}
							value, failure := owned.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}})
							if failure != nil {
								t.Fatal(failure)
							}
							distance, failure := value.Float()
							if failure != nil || distance != 5 {
								t.Fatal(distance, failure)
							}
						}
					})
				}, true)
				if call.Procedure != before.Get(call.Ref.Address) || call.Args[0].ResultUsage().Type != base.SQLType {
					t.Fatal("remap changed its source expression")
				}
			})
		}
	})
	t.Run("one_step_operator_collision_chain", func(t *testing.T) {
		op := before.MustOperator(operators[0])
		original := *op
		if extension.RemapDescriptor(op, nil) != op {
			t.Fatal("unchanged operator copied")
		}
		addresses := []ids.ObjectAddress{op.Address(), op.Namespace(), op.Extension, {ClassID: ids.TypeClassID, ObjectID: op.Left}, {ClassID: ids.ProcedureClassID, ObjectID: op.Function}, {ClassID: ids.ProcedureClassID, ObjectID: op.Restriction}, {ClassID: ids.ProcedureClassID, ObjectID: op.Join}}
		chain := map[ids.ObjectAddress]ids.ObjectAddress{}
		for i, address := range addresses {
			first := ids.ObjectAddress{ClassID: address.ClassID, ObjectID: ids.OID(950000 + i*2)}
			second := ids.ObjectAddress{ClassID: address.ClassID, ObjectID: first.ObjectID + 1}
			chain[address] = first
			chain[first] = second
		}
		mapped := extension.RemapDescriptor(op, chain).(*catalog.Operator)
		if mapped.Address() != chain[op.Address()] || mapped.Namespace() != chain[op.Namespace()] || mapped.Extension != chain[op.Extension] || mapped.Left != chain[addresses[3]].ObjectID || mapped.Right != mapped.Left || mapped.Function != chain[addresses[4]].ObjectID || mapped.Commutator != chain[op.Address()].ObjectID || mapped.Restriction != chain[addresses[5]].ObjectID || mapped.Join != chain[addresses[6]].ObjectID || *op != original {
			t.Fatal("operator remap chased collision chain or mutated source", mapped)
		}
	})
}
