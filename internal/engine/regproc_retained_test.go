package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineRetainedWriteInputAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE retained_routine_pair AS (p regprocedure, r regrole, t regtype)"))
	pair := domainUnitType(t, e.CatalogRead(), "retained_routine_pair").typ
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	value, err := text.Input(context.Background(), `("abs(integer)",postgres,integer)`, -1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sqltype.PlanCoercion(text.Usage(), pair.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	// A restored fragment expression retains this character-to-container cast.
	// Its consuming statement must supply input authority without rebinding it.
	node := coercionExpression{
		expressionInfo: expressionInfo{usage: pair.Usage()},
		child:          literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: value},
		plan:           plan,
		mode:           sqltype.Explicit,
	}
	for _, route := range []string{"default", "generation", "check"} {
		for _, named := range []bool{false, true} {
			name := route + "/fresh"
			input := sqltype.InputEnvironment{}
			if named {
				name = route + "/existing-diagnostic-name"
				input.TypeDiagnosticName = func(sqltype.ID) string { return "retained-name" }
			}
			t.Run(name, func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), e, input, nil)
				var defaults []BoundExpr
				var generations map[int32]columnGeneration
				var checks []checkConstraint
				switch route {
				case "default":
					defaults = []BoundExpr{node}
				case "generation":
					generations = map[int32]columnGeneration{1: {planned: node}}
				case "check":
					checks = []checkConstraint{{expression: node}}
				}
				if err := b.captureRetainedWriteInput(defaults, generations, checks); err != nil {
					t.Fatal(err)
				}
				if named && b.input.TypeDiagnosticName(pair.ID()) != "retained-name" {
					t.Fatal("catalog input capture replaced existing diagnostic authority")
				}
				actual, err := node.Eval(&EvalContext{Context: context.Background(), Input: b.input})
				if err != nil {
					t.Fatal("retained coercion lost catalog input authority", err)
				}
				procedure, err := actual.CompositeField(0).ProcedureOID()
				if err != nil || procedure != 1397 {
					t.Fatal("retained routine identity", procedure, err)
				}
				role, err := actual.CompositeField(1).RoleOID()
				postgres, ok := e.Roles().Lookup("postgres")
				if err != nil || !ok || uint64(role) != postgres.Ref.ID() {
					t.Fatal("retained receiving role identity", role, err)
				}
				typeID, err := actual.CompositeField(2).TypeOID()
				if err != nil || typeID != uint32(sqltype.Int4) {
					t.Fatal("retained type identity", typeID, err)
				}
			})
		}
	}
}

