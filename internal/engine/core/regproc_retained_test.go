package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/core"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineMixedOperatorCompoundCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	admin := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE routine_operand_a",
		"CREATE ROLE routine_operand_b",
		"CREATE SCHEMA routine_types AUTHORIZATION routine_operand_a",
		"CREATE DOMAIN routine_types.secret AS integer",
		"CREATE TYPE public.routine_operator_pair AS (p regprocedure,o regoperator,t regtype)",
		"SET search_path TO public",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	ns := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "routine_types"})
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: "secret"})
	typ := e.CatalogRead().MustType(address)
	regoperatorPreparedInstall(t, e, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 92001, ids.OID(typ.SQLType.ID()), 23)
	enginetest.SessionSQL(t, s, "SET ROLE routine_operand_a")
	ast, d := enginetest.PreparedDescription(t, e, s, `SELECT '("abs(integer)","public.#=#(routine_types.secret,integer)",integer)'::public.routine_operator_pair AS c,'551'::regoperator AS o,'23'::regtype AS t`)
	if d.Regtypes == nil || len(d.Regtypes.Constants) != 3 {
		t.Fatal("mixed datum did not enter shared analysis", d.Regtypes)
	}
	saved := regoperatorPreparedSnapshot(d.Regtypes.Constants)
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
	// Capture each parsed signature operand in source order: abs(integer),
	// then #=#(secret,integer). The leading INT4 is additive routine-signature
	// provenance; retain both original operator operands and their order.
	// Captured input stamps use the default purpose; the binder projects them
	// to operand-only dependencies, not executable type uses.
	want := []catalog.ExpressionDependency{
		{Ref: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), int4.Address()), Revision: int4.Revision(), Purpose: catalog.DefinitionDependency},
		{Ref: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), typ.Address()), Revision: typ.Revision(), Purpose: catalog.DefinitionDependency},
		{Ref: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), int4.Address()), Revision: int4.Revision(), Purpose: catalog.DefinitionDependency},
	}
	if !reflect.DeepEqual(c.Operands, want) || !slices.Contains(c.Qualified, ns) {
		t.Fatal("mixed owner lost exact operand stamps or qualified authority", c)
	}
	assertDependencies := func(deps []catalog.ExpressionDependency) {
		t.Helper()
		for _, operand := range want {
			operand.Purpose = catalog.OperandDependency
			if !slices.Contains(deps, operand) {
				t.Fatal("mixed operand missing from binder manifest", operand, deps)
			}
		}
	}
	assertDependencies(d.Analysis.Dependencies)
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
		expected := core.Description{Regtypes: &regobj.Analysis{Constants: cells}}
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
	if !regoperatorPreparedCellValid(t, e, s, d.Regtypes) {
		t.Fatal("fresh mixed cell invalid")
	}
	signature := slices.Clone(d.Results)
	before := d.Analysis
	enginetest.SessionSQL(t, admin, "ALTER SCHEMA routine_types OWNER TO routine_operand_b")
	if regoperatorPreparedCellValid(t, e, s, d.Regtypes) {
		t.Fatal("qualified operand authority change retained mixed cell")
	}
	result, failure := enginetest.PreparedExecute(e, s, ast, &d)
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "42501" || result.Rows != nil {
		if result.Rows != nil {
			result.Rows.Close()
		}
		t.Fatal("mixed authority denial did not fail without rows", failure)
	}
	if d.Analysis != before || !reflect.DeepEqual(d.Regtypes.Constants, saved) || !reflect.DeepEqual(d.Results, signature) {
		t.Fatal("failed mixed reanalysis published partial capture or signature")
	}
	enginetest.SessionSQL(t, admin, "ALTER SCHEMA routine_types OWNER TO routine_operand_a")
	result, err := enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal("held mixed statement did not recover", err)
	}
	defer result.Rows.Close()
	values := make([]sqltype.Value, 3)
	present, err := result.Rows.Next(context.Background(), values)
	if err != nil || !present || !sqltype.SameDatum(values[0], c.Value) {
		t.Fatal("recovered mixed datum changed identity", values, err)
	}
	assertDependencies(d.Analysis.Dependencies)
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
	if regoperatorPreparedCellValid(t, e, s, d.Regtypes) {
		t.Fatal("mixed capture accepted stale operand revision")
	}
	priorAnalysis := d.Analysis
	revised, err := enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal("mixed revision recapture failed", err)
	}
	defer revised.Rows.Close()
	present, err = revised.Rows.Next(context.Background(), values)
	if err != nil || !present || !sqltype.SameDatum(values[0], c.Value) || d.Analysis == priorAnalysis {
		t.Fatal("mixed revision did not recapture unchanged actual identities", values, err)
	}
	want[1].Revision = changed.Revision()
	if !reflect.DeepEqual(d.Regtypes.Constants[0].Operands, want) {
		t.Fatal("mixed recapture retained stale operand stamps", d.Regtypes.Constants[0].Operands, want)
	}
	assertDependencies(d.Analysis.Dependencies)
	if !reflect.DeepEqual(d.Results, signature) || !regoperatorPreparedCellValid(t, e, s, d.Regtypes) {
		t.Fatal("revised mixed capture changed signature or remained invalid")
	}
}

func TestRoutineNameDatumCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	const declaration = "CREATE FUNCTION routine_datum_target(integer,integer) RETURNS integer LANGUAGE SQL SET search_path TO public AS 'SELECT $1+$2'"
	enginetest.SessionSQL(t, s, declaration)
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "routine_datum_target", Signature: "23,23"})
	target := view.MustProcedure(address)
	const literal = "SELECT 'routine_datum_target'::regproc AS r,'routine_datum_target(integer,integer)'::regprocedure AS p,'4294967295'::regprocedure AS n"
	ast, d := enginetest.PreparedDescription(t, e, s, literal)
	if d.Regtypes == nil || len(d.Regtypes.Constants) != 3 {
		t.Fatal("routine name datums did not enter canonical capture", d.Regtypes)
	}
	saved := regoperatorPreparedSnapshot(d.Regtypes.Constants)
	int4 := view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
	operand := catalog.ExpressionDependency{Ref: e.Catalog.Domain.Ref(view.Epoch(), int4.Address()), Revision: int4.Revision()}
	if len(saved[0].Operands) != 0 || !reflect.DeepEqual(saved[1].Operands, []catalog.ExpressionDependency{operand, operand}) || len(saved[2].Operands) != 0 {
		t.Fatal("routine datum invented callable operands or lost repeated signature types", saved)
	}
	for _, dependency := range d.Analysis.Dependencies {
		if dependency.Ref.Address == address {
			t.Fatal("routine name datum acquired selected callable dependency", dependency)
		}
	}
	// A real invocation of the same selected descriptor still requires its
	// exact definition stamp; the name datum cannot stand in for that capture.
	callAST, call := enginetest.PreparedDescription(t, e, s, "SELECT routine_datum_target(1,2) AS n")
	callable := catalog.ExpressionDependency{Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Revision: target.Revision(), Purpose: catalog.DefinitionDependency}
	if !slices.Contains(call.Analysis.Dependencies, callable) {
		t.Fatal("actual invocation lost selected callable dependency", call.Analysis.Dependencies, callable)
	}
	result, err := enginetest.PreparedExecute(e, s, callAST, &call)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows == nil {
		t.Fatal("actual invocation returned no cursor")
	}
	defer result.Rows.Close()
	regoperatorPreparedValues(t, result, []sqltype.ID{sqltype.Int4}, []string{"3"})
	result.Rows.Close()
	signature := slices.Clone(d.Results)
	assertHeld := func() {
		t.Helper()
		if !regoperatorPreparedCellValid(t, e, s, d.Regtypes) {
			t.Fatal("referent-only change retired accepted routine datum")
		}
		result, err := enginetest.PreparedExecute(e, s, ast, &d)
		if err != nil {
			t.Fatal("accepted routine datum was re-input", err)
		}
		if result.Rows == nil {
			t.Fatal("held routine datum returned no cursor")
		}
		defer result.Rows.Close()
		values := make([]sqltype.Value, 3)
		present, err := result.Rows.Next(context.Background(), values)
		if err != nil || !present {
			t.Fatal("held routine datum row", present, err)
		}
		for i := range values {
			if !sqltype.SameDatum(values[i], saved[i].Value) {
				t.Fatal("held routine datum retargeted", i, values[i], saved[i].Value)
			}
		}
		if !reflect.DeepEqual(d.Regtypes.Constants, saved) || !reflect.DeepEqual(d.Results, signature) {
			t.Fatal("referent-only change mutated accepted capture or metadata")
		}
	}
	enginetest.SessionSQL(t, s, "ALTER FUNCTION routine_datum_target(integer,integer) COST 17")
	assertHeld()
	enginetest.SessionSQL(t, s, "DROP FUNCTION routine_datum_target(integer,integer)")
	assertHeld()
	result, err = enginetest.PreparedExecute(e, s, callAST, &call)
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("dropped actual callable returned rows", err)
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
		t.Fatal("dropped actual invocation did not re-resolve its callable", err)
	}
	enginetest.SessionSQL(t, s, declaration)
	current := e.CatalogRead().MustProcedure(e.CatalogRead().NameAddress(catalog.DescriptorNameKey(target)))
	if current.Address() == address {
		t.Fatal("recreated routine reused retired identity")
	}
	assertHeld()
	_, fresh := enginetest.PreparedDescription(t, e, s, literal)
	if fresh.Regtypes == nil || len(fresh.Regtypes.Constants) != 3 {
		t.Fatal("fresh recreated input lost canonical capture", fresh.Regtypes)
	}
	for _, constant := range fresh.Regtypes.Constants[:2] {
		if oid, err := constant.Value.ProcedureOID(); err != nil || oid != uint32(current.Address().ObjectID) {
			t.Fatal("fresh input failed to resolve recreated routine", constant, err)
		}
	}
}
