package core_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRelationSizeBindingProtocol(t *testing.T) {
	e:=engine.New()
	wrapper,ok:=e.CatalogRead().Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:2325})
	if !ok||len(wrapper.Body)!=1||wrapper.Body[0].Routine.Address().ObjectID!=2332||wrapper.Body[0].Demand!=catalog.BodyAlways{t.Fatal("released SQL body authority edge",wrapper)}
	if wrapper.DeclarationBody==nil||wrapper.SQLBody!=nil{t.Fatal("SQL wrapper declaration lost separate immutable body ownership")}
	body,err:=wrapper.DeclarationBody.StructuralOutput(context.Background())
	if err!=nil||body==""{t.Fatal("SQL wrapper declaration body output",body,err)}
	null:=parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}}
	for _,tc:=range []struct{name string; oid uint32; formal sqltype.ID; fork bool}{
		{"pg_database_size",2168,sqltype.Name,false},{"pg_database_size",2324,sqltype.Oid,false},
		{"pg_tablespace_size",2323,sqltype.Name,false},{"pg_tablespace_size",2322,sqltype.Oid,false},
		{"pg_relation_size",2325,sqltype.Regclass,false},{"pg_relation_size",2332,sqltype.Regclass,true},
		{"pg_table_size",2997,sqltype.Regclass,false},{"pg_indexes_size",2998,sqltype.Regclass,false},{"pg_total_relation_size",2286,sqltype.Regclass,false},
	}{
		var arg parser.Expr=null
		if tc.formal==sqltype.Oid {arg=parser.CastExpr{Child:null,TypeName:parser.TypeName{Name:"oid"}}}
		args:=[]parser.Expr{arg};if tc.fork{args=append(args,null)}
		node:=catalogRelationsUnitBind(t,e,tc.name,args...).(functions.SizeInquiry)
		if err:=node.Validate();err!=nil{t.Fatal(err)}
		ref:=expr.InfoOf(node).RoutineReference
		if ref==nil||ref.RoutineOID()!=tc.oid||node.Arg.ResultUsage().Type.ID()!=tc.formal||node.ResultUsage().Type.ID()!=sqltype.Int8{t.Fatal(tc,node,ref)}
		if exec.PlannedVolatility(node)!=expr.Volatile||plan.GenerationImmutable(node)||node.PlanningFold()!=expr.FoldNever{t.Fatal("size became cached",tc)}
		children:=node.Children()
		rebuilt,err:=node.Rebuild(expr.InfoOf(node),children)
		if err!=nil||!analyze.SameGroupExpression(node,rebuilt){t.Fatal(err)}
		if _,err:=node.Rebuild(expr.InfoOf(node),nil);err==nil{t.Fatal("invalid reconstruction accepted")}
		mapped,err:=stored.RemapExpression(node,func(ref ids.ObjectRef)ids.ObjectRef{return ref},nil)
		if err!=nil||!analyze.SameGroupExpression(node,mapped){t.Fatal("stored provider",err)}
		capture,ok,err:=node.Capture(expr.CaptureFunction)
		if err!=nil||!ok||capture.Count!=len(children){t.Fatal(capture,err)}
		found:=false
		for _,decl:=range routine.BuiltinRoutineDeclarations{if uint32(decl.Identity.ID)==tc.oid{found=true;if !decl.Strict||decl.Volatility!=expr.Volatile||decl.Parallel!='s'||decl.ReturnsSet{t.Fatal(decl)}}}
		if !found{t.Fatal("missing executable ABI",tc)}
	}
}

func relationSizeOwnerFixture(t *testing.T) (*core.Engine,*catalog.Relation) {
	t.Helper()
	e:=engine.New();base:=e.CatalogRead();stage:=e.BeginCatalogStage(base)
	owner:=e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	r:=&catalog.Relation{ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:20000},ObjectNamespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},ObjectName:"size_owner",ObjectRevision:1,Owner:owner,Columns:[]catalog.Column{{Name:"i",Usage:enginetest.Usage(e,sqltype.Int4)}},Index:map[string]int{"i":0},Attributes:[]int32{1},ColumnMeta:catalog.ColumnMetadata{HighWater:1}}
	r.TableMethod = 2
	if err:=stage.Create(r);err!=nil{t.Fatal(err)}
	publication,err:=e.ValidateCatalogStage(context.Background(),stage);if err!=nil{t.Fatal(err)}
	if _,err:=enginetest.PublishCatalog(e,publication,core.PublicationToken{Epoch:base.Epoch()});err!=nil{t.Fatal(err)}
	return e,r
}

