package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func operatorUnitName(schema, name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: schema}, {Text: name}}}
}
func operatorUnitDefinition(name, function string) *parser.CreateOperator {
	return &parser.CreateOperator{Name: operatorUnitName("public", name), Options: []parser.OperatorOption{{Name: "leftarg", HasValue: true, Kind: parser.OperatorOptionTypeArgument, Type: parser.TypeName{Name: "int4"}}, {Name: "rightarg", HasValue: true, Kind: parser.OperatorOptionTypeArgument, Type: parser.TypeName{Name: "int4"}}, {Name: "function", HasValue: true, Kind: parser.OperatorOptionTypeArgument, Type: parser.TypeName{Name: function}}}}
}
func operatorUnitStage(t *testing.T, e *Engine, op *session.Operation, auth AuthorizationView, s *parser.CreateOperator) *CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(auth.Catalog)
	e.mu.Lock()
	err := routine.StageCreateOperator(ddlHost{e: e, op: op, stage: stage}, auth, s)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage
}

// operatorUnitLookup reads the published or staged operator public.name(int4, int4).
func operatorUnitLookup(t *testing.T, view catalog.Read, name string) *catalog.Operator {
	t.Helper()
	public, ok := view.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if !ok {
		t.Fatal("missing public schema")
	}
	key := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectNamespace: public, ObjectName: name, Left: 23, Right: 23}
	d, ok := view.Operator(view.NameAddress(catalog.DescriptorNameKey(&key)))
	if !ok {
		t.Fatal("missing operator", name)
	}
	return d
}
func TestUserOperatorCatalogShellCompletion(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	create := operatorUnitDefinition("#=#", "int4eq")
	create.Options = append(create.Options, parser.OperatorOption{Name: "negator", HasValue: true, Kind: parser.OperatorOptionNameArgument, Reference: operatorUnitName("public", "#!#")})
	stage := operatorUnitStage(t, e, op, auth, create)
	collationUnitPublish(t, e, stage)
	view := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	a := view.NameAddress(ids.NameKey{ClassID: ids.OperatorClassID, Namespace: public, Name: "#=#", Signature: "23,23"})
	shellAddress := view.NameAddress(ids.NameKey{ClassID: ids.OperatorClassID, Namespace: public, Name: "#!#", Signature: "23,23"})
	shell := view.MustOperator(shellAddress)
	if shell.Function != 0 || shell.Result != 0 || shell.Negator != a.ObjectID || shell.Owner != auth.State.CurrentUser {
		t.Fatal("invalid forward shell", shell)
	}
	auth.Recapture(view)
	complete := operatorUnitDefinition("#!#", "int4ne")
	complete.Options = append(complete.Options, parser.OperatorOption{Name: "negator", HasValue: true, Kind: parser.OperatorOptionNameArgument, Reference: operatorUnitName("public", "#=#")})
	stage = operatorUnitStage(t, e, op, auth, complete)
	collationUnitPublish(t, e, stage)
	completed := e.CatalogRead().MustOperator(shellAddress)
	if completed.Address() != shell.Address() || completed.Function != 144 || completed.Result != 16 || completed.Negator != a.ObjectID {
		t.Fatal("completion lost real identity or binding", completed)
	}
	if shell.Function != 0 || shell.Result != 0 {
		t.Fatal("published predecessor mutated")
	}
	auth.Recapture(e.CatalogRead())
	bad := operatorUnitDefinition("#?#", "int4pl")
	bad.Options = append(bad.Options, parser.OperatorOption{Name: "negator", HasValue: true, Kind: parser.OperatorOptionNameArgument, Reference: operatorUnitName("public", "#@#")})
	stage = e.BeginCatalogStage(auth.Catalog)
	e.mu.Lock()
	err := routine.StageCreateOperator(ddlHost{e: e, op: op, stage: stage}, auth, bad)
	e.mu.Unlock()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42P13" || d.Message != "only boolean operators can have negators" {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != auth.Catalog.State() {
		t.Fatal("failed stage published")
	}
}
func TestUserOperatorCatalogValidation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	stage := e.BeginCatalogStage(view)
	d := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 90000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, ObjectName: "#~#", Left: 23, Right: 23, Owner: e.roles.domain.Principal(10), Function: 177, Result: 23}
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	candidate := e.stageRead(stage)
	if err := (routine.OperatorProvider{}).Validate(candidate, d); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*catalog.Operator){func(n *catalog.Operator) { n.Result = 20 }, func(n *catalog.Operator) { n.Left = 21 }, func(n *catalog.Operator) { n.Right = 0 }, func(n *catalog.Operator) { n.Function = 0 }, func(n *catalog.Operator) { n.Negator = n.Address().ObjectID }} {
		next := *d
		alter(&next)
		if err := (routine.OperatorProvider{}).Validate(candidate, &next); err == nil {
			t.Fatal("invalid descriptor accepted", next)
		}
	}
	for _, name := range []string{"#~#", "?-", "%-", "!", "+", "~~"} {
		if !routine.ValidOperatorName(name) {
			t.Fatal("valid operator name", name)
		}
	}
	for _, name := range []string{"", "a", "=>", "!=", "--", "/*", "++", "~-a"} {
		if routine.ValidOperatorName(name) {
			t.Fatal("invalid operator name", name)
		}
	}
}
func TestUserOperatorCatalogLookupOnlySeedValidation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	seed := view.MustOperator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 3247})
	if seed.Name() != "?" || seed.Left != 3802 || seed.Right != 25 || seed.Result != 16 || seed.Function != 4047 {
		t.Fatal("released JSONB existence metadata changed", seed)
	}
	if _, ok := view.Find(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4047}); ok {
		t.Fatal("lookup-only routine acquired a callable descriptor")
	}
	if err := (routine.OperatorProvider{}).Validate(view, seed); err != nil {
		t.Fatal("lookup-only released seed rejected", err)
	}
	for _, alter := range []func(*catalog.Operator){
		func(d *catalog.Operator) { d.Result = 23 },
		func(d *catalog.Operator) { d.Function = 177 },
		func(d *catalog.Operator) { d.Result, d.Function = 0, 0 },
	} {
		next := *seed
		alter(&next)
		if err := (routine.OperatorProvider{}).Validate(view, &next); err == nil {
			t.Fatal("corrupt released metadata accepted", next)
		}
	}
	stage := e.BeginCatalogStage(view)
	custom := *seed
	custom.ObjectAddress.ObjectID = 90000
	custom.ObjectNamespace.ObjectID = ids.PublicNamespaceOID
	custom.ObjectName = "#?#"
	if err := stage.Create(&custom); err != nil {
		t.Fatal(err)
	}
	if err := (routine.OperatorProvider{}).Validate(e.stageRead(stage), &custom); err == nil {
		t.Fatal("lookup-only identity accepted as a custom implementation")
	}
}
func TestUserOperatorRoutineBindingActualProviders(t *testing.T) {
	e := New()
	ctx := context.Background()
	view := e.CatalogRead()
	for _, tc := range []struct {
		oid    ids.OID
		inputs []int64
		want   int64
	}{{177, []int64{2, 3}, 5}, {181, []int64{2, 3}, -1}, {212, []int64{3}, -3}} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		typ, _ := view.LookupType(sqltype.Int4)
		args := make([]expr.Expr, len(tc.inputs))
		for i, n := range tc.inputs {
			value, err := sqltype.Integer(typ, n)
			if err != nil {
				t.Fatal(err)
			}
			args[i] = plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
		}
		procedure := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid})
		node, err := b.BindSelectedOperatorRoutine(procedure, args, parser.Position{})
		if err != nil {
			t.Fatal(err)
		}
		selected, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || selected != procedure {
			t.Fatal("selected routine authority", selected, err)
		}
		result, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.Integer()
		if err != nil || n != tc.want {
			t.Fatal("actual provider result", n, err)
		}
		if b.Clause.SelectedOperatorArgs != nil {
			t.Fatal("argument override leaked")
		}
	}
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	text, _ := view.LookupType(sqltype.Text)
	arg := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "ABC")}
	routine := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 870})
	node, err := b.BindSelectedOperatorRoutine(routine, []expr.Expr{arg}, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := text.Output(ctx, result)
	if err != nil || got != "abc" {
		t.Fatal("nonarithmetic provider", got, err)
	}
}
func TestUserOperatorResolutionOrderedTypes(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	int4, _ := view.LookupType(sqltype.Int4)
	int8, _ := view.LookupType(sqltype.Int8)
	int2, _ := view.LookupType(sqltype.Int2)
	domain, err := sqltype.Domain(90000, "d", "public.d", int4.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		candidates  []sqltype.OperatorInputs
		left, right *sqltype.Descriptor
		unary       bool
		want        int
		code        string
	}{
		{"exact_domain", []sqltype.OperatorInputs{{Left: 23, Right: 23}, {Left: 90000, Right: 90000}}, domain, domain, false, 1, ""},
		{"unknown_domain", []sqltype.OperatorInputs{{Left: 23, Right: 23}, {Left: 90000, Right: 90000}}, domain, nil, false, 1, ""},
		{"implicit", []sqltype.OperatorInputs{{Left: 23, Right: 23}}, int2, int2, false, 0, ""},
		{"exact", []sqltype.OperatorInputs{{Left: 23, Right: 23}, {Left: 20, Right: 20}}, int8, int8, false, 1, ""},
		{"ambiguous_smallint", []sqltype.OperatorInputs{{Left: 23, Right: 23}, {Left: 20, Right: 20}}, int2, int2, false, -1, "42725"},
		{"unknown_pair", []sqltype.OperatorInputs{{Left: 23, Right: 23}, {Left: 20, Right: 20}}, nil, nil, false, -1, "42725"},
		{"unary_unknown", []sqltype.OperatorInputs{{Right: 23}, {Right: 20}}, nil, nil, true, -1, "42725"},
		{"no_assignment", []sqltype.OperatorInputs{{Left: 21, Right: 21}}, int8, int8, false, -1, "42883"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index, err := sqltype.ResolveOperator(tc.candidates, tc.left, tc.right, tc.unary)
			if tc.code != "" {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != tc.code {
					t.Fatal(index, err)
				}
				return
			}
			if err != nil || index != tc.want {
				t.Fatal(index, err)
			}
		})
	}
}

