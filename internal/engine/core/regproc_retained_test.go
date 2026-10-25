package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineMixedOperatorCompoundCapture(t *testing.T) {
	e := engine.New()
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
	if core.DescriptionRegtypes(&d) == nil || len(core.DescriptionRegtypes(&d).Constants) != 3 {
		t.Fatal("mixed datum did not enter shared analysis", core.DescriptionRegtypes(&d))
	}
	saved := regoperatorPreparedSnapshot(core.DescriptionRegtypes(&d).Constants)
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
		{Ref: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), typ.Address()), Revision: typ.Revision()},
		{Ref: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), int4.Address()), Revision: int4.Revision()},
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
	assertDependencies(core.PreparedAnalysisDependencies(core.DescriptionAnalysis(&d)))
	// Rebind actual captured datums at one occurrence. Invalid text ensures
	// that each target selects its retained cell rather than reparsing.
	func() {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		cells := regoperatorPreparedSnapshot(saved)
		for i := range cells {
			cells[i].Offset = 7
		}
		expected := *core.NewDescription(core.DescriptionFields{Regtypes: &regobj.Analysis{Constants: cells}})
		b := core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, &expected)
		b.Query.Auth = &auth
		pos := parser.Position{Offset: 7, Character: 8}
		fresh := core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
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
	if !regoperatorPreparedCellValid(t, e, s, core.DescriptionRegtypes(&d)) {
		t.Fatal("fresh mixed cell invalid")
	}
	signature := slices.Clone(d.Results)
	before := core.DescriptionAnalysis(&d)
	sessionSQL(t, admin, "ALTER SCHEMA routine_types OWNER TO routine_operand_b")
	if regoperatorPreparedCellValid(t, e, s, core.DescriptionRegtypes(&d)) {
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
	if core.DescriptionAnalysis(&d) != before || !reflect.DeepEqual(core.DescriptionRegtypes(&d).Constants, saved) || !reflect.DeepEqual(d.Results, signature) {
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
	assertDependencies(core.PreparedAnalysisDependencies(core.DescriptionAnalysis(&d)))
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
	if regoperatorPreparedCellValid(t, e, s, core.DescriptionRegtypes(&d)) {
		t.Fatal("mixed capture accepted stale operand revision")
	}
	priorAnalysis := core.DescriptionAnalysis(&d)
	revised, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal("mixed revision recapture failed", err)
	}
	defer revised.Rows.Close()
	present, err = revised.Rows.Next(context.Background(), values)
	if err != nil || !present || !sqltype.SameDatum(values[0], c.Value) || core.DescriptionAnalysis(&d) == priorAnalysis {
		t.Fatal("mixed revision did not recapture unchanged actual identities", values, err)
	}
	want[0].Revision = changed.Revision()
	if !reflect.DeepEqual(core.DescriptionRegtypes(&d).Constants[0].Operands, want) {
		t.Fatal("mixed recapture retained stale operand stamps", core.DescriptionRegtypes(&d).Constants[0].Operands, want)
	}
	assertDependencies(core.PreparedAnalysisDependencies(core.DescriptionAnalysis(&d)))
	if !reflect.DeepEqual(d.Results, signature) || !regoperatorPreparedCellValid(t, e, s, core.DescriptionRegtypes(&d)) {
		t.Fatal("revised mixed capture changed signature or remained invalid")
	}
}
