package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/table"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestConversionDefaultLookupSnapshotOrder(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	earlier := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90010}, ObjectName: "earlier", ObjectRevision: 1, Owner: e.RoleStore.Domain.Principal(10)}
	low := conversionUnitDefinition(e, 90000, "z_low_oid", true)
	high := conversionUnitDefinition(e, 90001, "a_high_oid", true)
	first := conversionUnitDefinition(e, 90002, "first_namespace", true)
	first.ObjectNamespace = earlier.Address()
	nondefault := conversionUnitDefinition(e, 90003, "shadow_nondefault", false)
	nondefault.ObjectNamespace = earlier.Address()
	stage := e.BeginCatalogStage(view)
	for _, d := range []catalog.Descriptor{earlier, low, high, first, nondefault} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	scope := &StatementScope{Inquiry: inquirySnapshot{visibilityCatalog: view, visibilityOtherPath: []ids.ObjectAddress{earlier.Address(), low.Namespace()}}}
	procedure, err := scope.conversionProcedure(6, 8)
	if err != nil || procedure == nil || procedure.Address() != low.Handler || scope.presentationOrZero().conversionDefaults[[2]int{6, 8}].conversion != first {
		t.Fatal("namespace priority", procedure, err)
	}
	reversed := &StatementScope{Inquiry: inquirySnapshot{visibilityCatalog: view, visibilityOtherPath: []ids.ObjectAddress{low.Namespace(), earlier.Address()}}}
	if _, err = reversed.conversionProcedure(6, 8); err != nil || reversed.presentationOrZero().conversionDefaults[[2]int{6, 8}].conversion != low {
		t.Fatal("default index OID order, not name order", err)
	}
	removedDefault := *first
	removedDefault.DefaultConversion, removedDefault.ObjectRevision = false, 2
	stage = e.BeginCatalogStage(view)
	if err = stage.Replace(&removedDefault); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	fresh := &StatementScope{Inquiry: inquirySnapshot{visibilityCatalog: e.CatalogRead(), visibilityOtherPath: scope.Inquiry.visibilityOtherPath}}
	if _, err = fresh.conversionProcedure(6, 8); err != nil || fresh.presentationOrZero().conversionDefaults[[2]int{6, 8}].conversion != low {
		t.Fatal("nondefault repaired lookup", err)
	}
	if _, err = scope.conversionProcedure(6, 8); err != nil || scope.presentationOrZero().conversionDefaults[[2]int{6, 8}].conversion != first {
		t.Fatal("old execution snapshot changed", err)
	}
	text, _ := view.LookupType(sqltype.Text)
	name, _ := view.LookupType(sqltype.Name)
	bytea, _ := view.LookupType(sqltype.Bytea)
	binaryPlan, err := sqltype.BindBinary("convert_to", bytea.Usage(), text.Usage(), name.Usage())
	if err != nil {
		t.Fatal(err)
	}
	node := functions.Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: binaryPlan, Args: []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "é")},
		plan.LiteralExpr{Info: expr.Info{Usage: name.Usage()}, Value: sqltype.String(name, "LATIN1")},
	}}
	result, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: HostOf(fresh)}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := result.Binary()
	if err != nil || raw != "\xe9" {
		t.Fatal("actual selected handler output", raw, err)
	}
	missing := &StatementScope{Inquiry: inquirySnapshot{visibilityCatalog: view, visibilityOtherPath: []ids.ObjectAddress{}}}
	if _, err = node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: HostOf(missing)}}); err == nil {
		t.Fatal("hardwired provider fallback repaired missing default")
	}
	node.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "")}
	if _, err = node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err != nil {
		t.Fatal("empty input demanded default snapshot", err)
	}
}