func TestUserOperatorResolutionUnknownPositions(t *testing.T) {
	// PostgreSQL REL_18_6 func_select_candidate requires preferred float8 at
	// both unknown positions. Neither mixed signature satisfies both, so
	// resolution is ambiguous regardless of declaration order.
	for _, tc := range []struct {
		name       string
		candidates [2]sqltype.OperatorInputs
	}{
		{"float48_first", [2]sqltype.OperatorInputs{{Left: sqltype.Float4, Right: sqltype.Float8}, {Left: sqltype.Float8, Right: sqltype.Float4}}},
		{"float84_first", [2]sqltype.OperatorInputs{{Left: sqltype.Float8, Right: sqltype.Float4}, {Left: sqltype.Float4, Right: sqltype.Float8}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index, err := sqltype.ResolveOperator(tc.candidates[:], nil, nil, false)
			var diagnostic *sqlstate.Error
			if index != -1 || !errors.As(err, &diagnostic) || diagnostic.Code != "42725" || diagnostic.Message != "operator is not unique" {
				t.Fatalf("got index %d, error %v; want ambiguous operator (42725)", index, err)
			}
		})
	}
}
func TestUserOperatorResolutionPolymorphicUnknown(t *testing.T) {
	array, ok := New().CatalogRead().LookupType(sqltype.ID(1007))
	if !ok {
		t.Fatal("missing INT4[] descriptor")
	}
	domain, err := sqltype.Domain(90000, "array_domain", "public.array_domain", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	arraySignature := sqltype.OperatorInputs{Left: 2277, Right: 2277}
	for _, input := range []struct {
		name string
		typ  *sqltype.Descriptor
	}{
		{"array", array},
		{"domain", domain},
	} {
		for _, side := range []string{"known_left", "known_right"} {
			for _, order := range []string{"array_first", "array_last"} {
				t.Run(input.name+"/"+side+"/"+order, func(t *testing.T) {
					// ANYELEMENT accepts the known array initially, but the
					// assumed array cannot satisfy the unknown ANYRANGE side.
					rangeSignature := sqltype.OperatorInputs{Left: 2283, Right: 3831}
					left, right := input.typ, (*sqltype.Descriptor)(nil)
					if side == "known_right" {
						left, right = nil, input.typ
						rangeSignature.Left, rangeSignature.Right = rangeSignature.Right, rangeSignature.Left
					}
					candidates := []sqltype.OperatorInputs{
						arraySignature, rangeSignature,
						{Left: sqltype.Int4, Right: sqltype.Int4},
					}
					want := 0
					if order == "array_last" {
						candidates[0], candidates[2] = candidates[2], candidates[0]
						want = 2
					}
					index, err := sqltype.ResolveOperator(candidates, left, right, false)
					if err != nil || index != want {
						t.Fatalf("got index %d, error %v; want array signature at %d", index, err, want)
					}
					if candidates[index] != arraySignature {
						t.Fatalf("selected %v; want ANYARRAY/ANYARRAY", candidates[index])
					}
				})
			}
		}
	}
}

func TestUserOperatorRemapIdentitySharing(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	typ, _ := view.LookupType(sqltype.Regoperator)
	value, err := sqltype.OperatorValue(typ, 90000)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := regobj.RemapOperatorDatum(value, func(id ids.OID) ids.OID { return id })
	if err != nil || unchanged != value {
		t.Fatal("unchanged datum copied", err)
	}
	remapped, err := regobj.RemapOperatorDatum(value, func(id ids.OID) ids.OID {
		if id == 90000 {
			return 90001
		}
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := remapped.OperatorOID()
	if err != nil || id != 90001 {
		t.Fatal(id, err)
	}
	stage := e.BeginCatalogStage(view)
	source := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 90000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, ObjectName: "#~#", Left: 23, Right: 23, Result: 23, Function: 177, Owner: e.roles.domain.Principal(10)}
	destination := *source
	destination.ObjectAddress.ObjectID = 90001
	destination.ObjectName = "#/#"
	if err = stage.Create(source); err != nil {
		t.Fatal(err)
	}
	if err = stage.Create(&destination); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	original := e.catalog.Domain.Ref(view.Epoch(), source.Address())
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	integer, _ := view.LookupType(sqltype.Int4)
	two, _ := sqltype.Integer(integer, 2)
	three, _ := sqltype.Integer(integer, 3)
	args := []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: two}, plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: three}}
	routine := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 177})
	provider, err := b.BindSelectedOperatorRoutine(routine, args, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	info := expr.InfoOf(provider)
	info.RoutineReference = nil
	wrapped := plan.SelectedOperatorExpr{Info: info, Operator: source, Ref: original, Provider: provider}
	assertManifest := func(node expr.Expr, operator ids.ObjectAddress) {
		t.Helper()
		_, manifest, err := renderColumnDefault(context.Background(), view, node)
		if err != nil {
			t.Fatal("stored selected operator", err)
		}
		operatorCount, routineCount := 0, 0
		for _, ref := range manifest {
			if ref.Domain() != e.catalog.Domain || ref.Epoch != view.Epoch() {
				t.Fatal("foreign stored lifetime", ref)
			}
			if ref.Address == operator {
				operatorCount++
			}
			if ref.Address == routine.Address() {
				routineCount++
			}
		}
		if operatorCount != 1 || routineCount != 1 {
			t.Fatal("stored recipe must retain both operator and actual routine exactly once", manifest)
		}
		_, auth := conversionUnitAuth(t, e)
		auth.Recapture(view)
		recipe := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineRecipe: true, RoutineReferences: manifest}}
		if err := exec.CheckRecipeRoutineDescriptor(&recipe, routineAccessOf(auth), nil, routine); err != nil {
			t.Fatal("stored provider admission", err)
		}
	}
	assertManifest(wrapped, source.Address())
	mapped, err := remapBoundExpression(wrapped, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref.Address == original.Address {
			ref.Address.ObjectID = 90001
		}
		return ref
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := mapped.(plan.SelectedOperatorExpr)
	if got.Ref.Address.ObjectID != 90001 || wrapped.Ref != original {
		t.Fatal("operator reference remap lost provenance")
	}
	children := 0
	plan.ExpressionChildren(got, func(expr.Expr) { children++ })
	if children != 1 {
		t.Fatal("provider walked twice", children)
	}
	domain, err := sqltype.Domain(90002, "alias_domain", "public.alias_domain", typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrappedValue, err := domain.Wrap(value)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := sqltype.Composite(90003, "alias_pair", "public.alias_pair", 90004, []sqltype.CompositeMember{{Attribute: 1, Name: "op", Usage: domain.Usage()}, {Attribute: 2, Name: "other", Usage: typ.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	row, err := sqltype.CompositeValue(pair, []sqltype.Value{wrappedValue, sqltype.Null(typ)})
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(90004, "_alias_pair", pair)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: -2, Length: 2}}, []sqltype.Value{row, sqltype.Null(pair)})
	if err != nil {
		t.Fatal(err)
	}
	same, err := regobj.RemapOperatorDatum(nested, func(id ids.OID) ids.OID { return id })
	if err != nil || same != nested {
		t.Fatal("unchanged nested storage copied", err)
	}
	moved, err := regobj.RemapOperatorDatum(nested, func(id ids.OID) ids.OID {
		if id == 90000 {
			return 90001
		}
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	if moved.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: -2, Length: 2}) || !moved.ArrayElement(1).IsNull() || !moved.ArrayElement(0).CompositeField(1).IsNull() {
		t.Fatal("nested shape or NULL changed")
	}
	mappedID, err := moved.ArrayElement(0).CompositeField(0).OperatorOID()
	if err != nil || mappedID != 90001 {
		t.Fatal("nested domain alias remap", mappedID, err)
	}
	retagged, _, err := retagBoundTypes(view, mapped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	assertManifest(retagged, destination.Address())
	renderer := pgRenderer{view: view}
	sql, err := renderer.sql(context.Background(), retagged, pgRenderParent{})
	if err != nil || !strings.Contains(sql, `OPERATOR("public".#/#)`) {
		t.Fatal("selected name lost during remap/deparse", sql, err)
	}
}

func TestUserOperatorCatalogPublicationFences(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	stage := operatorUnitStage(t, e, op, auth, operatorUnitDefinition("#~#", "int4pl"))
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	before := e.CatalogRead()
	cancel()
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || e.CatalogRead().State() != before.State() {
		t.Fatal("canceled publication changed operator authority", changes, err)
	}
	e.DiscardCatalogStage(stage)
	stage = e.BeginCatalogStage(before)
	unowned := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 90000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, ObjectName: "#~#", Left: 23, Right: 23}
	if err = stage.Create(unowned); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("ownerless shell accepted")
	}
	e.DiscardCatalogStage(stage)
	stage = e.BeginCatalogStage(before)
	foreign := *unowned
	foreign.Owner = New().roles.domain.Principal(10)
	if err = stage.Create(&foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("foreign principal adopted")
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("invalid principal stage published")
	}
}

