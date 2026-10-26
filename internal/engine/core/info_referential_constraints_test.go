package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type ircUnitFixture struct {
	f defaultUnitFixture
	state *catalog.State
	parent,child,index *catalog.Relation
}
func newIRCUnitFixture(t *testing.T)ircUnitFixture {
	t.Helper();f:=newDefaultUnitFixture(t);state:=catalog.CloneState(f.e.Catalog.State)
	address:=func(id ids.OID)ids.ObjectAddress{return ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:id}}
	parent:=&catalog.Relation{ObjectAddress:address(24800),ObjectNamespace:f.schema,ObjectName:"parent",Owner:f.owner,RelationKind:catalog.TableRelation,Constraints:catalog.RelationConstraints{Unique:[]catalog.UniqueConstraint{{OID:24810,Name:"parent_pk",Index:address(24801),Primary:true}}}}
	index:=&catalog.Relation{ObjectAddress:address(24801),ObjectNamespace:f.schema,ObjectName:"parent_pk",Owner:f.owner,SQLIndex:&catalog.IndexDefinition{Parent:parent.Address(),Unique:true}}
	child:=&catalog.Relation{ObjectAddress:address(24802),ObjectNamespace:f.schema,ObjectName:"child",Owner:f.owner,RelationKind:catalog.TableRelation,Constraints:catalog.RelationConstraints{ForeignKeys:[]catalog.ForeignKeyConstraint{{OID:24811,Name:"first",Target:parent.Address(),Index:index.Address()},{OID:24812,Name:"second",Target:parent.Address(),Index:index.Address()},{OID:24813,Name:"third",Target:parent.Address(),Index:index.Address()}}}}
	child.Security.ACL=authorization.ACLValue{Explicit:true}
	for _,d:=range []*catalog.Relation{parent,index,child}{state.SetDescriptor(d.Address(),d)}
	return ircUnitFixture{f:f,state:state,parent:parent,index:index,child:child}
}
func (f ircUnitFixture)input(t *testing.T,state *catalog.State,actor authorization.PrincipalRef)pgcatalog.Input{
	t.Helper();view:=f.f.e.Catalog.Read(state);auth:=dtpUnitAuthority(t,f.f,view);auth.State.CurrentUser=actor
	source:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.InfoReferentialConstraintsOID})
	return pgcatalog.Input{Context:t.Context(),Owner:view.Owner(),Catalog:view,Roles:auth.Roles,Source:source,Ref:view.Owner().Domain.Ref(view.Epoch(),source.Address()),Runtime:core.EngineCatalogRuntime{Auth:auth,Database:"captured_database"}}
}
func ircUnitBag(t *testing.T,rows [][]sqltype.Value)map[[9]string]int{
	t.Helper();bag:=map[[9]string]int{}
	for _,row:=range rows{if len(row)!=9{t.Fatal("projection width")};var key [9]string;for i,value:=range row{if value.IsNull(){key[i]="<NULL>";continue};base,err:=value.BaseValue();if err!=nil{t.Fatal(err)};key[i],err=base.Text();if err!=nil{t.Fatal(err)}};bag[key]++};return bag
}
func ircUnitExpected(target string)map[[9]string]int{
	bag:=map[[9]string]int{};for _,name:=range []string{"first","second","third"}{bag[[9]string{"captured_database","default_schema",name,"captured_database","default_schema",target,"NONE","NO ACTION","NO ACTION"}]++};return bag
}
func ircUnitCapture(t *testing.T,in pgcatalog.Input,want map[[9]string]int)[][]sqltype.Value{
	t.Helper();produce,ok:=pgcatalog.ProducerFor(pgcatalog.InfoReferentialConstraintsOID);if !ok{t.Fatal("missing producer")};source,err:=core.CaptureCatalogSourceRows(in,produce);if err!=nil{t.Fatal(err)};defer source.Close()
	if source.Anonymous==nil||source.View!=nil||source.Ref!=in.Ref{t.Fatal("detached capture identity")}
	for _,row:=range source.Anonymous{for i,value:=range row{if value.Type()!=in.Source.Columns[i].Usage.Type||!value.ValidResult(in.Source.Columns[i].Usage){t.Fatal("captured domain generation",i)}}}
	if got:=ircUnitBag(t,source.Anonymous);!reflect.DeepEqual(got,want){t.Fatal("exact bag",got,want)};return source.Anonymous
}