func TestConversionDefaultLookupExecuteACL(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	actor := auth.Roles.LookupRef("pg_monitor")
	auth.State.CurrentUser = actor
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4375})
	if err := auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege}); err != nil {
		t.Fatal("real PUBLIC EXECUTE default", err)
	}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, refreshErr := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.State.CurrentUser = actor
	var stage *CatalogStage
	var err error
	withPublicSchemaUnitOwner(t, e, "pg_monitor", func() {
		e.Mu.Lock()
		auth, refreshErr = e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if refreshErr != nil {
			t.Fatal(refreshErr)
		}
		auth.State.CurrentUser = actor
		create := &parser.CreateConversion{Name: conversionUnitName("public", "denied"), Source: "UTF8", Destination: "LATIN1", Function: conversionUnitName("pg_catalog", "utf8_to_iso8859_1")}
		stage = e.BeginCatalogStage(auth.Catalog)
		before := auth.Catalog.State()
		e.Mu.Lock()
		err = typedef.StageCreateConversionLocked(TypeHost{TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, create)
		e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function pg_catalog.utf8_to_iso8859_1" {
			t.Fatal("actual routine ACL denial", err)
		}
		e.DiscardCatalogStage(stage)
		if e.CatalogRead().State() != before {
			t.Fatal("denied EXECUTE published conversion")
		}
		if err = e.SetObjectACL(context.Background(), ref, authorization.ACLValue{}); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, refreshErr = e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if refreshErr != nil {
			t.Fatal(refreshErr)
		}
		auth.State.CurrentUser = actor
		stage = e.BeginCatalogStage(auth.Catalog)
		e.Mu.Lock()
		err = typedef.StageCreateConversionLocked(TypeHost{TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, create)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal("restored real handler grant did not recover CREATE", err)
		}
		var created *catalog.Conversion
		for _, descriptor := range stage.Descriptors() {
			created, _ = descriptor.(*catalog.Conversion)
			if created != nil {
				break
			}
		}
		if created == nil || created.Owner != actor || created.Handler != ref.Address || created.Source != 6 || created.Destination != 8 {
			t.Fatal("granted CREATE lost actual authority/provider", created)
		}
		if e.CatalogRead().Get(created.Address()) != nil {
			t.Fatal("granted staging published before validation")
		}
		collationUnitPublish(t, e, stage)
		if e.CatalogRead().Get(created.Address()) != created {
			t.Fatal("recovered CREATE did not publish real descriptor")
		}
	})
	e.Mu.Lock()
	auth, refreshErr = e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.State.CurrentUser = actor
	private := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90010}, ObjectName: "private", ObjectRevision: 1, Owner: e.RoleStore.Domain.Principal(10)}
	stage = e.BeginCatalogStage(auth.Catalog)
	if err = stage.Create(private); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	e.Mu.Lock()
	auth, refreshErr = e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.State.CurrentUser = actor
	if err = auth.CanUse(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), private.Address())); err == nil {
		t.Fatal("private schema supplied implicit USAGE")
	}
	if err = e.SetObjectACL(context.Background(), e.Catalog.Domain.Ref(auth.Catalog.Epoch(), private.Address()), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: private.Owner, Grantee: authorization.ACLGrantee{Role: actor}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, refreshErr = e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	auth.State.CurrentUser = actor
	if err = auth.CanUse(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), private.Address())); err != nil {
		t.Fatal("actual namespace USAGE grant", err)
	}
}