func TestUserOperatorCatalogExactUtilityLifecycle(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	create := operatorUnitDefinition("#=#", "int4eq")
	create.Options = append(create.Options, parser.OperatorOption{Name: "commutator", HasValue: true, Kind: parser.OperatorOptionNameArgument, Reference: create.Name})
	stage := operatorUnitStage(t, e, op, auth, create)
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	signature := parser.OperatorSignature{Name: create.Name, Left: parser.TypeName{Name: "int4"}, Right: parser.TypeName{Name: "int4"}}
	d := operatorUnitLookup(t, auth.Catalog, "#=#")
	if d.Address().ClassID != ids.OperatorClassID || d.Function != 65 || d.Commutator != d.Address().ObjectID {
		t.Fatal("self link must select the exact existing definition", d)
	}
	base := auth.Catalog.State()
	stage = e.BeginCatalogStage(auth.Catalog)
	e.mu.Lock()
	err := routine.StageCreateOperator(ddlHost{e: e, op: op, stage: stage}, auth, create)
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || diagnostic.Message != "operator #=# already exists" {
		t.Fatal("duplicate must fail before catalog publication", err)
	}
	if e.CatalogRead().State() != base {
		t.Fatal("duplicate changed the published catalog")
	}
	stage = e.BeginCatalogStage(auth.Catalog)
	e.mu.Lock()
	err = routine.StageAlterOperator(ddlHost{e: e, op: op, stage: stage}, auth, &parser.AlterOperator{Signature: signature, Action: "options", Options: []parser.OperatorOption{{Name: "hashes", HasValue: true, Kind: parser.OperatorOptionStringArgument, Value: "true"}}})
	e.mu.Unlock()
	if err != nil {
		t.Fatal("exact ALTER lookup", err)
	}
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	altered := auth.Catalog.MustOperator(d.Address())
	if !altered.Hash || altered.Address() != d.Address() || d.Hash {
		t.Fatal("ALTER must preserve identity and immutable predecessor", altered, d)
	}
	stage = e.BeginCatalogStage(auth.Catalog)
	if err := routine.StageDropOperator(ddlHost{e: e, op: op, stage: stage}, auth, &parser.DropOperator{Signatures: []parser.OperatorSignature{signature}}); err != nil {
		t.Fatal("exact DROP lookup", err)
	}
	if len(stage.domainTargets) != 1 || stage.domainTargets[0] != d.Address() {
		t.Fatal("DROP selected the wrong identity", stage.domainTargets)
	}
}

