package core_test

import (
	"context"
	"testing"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTablefuncProviderIdentity(t *testing.T){
	e:=engine.New()
	op,done:=enginetest.RoleOperation(t,e,"postgres")
	enginetest.RoleExecute(t,e,op,"CREATE EXTENSION tablefunc")
	description,err:=e.Describe(op,enginetest.Statement(t,"SELECT * FROM normal_rand($1,$2,$3)"))
	if err!=nil||description.Parameters.Count!=3||len(description.Parameters.Types)!=3||len(description.Results)!=1{done();t.Fatal(description,err)}
	for i,want:=range []sqltype.ID{sqltype.Int4,sqltype.Float8,sqltype.Float8}{if description.Parameters.Types[i].Type.ID()!=want{done();t.Fatal(description.Parameters)}}
	if description.Results[0].Usage.Type.ID()!=sqltype.Float8||description.Results[0].Usage.Typmod != -1||description.Results[0].Usage.Collation.ID != 0{done();t.Fatal(description.Results)}
	done()
	read:=e.CatalogRead()
	ext:=read.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"tablefunc"}))
	if len(ext.Members)!=1{t.Fatal("unimplemented members advertised",ext.Members)}
	proc:=read.MustProcedure(ext.Members[0])
	if proc.Signature!="23,701,701"||proc.Result!=701||!proc.Strict||proc.Volatility!=expr.Volatile||!proc.Installed.ReturnsSet||proc.Installed.Rows!=1000||proc.Installed.Parallel!='u'{t.Fatal(proc)}
	if symbol,err:=extension.ProcedureSymbol(read,proc);err!=nil||symbol!="normal_rand"{t.Fatal(symbol,err)}
	if err:=extension.StockTablefunc.ValidateProcedure(read,proc);err!=nil{t.Fatal(err)}
	collision := &normalCollisionStage{conflict:proc.Address()}
	candidate := *ext
	candidate.Members = nil
	var conflict *sqlstate.Error
	failure := extension.StockTablefunc.Stage(collision,&candidate,ext.Owner)
	if !errors.As(failure,&conflict)||conflict.Code!="42723"||conflict.Message!=`function "normal_rand" already exists with same argument types`||collision.created||candidate.Members!=nil||len(ext.Members)!=1{t.Fatal("non-atomic conflicting stage",failure,candidate.Members)}
	forged:=*proc
	if err:=extension.StockTablefunc.ValidateProcedure(read,&forged);err==nil{t.Fatal("copied descriptor authorized")}
	ref:=e.Catalog.Domain.Ref(read.Epoch(),proc.Address())
	if err:=extension.ValidateTablefuncIdentity(read,proc,ref);err!=nil{t.Fatal(err)}
	stale:=ref;stale.Epoch++
	if err:=extension.ValidateTablefuncIdentity(read,proc,stale);err==nil{t.Fatal("stale identity")}
	b:=newTestExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil);b.Clause.AggregateClause="SELECT"
	raw:=parser.FunctionExpr{Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:"normal_rand"}}},Args:[]parser.Expr{parser.LiteralExpr{Value:parser.Value{Kind:parser.IntegerValue,Integer:3}},parser.LiteralExpr{Value:parser.Value{Kind:parser.IntegerValue,Integer:9}},parser.LiteralExpr{Value:parser.Value{Kind:parser.IntegerValue,Integer:0}}}}
	node,err:=b.BindFunctionProvider(raw);if err!=nil{t.Fatal(err)}
	n:=node.(plan.InstalledTablefuncNormalExpr)
	if n.Procedure!=proc||n.Ref!=ref||n.Properties().Volatility!=expr.Volatile{t.Fatal(n)}
	if err:=stored.ValidateInstalledTablefuncNormal(n,read);err!=nil{t.Fatal(err)}
	rebuilt,err:=n.Rebuild(n.Info,n.Children());if err!=nil||!n.SameSourceOccurrence(rebuilt){t.Fatal("lost identity",err)}
	other:=n;other.Ref.Epoch++
	if n.SameSourceOccurrence(other){t.Fatal("cross-install source identity")}
	other=n;other.Procedure=&forged
	if n.SameSourceOccurrence(other){t.Fatal("copied procedure source identity")}
	if err:=stored.ValidateInstalledTablefuncNormal(other,read);err==nil{t.Fatal("forged installed expression")}
}

type normalCollisionStage struct {
	extension.Stage
	conflict ids.ObjectAddress
	created bool
}
func (*normalCollisionStage) AllocateOID()(ids.OID,error){return 900000,nil}
func (*normalCollisionStage) Revision()(ids.DefinitionRevision,error){return 1,nil}
func (s *normalCollisionStage) LookupName(ids.NameKey)ids.ObjectAddress{return s.conflict}
func (s *normalCollisionStage) Create(catalog.Descriptor)error{s.created=true;return nil}
