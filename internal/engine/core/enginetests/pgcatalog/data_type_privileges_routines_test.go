package pgcatalog_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestDataTypePrivilegesSourceValidation(t *testing.T) {
	f:=newDefaultUnitFixture(t)
	view:=f.e.CatalogRead()
	auth:=dtpUnitAuthority(t,f,view)
	source:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.DataTypePrivilegesOID})
	input:=pgcatalog.Input{Context:context.Background(),Catalog:view,Ref:view.Owner().Domain.Ref(view.Epoch(),source.Address()),Source:source,Owner:auth.Owner,Roles:auth.Roles}
	if rows,err:=captureDataTypePrivileges(input);rows!=nil||err==nil{t.Fatal("nil runtime accepted",rows,err)}
	for _,mutate:=range []func(*catalog.Relation){
		func(d *catalog.Relation){d.VirtualView=false},
		func(d *catalog.Relation){d.Columns=d.Columns[:4]},
		func(d *catalog.Relation){d.Columns[0].Name="wrong"},
		func(d *catalog.Relation){typ,_:=view.LookupType(sqltype.Name);d.Columns[0].Usage=typ.Usage()},
		func(d *catalog.Relation){d.Columns[0].Usage=d.Columns[3].Usage},
		func(d *catalog.Relation){d.Columns[0].OriginTable=0},
		func(d *catalog.Relation){d.Columns[0].OriginAttribute=2},
		func(d *catalog.Relation){d.Columns[0].Nullable=false},
		func(d *catalog.Relation){d.Columns[0].NullabilityKnown=false},
		func(d *catalog.Relation){d.Columns[0].Usage.Typmod=4},
		func(d *catalog.Relation){d.Columns[0].Usage.Collation=sqltype.CollationRef{}},
		func(d *catalog.Relation){d.Columns[0].DeclaredDimensions=1},
		func(d *catalog.Relation){d.Attributes[0]=2},
	}{
		state:=catalog.CloneState(view.State());bad:=*source
		bad.Columns=append([]catalog.Column(nil),source.Columns...);bad.Attributes=append([]int32(nil),source.Attributes...)
		mutate(&bad);state.SetDescriptor(bad.Address(),&bad)
		staged:=f.e.Catalog.Read(state)
		if rows,err:=dtpUnitCapture(context.Background(),staged,dtpUnitAuthority(t,f,staged));rows!=nil||err==nil{t.Fatal("malformed same-owner source accepted",bad,rows,err)}
	}
	// The source's declared domain must refer to the exact catalog-owned base.
	state:=catalog.CloneState(view.State())
	base:=*view.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:19})
	base.SQLType,_=view.LookupType(sqltype.Text)
	state.SetDescriptor(base.Address(),&base)
	staged:=f.e.Catalog.Read(state)
	if rows,err:=dtpUnitCapture(context.Background(),staged,dtpUnitAuthority(t,f,staged));rows!=nil||err==nil{t.Fatal("foreign source base accepted",rows,err)}
}

func dtpUnitRoutineIDs(t *testing.T,source *exec.Source,name string) []string {
	t.Helper()
	var got []string
	for _,row:=range source.Anonymous{
		n:=dtpUnitText(t,row[2]);kind:=dtpUnitText(t,row[3])
		if kind!="ROUTINE"||len(n)<len(name)+1||n[:len(name)+1]!=name+"_"{continue}
		if row[4].IsNull(){got=append(got,"NULL")}else{got=append(got,dtpUnitText(t,row[4]))}
	}
	sort.Strings(got);return got
}

