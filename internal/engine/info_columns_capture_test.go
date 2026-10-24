package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
)

func columnsUnitPairedCapture(t *testing.T) {
	t.Helper()
	f := newColumnsUnitFixture(t)
	e := f.dc.f.e
	e.catalog.PublishLocked(f.state)
	f.dc.f.op.Session.DatabaseName = "captured_database"
	f.dc.f.actor(f.dc.f.member)
	scope, err := e.StartStatement(f.dc.f.op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old := scope.inquiry.commentCatalog
	source := old.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnsOID})
	next := catalog.CloneState(f.state)
	table := *f.table
	table.ColumnMeta.Defaults = nil
	table.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.dc.f, authorization.SelectPrivilege, 0)}}
	next.SetDescriptor(table.Address(), &table)
	roles := cloneRoleFixture(e.roles.published)
	var edges []authorization.Membership
	for _, edge := range fixtureEdges(roles) {
		if edge.Member != f.dc.f.member || edge.Role != f.dc.f.owner {
			edges = append(edges, edge)
		}
	}
	setFixtureEdges(roles, edges)
	e.catalog.PublishLocked(next)
	e.roles.published = roles
	e.mu.Lock()
	captured, err := e.openRelationSourceLocked(scope, old.Owner().Domain.Ref(old.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || captured == nil {
		t.Fatal("paired columns source", err)
	}
	defer captured.Close()
	if got := columnsUnitTable(t, captured.Anonymous); !reflect.DeepEqual(got, columnsUnitExpected()) {
		t.Fatal("mixed catalog/roles/default frontier", got)
	}
	if got := columnsUnitTable(t, columnsUnitRows(t, f.input(t, next, f.dc.f.member))); len(got) != 0 {
		t.Fatal("new membership snapshot", got)
	}
	fresh := columnsUnitTable(t, columnsUnitRows(t, f.input(t, next, f.dc.f.reader)))
	if len(fresh) != 3 || fresh["a"][5] != nil {
		t.Fatal("new ACL/default snapshot", fresh)
	}
	if got := columnsUnitTable(t, captured.Anonymous); !reflect.DeepEqual(got, columnsUnitExpected()) {
		t.Fatal("retained rows changed", got)
	}
}
