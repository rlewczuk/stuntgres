package core_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGAuthMembersSourceDescriptor(t *testing.T) {
	e := engine.New()
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:1261})
	if d.VirtualView || d.CatalogView || d.RelationKind != catalog.CatalogRelation || d.RowType != 2843 || len(d.Columns) != 7 || d.ColumnMeta.HighWater != 7 { t.Fatal("membership descriptor") }
	names := []string{"oid","roleid","member","grantor","admin_option","inherit_option","set_option"}
	for i,c := range d.Columns {
		typ := sqltype.Oid
		if i >= 4 { typ = sqltype.Bool }
		if c.Name != names[i] || c.Usage.Type.ID() != typ || c.Nullable || !c.NullabilityKnown || c.OriginTable != 1261 || c.OriginAttribute != int16(i+1) || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i { t.Fatal("physical origin",i,c) }
		fact := d.ColumnMeta.CatalogAttributes[i]
		if !fact.NotNull || fact.Collation != 0 || fact.Dimensions != 0 { t.Fatal("attribute",i,fact) }
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege { t.Fatal("PUBLIC SELECT") }
	if err := core.RelationProvider.Validate(e.CatalogRead(),d); err != nil { t.Fatal(err) }
	for _,id := range []ids.OID{2843,10058} { if e.Catalog.State.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:id}).OwningRelation != d.Address() { t.Fatal("row/array ownership") } }
	if !pgcatalog.SummaryTable(1261) || !pgcatalog.CatalogHasIndexes(1261) || pgcatalog.SummaryViewDefinition(1261) != "" { t.Fatal("real-table classification") }
}

func TestPGAuthMembersSourceCapture(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d := view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:1261})
	ref := e.Catalog.Domain.Ref(view.Epoch(),d.Address())
	state := emptyRoleFixture(e)
	parent := pgGroupUnitRole(e,state,20000,"parent",false)
	member := pgGroupUnitRole(e,state,20001,"member",true)
	grantor := pgGroupUnitRole(e,state,20002,"grantor",false)
	setFixtureEdges(state,[]authorization.Membership{{Role:parent,Member:member,Grantor:grantor},{Role:parent,Member:member,Grantor:parent,Admin:true,Inherit:true,Set:true}})
	root, _, err := state.Root.AssignMembershipOIDs(30000)
	if err != nil { t.Fatal(err) }
	state.Root = root
	in := pgcatalog.Input{Context:context.Background(),Catalog:view,Owner:&e.Catalog.Owner,Roles:e.RoleStore.Read(&e.Catalog.Owner,view,state),Ref:ref,Source:d,Runtime:core.EngineCatalogRuntime{}}
	produce := registeredCatalogProducer(1261)
	rows, err := produce(in)
	if err != nil || len(rows) != 2 { t.Fatal(rows,err) }
	before := make([][]sqltype.Value,len(rows))
	for i,row := range rows { before[i] = append([]sqltype.Value(nil),row...) }
	renamed := cloneRoleFixture(state)
	parentDescriptor := fixtureRole(renamed,parent)
	parentDescriptor.Name = "renamed_parent"
	renamed.Root, err = renamed.Root.PutDescriptor(parentDescriptor)
	if err != nil { t.Fatal(err) }
	renameInput := in
	renameInput.Roles = e.RoleStore.Read(&e.Catalog.Owner,view,renamed)
	renameRows, err := produce(renameInput)
	if err != nil || !reflect.DeepEqual(renameRows,before) { t.Fatal("rename changed membership identities or options",err) }
	descriptor, ok := renameInput.Roles.Lookup("renamed_parent")
	if !ok || descriptor.Ref != parent { t.Fatal("rename replaced principal identity",descriptor) }
	if _, ok := renameInput.Roles.Lookup("parent"); ok { t.Fatal("rename retained old name") }
	if original, ok := in.Roles.Lookup("parent"); !ok || original.Ref != parent { t.Fatal("rename changed retained authority") }
	rolesSource := view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:12007})
	renameInput.Source = rolesSource
	renameInput.Ref = e.Catalog.Domain.Ref(view.Epoch(),rolesSource.Address())
	rolesRows, err := registeredCatalogProducer(12007)(renameInput)
	if err != nil { t.Fatal(err) }
	oidColumn, ok := rolesSource.Index["oid"]
	if !ok { t.Fatal("pg_roles lacks oid column") }
	matches := 0
	for _, row := range rolesRows {
		if len(row) != len(rolesSource.Columns) { t.Fatal("pg_roles producer row shape",len(row)) }
		id, err := row[oidColumn].OID()
		if err != nil { t.Fatal(err) }
		if uint64(id) == parent.ID() {
			name, err := row[0].Text()
			if err != nil || name != "renamed_parent" { t.Fatal("projected renamed role",name,err) }
			matches++
		}
	}
	if matches != 1 { t.Fatal("renamed role projection multiplicity",matches) }
	updated := cloneRoleFixture(state)
	setFixtureEdges(updated,nil)
	e.RoleStore.PublishLocked(updated)
	again, err := produce(in)
	if err != nil || !reflect.DeepEqual(again,before) || !reflect.DeepEqual(rows,before) { t.Fatal("retained capture",err) }
	empty := in
	empty.Roles = e.RoleStore.Read(&e.Catalog.Owner,view,updated)
	if got, err := produce(empty); err != nil || got == nil || len(got) != 0 { t.Fatal("typed empty",got,err) }
	for _,remaining := range []int{1,3,4} {
		bad := in
		bad.Context = &dtpCancelContext{Context:context.Background(),remaining:remaining}
		if got,err := produce(bad); got != nil || !errors.Is(err,context.Canceled) { t.Fatal("cancellation prefix",got,err) }
	}
	foreign := engine.New()
	for _,mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input){ in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input){ in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input){ in.Source = nil },
		func(in *pgcatalog.Input){ copy := *d; copy.Columns = copy.Columns[:6]; in.Source = &copy },
		func(in *pgcatalog.Input){ in.Ref = foreign.Catalog.Domain.Ref(view.Epoch(),d.Address()) },
	} {
		bad := in
		mutate(&bad)
		if got,err := produce(bad); got != nil || err == nil { t.Fatal("malformed source accepted",got,err) }
	}
	unassigned := cloneRoleFixture(state)
	setFixtureEdges(unassigned,[]authorization.Membership{{Role:parent,Member:member,Grantor:grantor}})
	stale := cloneRoleFixture(state)
	grantorDescriptor := fixtureRole(stale,grantor)
	stale.Root = stale.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{grantor:grantorDescriptor})
	for _, badState := range []*access.RoleRecord{unassigned,stale} {
		bad := in
		bad.Roles = e.RoleStore.Read(&e.Catalog.Owner,view,badState)
		if got,err := produce(bad); got != nil || err == nil { t.Fatal("invalid stored edge accepted",got,err) }
	}
	highState := cloneRoleFixture(state)
	high := pgGroupUnitRole(e,highState,math.MaxUint32+1,"overflow",true)
	setFixtureEdges(highState,[]authorization.Membership{{Role:parent,Member:high,Grantor:grantor}})
	highState.Root, _, err = highState.Root.AssignMembershipOIDs(30002)
	if err != nil { t.Fatal(err) }
	bad := in
	bad.Roles = e.RoleStore.Read(&e.Catalog.Owner,view,highState)
	if got,err := produce(bad); got != nil || err == nil { t.Fatal("truncated principal",got,err) }
}