func TestUserOperatorCatalogOrphanShellDumpNameCollision(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	stage := operatorUnitStage(t, e, op, auth, operatorUnitDefinition("#~~#", "int4pl"))
	shell := &catalog.Operator{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 90000},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
		ObjectRevision:  1, ObjectName: "#?#", Left: 23, Right: 23, Owner: auth.State.CurrentUser,
	}
	if err := stage.Create(shell); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	declaration, err := operatorDumpDeclaration(context.Background(), dumpPresenter{}, auth.Catalog, auth.Roles, shell)
	if err != nil {
		t.Fatal(err)
	}
	prefix := catalog.QuoteIdentifier("public") + "."
	if strings.Contains(declaration.SQL, "CREATE OPERATOR "+prefix+"#~~# ") ||
		!strings.Contains(declaration.SQL, "CREATE OPERATOR "+prefix+"#~!# ") ||
		!strings.Contains(declaration.SQL, "DROP OPERATOR "+prefix+"#~!# ") {
		t.Fatal("orphan generator must skip occupied exact signature", declaration.SQL)
	}
	if existing := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.OperatorClassID, Namespace: shell.Namespace(), Name: "#~~#", Signature: "23,23"}); existing == (ids.ObjectAddress{}) {
		t.Fatal("dump lost the occupied definition")
	}
}

