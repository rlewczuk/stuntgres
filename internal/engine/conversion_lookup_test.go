package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestConversionDefaultLookupSnapshotOrder(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	earlier := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 90010}, name: "earlier", revision: 1, owner: e.authorizationDomain.Principal(10)}
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
	scope := &StatementScope{visibilityCatalog: view, visibilityOtherPath: []ObjectAddress{earlier.address, low.Namespace()}}
	procedure, err := scope.conversionProcedure(6, 8)
	if err != nil || procedure == nil || procedure.address != low.handler || scope.conversionDefaults[[2]int{6, 8}].conversion != first {
		t.Fatal("namespace priority", procedure, err)
	}
	reversed := &StatementScope{visibilityCatalog: view, visibilityOtherPath: []ObjectAddress{low.Namespace(), earlier.address}}
	if _, err = reversed.conversionProcedure(6, 8); err != nil || reversed.conversionDefaults[[2]int{6, 8}].conversion != low {
		t.Fatal("default index OID order, not name order", err)
	}
	removedDefault := *first
	removedDefault.defaultConversion, removedDefault.revision = false, 2
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
	node := binaryExpression{expressionInfo: expressionInfo{usage: bytea.Usage()}, plan: plan}
	values := []sqltype.Value{sqltype.String(text, "é"), sqltype.String(name, "LATIN1")}
	result, err := node.evalConversion(&EvalContext{Context: context.Background(), statementScope: fresh}, values)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := result.Binary()
	if err != nil || raw != "\xe9" {
		t.Fatal("actual selected handler output", raw, err)
	}
	missing := &StatementScope{visibilityCatalog: view, visibilityOtherPath: []ObjectAddress{}}
	if _, err = node.evalConversion(&EvalContext{Context: context.Background(), statementScope: missing}, values); err == nil {
		t.Fatal("hardwired provider fallback repaired missing default")
	}
	values[0] = sqltype.String(text, "")
	if _, err = node.evalConversion(&EvalContext{Context: context.Background()}, values); err != nil {
		t.Fatal("empty input demanded default snapshot", err)
	}
}

func TestConversionDefaultLookupExecuteACL(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	actor := auth.roles.LookupRef("pg_monitor")
	auth.state.CurrentUser = actor
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: ProcedureClassID, ObjectID: 4375}}
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
	create := &parser.CreateConversion{Name: conversionUnitName("public", "denied"), Source: "UTF8", Destination: "LATIN1", Function: conversionUnitName("pg_catalog", "utf8_to_iso8859_1")}
	stage := e.BeginCatalogStage(auth.catalog)
	before := auth.catalog.state
	e.mu.Lock()
	err := e.stageCreateConversionLocked(op, auth, stage, create)
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function pg_catalog.utf8_to_iso8859_1" {
		t.Fatal("actual routine ACL denial", err)
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().state != before {
		t.Fatal("denied EXECUTE published conversion")
	}
	private := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 90010}, name: "private", revision: 1, owner: e.authorizationDomain.Principal(10)}
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
	if err = auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: private.address}); err == nil {
		t.Fatal("private schema supplied implicit USAGE")
	}
	if err = e.SetObjectACL(context.Background(), ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: private.address}, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: private.owner, Grantee: authorization.ACLGrantee{Role: actor}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, refreshErr = e.authorizationLocked(op)
	e.mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.state.CurrentUser = actor
	if err = auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: private.address}); err != nil {
		t.Fatal("actual namespace USAGE grant", err)
	}
}

func TestConversionRegprocCapturedProcedurePresentation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	public := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	catalog := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
	// Use actual registered unaccent providers for overload/name shadowing, not
	// an invented conversion callback or generic SQL CREATE FUNCTION fixture.
	first := &procedureDescriptor{address: ObjectAddress{ClassID: ProcedureClassID, ObjectID: 90000}, namespace: public, revision: 1, name: "unaccent", signature: unaccentTextSignature, owner: e.authorizationDomain.Principal(10), result: 25, volatility: Stable, strict: true, provider: stockUnaccent}
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
	path := []ObjectAddress{public, catalog}
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
	if err = stage.Remove(ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: first.address}); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if actual := regprocSpelling(e.CatalogRead(), path, second.address.ObjectID); actual != "unaccent" {
		t.Fatal("fresh routine output", actual)
	}
	if actual, err := rows.FormatRegproc(uint32(second.address.ObjectID)); err != nil || actual != "pg_catalog.unaccent" {
		t.Fatal("retained REGPROC presentation changed", actual, err)
	}
}

func TestConversionRegprocStorageBoundary(t *testing.T) {
	e := New()
	op, _ := conversionUnitAuth(t, e)
	typ, _ := e.CatalogRead().LookupType(sqltype.Regproc)
	if err := validateQueryTableOutputType(typ); err == nil {
		t.Fatal("derived REGPROC admitted as query storage")
	}
	_, err := e.resolveTableColumnType(parser.TypeName{Name: "regproc"}, op)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
		t.Fatal("REGPROC table declaration admitted", err)
	}
}