func TestDataTypePrivilegesRoutineDeclarations(t *testing.T) {
	f:=newDefaultUnitFixture(t)
	for _,q:=range []string{
		"CREATE FUNCTION public.dtp_formals(IN a integer,INOUT b integer,OUT c text) LANGUAGE SQL AS 'SELECT b,a::text'",
		"CREATE PROCEDURE public.dtp_null() LANGUAGE SQL AS 'SELECT 1'",
		"REVOKE EXECUTE ON FUNCTION public.dtp_formals(integer,integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION public.dtp_formals(integer,integer) TO default_reader",
	}{enginetest.RoleExecute(t,f.e,f.op,q)}
	view:=f.e.CatalogRead();auth:=dtpUnitAuthority(t,f,view)
	captured,err:=dtpUnitCapture(context.Background(),view,auth);if err!=nil{t.Fatal(err)}
	for name,want:=range map[string][]string{"dtp_formals":{"0","1","2","3"},"dtp_null":{"NULL"}}{
		if got:=dtpUnitRoutineIDs(t,captured,name);!reflect.DeepEqual(got,want){t.Fatal(name,got,want)}
	}
	for identity,want:=range map[string][]string{"abs_1397":{"0","1"},"sum_2108":{"0","1"},"row_number_3100":{"0"},"jsonb_each_3208":{"0","1","2","3"}}{
		var got []string
		for _,row:=range captured.Anonymous{
			name:=dtpUnitText(t,row[2])
			if name==identity{got=append(got,dtpUnitText(t,row[4]))}
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got,want){t.Fatal("builtin/installed declaration",identity,got,want)}
	}
	source:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.DataTypePrivilegesOID})
	for _,row:=range captured.Anonymous{for i,v:=range row{if v.Type()!=source.Columns[i].Usage.Type{t.Fatal("unwrapped source cell",i,v)}}}
	auth.State.CurrentUser=f.reader
	held,err:=dtpUnitCapture(context.Background(),view,auth);if err!=nil{t.Fatal(err)}
	if got:=dtpUnitRoutineIDs(t,held,"dtp_formals");!reflect.DeepEqual(got,[]string{"0","1","2","3"}){t.Fatal("EXECUTE-only declaration",got)}
	before:=make([][]sqltype.Value,len(held.Anonymous))
	for i,row:=range held.Anonymous{before[i]=append([]sqltype.Value(nil),row...)}
	enginetest.RoleExecute(t,f.e,f.op,"REVOKE EXECUTE ON FUNCTION public.dtp_formals(integer,integer) FROM default_reader")
	freshView:=f.e.CatalogRead();freshAuth:=dtpUnitAuthority(t,f,freshView);freshAuth.State.CurrentUser=f.reader
	fresh,err:=dtpUnitCapture(context.Background(),freshView,freshAuth)
	if err!=nil||len(dtpUnitRoutineIDs(t,fresh,"dtp_formals"))!=0||!reflect.DeepEqual(before,held.Anonymous){t.Fatal("routine capture lifetime",err)}
	enginetest.RoleExecute(t,f.e,f.op,"ALTER FUNCTION public.dtp_formals(integer,integer) OWNER TO default_owner")
	enginetest.RoleExecute(t,f.e,f.op,"REVOKE ALL ON FUNCTION public.dtp_formals(integer,integer) FROM default_owner")
	ownedView:=f.e.CatalogRead()
	for _,principal:=range []authorization.PrincipalRef{f.owner,f.member}{
		ownedAuth:=dtpUnitAuthority(t,f,ownedView);ownedAuth.State.CurrentUser=principal
		owned,err:=dtpUnitCapture(context.Background(),ownedView,ownedAuth)
		if err!=nil||!reflect.DeepEqual(dtpUnitRoutineIDs(t,owned,"dtp_formals"),[]string{"0","1","2","3"}){t.Fatal("enabled routine ownership",principal,err)}
	}
}

type dtpFormalCancelRuntime struct {
	pgcatalog.Runtime
	cancel context.CancelFunc
	target sqltype.ID
	calls int
}
func(r *dtpFormalCancelRuntime)ValidateType(view catalog.Read,typ *catalog.Type)error{
	if err:=r.Runtime.ValidateType(view,typ);err!=nil{return err}
	if typ.SQLType.ID()==r.target{r.calls++;r.cancel()}
	return nil
}