func TestUserOperatorCatalogDropClosureRootsAndLinks(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	create := operatorUnitDefinition("#=#", "int4eq")
	create.Options = append(create.Options, parser.OperatorOption{Name: "negator", HasValue: true, Kind: parser.OperatorOptionNameArgument, Reference: operatorUnitName("public", "#!#")})
	stage := operatorUnitStage(t, e, op, auth, create)
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	stage = operatorUnitStage(t, e, op, auth, operatorUnitDefinition("#~#", "int4pl"))
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	key := func(name string) ids.NameKey {
		return ids.NameKey{ClassID: ids.OperatorClassID, Namespace: public, Name: name, Signature: "23,23"}
	}
	survivorAddress := auth.Catalog.NameAddress(key("#=#"))
	survivor := auth.Catalog.MustOperator(survivorAddress)
	shellAddress := auth.Catalog.NameAddress(key("#!#"))
	otherAddress := auth.Catalog.NameAddress(key("#~#"))
	if survivor.Negator != shellAddress.ObjectID {
		t.Fatal("missing forward-shell link", survivor)
	}
	signature := func(name string) parser.OperatorSignature {
		return parser.OperatorSignature{Name: operatorUnitName("public", name), Left: parser.TypeName{Name: "int4"}, Right: parser.TypeName{Name: "int4"}}
	}
	stage = e.BeginCatalogStage(auth.Catalog)
	if err := routine.StageDropOperator(ddlHost{e: e, op: op, stage: stage}, auth, &parser.DropOperator{Signatures: []parser.OperatorSignature{signature("#!#"), signature("#~#"), signature("#!#")}}); err != nil {
		t.Fatal(err)
	}
	if len(stage.domainTargets) != 2 {
		t.Fatal("DROP roots not deduplicated", stage.domainTargets)
	}
	if err := stageExpressionDependencies(context.Background(), stage, stage.domainTargets, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal("links are not dependency blockers", err)
	}
	collationUnitPublish(t, e, stage)
	view := e.CatalogRead()
	for _, address := range []ids.ObjectAddress{shellAddress, otherAddress} {
		if _, present := view.Find(address); present {
			t.Fatal("closure failed to retire an explicit operator root", address)
		}
	}
	for _, name := range []string{"#!#", "#~#"} {
		if _, present := view.LookupName(key(name)); present {
			t.Fatal("retired operator remains in exact name index", name)
		}
	}
	next := view.MustOperator(survivorAddress)
	if next.Negator != 0 || next.Function != survivor.Function || next.Revision() == survivor.Revision() {
		t.Fatal("surviving peer was not atomically relinked", next)
	}
	if survivor.Negator != shellAddress.ObjectID || auth.Catalog.Get(shellAddress) == nil {
		t.Fatal("DROP mutated its captured predecessor")
	}
}