func TestRelationSizeRuntimeOwnership(t *testing.T) {
	e,r:=relationSizeOwnerFixture(t)
	s:=tablespaceInfoUnitScope(t,e)
	host:=core.HostOf(s)
	int8:=enginetest.Usage(e,sqltype.Int8).Type
	regclass:=enginetest.Usage(e,sqltype.Regclass).Type
	arg:=sqltype.String(regclass,strconv.FormatUint(uint64(r.Address().ObjectID),10))
	value,_:=sqltype.Integer(enginetest.Usage(e,sqltype.Int4).Type,1)
	read:=func(want int64){t.Helper();got,err:=host.SizeInquiry(functions.TableSize,arg,"main",int8);if err!=nil{t.Fatal(err)};n,err:=got.Integer();if err!=nil||n!=want{t.Fatal(n,want,err)}}
	read(64)
	e.Mu.Lock()
	root:=e.Storage.Relations[r.Address()]
	root.Versions=[]storage.RowVersion{{Row:1,Version:1,Values:[]sqltype.Value{value}}}
	e.Mu.Unlock()
	read(113)
	// A private catalog/storage incarnation is used instead of the public
	// predecessor, and own writes are appended once, not SELECT-filtered.
	private:=&storage.Relation{Versions:[]storage.RowVersion{{Row:2,Version:2,Values:[]sqltype.Value{value}}}}
	s.Catalog=&session.Journal{Storage:map[ids.ObjectAddress]session.StorageChange{r.Address():{Replacement:private,Relation:r}}}
	s.OwnWrites=map[ids.ObjectAddress][]storage.RowVersion{r.Address():{{Row:3,Version:3,Values:[]sqltype.Value{value}}}}
	read(153)
	s.Stage.Rows=map[ids.ObjectAddress][]storage.RowVersion{r.Address():{{Row:4,Version:4,Values:[]sqltype.Value{value}}}}
	read(193)
	leave:=core.ActivateRoutineCommandPolicy(s,&catalog.Procedure{Volatility:expr.Volatile})
	restore:=s.ActivateSQLRoutine(nil,s.Inquiry.CommentCatalog)
	e.Mu.Lock()
	captureErr:=e.CaptureCommentSnapshotLocked(s)
	e.Mu.Unlock()
	if captureErr!=nil{t.Fatal(captureErr)}
	read(193) // Activation merged the same Stage version into OwnWrites.
	if err:=restore(nil);err!=nil{t.Fatal(err)}
	leave()
	s.Stage.Replacing=true
	s.Stage.Rows[r.Address()]=[]storage.RowVersion{{Row:3,Version:5,Values:[]sqltype.Value{value}}}
	read(153)
	s.ProjectedRoots=true
	s.Stage.Rows=nil
	read(113)
	s.Stage.Rows=map[ids.ObjectAddress][]storage.RowVersion{r.Address():{{Row:6,Version:6,Values:[]sqltype.Value{value}}}}
	read(153)
	s.ProjectedRoots=false
	s.Stage.Replacing=false
	s.Stage.Rows=nil
	e.Mu.Lock();root.Versions=nil;e.Mu.Unlock()
	read(153)
	s.OwnWrites=nil;s.Catalog=nil
	read(64)
	missing:=sqltype.String(regclass,"0")
	got,err:=host.SizeInquiry(functions.RelationSize,missing,"INVALID",int8)
	if err!=nil||!got.IsNull()||got.Type()!=int8{t.Fatal(got,err)}
	if _,err:=host.SizeInquiry(functions.RelationSize,arg,"INVALID",int8);err==nil{t.Fatal("invalid fork admitted")}
	retained:=s.Inquiry.CommentCatalog
	s.Inquiry.CommentCatalog=engine.New().CatalogRead()
	if _,err:=host.SizeInquiry(functions.TableSize,arg,"main",int8);err==nil{t.Fatal("foreign owner accepted")}
	s.Inquiry.CommentCatalog=retained
	// Same numeric OID in a replacement catalog can never adopt new storage.
	e.Mu.Lock();replacement:=catalog.CloneState(e.Catalog.State);replacement.SetEpoch(retained.Epoch()+1);e.Catalog.PublishLocked(replacement);e.Mu.Unlock()
	if _,err:=host.SizeInquiry(functions.TableSize,arg,"main",int8);!errors.Is(err,ids.ErrCatalogReplaced){t.Fatal("replacement fence",err)}
}