func TestConversionRegprocCapturedProcedurePresentation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	catalogAddress := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	// Use actual registered unaccent providers for overload/name shadowing, not
	// an invented conversion callback or generic SQL CREATE FUNCTION fixture.
	first := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90000}, ObjectNamespace: public, ObjectRevision: 1, ObjectName: "unaccent", Signature: catalog.UnaccentTextSignature, RoutineKind: catalog.FunctionRoutine, Owner: e.RoleStore.Domain.Principal(10), Result: 25, Volatility: expr.Stable, Strict: true, Implementation: extension.StockUnaccent}
	second := *first
	second.ObjectAddress.ObjectID, second.ObjectNamespace = 90001, catalogAddress
	stage := e.BeginCatalogStage(view)
	if err := stage.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&second); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	path := []ids.ObjectAddress{public, catalogAddress}
	if actual := regobj.ProcSpelling(view, path, second.Address().ObjectID); actual != "pg_catalog.unaccent" {
		t.Fatal("real namespace shadow output", actual)
	}
	if actual := regobj.ProcSpelling(view, path, first.Address().ObjectID); actual != "unaccent" {
		t.Fatal("visible routine output", actual)
	}
	proc, _ := view.LookupType(sqltype.Regproc)
	value, err := sqltype.RegprocValue(proc, uint32(second.Address().ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	rows := &exec.FinalRows{}
	if err = regobj.CaptureProcOutputValue(rows, view, path, value); err != nil {
		t.Fatal(err)
	}
	stage = e.BeginCatalogStage(view)
	if err = stage.Remove(e.Catalog.Domain.Ref(view.Epoch(), first.Address())); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if actual := regobj.ProcSpelling(e.CatalogRead(), path, second.Address().ObjectID); actual != "unaccent" {
		t.Fatal("fresh routine output", actual)
	}
	if actual, err := rows.FormatRegproc(uint32(second.Address().ObjectID)); err != nil || actual != "pg_catalog.unaccent" {
		t.Fatal("retained REGPROC presentation changed", actual, err)
	}
	// Remove the referent itself through real catalog publication. Native SQL
	// DROP EXTENSION is unavailable; this supplements the public PostgreSQL
	// drop/reinstall witness with the native captured-identity invariant.
	view = e.CatalogRead()
	procedureType, _ := view.LookupType(sqltype.Regprocedure)
	procedureValue, err := sqltype.RegprocValue(procedureType, uint32(second.Address().ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	if err := regobj.CaptureProcOutputValue(rows, view, path, procedureValue); err != nil {
		t.Fatal(err)
	}
	heldDescription := &StatementDescription{regtypes: &regobj.Analysis{Constants: []analyze.RegtypeConstant{{Offset: 7, TypeID: sqltype.Regprocedure, Value: procedureValue}}}}
	stage = e.BeginCatalogStage(view)
	if err := stage.Remove(e.Catalog.Domain.Ref(view.Epoch(), second.Address())); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	binder := &analyze.Binder{Ctx: context.Background(), Expected: expectedOf(heldDescription)}
	heldValue, err := binder.RoutineConstant("pg_catalog.unaccent(text)", procedureType.Usage(), parser.Position{Offset: 7, Character: 8})
	if err != nil {
		t.Fatal("retained literal after referent removal", err)
	}
	if id, err := heldValue.ProcedureOID(); err != nil || id != uint32(second.Address().ObjectID) {
		t.Fatal("retained literal identity changed", id, err)
	}
	op, auth := conversionUnitAuth(t, e)
	types, err := CaptureRegtypeInput(auth, op)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := CaptureRoutineInput(auth, types)
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
	if actual := regobj.RoutineSpelling(e.CatalogRead(), path, second.Address().ObjectID, true, false); actual != "90001" {
		t.Fatal("fresh removed-referent output", actual)
	}
	if actual, err := rows.FormatRegproc(uint32(second.Address().ObjectID)); err != nil || actual != "pg_catalog.unaccent" {
		t.Fatal("retained REGPROC after referent removal", actual, err)
	}
	if actual, err := rows.FormatRegprocedure(uint32(second.Address().ObjectID)); err != nil || actual != "unaccent(text)" {
		t.Fatal("retained REGPROCEDURE after referent removal", actual, err)
	}
}

func TestRoutineAliasStorageAdmission(t *testing.T) {
	e := New()
	op, _ := conversionUnitAuth(t, e)
	for _, id := range []sqltype.ID{sqltype.Regproc, sqltype.Regprocedure, 1008, 2207} {
		typ, _ := e.CatalogRead().LookupType(id)
		if err := table.ValidateQueryTableOutputType(typ); err != nil {
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