func TestDataTypePrivilegesFormalCancellation(t *testing.T){
	f:=newDefaultUnitFixture(t)
	enginetest.RoleExecute(t,f.e,f.op,"CREATE DOMAIN public.dtp_cancel_domain AS integer")
	enginetest.RoleExecute(t,f.e,f.op,"CREATE FUNCTION public.dtp_cancel(a dtp_cancel_domain,b dtp_cancel_domain) RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
	enginetest.RoleExecute(t,f.e,f.op,"CREATE TABLE public.dtp_cancel_sentinel(a integer)")
	enginetest.RoleExecute(t,f.e,f.op,"INSERT INTO public.dtp_cancel_sentinel VALUES(7)")
	view:=f.e.CatalogRead();auth:=dtpUnitAuthority(t,f,view)
	typ:=domainUnitType(t,view,"dtp_cancel_domain")
	ctx,cancel:=context.WithCancel(context.Background());defer cancel()
	runtime:=&dtpFormalCancelRuntime{Runtime:core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth:auth,Temporary:core.CatalogSourceTemporaryOwner(auth),Database:"unit_database"}),cancel:cancel,target:typ.SQLType.ID()}
	source:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.DataTypePrivilegesOID})
	rows,err:=catalogrt.CaptureRows(pgcatalog.Input{Context:ctx,Catalog:view,Ref:view.Owner().Domain.Ref(view.Epoch(),source.Address()),Source:source,Owner:auth.Owner,Roles:auth.Roles,Runtime:runtime},captureDataTypePrivileges)
	if rows!=nil||!errors.Is(err,context.Canceled)||runtime.calls!=1{t.Fatal("formal cancellation exposed partial rows",rows,err,runtime.calls)}
	if fresh,err:=dtpUnitCapture(context.Background(),view,auth);err!=nil||!reflect.DeepEqual(dtpUnitRoutineIDs(t,fresh,"dtp_cancel"),[]string{"0","1","2"}){t.Fatal("cancellation changed declaration",err)}
	if f.e.CatalogRead().State()!=view.State(){t.Fatal("cancellation changed catalog authority")}
	sentinel:=enginetest.RoleExecute(t,f.e,f.op,"SELECT a FROM public.dtp_cancel_sentinel")
	var value [1]sqltype.Value
	if more,err:=sentinel.Rows.Next(context.Background(),value[:]);err!=nil||!more{t.Fatal("cancellation lost sentinel",more,err)}
	if n,err:=value[0].Integer();err!=nil||n!=7{t.Fatal("cancellation changed sentinel",n,err)}
	if more,err:=sentinel.Rows.Next(context.Background(),value[:]);err!=nil||more{t.Fatal("cancellation changed sentinel inventory",more,err)}
}

