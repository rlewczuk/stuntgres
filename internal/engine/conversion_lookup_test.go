package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestConversionDefaultLookupSnapshotOrder(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	earlier := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90010}, name: "earlier", revision: 1, Owner: e.authorizationDomain.Principal(10)}
	low := conversionUnitDefinition(e, 90000, "z_low_oid", true)
	high := conversionUnitDefinition(e, 90001, "a_high_oid", true)
	first := conversionUnitDefinition(e, 90002, "first_namespace", true)
	first.namespace = earlier.address
	nondefault := conversionUnitDefinition(e, 90003, "shadow_nondefault", false)
	nondefault.namespace = earlier.address
	stage := e.BeginCatalogStage(view)
	for _, d := range []Descriptor{earlier, low, high, first, nondefault} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	scope := &StatementScope{visibilityCatalog: view, visibilityOtherPath: []ids.ObjectAddress{earlier.address, low.Namespace()}}
	procedure, err := scope.conversionProcedure(6, 8)
	if err != nil || procedure == nil || procedure.address != low.Handler || scope.conversionDefaults[[2]int{6, 8}].conversion != first {
		t.Fatal("namespace priority", procedure, err)
	}
	reversed := &StatementScope{visibilityCatalog: view, visibilityOtherPath: []ids.ObjectAddress{low.Namespace(), earlier.address}}
	if _, err = reversed.conversionProcedure(6, 8); err != nil || reversed.conversionDefaults[[2]int{6, 8}].conversion != low {
		t.Fatal("default index OID order, not name order", err)
	}
	removedDefault := *first
	removedDefault.DefaultConversion, removedDefault.revision = false, 2
	stage = e.BeginCatalogStage(view)
	if err = stage.Replace(&removedDefault); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	fresh := &StatementScope{visibilityCatalog: e.CatalogRead(), visibilityOtherPath: scope.visibilityOtherPath}
	if _, err = fresh.conversionProcedure(6, 8); err != nil || fresh.conversionDefaults[[2]int{6, 8}].conversion != low {
		t.Fatal("nondefault repaired lookup", err)
	}
	if _, err = scope.conversionProcedure(6, 8); err != nil || scope.conversionDefaults[[2]int{6, 8}].conversion != first {
		t.Fatal("old execution snapshot changed", err)
	}
	text, _ := view.LookupType(sqltype.Text)
	name, _ := view.LookupType(sqltype.Name)
	bytea, _ := view.LookupType(sqltype.Bytea)
	plan, err := sqltype.BindBinary("convert_to", bytea.Usage(), text.Usage(), name.Usage())
	if err != nil {
		t.Fatal(err)
	}
	node := functions.Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: plan, Args: []expr.Expr{
		literalExpression{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "é")},
		literalExpression{Info: expr.Info{Usage: name.Usage()}, value: sqltype.String(name, "LATIN1")},
	}}
	result, err := node.Eval(&EvalContext{Context: context.Background(), statementScope: fresh})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := result.Binary()
	if err != nil || raw != "\xe9" {
		t.Fatal("actual selected handler output", raw, err)
	}
	missing := &StatementScope{visibilityCatalog: view, visibilityOtherPath: []ids.ObjectAddress{}}
	if _, err = node.Eval(&EvalContext{Context: context.Background(), statementScope: missing}); err == nil {
		t.Fatal("hardwired provider fallback repaired missing default")
	}
	node.Args[0] = literalExpression{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "")}
	if _, err = node.Eval(&EvalContext{Context: context.Background()}); err != nil {
		t.Fatal("empty input demanded default snapshot", err)
	}
}

func TestConversionDefaultLookupExecuteACL(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	actor := auth.roles.LookupRef("pg_monitor")
	auth.state.CurrentUser = actor
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4375})
	if err := auth.Check(AccessRequirement{Object: ref, Privileges: authorization.ExecutePrivilege}); err != nil {
		t.Fatal("real PUBLIC EXECUTE default", err)
	}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, refreshErr := e.authorizationLocked(op)
	e.mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.state.CurrentUser = actor
	var stage *CatalogStage
	var err error
	withPublicSchemaUnitOwner(t, e, "pg_monitor", func() {
		e.mu.Lock()
		auth, refreshErr = e.authorizationLocked(op)
		e.mu.Unlock()
		if refreshErr != nil {
			t.Fatal(refreshErr)
		}
		auth.state.CurrentUser = actor
		create := &parser.CreateConversion{Name: conversionUnitName("public", "denied"), Source: "UTF8", Destination: "LATIN1", Function: conversionUnitName("pg_catalog", "utf8_to_iso8859_1")}
		stage = e.BeginCatalogStage(auth.catalog)
		before := auth.catalog.state
		e.mu.Lock()
		err = e.stageCreateConversionLocked(op, auth, stage, create)
		e.mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function pg_catalog.utf8_to_iso8859_1" {
			t.Fatal("actual routine ACL denial", err)
		}
		e.DiscardCatalogStage(stage)
		if e.CatalogRead().state != before {
			t.Fatal("denied EXECUTE published conversion")
		}
	})
	e.mu.Lock()
	auth, refreshErr = e.authorizationLocked(op)
	e.mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.state.CurrentUser = actor
	private := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90010}, name: "private", revision: 1, Owner: e.authorizationDomain.Principal(10)}
	stage = e.BeginCatalogStage(auth.catalog)
	if err = stage.Create(private); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	e.mu.Lock()
	auth, refreshErr = e.authorizationLocked(op)
	e.mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.state.CurrentUser = actor
	if err = auth.CanUse(e.catalog.domain.Ref(auth.catalog.Epoch(), private.address)); err == nil {
		t.Fatal("private schema supplied implicit USAGE")
	}
	if err = e.SetObjectACL(context.Background(), e.catalog.domain.Ref(auth.catalog.Epoch(), private.address), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: private.Owner, Grantee: authorization.ACLGrantee{Role: actor}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, refreshErr = e.authorizationLocked(op)
	e.mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.state.CurrentUser = actor
	if err = auth.CanUse(e.catalog.domain.Ref(auth.catalog.Epoch(), private.address)); err != nil {
		t.Fatal("actual namespace USAGE grant", err)
	}
}