func TestInfoReferentialConstraintsCapture(t *testing.T){
	f:=newIRCUnitFixture(t);old:=f.input(t,f.state,f.f.owner);want:=ircUnitExpected("parent_pk");retained:=ircUnitCapture(t,old,want)
	changed:=catalog.CloneState(f.state);parent:=*f.parent;parent.Constraints.Unique=append([]catalog.UniqueConstraint(nil),parent.Constraints.Unique...);parent.Constraints.Unique[0].Name="renamed";changed.SetDescriptor(parent.Address(),&parent)
	child:=*f.child;child.Owner=f.f.reader;changed.SetDescriptor(child.Address(),&child)
	ircUnitCapture(t,f.input(t,changed,f.f.owner),map[[9]string]int{});ircUnitCapture(t,f.input(t,changed,f.f.reader),ircUnitExpected("renamed"))
	granted:=catalog.CloneState(changed);child.Security.ACL=authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:f.f.reader,Grantee:authorization.ACLGrantee{Role:f.f.owner},Privileges:authorization.UpdatePrivilege}}};granted.SetDescriptor(child.Address(),&child)
	ircUnitCapture(t,f.input(t,granted,f.f.owner),ircUnitExpected("renamed"));ircUnitCapture(t,old,want)
	if !reflect.DeepEqual(ircUnitBag(t,retained),want){t.Fatal("retained rows changed")}
	produce,_:=pgcatalog.ProducerFor(pgcatalog.InfoReferentialConstraintsOID)
	for _,mutate:=range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input){in.Owner=&engine.New().Catalog.Owner},
		func(in *pgcatalog.Input){in.Catalog=engine.New().CatalogRead()},
		func(in *pgcatalog.Input){in.Roles=engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})},
		func(in *pgcatalog.Input){in.Ref.Epoch++},func(in *pgcatalog.Input){in.Ref.Epoch=0},
		func(in *pgcatalog.Input){in.Ref.Address.SubID=1},func(in *pgcatalog.Input){in.Ref.Address.ObjectID++},
		func(in *pgcatalog.Input){in.Ref=new(ids.Domain).Ref(in.Ref.Epoch,in.Ref.Address)},
		func(in *pgcatalog.Input){copy:=*in.Source;in.Source=&copy},
		func(in *pgcatalog.Input){r:=in.Runtime.(core.EngineCatalogRuntime);r.Auth.RoleRecord=nil;in.Runtime=r},
		func(in *pgcatalog.Input){r:=in.Runtime.(core.EngineCatalogRuntime);r.Auth.State.CurrentUser=authorization.PrincipalRef{};in.Runtime=r},
	}{in:=old;mutate(&in);if rows,err:=produce(in);rows!=nil||err==nil{t.Fatal("invalid capture accepted",rows,err)}}
	for _,kind:=range []string{"width","domain","domain-generation","base-generation","target","index","wrong-parent","namespace","action"}{t.Run(kind,func(t *testing.T){
		state:=catalog.CloneState(f.state)
		switch kind{
		case "width","domain","domain-generation":
			source:=*old.Source;source.Columns=append([]catalog.Column(nil),source.Columns...)
			if kind=="width"{source.Columns=source.Columns[:8]}else if kind=="domain"{source.Columns[0].Usage.Type,_=old.Catalog.LookupType(sqltype.Name)}else{source.Columns[0].Usage.Type,_=engine.New().CatalogRead().LookupType(13248)};state.SetDescriptor(source.Address(),&source)
		case "base-generation":base:=*old.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:19});base.SQLType,_=engine.New().CatalogRead().LookupType(sqltype.Name);state.SetDescriptor(base.Address(),&base)
		case "target":state.RemoveDescriptor(f.parent.Address())
		case "index":state.RemoveDescriptor(f.index.Address())
		case "wrong-parent":index:=*f.index;definition:=*index.SQLIndex;definition.Parent=f.child.Address();index.SQLIndex=&definition;state.SetDescriptor(index.Address(),&index)
		case "namespace":parent:=*f.parent;parent.ObjectNamespace.ObjectID=999999;state.SetDescriptor(parent.Address(),&parent)
		case "action":child:=*f.child;child.Constraints.ForeignKeys=append([]catalog.ForeignKeyConstraint(nil),child.Constraints.ForeignKeys...);child.Constraints.ForeignKeys[1].UpdateAction=255;state.SetDescriptor(child.Address(),&child)
		}
		if rows,err:=produce(f.input(t,state,f.f.owner));rows!=nil||err==nil{t.Fatal("malformed metadata accepted",rows,err)}
	})}
	t.Run("paired_statement_frontier",func(t *testing.T){
		f:=newIRCUnitFixture(t);f.f.e.Catalog.PublishLocked(f.state);f.f.op.Session.DatabaseName="captured_database";f.f.actor(f.f.member)
		scope,err:=f.f.e.StartStatement(f.f.op);if err!=nil{t.Fatal(err)};defer func(){if err:=scope.Finish(nil);err!=nil{t.Error(err)}}()
		f.f.e.Mu.Lock();err=f.f.e.CaptureCommentSnapshotLocked(scope);f.f.e.Mu.Unlock();if err!=nil{t.Fatal(err)}
		view:=scope.Inquiry.CommentCatalog;source:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.InfoReferentialConstraintsOID})
		next:=catalog.CloneState(f.state);child:=*f.child;child.Owner=f.f.reader;next.SetDescriptor(child.Address(),&child);f.f.e.Catalog.PublishLocked(next)
		roles:=cloneRoleFixture(f.f.e.RoleStore.Published)
		var edges []authorization.Membership
		for _,edge:=range fixtureEdges(roles) {
			if edge.Role!=f.f.owner || edge.Member!=f.f.member { edges=append(edges,edge) }
		}
		setFixtureEdges(roles,edges)
		f.f.e.RoleStore.PublishLocked(roles)
		f.f.e.Mu.Lock();captured,err:=core.EngineOpenRelationSourceLocked(f.f.e,scope,view.Owner().Domain.Ref(view.Epoch(),source.Address()),source.Owner,plan.RowMark{},nil,nil);if err!=nil{t.Fatal(err)};defer captured.Close()
		if !reflect.DeepEqual(ircUnitBag(t,captured.Anonymous),ircUnitExpected("parent_pk")){t.Fatal("mixed inquiry frontier")}
		for _,row:=range captured.Anonymous{for i,value:=range row{if value.Type()!=source.Columns[i].Usage.Type{t.Fatal("mixed domain generation")}}}
		ircUnitCapture(t,f.input(t,next,f.f.member),map[[9]string]int{})
		ircUnitCapture(t,f.input(t,f.state,f.f.member),map[[9]string]int{})
	})
}