func TestDataTypePrivilegesMalformedDeclarations(t *testing.T){
	f:=newDefaultUnitFixture(t)
	enginetest.RoleExecute(t,f.e,f.op,"CREATE FUNCTION public.dtp_malformed(a integer,b integer) RETURNS integer LANGUAGE SQL AS 'SELECT a'")
	view:=f.e.CatalogRead();var routine *catalog.Procedure
	for _,d:=range view.Descriptors(){if p,ok:=d.(*catalog.Procedure);ok&&p.Name()=="dtp_malformed"{routine=p;break}}
	if routine==nil{t.Fatal("missing real declaration")}
	for _,mutate:=range []func(*catalog.Procedure){
		func(d *catalog.Procedure){d.ObjectNamespace=ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:999999}},
		func(d *catalog.Procedure){d.Result=999999},
		func(d *catalog.Procedure){def:=*d.SQL;def.FormalInputs=append([]int(nil),def.FormalInputs...);def.FormalInputs[1]=0;d.SQL=&def},
		func(d *catalog.Procedure){def:=*d.SQL;def.Formals=append([]proc.Formal(nil),def.Formals...);def.Formals[0].Usage.Type=nil;d.SQL=&def},
	}{
		state:=catalog.CloneState(view.State());bad:=*routine;mutate(&bad);state.SetDescriptor(bad.Address(),&bad)
		staged:=f.e.Catalog.Read(state)
		if rows,err:=dtpUnitCapture(context.Background(),staged,dtpUnitAuthority(t,f,staged));rows!=nil||err==nil{t.Fatal("malformed routine disclosed partial inventory",rows,err)}
	}
	// Fixed builtin signatures pass through the same declaration visitor.
	state:=catalog.CloneState(view.State());bad:=*view.MustProcedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:1397});bad.Signature="not-a-type";state.SetDescriptor(bad.Address(),&bad)
	staged:=f.e.Catalog.Read(state)
	if rows,err:=dtpUnitCapture(context.Background(),staged,dtpUnitAuthority(t,f,staged));rows!=nil||err==nil{t.Fatal("malformed signature disclosed rows",rows,err)}
	// Dropped metadata is not a live datatype use; malformed live attnums fail.
	int4,_:=view.LookupType(sqltype.Int4)
	relation:=&catalog.Relation{ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:49001},ObjectNamespace:f.schema,ObjectName:"dtp_dropped",Owner:f.owner,RelationKind:catalog.TableRelation,Attributes:[]int32{1,2},Columns:[]catalog.Column{{Name:"a",Usage:int4.Usage()},{Name:"b",Usage:int4.Usage()}},ColumnMeta:catalog.ColumnMetadata{Dropped:map[int32]catalog.AttributeMetadata{2:{}}}}
	state=catalog.CloneState(view.State());state.SetDescriptor(relation.Address(),relation);staged=f.e.Catalog.Read(state)
	rows,err:=dtpUnitCapture(context.Background(),staged,dtpUnitAuthority(t,f,staged));if err!=nil{t.Fatal(err)}
	var got []string;for _,row:=range rows.Anonymous{if dtpUnitText(t,row[2])=="dtp_dropped"{got=append(got,dtpUnitText(t,row[4]))}}
	if !reflect.DeepEqual(got,[]string{"1"}){t.Fatal("dropped attribute disclosed",got)}
	badRelation:=*relation;badRelation.Attributes=[]int32{0,2};state.SetDescriptor(relation.Address(),&badRelation);staged=f.e.Catalog.Read(state)
	if rows,err:=dtpUnitCapture(context.Background(),staged,dtpUnitAuthority(t,f,staged));rows!=nil||err==nil{t.Fatal("nonpositive live attribute accepted",rows,err)}
}

func TestDataTypePrivilegesSeededDomainACL(t *testing.T){
	f:=newDefaultUnitFixture(t)
	check:=func(visible bool){
		view:=f.e.CatalogRead();auth:=dtpUnitAuthority(t,f,view);auth.State.CurrentUser=f.reader
		rows,err:=dtpUnitCapture(context.Background(),view,auth);if err!=nil{t.Fatal(err)}
		count:=0
		for _,row:=range rows.Anonymous{if dtpUnitText(t,row[2])=="cardinal_number"&&dtpUnitText(t,row[3])=="DOMAIN"{count++}}
		want:=0;if visible{want=1};if count!=want{t.Fatal("seeded domain ignored current USAGE",count,want)}
	}
	check(true)
	enginetest.RoleExecute(t,f.e,f.op,"REVOKE USAGE ON DOMAIN information_schema.cardinal_number FROM PUBLIC")
	check(false)
	enginetest.RoleExecute(t,f.e,f.op,"GRANT USAGE ON DOMAIN information_schema.cardinal_number TO default_reader")
	check(true)
	enginetest.RoleExecute(t,f.e,f.op,"REVOKE USAGE ON DOMAIN information_schema.cardinal_number FROM default_reader")
	check(false)
	enginetest.RoleExecute(t,f.e,f.op,"GRANT USAGE ON DOMAIN information_schema.cardinal_number TO PUBLIC")
	check(true)
}