func TestConversionRegprocCapturedProcedurePresentation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	// Use actual registered unaccent providers for overload/name shadowing, not
	// an invented conversion callback or generic SQL CREATE FUNCTION fixture.
	first := &procedureDescriptor{address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90000}, namespace: public, revision: 1, name: "unaccent", Signature: unaccentTextSignature, RoutineKind: functionRoutine, Owner: e.authorizationDomain.Principal(10), Result: 25, Volatility: expr.Stable, Strict: true, Implementation: stockUnaccent}
	second := *first
	second.address.ObjectID, second.namespace = 90001, catalog
	stage := e.BeginCatalogStage(view)
	if err := stage.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&second); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	path := []ids.ObjectAddress{public, catalog}
	if actual := regprocSpelling(view, path, second.address.ObjectID); actual != "pg_catalog.unaccent" {
		t.Fatal("real namespace shadow output", actual)
	}
	if actual := regprocSpelling(view, path, first.address.ObjectID); actual != "unaccent" {
		t.Fatal("visible routine output", actual)
	}
	proc, _ := view.LookupType(sqltype.Regproc)
	value, err := sqltype.RegprocValue(proc, uint32(second.address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	rows := &finalRows{}
	if err = captureRegprocOutputValue(rows, view, path, value); err != nil {
		t.Fatal(err)
	}
	stage = e.BeginCatalogStage(view)
	if err = stage.Remove(e.catalog.domain.Ref(view.Epoch(), first.address)); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if actual := regprocSpelling(e.CatalogRead(), path, second.address.ObjectID); actual != "unaccent" {
		t.Fatal("fresh routine output", actual)
	}
	if actual, err := rows.FormatRegproc(uint32(second.address.ObjectID)); err != nil || actual != "pg_catalog.unaccent" {
		t.Fatal("retained REGPROC presentation changed", actual, err)
	}
	// Remove the referent itself through real catalog publication. Native SQL
	// DROP EXTENSION is unavailable; this supplements the public PostgreSQL
	// drop/reinstall witness with the native captured-identity invariant.
	view = e.CatalogRead()
	procedureType, _ := view.LookupType(sqltype.Regprocedure)
	procedureValue, err := sqltype.RegprocValue(procedureType, uint32(second.address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	if err := captureRegprocOutputValue(rows, view, path, procedureValue); err != nil {
		t.Fatal(err)
	}
	heldDescription := &StatementDescription{regtypes: &regtypeAnalysis{constants: []regtypeConstant{{offset: 7, typeID: sqltype.Regprocedure, value: procedureValue}}}}
	stage = e.BeginCatalogStage(view)
	if err := stage.Remove(e.catalog.domain.Ref(view.Epoch(), second.address)); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	binder := &expressionBinder{ctx: context.Background(), expected: heldDescription}
	heldValue, err := binder.routineConstant("pg_catalog.unaccent(text)", procedureType.Usage(), parser.Position{Offset: 7, Character: 8})
	if err != nil {
		t.Fatal("retained literal after referent removal", err)
	}
	if id, err := heldValue.ProcedureOID(); err != nil || id != uint32(second.address.ObjectID) {
		t.Fatal("retained literal identity changed", id, err)
	}
	op, auth := conversionUnitAuth(t, e)
	types, err := e.captureRegtypeInput(auth, op)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := e.captureRoutineInput(auth, types)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.ResolveRegprocedure(context.Background(), "pg_catalog.unaccent(text)"); err == nil {
		t.Fatal("fresh input resolved removed referent")
	} else {
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != `function "pg_catalog.unaccent(text)" does not exist` {
			t.Fatal("fresh removed-referent diagnostic", err)
		}
	}
	if actual := routineSpelling(e.CatalogRead(), path, second.address.ObjectID, true, false); actual != "90001" {
		t.Fatal("fresh removed-referent output", actual)
	}
	if actual, err := rows.FormatRegproc(uint32(second.address.ObjectID)); err != nil || actual != "pg_catalog.unaccent" {
		t.Fatal("retained REGPROC after referent removal", actual, err)
	}
	if actual, err := rows.FormatRegprocedure(uint32(second.address.ObjectID)); err != nil || actual != "unaccent(text)" {
		t.Fatal("retained REGPROCEDURE after referent removal", actual, err)
	}
}

func TestRoutineAliasStorageAdmission(t *testing.T) {
	e := New()
	op, _ := conversionUnitAuth(t, e)
	for _, id := range []sqltype.ID{sqltype.Regproc, sqltype.Regprocedure, 1008, 2207} {
		typ, _ := e.CatalogRead().LookupType(id)
		if err := validateQueryTableOutputType(typ); err != nil {
			t.Fatal("routine alias query storage rejected", err)
		}
	}
	for _, name := range []string{"regproc", "regprocedure"} {
		usage, err := e.resolveTableColumnType(parser.TypeName{Name: name}, op)
		if err != nil || !usage.Type.ContainsRegproc() {
			t.Fatal("routine alias declaration rejected", usage, err)
		}
	}
}
