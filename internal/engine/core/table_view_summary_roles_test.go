package core_test

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
