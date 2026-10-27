package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func subscriptionInput(t *testing.T, e *core.Engine, body func(pgcatalog.Input, core.AuthorizationView)) {
	t.Helper()
	op,done,err := enginetest.Operation(e,context.Background())
	if err != nil { t.Fatal(err) }; defer done()
	e.Mu.Lock(); defer e.Mu.Unlock()
	auth,err := e.AuthorizationLocked(op)
	if err != nil { t.Fatal(err) }
	a := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:6100}
	body(pgcatalog.Input{Context:op.Context(),Owner:auth.Owner,Catalog:auth.Catalog,Roles:auth.Roles,Ref:auth.Ref(a),Source:auth.Catalog.MustRelation(a),Runtime:core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth})},auth)
}

func TestPGSubscriptionRegistrationFacts(t *testing.T) {
	e,other := engine.New(),engine.New()
	v := e.CatalogRead()
	a := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:6100}
	d := v.MustRelation(a)
	if d.Name() != "pg_subscription" || d.Namespace().ObjectID != 11 || d.RowType != 6101 || d.RelationKind != catalog.CatalogRelation || d.VirtualView || d.CatalogView || !catalog.SharedRelation(d) || !d.History.HasIndexes || d.ColumnMeta.HighWater != 18 || len(d.Columns) != 18 || !pgcatalog.RegisteredSource(v,d) { t.Fatal("subscription registration",d) }
	if _,ok := pgcatalog.ProducerFor(6100); !ok { t.Fatal("missing producer") }
	names := []string{"oid","subdbid","subskiplsn","subname","subowner","subenabled","subbinary","substream","subtwophasestate","subdisableonerr","subpasswordrequired","subrunasowner","subfailover","subconninfo","subslotname","subsynccommit","subpublications","suborigin"}
	types := []sqltype.ID{26,26,3220,19,26,16,16,18,18,16,16,16,16,25,19,25,1009,25}
	for i,c := range d.Columns {
		typ,ok := v.LookupType(types[i]); fact := d.ColumnMeta.CatalogAttributes[i]
		collation,dims := uint32(0),int16(0)
		if i == 3 || i >= 13 { collation = 950 }; if i == 16 { dims = 1 }
		if !ok || c.Name != names[i] || c.Usage.Type != typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.Nullable != (i == 14 || i == 17) || !c.NullabilityKnown || c.OriginTable != 6100 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i || fact.NotNull == c.Nullable || fact.Collation != collation || fact.Dimensions != dims { t.Fatal("subscription attribute",i,c,fact) }
		acl,granted := d.Security.ColumnACL[int32(i+1)]
		if granted != (i != 13) { t.Fatal("PUBLIC column grant",i,acl) }
		if granted && (!acl.Explicit || len(acl.Entries) != 1 || acl.Entries[0].Grantee != (authorization.ACLGrantee{Public: true}) || acl.Entries[0].Grantor != d.Owner || acl.Entries[0].Privileges != authorization.SelectPrivilege || acl.Entries[0].GrantOptions != 0) { t.Fatal("column ACL",i,acl) }
		foreign,_ := other.CatalogRead().LookupType(types[i]); if foreign == typ { t.Fatal("shared type ownership",i) }
	}
	if d.Owner != e.RoleStore.Domain.Principal(10) || d.Owner == other.CatalogRead().MustRelation(a).Owner || !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || len(d.Security.ColumnACL) != 17 { t.Fatal("bootstrap authority",d.Security) }
	ownerACL := d.Security.ACL.Entries[0]
	tablePrivileges := authorization.InsertPrivilege | authorization.SelectPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.ReferencesPrivilege | authorization.TriggerPrivilege | authorization.MaintainPrivilege
	if ownerACL.Grantor != d.Owner || ownerACL.Grantee != (authorization.ACLGrantee{Role: d.Owner}) || ownerACL.Privileges != tablePrivileges || ownerACL.GrantOptions != 0 { t.Fatal("owner table ACL",ownerACL) }
	row := v.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:6101})
	array := v.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:10112})
	if row.Array != 10112 || array.SQLType.Element() != row.SQLType || row.OwningRelation != a || array.OwningRelation != a || row.Owner != d.Owner || array.Owner != d.Owner { t.Fatal("row array ownership",row,array) }
	if err := core.RelationProvider.Validate(v,d); err != nil { t.Fatal(err) }
	// Publish an ACL change in just one engine; never mutate published maps.
	ref := e.Catalog.Domain.Ref(v.Epoch(),a); ref.Address.SubID = 14
	acl := authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:d.Owner,Grantee:authorization.ACLGrantee{Public:true},Privileges:authorization.SelectPrivilege}}}
	if err := enginetest.SetObjectACL(e,context.Background(),ref,acl); err != nil { t.Fatal(err) }
	if _,ok := other.CatalogRead().MustRelation(a).Security.ColumnACL[14]; ok { t.Fatal("ACL map crossed engines") }
	if _,ok := d.Security.ColumnACL[14]; ok { t.Fatal("ACL changed retained root") }
}

