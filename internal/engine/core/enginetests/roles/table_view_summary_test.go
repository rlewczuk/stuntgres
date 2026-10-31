package roles_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
)

func TestTableSummaryColumnACLNeutrality(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER summary_reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE summary_denied(a INT,b INT)")
	ref := enginetest.RoleTableRef(t, e, "summary_denied")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	reader, _ := e.Roles().Lookup("summary_reader")
	column := ref
	column.Address.SubID = 1
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	for _, acl := range []authorization.ACLValue{grant, {Explicit: true}} {
		if err := enginetest.SetObjectACL(e, context.Background(), column, acl); err != nil {
			t.Fatal(err)
		}
		catalog := e.CatalogRead()
		source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12003})
		e.Mu.Lock()
		captured, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: catalog, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, catalog), Ref: ids.ObjectRef{}, Source: source, Owner: &e.Catalog.Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Presentation: stored.Presentation{}})}, captureTableViewSummary)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, row := range captured.Anonymous {
			name, err := row[1].Text()
			if err != nil {
				t.Fatal(err)
			}
			if name == "summary_denied" {
				count++
				owner, err := row[2].Text()
				if err != nil || owner != "postgres" || !row[3].IsNull() {
					t.Fatal("ACL affected logical summary", owner, err, row)
				}
			}
		}
		if count != 1 {
			t.Fatal("column ACL changed summary membership", count)
		}
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION summary_reader")
	enginetest.RoleError(t, e, op, "SELECT a FROM summary_denied", "42501", "permission denied for table summary_denied", "")
}

// Unit tests name producers by their former engine identifiers. Each one is
// resolved through the real pgcatalog registry, so a missing registration
// fails at package initialization instead of silently testing another path.
var (
	captureAvailableExtensions = enginetest.RegisteredCatalogProducer(12081)
	captureDataTypePrivileges  = enginetest.RegisteredCatalogProducer(16201)
	captureExtensionCatalog    = enginetest.RegisteredCatalogProducer(1255)
	capturePGAM                = enginetest.RegisteredCatalogProducer(2601)
	capturePGAggregate         = enginetest.RegisteredCatalogProducer(2600)
	capturePGAttribute         = enginetest.RegisteredCatalogProducer(1249)
	capturePGCast              = enginetest.RegisteredCatalogProducer(2605)
	capturePGCollation         = enginetest.RegisteredCatalogProducer(3456)
	capturePGConstraint        = enginetest.RegisteredCatalogProducer(2606)
	capturePGCursors           = enginetest.RegisteredCatalogProducer(12077)
	capturePGDatabase          = enginetest.RegisteredCatalogProducer(1262)
	capturePGDefaultACL        = enginetest.RegisteredCatalogProducer(826)
	capturePGDepend            = enginetest.RegisteredCatalogProducer(2608)
	capturePGDescription       = enginetest.RegisteredCatalogProducer(2609)
	capturePGEnum              = enginetest.RegisteredCatalogProducer(3501)
	capturePGGroup             = enginetest.RegisteredCatalogProducer(12008)
	capturePGIndexes           = enginetest.RegisteredCatalogProducer(12002)
	capturePGNamespace         = enginetest.RegisteredCatalogProducer(2615)
	capturePGOpclass           = enginetest.RegisteredCatalogProducer(2616)
	capturePGOperator          = enginetest.RegisteredCatalogProducer(2617)
	capturePGParameterACL      = enginetest.RegisteredCatalogProducer(6243)
	capturePGRange             = enginetest.RegisteredCatalogProducer(3541)
	capturePGRoles             = enginetest.RegisteredCatalogProducer(12007)
	capturePGSequence          = enginetest.RegisteredCatalogProducer(2224)
	capturePGSettings          = enginetest.RegisteredCatalogProducer(16202)
	capturePGShDescription     = enginetest.RegisteredCatalogProducer(2396)
	capturePGTablespace        = enginetest.RegisteredCatalogProducer(1213)
	capturePGType              = enginetest.RegisteredCatalogProducer(1247)
	capturePGUser              = enginetest.RegisteredCatalogProducer(12014)
	capturePreparedSource      = enginetest.RegisteredCatalogProducer(12001)
	captureTablePrivileges     = enginetest.RegisteredCatalogProducer(16200)
	captureTableViewSummary    = enginetest.RegisteredCatalogProducer(12000)
	captureTimezoneSource      = enginetest.RegisteredCatalogProducer(12004)
)
