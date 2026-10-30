package core_test

import (
 "context"
 "errors"
 "strconv"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
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
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func TestDatabaseFilesBindingProtocol(t *testing.T) {
 e := engine.New()
 null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
 for _, tc := range []struct{name string; oid uint32; formal, result sqltype.ID; count int}{
  {"pg_relation_filenode",2999,sqltype.Regclass,sqltype.Oid,1},
  {"pg_relation_filepath",3034,sqltype.Regclass,sqltype.Text,1},
  {"pg_filenode_relation",3454,sqltype.Oid,sqltype.Regclass,2},
 } {
  args := []parser.Expr{null}; if tc.count == 2 { args = append(args,null) }
  n := catalogRelationsUnitBind(t,e,tc.name,args...).(functions.DatabaseFilesInquiry)
  if err := n.Validate(); err != nil { t.Fatal(err) }
  ref := expr.InfoOf(n).RoutineReference
  if ref == nil || ref.RoutineOID()!=tc.oid || n.Arg.ResultUsage().Type.ID()!=tc.formal || n.ResultUsage().Type.ID()!=tc.result { t.Fatal(tc,n,ref) }
  if exec.PlannedVolatility(n)!=expr.Stable || plan.GenerationImmutable(n) || n.PlanningFold()!=expr.FoldNever { t.Fatal("cached location inquiry",tc) }
  rebuilt,err := n.Rebuild(expr.InfoOf(n),n.Children())
  if err!=nil || !analyze.SameGroupExpression(n,rebuilt) { t.Fatal(err) }
  if _,err:=n.Rebuild(expr.InfoOf(n),nil);err==nil { t.Fatal("invalid children accepted") }
  mapped,err:=stored.RemapExpression(n,func(ref ids.ObjectRef) ids.ObjectRef{return ref},nil)
  if err!=nil || !analyze.SameGroupExpression(n,mapped){t.Fatal(err)}
  capture,ok,err:=n.Capture(expr.CaptureFunction)
  if err!=nil||!ok||capture.Count!=tc.count {t.Fatal(capture,err)}
  rendering,err:=n.Render();if err!=nil||rendering.Name!=tc.name||!rendering.Replay.CatalogSnapshot {t.Fatal(rendering,err)}
  bad:=n;bad.Kind=255;if bad.Validate()==nil{t.Fatal("invalid kind")}
  bad=n;bad.Arg=nil;if bad.Validate()==nil{t.Fatal("missing operand")}
  bad=n;bad.Usage=enginetest.Usage(e,sqltype.Bool);if bad.Validate()==nil{t.Fatal("invalid result")}
  wrong:=plan.LiteralExpr{Info:expr.Info{Usage:enginetest.Usage(e,sqltype.Bool)},Value:sqltype.Null(enginetest.Usage(e,sqltype.Bool).Type)}
  bad=n;bad.Arg=wrong;if bad.Validate()==nil{t.Fatal("wrong formal")}
  bad=n
  if tc.count==2{bad.Second=nil}else{bad.Second=n.Arg}
  if bad.Validate()==nil{t.Fatal("wrong arity")}
  children:=n.Children()
  children[0]=wrong
  if _,err:=n.Rebuild(expr.InfoOf(n),children);err==nil{t.Fatal("rebuild admitted wrong formal")}
  found:=false
  for _,d:=range routine.BuiltinRoutineDeclarations {if uint32(d.Identity.ID)==tc.oid {found=true;if !d.Strict||d.Volatility!=expr.Stable||d.Parallel!='s'||d.ReturnsSet {t.Fatal(d)}}}
  if !found {t.Fatal("missing ABI",tc)}
 }
}

func TestDatabaseFilesRuntimeBoundary(t *testing.T) {
 e,r:=relationSizeOwnerFixture(t)
 s:=tablespaceInfoUnitScope(t,e)
 host:=core.HostOf(s)
 regclass:=enginetest.Usage(e,sqltype.Regclass).Type
 oid:=enginetest.Usage(e,sqltype.Oid).Type
 text:=enginetest.Usage(e,sqltype.Text).Type
 arg:=sqltype.String(regclass,strconv.FormatUint(uint64(r.Address().ObjectID),10))
 var unused sqltype.Value
 for _,tc:=range []struct{kind catalog.RelationKind; catalogView bool; stored bool}{
  {catalog.TableRelation,false,true},{catalog.IndexRelation,false,true},{catalog.SequenceRelation,false,true},{catalog.MaterializedViewRelation,false,true},{catalog.CatalogRelation,false,true},
  {catalog.ViewRelation,false,false},{catalog.CatalogRelation,true,false},{catalog.CompositeRelation,false,false},{catalog.ForeignRelation,false,false},{catalog.PartitionedRelation,false,false},
 } {
  // Publish an isolated descriptor copy, preserving retained snapshot immutability.
  next:=*r
  next.RelationKind,next.CatalogView=tc.kind,tc.catalogView
  if tc.kind==catalog.IndexRelation {next.SQLIndex=&catalog.IndexDefinition{Parent:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:0}}}
  e.Mu.Lock()
  state:=catalog.CloneState(e.Catalog.State)
  state.SetDescriptor(next.Address(),&next)
  e.Catalog.PublishLocked(state)
  e.Mu.Unlock()
  for _,kind:=range []functions.DatabaseFilesKind{functions.RelationFilenode,functions.RelationFilepath} {
   result:=oid;if kind==functions.RelationFilepath {result=text}
   got,err:=host.DatabaseFilesInquiry(kind,arg,unused,result)
   if tc.stored {
    var state interface{SQLState()string}
    message:="pg_relation_filenode is not supported: the embedded engine has no physical PostgreSQL relation filenode mapping"
    if kind==functions.RelationFilepath{message="pg_relation_filepath is not supported: the embedded engine has no physical PostgreSQL relation file paths"}
    if !errors.As(err,&state)||state.SQLState()!="0A000"||err.Error()!="stuntgres: "+message {t.Fatal(tc,got,err)}
   } else if err!=nil||!got.IsNull()||got.Type()!=result {t.Fatal(tc,got,err)}
   if e.Catalog.State!=state || next.ObjectRevision!=r.ObjectRevision {t.Fatal("location inquiry mutated catalog")}
  }
 }
 missing:=sqltype.String(regclass,"0")
 got,err:=host.DatabaseFilesInquiry(functions.RelationFilenode,missing,unused,oid)
 if err!=nil||!got.IsNull()||got.Type()!=oid{t.Fatal(got,err)}
 for _,pair:=range [][2]uint32{{0,0},{1663,0},{4294967295,123},{0,20000}} {
  space,_:=sqltype.OIDValue(oid,pair[0])
  node,_:=sqltype.OIDValue(oid,pair[1])
  got,err:=host.DatabaseFilesInquiry(functions.FilenodeRelation,space,node,regclass)
  if err!=nil||!got.IsNull()||got.Type()!=regclass{t.Fatal(got,err)}
 }
 // A pending-created descriptor is visible only through the statement journal.
 private:=*r
 private.ObjectAddress.ObjectID=20001
 private.ObjectName="df_pending"
 private.RelationKind=catalog.TableRelation
 privateArg:=sqltype.String(regclass,"20001")
 s.Catalog=&session.Journal{Descriptors:map[ids.ObjectAddress]catalog.Descriptor{private.Address():&private}}
 if _,err:=host.DatabaseFilesInquiry(functions.RelationFilenode,privateArg,unused,oid);err==nil{t.Fatal("private stored descriptor was missed")}
 s.Catalog=nil
 got,err=host.DatabaseFilesInquiry(functions.RelationFilenode,privateArg,unused,oid)
 if err!=nil||!got.IsNull(){t.Fatal("rollback published private descriptor",got,err)}
 // Partitioned indexes are storage-less even though ordinary indexes have storage.
 parent:=*r
 parent.ObjectAddress.ObjectID=20002
 parent.Partition=&catalog.PartitionDefinition{}
 idx:=*r
 idx.RelationKind=catalog.IndexRelation
 idx.SQLIndex=&catalog.IndexDefinition{Parent:parent.Address()}
 s.Catalog=&session.Journal{Descriptors:map[ids.ObjectAddress]catalog.Descriptor{parent.Address():&parent,idx.Address():&idx}}
 got,err=host.DatabaseFilesInquiry(functions.RelationFilenode,arg,unused,oid)
 if err!=nil||!got.IsNull(){t.Fatal("partitioned index has no storage",got,err)}
 s.Catalog=nil
 for _,persistence:=range []string{"temporary","unlogged"} {
  temporary:=*r
  temporary.Persistence=persistence
  temporary.RelationKind=catalog.TableRelation
  s.Catalog=&session.Journal{Descriptors:map[ids.ObjectAddress]catalog.Descriptor{temporary.Address():&temporary}}
  if _,err:=host.DatabaseFilesInquiry(functions.RelationFilepath,arg,unused,text);err==nil{t.Fatal("persistence hid stored class",persistence)}
 }
 s.Catalog=nil
 canceledRuntime,cancelRuntime:=context.WithCancel(context.Background())
 cancelRuntime()
 originalContext:=s.Op.Ctx
 s.Op.Ctx=canceledRuntime
 if _,err:=host.DatabaseFilesInquiry(functions.RelationFilenode,missing,unused,oid);!errors.Is(err,context.Canceled){t.Fatal("runtime cancellation",err)}
 s.Op.Ctx=originalContext
 retained:=s.Inquiry.CommentCatalog
 for _,foreign:=range []catalog.Read{{},engine.New().CatalogRead()} {
  s.Inquiry.CommentCatalog=foreign
  if _,err:=host.DatabaseFilesInquiry(functions.RelationFilenode,missing,unused,oid);err==nil{t.Fatal("invalid authority")}
 }
 s.Inquiry.CommentCatalog=retained
 e.Mu.Lock();replacement:=catalog.CloneState(e.Catalog.State);replacement.SetEpoch(retained.Epoch()+1);e.Catalog.PublishLocked(replacement);e.Mu.Unlock()
 if _,err:=host.DatabaseFilesInquiry(functions.RelationFilenode,missing,unused,oid);!errors.Is(err,ids.ErrCatalogReplaced){t.Fatal(err)}
 c:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background()}}
 null:=plan.LiteralExpr{Info:expr.Info{Usage:oid.Usage()},Value:sqltype.Null(oid)}
 value,_:=sqltype.OIDValue(oid,0)
 nonnull:=plan.LiteralExpr{Info:expr.Info{Usage:oid.Usage()},Value:value}
 for _,children:=range [][2]expr.Expr{{null,nonnull},{nonnull,null}}{
  n:=functions.DatabaseFilesInquiry{Info:expr.Info{Usage:regclass.Usage()},Kind:functions.FilenodeRelation,Arg:children[0],Second:children[1]}
  got,err:=n.Eval(c)
  if err!=nil||!got.IsNull()||got.Type()!=regclass{t.Fatal("strict NULL demanded runtime",got,err)}
 }
 sentinel:=errors.New("operand failure")
 n:=functions.DatabaseFilesInquiry{Info:expr.Info{Usage:regclass.Usage()},Kind:functions.FilenodeRelation,Arg:null,Second:databaseFilesErrorArg{LiteralExpr:nonnull,err:sentinel}}
 if _,err:=n.Eval(c);!errors.Is(err,sentinel){t.Fatal("NULL swallowed second operand error",err)}
 canceled,cancel:=context.WithCancel(context.Background());cancel()
 c.EvalStatement.Context=canceled
 if _,err:=n.Eval(c);!errors.Is(err,context.Canceled){t.Fatal("cancellation",err)}
}

type databaseFilesErrorArg struct{plan.LiteralExpr;err error}
func(n databaseFilesErrorArg) Eval(expr.Context)(sqltype.Value,error){return sqltype.Value{},n.err}