func TestInfoReferentialConstraintsCancellation(t *testing.T){
	f:=newIRCUnitFixture(t);in:=f.input(t,f.state,f.f.owner);produce,_:=pgcatalog.ProducerFor(pgcatalog.InfoReferentialConstraintsOID)
	ctx,cancel:=context.WithCancel(t.Context());cancel();in.Context=ctx
	if rows,err:=produce(in);rows!=nil||!errors.Is(err,context.Canceled){t.Fatal("precanceled capture",rows,err)}
	// Interrupt at the last actual traversal checkpoint, after earlier FKs have
	// produced a prefix; no sleeps or scheduler-dependent race is involved.
	const checkpoints=1<<30
	count:=&dtpCancelContext{Context:context.Background(),remaining:checkpoints};in.Context=count
	if rows,err:=produce(in);err!=nil||len(rows)!=3{t.Fatal("baseline",rows,err)}
	in.Context=&dtpCancelContext{Context:context.Background(),remaining:checkpoints-count.remaining}
	rows,err:=produce(in);if rows!=nil||!errors.Is(err,context.Canceled){t.Fatal("canceled prefix published",rows,err)}
	var sqlstate interface{SQLState()string};if errors.As(err,&sqlstate){t.Fatal("ordinary cancellation exposed SQLSTATE")}
}
