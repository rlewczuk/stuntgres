package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineRetainedWriteInputAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE retained_routine_pair AS (p regprocedure, r regrole, t regtype)"))
	pair := domainUnitType(t, e.CatalogRead(), "retained_routine_pair").SQLType
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	value, err := text.Input(context.Background(), `("abs(integer)",postgres,integer)`, -1)
	if err != nil {
		t.Fatal(err)
	}
	coercionPlan, err := sqltype.PlanCoercion(text.Usage(), pair.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	// A restored fragment expression retains this character-to-container cast.
	// Its consuming statement must supply input authority without rebinding it.
	node := plan.CoercionExpr{
		Info:  expr.Info{Usage: pair.Usage()},
		Child: plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: value},
		Plan:  coercionPlan,
		Mode:  sqltype.Explicit,
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
				var defaults []expr.Expr
				var generations map[int32]catalog.ColumnGeneration
				var checks []catalog.CheckConstraint
				switch route {
				case "default":
					defaults = []expr.Expr{node}
				case "generation":
					generations = map[int32]catalog.ColumnGeneration{1: {Planned: node}}
				case "check":
					checks = []catalog.CheckConstraint{{Expression: node}}
				}
				if err := b.CaptureRetainedWriteInput(defaults, generations, checks); err != nil {
					t.Fatal(err)
				}
				if named && b.Input.TypeDiagnosticName(pair.ID()) != "retained-name" {
					t.Fatal("catalog input capture replaced existing diagnostic authority")
				}
				actual, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: b.Input}})
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
	ns := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "routine_types"})
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: "secret"})
	typ := e.CatalogRead().MustType(address)
	regoperatorPreparedInstall(t, e, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 92001, ids.OID(typ.SQLType.ID()), 23)
	sessionSQL(t, s, "SET ROLE routine_operand_a")
	ast, d := sequencePreparedDescription(t, e, s, `SELECT '("abs(integer)","public.#=#(routine_types.secret,integer)",integer)'::public.routine_operator_pair AS c,'551'::regoperator AS o,'23'::regtype AS t`)
	if d.regtypes == nil || len(d.regtypes.constants) != 3 {
		t.Fatal("mixed datum did not enter shared analysis", d.regtypes)
	}
	saved := regoperatorPreparedSnapshot(d.regtypes.constants)
	c := saved[0]
	if c.TypeID == 0 || c.Operator != 0 || c.Value.Type() == nil || !c.Value.Type().ContainsRegproc() || !c.Value.Type().ContainsOperatorAlias() {
		t.Fatal("mixed datum lost routine-owner classification", c)
	}
	if saved[1].Operator != sqltype.Regoperator || saved[1].TypeID != 0 || saved[2].Operator != 0 || saved[2].TypeID != 0 {
		t.Fatal("operator and standalone REGTYPE classifications conflated", saved)
	}
	procedure, procedureErr := c.Value.CompositeField(0).ProcedureOID()
	operatorID, operatorErr := c.Value.CompositeField(1).OperatorOID()
	typeID, typeErr := c.Value.CompositeField(2).TypeOID()
	if procedureErr != nil || operatorErr != nil || typeErr != nil || procedure != 1397 || operatorID != 92001 || typeID != 23 {
		t.Fatal("fresh mixed members lost actual identities", procedure, operatorID, typeID, procedureErr, operatorErr, typeErr)
	}
	int4 := e.CatalogRead().Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
	want := []catalog.ExpressionDependency{
		{Ref: e.catalog.Domain.Ref(e.CatalogRead().Epoch(), typ.Address()), Revision: typ.Revision()},
		{Ref: e.catalog.Domain.Ref(e.CatalogRead().Epoch(), int4.Address()), Revision: int4.Revision()},
	}
	if !reflect.DeepEqual(c.Operands, want) || !slices.Contains(c.Qualified, ns) {
		t.Fatal("mixed owner lost exact operand stamps or qualified authority", c)
	}
	assertDependencies := func(deps []catalog.ExpressionDependency) {
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
			cells[i].Offset = 7
		}
		expected := StatementDescription{regtypes: &regtypeAnalysis{constants: cells}}
		b := newExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, &expected)
		b.Query.Auth = &auth
		pos := parser.Position{Offset: 7, Character: 8}
		fresh := newExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
		fresh.Query.Auth = &auth
		freshValue, err := fresh.RoutineConstant(`("abs(integer)","public.#=#(routine_types.secret,integer)",integer)`, c.Value.Type().Usage(), pos)
		if err != nil || !sqltype.SameDatum(freshValue, c.Value) || len(fresh.Prepared.RegtypeConstants) != 1 {
			t.Fatal("fresh mixed capture at shared occurrence", freshValue, err, fresh.Prepared.RegtypeConstants)
		}
		assertDependencies(fresh.Dependencies)
		if !reflect.DeepEqual(fresh.Prepared.RegtypeConstants[0], cells[0]) {
			t.Fatal("fresh capture differs from retained occurrence", fresh.Prepared.RegtypeConstants[0], cells[0])
		}
		value, err := b.RoutineConstant("not a composite", c.Value.Type().Usage(), pos)
		if err != nil || !sqltype.SameDatum(value, c.Value) {
			t.Fatal("retained mixed datum was reparsed or cross-selected", value, err)
		}
		assertDependencies(b.Dependencies)
		if len(b.Prepared.RegtypeConstants) != 1 || !reflect.DeepEqual(b.Prepared.RegtypeConstants[0], cells[0]) {
			t.Fatal("retained mixed capture lost qualification or operand stamps", b.Prepared.RegtypeConstants)
		}
		operator, _ := auth.Catalog.LookupType(sqltype.Regoperator)
		value, err = b.OperatorConstant("missing operator", operator, pos)
		oid, oidErr := value.OperatorOID()
		if err != nil || oidErr != nil || oid != 551 {
			t.Fatal("retained operator cross-selected routine cell", oid, err, oidErr)
		}
		value, err = b.RegtypeConstant("missing type", pos)
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
	result, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal("held mixed statement did not recover", err)
	}
	defer result.Rows.Close()
	values := make([]sqltype.Value, 3)
	present, err := result.Rows.Next(context.Background(), values)
	if err != nil || !present || !sqltype.SameDatum(values[0], c.Value) {
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
	changed.ObjectRevision++
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
	if err != nil || !present || !sqltype.SameDatum(values[0], c.Value) || d.analysis == priorAnalysis {
		t.Fatal("mixed revision did not recapture unchanged actual identities", values, err)
	}
	want[0].Revision = changed.Revision()
	if !reflect.DeepEqual(d.regtypes.constants[0].Operands, want) {
		t.Fatal("mixed recapture retained stale operand stamps", d.regtypes.constants[0].Operands, want)
	}
	assertDependencies(d.analysis.dependencies)
	if !reflect.DeepEqual(d.Results, signature) || !regoperatorPreparedCellValid(t, e, s, d.regtypes) {
		t.Fatal("revised mixed capture changed signature or remained invalid")
	}
}