func TestPGSubscriptionColumnAuthority(t *testing.T) {
	e := engine.New(); aclUnitRoles(e,"subscription_unit_reader")
	v := e.CatalogRead(); roles := e.Roles(); a := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:6100}; d := v.MustRelation(a)
	auth := e.AuthorityView(access.View{Owner:&e.Catalog.Owner,PairedCatalog:v,Catalog:v,Roles:roles,RoleRecord:e.RoleStore.Published,State:authorization.RoleState{CurrentUser:roles.LookupRef("subscription_unit_reader")}})
	ref := auth.Ref(a)
	whole,err := auth.Effective(ref,false)
	if err != nil || whole&authorization.SelectPrivilege != 0 { t.Fatal("unexpected whole-table SELECT",whole,err) }
	var public,all []int
	for i := range d.Columns {
		all = append(all,i); if i != 13 { public = append(public,i) }
		column := ref; column.Address.SubID = int32(i+1)
		rights,err := auth.Effective(column,false)
		if err != nil || (rights&authorization.SelectPrivilege != 0) != (i != 13) { t.Fatal("effective column",i,rights,err) }
	}
	for _,indices := range [][]int{nil,public,{0},{17}} { if err := auth.CheckColumns(ref,d,authorization.SelectPrivilege,indices); err != nil { t.Fatal(err) } }
	for _,indices := range [][]int{{13},{0,13},all} {
		err := auth.CheckColumns(ref,d,authorization.SelectPrivilege,indices)
		enginetest.State(t,err,"42501")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Message != "permission denied for table pg_subscription" { t.Fatal(err) }
	}
	super := catalogAuthorization(e,v)
	if err := super.CheckColumns(ref,d,authorization.SelectPrivilege,all); err != nil { t.Fatal(err) }
	// Publish real membership candidates, then resolve the inherited predefined
	// privilege through the ordinary authority view.
	for _, revoke := range []bool{false,true} {
		e.Mu.Lock()
		current := e.RoleStore.Published
		read := e.RoleStore.PublishedRead(&e.Catalog.Owner,e.Catalog.Current())
		root,changed,_,err := authorization.MembershipCandidate(context.Background(),read,authorization.RoleState{CurrentUser:d.Owner},&parser.RoleMembership{Targets:[]parser.RoleSpec{{Name:"pg_read_all_data"}},Members:[]parser.RoleSpec{{Name:"subscription_unit_reader"}},Revoke:revoke},nil,true,true)
		if err == nil && changed {
			next := current.Advance()
			next.Root = root
			e.RoleStore.PublishLocked(next)
		}
		e.Mu.Unlock()
		if err != nil || !changed { t.Fatal("membership publication",changed,err) }
		fresh := e.CatalogRead()
		paired := e.RoleStore.PublishedRead(&e.Catalog.Owner,fresh)
		next := e.AuthorityView(access.View{Owner:&e.Catalog.Owner,PairedCatalog:fresh,Catalog:fresh,Roles:paired,RoleRecord:e.RoleStore.Published,State:auth.State})
		err = next.CheckColumns(next.Ref(a),fresh.MustRelation(a),authorization.SelectPrivilege,all)
		if revoke { enginetest.State(t,err,"42501") } else if err != nil { t.Fatal(err) }
	}
	for _,subID := range []int32{14,0} {
		grant := ref; grant.Address.SubID = subID
		acl := authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:d.Owner,Grantee:authorization.ACLGrantee{Role:roles.LookupRef("subscription_unit_reader")},Privileges:authorization.SelectPrivilege}}}
		if err := enginetest.SetObjectACL(e,context.Background(),grant,acl); err != nil { t.Fatal(err) }
		fresh := e.CatalogRead()
		next := e.AuthorityView(access.View{Owner:&e.Catalog.Owner,PairedCatalog:fresh,Catalog:fresh,Roles:e.RoleStore.PublishedRead(&e.Catalog.Owner,fresh),RoleRecord:e.RoleStore.Published,State:auth.State})
		if err := next.CheckColumns(next.Ref(a),fresh.MustRelation(a),authorization.SelectPrivilege,all); err != nil { t.Fatal(err) }
		if err := enginetest.SetObjectACL(e,context.Background(),grant,authorization.ACLValue{}); err != nil { t.Fatal(err) }
	}
}