func TestRoutineMixedOperatorCompoundCapture(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	admin := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE routine_operand_a",
		"CREATE ROLE routine_operand_b",
		"CREATE SCHEMA routine_types AUTHORIZATION routine_operand_a",
		"CREATE DOMAIN routine_types.secret AS integer",
		"CREATE TYPE public.routine_operator_pair AS (p regprocedure,o regoperator,t regtype)",
		"SET search_path TO public",
	} {
		sessionSQL(t, s, q)
	}
	ns := e.CatalogRead().state.names[NameKey{ClassID: NamespaceClassID, Name: "routine_types"}]
	address := e.CatalogRead().state.names[NameKey{ClassID: TypeClassID, Namespace: ns, Name: "secret"}]
	typ := e.CatalogRead().state.descriptors[address].(*typeDescriptor)
	regoperatorPreparedInstall(t, e, ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, 92001, OID(typ.typ.ID()), 23)
	sessionSQL(t, s, "SET ROLE routine_operand_a")
	ast, d := sequencePreparedDescription(t, e, s, `SELECT '("abs(integer)","public.#=#(routine_types.secret,integer)",integer)'::public.routine_operator_pair AS c,'551'::regoperator AS o,'23'::regtype AS t`)
	if d.regtypes == nil || len(d.regtypes.constants) != 3 {
		t.Fatal("mixed datum did not enter shared analysis", d.regtypes)
	}
	saved := regoperatorPreparedSnapshot(d.regtypes.constants)
	c := saved[0]
	if c.typeID == 0 || c.operator != 0 || c.value.Type() == nil || !c.value.Type().ContainsRegproc() || !c.value.Type().ContainsOperatorAlias() {
		t.Fatal("mixed datum lost routine-owner classification", c)
	}
	if saved[1].operator != sqltype.Regoperator || saved[1].typeID != 0 || saved[2].operator != 0 || saved[2].typeID != 0 {
		t.Fatal("operator and standalone REGTYPE classifications conflated", saved)
	}
	procedure, procedureErr := c.value.CompositeField(0).ProcedureOID()
	operatorID, operatorErr := c.value.CompositeField(1).OperatorOID()
	typeID, typeErr := c.value.CompositeField(2).TypeOID()
	if procedureErr != nil || operatorErr != nil || typeErr != nil || procedure != 1397 || operatorID != 92001 || typeID != 23 {
		t.Fatal("fresh mixed members lost actual identities", procedure, operatorID, typeID, procedureErr, operatorErr, typeErr)
	}
	int4 := e.CatalogRead().state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 23}]
	want := []expressionDependency{
		{ref: ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: typ.Address()}, revision: typ.Revision()},
		{ref: ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: int4.Address()}, revision: int4.Revision()},
	}
	if !reflect.DeepEqual(c.operands, want) || !slices.Contains(c.qualified, ns) {
		t.Fatal("mixed owner lost exact operand stamps or qualified authority", c)
	}
	assertDependencies := func(deps []expressionDependency) {
		t.Helper()
		for _, operand := range want {
			if !slices.Contains(deps, operand) {
				t.Fatal("mixed operand missing from binder manifest", operand, deps)
			}
		}
	}
	assertDependencies(d.analysis.dependencies)
	// Rebind actual captured datums at one occurrence. Invalid text ensures
	// that each target selects its retained cell rather than reparsing.
	func() {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		cells := regoperatorPreparedSnapshot(saved)
		for i := range cells {
			cells[i].offset = 7
		}
		expected := StatementDescription{regtypes: &regtypeAnalysis{constants: cells}}
		b := newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, &expected)
		b.queryAuth = &auth
		pos := parser.Position{Offset: 7, Character: 8}
		fresh := newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, nil)
		fresh.queryAuth = &auth
		freshValue, err := fresh.routineConstant(`("abs(integer)","public.#=#(routine_types.secret,integer)",integer)`, c.value.Type().Usage(), pos)
		if err != nil || !sqltype.SameDatum(freshValue, c.value) || len(fresh.regtypeConstants) != 1 {
			t.Fatal("fresh mixed capture at shared occurrence", freshValue, err, fresh.regtypeConstants)
		}
		assertDependencies(fresh.dependencies)
		if !reflect.DeepEqual(fresh.regtypeConstants[0], cells[0]) {
			t.Fatal("fresh capture differs from retained occurrence", fresh.regtypeConstants[0], cells[0])
		}
		value, err := b.routineConstant("not a composite", c.value.Type().Usage(), pos)
		if err != nil || !sqltype.SameDatum(value, c.value) {
			t.Fatal("retained mixed datum was reparsed or cross-selected", value, err)
		}
		assertDependencies(b.dependencies)
		if len(b.regtypeConstants) != 1 || !reflect.DeepEqual(b.regtypeConstants[0], cells[0]) {
			t.Fatal("retained mixed capture lost qualification or operand stamps", b.regtypeConstants)
		}
		operator, _ := auth.catalog.LookupType(sqltype.Regoperator)
		value, err = b.operatorConstant("missing operator", operator, pos)
		oid, oidErr := value.OperatorOID()
		if err != nil || oidErr != nil || oid != 551 {
			t.Fatal("retained operator cross-selected routine cell", oid, err, oidErr)
		}
		value, err = b.regtypeConstant("missing type", pos)
		id, idErr := value.TypeOID()
		if err != nil || idErr != nil || id != 23 {
			t.Fatal("standalone REGTYPE cross-selected mixed cell", id, err, idErr)
		}
	}()
	if !regoperatorPreparedCellValid(t, e, s, d.regtypes) {
		t.Fatal("fresh mixed cell invalid")
	}
	signature := slices.Clone(d.Results)
	before := d.analysis
	sessionSQL(t, admin, "ALTER SCHEMA routine_types OWNER TO routine_operand_b")
	if regoperatorPreparedCellValid(t, e, s, d.regtypes) {
		t.Fatal("qualified operand authority change retained mixed cell")
	}
	result, failure := sequencePreparedExecute(e, s, ast, &d)
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "42501" || result.Rows != nil {
		if result.Rows != nil {
			result.Rows.Close()
		}
		t.Fatal("mixed authority denial did not fail without rows", failure)
	}
	if d.analysis != before || !reflect.DeepEqual(d.regtypes.constants, saved) || !reflect.DeepEqual(d.Results, signature) {
		t.Fatal("failed mixed reanalysis published partial capture or signature")
	}
	sessionSQL(t, admin, "ALTER SCHEMA routine_types OWNER TO routine_operand_a")
	if !regoperatorPreparedCellValid(t, e, s, d.regtypes) {
		t.Fatal("restored qualified operand authority did not recover shared cell")
	}
	result, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal("held mixed statement did not recover", err)
	}
	defer result.Rows.Close()
	values := make([]sqltype.Value, 3)
	present, err := result.Rows.Next(context.Background(), values)
	if err != nil || !present || !sqltype.SameDatum(values[0], c.value) {
		t.Fatal("recovered mixed datum changed identity", values, err)
	}
	assertDependencies(d.analysis.dependencies)
	if !reflect.DeepEqual(d.Results, signature) {
		t.Fatal("recovered mixed statement changed signature")
	}
	// A real operand revision invalidates the mixed owner's cell, even though
	// neither the operator identity nor the composite value changes.
	stage := e.BeginCatalogStage(e.CatalogRead())
	changed := *typ
	changed.revision++
	if err := stage.Replace(&changed); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if regoperatorPreparedCellValid(t, e, s, d.regtypes) {
		t.Fatal("mixed capture accepted stale operand revision")
	}
	priorAnalysis := d.analysis
	revised, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal("mixed revision recapture failed", err)
	}
	defer revised.Rows.Close()
	present, err = revised.Rows.Next(context.Background(), values)
	if err != nil || !present || !sqltype.SameDatum(values[0], c.value) || d.analysis == priorAnalysis {
		t.Fatal("mixed revision did not recapture unchanged actual identities", values, err)
	}
	want[0].revision = changed.Revision()
	if !reflect.DeepEqual(d.regtypes.constants[0].operands, want) {
		t.Fatal("mixed recapture retained stale operand stamps", d.regtypes.constants[0].operands, want)
	}
	assertDependencies(d.analysis.dependencies)
	if !reflect.DeepEqual(d.Results, signature) || !regoperatorPreparedCellValid(t, e, s, d.regtypes) {
		t.Fatal("revised mixed capture changed signature or remained invalid")
	}
}