func TestPGSubscriptionCaptureOwnership(t *testing.T) {
	produce,_ := pgcatalog.ProducerFor(6100)
	e,foreign := engine.New(),engine.New()
	var other pgcatalog.Input
	subscriptionInput(t,foreign,func(in pgcatalog.Input,_ core.AuthorizationView) { other = in })
	retained := e.Roles(); aclUnitRoles(e,"subscription_new_root")
	subscriptionInput(t,e,func(in pgcatalog.Input,auth core.AuthorizationView) {
		descriptors,names,roles := maps.Collect(in.Catalog.State().Descriptors()),maps.Collect(in.Catalog.State().Names()),e.RoleStore.Published
		rows,err := produce(in)
		if err != nil || rows == nil || len(rows) != 0 { t.Fatal("empty producer",rows,err) }
		got,err := core.CaptureCatalogSourceRows(in,produce)
		if err != nil || got == nil || got.Anonymous == nil || len(got.Anonymous) != 0 || got.Ref != in.Ref { t.Fatal("empty wrapped source",got,err) }
		cases := []struct{name string; mutate func(*pgcatalog.Input)}{
			{"nil context",func(b *pgcatalog.Input){b.Context=nil}},
			{"nil owner",func(b *pgcatalog.Input){b.Owner=nil}},
			{"foreign owner",func(b *pgcatalog.Input){b.Owner=other.Owner}},
			{"foreign domain",func(b *pgcatalog.Input){b.Ref=other.Owner.Domain.Ref(in.Ref.Epoch,in.Ref.Address)}},
			{"epoch",func(b *pgcatalog.Input){b.Ref.Epoch--}},
			{"class",func(b *pgcatalog.Input){b.Ref.Address.ClassID=ids.TypeClassID}},
			{"object",func(b *pgcatalog.Input){b.Ref.Address.ObjectID++}},
			{"subobject",func(b *pgcatalog.Input){b.Ref.Address.SubID=1}},
			{"nil roles",func(b *pgcatalog.Input){b.Roles=authorization.Read{}}},
			{"foreign roles",func(b *pgcatalog.Input){b.Roles=other.Roles}},
			{"old roles",func(b *pgcatalog.Input){b.Roles=retained}},
			{"nil runtime",func(b *pgcatalog.Input){b.Runtime=nil}},
			{"foreign runtime",func(b *pgcatalog.Input){b.Runtime=other.Runtime}},
			{"nil engine",func(b *pgcatalog.Input){b.Runtime=core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth:auth})}},
			{"foreign engine",func(b *pgcatalog.Input){b.Runtime=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:foreign,Auth:auth})}},
			{"catalog root",func(b *pgcatalog.Input){b.Catalog=in.Catalog.WithState(catalog.CloneState(in.Catalog.State()))}},
			{"nil source",func(b *pgcatalog.Input){b.Source=nil}},
		}
		for _,tc := range cases { t.Run(tc.name,func(t *testing.T){ bad:=in; tc.mutate(&bad); raw,err:=produce(bad); subscriptionInvariant(t,raw==nil,err); wrapped,err:=core.CaptureCatalogSourceRows(bad,produce); subscriptionInvariant(t,wrapped==nil,err) }) }
		d := in.Source
		replacement := *d
		badSources := []*catalog.Relation{&replacement}
		for _,width := range []int{17,19} { next:=*d; next.Columns=append([]catalog.Column(nil),d.Columns...); if width==17 {next.Columns=next.Columns[:17]} else {next.Columns=append(next.Columns,d.Columns[0])}; badSources=append(badSources,&next) }
		name:=*d; name.ObjectName="impostor"; badSources=append(badSources,&name)
		attribute:=*d; attribute.Attributes=append([]int32(nil),d.Attributes...); attribute.Attributes[0]++; badSources=append(badSources,&attribute)
		for i := range d.Columns { next:=*d; next.Columns=append([]catalog.Column(nil),d.Columns...); next.Columns[i].Usage.Type,_=other.Catalog.LookupType(d.Columns[i].Usage.Type.ID()); badSources=append(badSources,&next) }
		for _,source := range badSources { bad:=in; bad.Source=source; raw,err:=produce(bad); subscriptionInvariant(t,raw==nil,err) }
		if !maps.Equal(descriptors,maps.Collect(in.Catalog.State().Descriptors())) || !maps.Equal(names,maps.Collect(in.Catalog.State().Names())) || roles != e.RoleStore.Published { t.Fatal("capture mutated roots") }
	})
}

func subscriptionInvariant(t *testing.T, nilRows bool, err error) {
	t.Helper()
	if !nilRows || err == nil { t.Fatal("invalid capture not atomic",nilRows,err) }
	var state interface{SQLState() string}; if errors.As(err,&state) { t.Fatal("invariant fabricated SQLSTATE",err) }
}

func TestPGSubscriptionCaptureCancellation(t *testing.T) {
	e:=engine.New(); produce,_:=pgcatalog.ProducerFor(6100)
	subscriptionInput(t,e,func(in pgcatalog.Input,_ core.AuthorizationView){
		before:=maps.Collect(in.Catalog.State().Descriptors()); names:=maps.Collect(in.Catalog.State().Names()); roles:=e.RoleStore.Published
		ctx,cancel:=context.WithCancel(in.Context); cancel(); in.Context=ctx
		raw,err:=produce(in); subscriptionInvariant(t,raw==nil,err); if !errors.Is(err,context.Canceled){t.Fatal(err)}
		rows,err:=core.CaptureCatalogSourceRows(in,produce); subscriptionInvariant(t,rows==nil,err); if !errors.Is(err,context.Canceled){t.Fatal(err)}
		if !reflect.DeepEqual(before,maps.Collect(in.Catalog.State().Descriptors())) || !maps.Equal(names,maps.Collect(in.Catalog.State().Names())) || roles!=e.RoleStore.Published {t.Fatal("canceled capture mutated roots")}
	})
}
