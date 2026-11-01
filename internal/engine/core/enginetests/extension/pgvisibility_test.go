package extension_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func visibilityInstalled(t *testing.T,e *core.Engine)*catalog.Extension{
	t.Helper()
	op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
	enginetest.RoleExecute(t,e,op,"CREATE EXTENSION pg_visibility")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"pg_visibility"}))
}
func visibilityExpression(t *testing.T,e *core.Engine,name string,block bool)plan.InstalledPgvisibilityExpr{
	t.Helper()
	binder:=enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
	binder.Clause.AggregateClause="FROM"
	binder.Query.Namespace=&analyze.QueryNamespace{}
	args:=[]parser.Expr{parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue,Text:"-"}}}
	if block {args=append(args,parser.LiteralExpr{Value:parser.Value{Kind:parser.IntegerValue,Integer:0}})}
	node,err:=binder.BindFunctionSource(parser.FunctionExpr{Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:name}}},Args:args})
	if err!=nil{t.Fatal(err)}
	return node.(plan.InstalledPgvisibilityExpr)
}
func TestPgvisibilityProviderIdentity(t *testing.T){
	e:=engine.New();ext:=visibilityInstalled(t,e);view:=e.CatalogRead()
	control:=extension.StockPgvisibility.Control()
	if control.Version!="1.2"||!control.Superuser||control.Trusted||!control.Relocatable||len(ext.Members)!=5 {t.Fatal(control,ext)}
	if err:=extension.StockPgvisibility.ValidateExtension(view,ext);err!=nil{t.Fatal(err)}
	for i,address:=range ext.Members {
		proc:=view.MustProcedure(address)
		wantSource:=[]string{"pg_visibility_map","pg_visibility","pg_visibility_map_rel","pg_visibility_rel","pg_visibility_map_summary"}[i]
		wantSignature:=catalog.ProcedureSignature("2205");if i<2 {wantSignature="2205,20"}
		if proc.Signature!=wantSignature||proc.Display.Source!=wantSource||proc.Display.Binary!="$libdir/pg_visibility"||proc.Result!=2249||!proc.Strict||proc.Volatility!=expr.Volatile||proc.Installed.Parallel!='u'||proc.Installed.ReturnsSet!=(i==2||i==3)||proc.OutputDeclaration().Name(0)!="" {t.Fatal("release declaration",proc)}
		scan:=view.Owner().Principals.Principal(authorization.StatScanTablesRoleID)
		granted:=false
		for _,entry:=range proc.ACL.Entries {
			if entry.Grantee.Public&&entry.Privileges&authorization.ExecutePrivilege!=0 {t.Fatal("PUBLIC authority",proc.ACL)}
			granted=granted||entry.Grantee.Role==scan&&entry.Privileges&authorization.ExecutePrivilege!=0
		}
		if !granted{t.Fatal("missing genuine scan role grant")}
		ref:=e.Catalog.Domain.Ref(view.Epoch(),address)
		if err:=extension.ValidatePgvisibilityIdentity(view,proc,ref);err!=nil{t.Fatal(err)}
		for _,mutate:=range []func(*catalog.Procedure){
			func(p *catalog.Procedure){p.Implementation=new(extension.PgvisibilityProvider)},
			func(p *catalog.Procedure){p.Display.Source="pg_visibility_map";if wantSource=="pg_visibility_map" {p.Display.Source="pg_visibility"}},
			func(p *catalog.Procedure){p.Signature="23"},
			func(p *catalog.Procedure){p.Strict=false},
			func(p *catalog.Procedure){p.Installed.ReturnsSet=!p.Installed.ReturnsSet},
			func(p *catalog.Procedure){p.Installed.Parallel='s'},
			func(p *catalog.Procedure){p.Installed.Outputs=catalog.NewRoutineOutputs([]ids.OID{16,16},"","all_visible","all_frozen")},
			func(p *catalog.Procedure){origin:=*p.Origin;origin.Declaration=(origin.Declaration+1)%5;p.Origin=&origin},
		} {
			state:=catalog.CloneState(view.State());bad:=*proc;mutate(&bad);state.SetDescriptor(address,&bad)
			if err:=extension.StockPgvisibility.ValidateProcedure(e.Catalog.Read(state),&bad);err==nil{t.Fatal("forged released declaration accepted",i)}
		}
	}
}

func TestPgvisibilityExpressionOwnership(t *testing.T){
	e:=engine.New();ext:=visibilityInstalled(t,e);view:=e.CatalogRead()
	for _,spec:=range []struct{name string;block bool}{{"pg_visibility_map",true},{"pg_visibility",true},{"pg_visibility_map",false},{"pg_visibility",false},{"pg_visibility_map_summary",false}} {
		n:=visibilityExpression(t,e,spec.name,spec.block)
		if n.Properties().Volatility!=expr.Volatile||!n.Properties().StageDependent||n.CallRoutineReference()!=n.Procedure {t.Fatal("invocation properties",n)}
		for _,rewrite:=range []func(expr.Info,[]expr.Expr)(expr.Expr,error){n.Rebuild,n.Substitute} {
			value,err:=rewrite(expr.InfoOf(n),n.Children());if err!=nil{t.Fatal(err)}
			copy:=value.(plan.InstalledPgvisibilityExpr)
			if copy.Ref!=n.Ref||copy.Procedure!=n.Procedure||copy.Kind!=n.Kind||copy.Slots!=n.Slots||&copy.Args[0]==&n.Args[0] {t.Fatal("reconstruction borrowed children or changed authority")}
		}
		if exec.PlannedVolatility(n)!=expr.Volatile||plan.GenerationImmutable(n) {t.Fatal("volatile call became foldable")}
		for _,tc:=range []struct{name string;mutate func(*plan.InstalledPgvisibilityExpr)}{
			{"routine_reference",func(n *plan.InstalledPgvisibilityExpr){n.RoutineReference=nil}},
			{"future_epoch",func(n *plan.InstalledPgvisibilityExpr){n.Ref.Epoch++}},
			{"zero_epoch",func(n *plan.InstalledPgvisibilityExpr){n.Ref.Epoch=0}},
			{"operation",func(n *plan.InstalledPgvisibilityExpr){n.Kind=functions.PgvisibilityKind(255)}},
			{"formal_slot",func(n *plan.InstalledPgvisibilityExpr){n.Slots[0]=1}},
		} {
			bad:=n;tc.mutate(&bad)
			if stored.ValidateInstalledPgvisibility(bad,view)==nil {t.Fatal("forged expression authority accepted",spec.name,spec.block,tc.name)}
		}
		if !n.Kind.Set()&&!analyze.ClosedPgstattupleDefault(n,view) {t.Fatal("authenticated scalar closure rejected")}
		if n.Kind.Set()&&analyze.ClosedPgstattupleDefault(n,view) {t.Fatal("set producer admitted to scalar default")}
		text,err:=(stored.Deparser{View:view}).SQL(context.Background(),n,stored.DeparseParent{})
		if err!=nil||strings.Contains(text,"\"\" =>") {t.Fatal("unnamed formal rendering",text,err)}
		mapping:=map[ids.ObjectAddress]ids.ObjectAddress{ext.Address():{ClassID:ids.ExtensionClassID,ObjectID:920000}}
		for i,address:=range ext.Members {mapping[address]=ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:ids.OID(920001+i)}}
		state:=catalog.CloneState(view.State());descriptor:=extension.DescriptorRemapper(mapping)
		for old:=range mapping {state.UnbindName(catalog.DescriptorNameKey(view.Get(old)));state.RemoveDescriptor(old)}
		for old,address:=range mapping {next:=descriptor(view.Get(old));state.SetDescriptor(address,next);state.BindName(catalog.DescriptorNameKey(next),address)}
		state.SetEpoch(state.Epoch()+1)
		remap:=func(ref ids.ObjectRef)ids.ObjectRef{ref.Epoch=state.Epoch();if address,ok:=mapping[ref.Address];ok{ref.Address=address};return ref}
		mapper:=stored.ViewQueryRemapper{Catalog:e.Catalog.Read(state),Ref:remap,Type:func(d *sqltype.Descriptor)*sqltype.Descriptor{return d},Procedure:extension.ProcedureRemapper(descriptor,nil,nil)}
		query:=&plan.ProjectNode{QuerySchema:plan.QuerySchema{{Name:"s",Usage:n.ResultUsage()}},Child:&plan.ValuesNode{Rows:[][]expr.Expr{{}}},Expressions:[]expr.Expr{n}}
		mappedQuery,err:=mapper.Query(query);if err!=nil{t.Fatal(err)}
		mapped:=mappedQuery.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgvisibilityExpr)
		if mapped.Ref!=remap(n.Ref)||mapped.CallRoutineReference()!=mapped.Procedure||mapped.Procedure.Address()!=mapping[n.Procedure.Address()]||n.Procedure!=view.Get(n.Ref.Address)||&mapped.Args[0]==&n.Args[0] {t.Fatal("destination remap lost independent authority or mutated old backing")}
		if err:=stored.ValidateInstalledPgvisibility(mapped,e.Catalog.Read(state));err!=nil{t.Fatal(err)}
	}
	visibilityPreparedOwner(t,e)
	visibilityProtocolOwner(t,e)
}

func visibilityPreparedOwner(t *testing.T,e *core.Engine){
	t.Helper()
	enginetest.Run(t,e,"CREATE TABLE visibility_owned(n integer)")
	s:=enginetest.StartSession(t,e,nil)
	// A demanded immutable child supplies distinguishing accepted fold backing;
	// the visibility invocation itself must remain volatile and symbolic.
	ast,d:=enginetest.PreparedDescription(t,e,s,"SELECT (pg_visibility_map('visibility_owned'::regclass,COALESCE(-(7::integer),0)::bigint+7::bigint)).all_visible")
	enginetest.PreparedFoldExecute(t,e,s,ast,&d)
	owner:=d.Folds;accepted:=owner.Current()
	if !owner.Live()||accepted.Generation==0||len(accepted.Entries)==0 {t.Fatal("missing real accepted definition generation")}
	snapshot:=slices.Clone(accepted.Entries)
	for i:=range snapshot {snapshot[i].Inputs=slices.Clone(snapshot[i].Inputs)}
	enginetest.PreparedFoldExecute(t,e,s,ast,&d)
	if d.Folds!=owner||owner.Current().Generation!=accepted.Generation||&owner.Current().Entries[0]!=&accepted.Entries[0] {t.Fatal("unchanged execution replaced accepted immutable backing")}
	op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)}
	scope,err:=e.StartStatement(op);if err!=nil{op.Done();t.Fatal(err)}
	local:=enginetest.CloneFoldDescription(t,d)
	scope.Expected=&local
	e.Mu.Lock()
	auth,err:=e.AuthorizationLocked(op)
	if err!=nil{e.Mu.Unlock();scope.Finish(err);op.Done();t.Fatal(err)}
	environment,err:=e.PreparedEnvironmentLocked(auth,true)
	if err!=nil{e.Mu.Unlock();scope.Finish(err);op.Done();t.Fatal(err)}
	publication,err:=core.BeginFoldPublication(&local,ast,environment,auth.Catalog,nil)
	if err!=nil{e.Mu.Unlock();scope.Finish(err);op.Done();t.Fatal(err)}
	publication.Group.Entries=append([]exec.FoldConstant(nil),publication.Group.Entries...)
	publication.Group.Private=true
	publication.Group.Entries[0].Value=sqltype.Null(publication.Group.Entries[0].Result.Type)
	ctx,cancel:=context.WithCancel(op.Ctx);cancel()
	err=publication.Accept(ctx,auth.Catalog,environment,e.Catalog.State.Epoch())
	e.Mu.Unlock();scope.Finish(err);op.Done()
	if !errors.Is(err,context.Canceled)||publication.Group.Sealed||len(owner.Current().Entries)!=len(snapshot)||len(local.Folds.Current().Entries)!=len(snapshot)||local.Folds.Current().Generation!=accepted.Generation||&local.Folds.Current().Entries[0]!=&accepted.Entries[0]||&owner.Current().Entries[0]!=&accepted.Entries[0] {t.Fatal("rejected publication changed accepted owner/generation/backing",err)}
	for i,want:=range snapshot {if !enginetest.SamePublishedFold(owner.Current().Entries[i],want){t.Fatal("rejected publication mutated accepted immutable entry",i)}}
}

func visibilityProtocolOwner(t *testing.T,e *core.Engine){
	t.Helper()
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"BEGIN")
	cycle:=s.NewProtocolCycle()
	command:=func(fn func(*core.StatementScope)error){enginetest.PreparedFoldProtocolCommand(t,e,s,cycle,fn)}
	query:="SELECT pg_visibility_map('visibility_owned'::regclass,COALESCE(-(7::integer),0)::bigint+7::bigint) AS flags"
	parse:=func(){command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("visibility_definition",query,nil);return err})}
	bind:=func(name string){command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal(name,"visibility_definition",&enginetest.ProtocolReader{Data:[]byte{0,0,0,0,0,0}});return err})}
	parse()
	registry:=core.PayloadOf(s).PreparedSQL
	definition:=registry.Lookup("visibility_definition")
	if definition==nil||definition.Description.Analysis==nil||definition.Description.Folds==nil{t.Fatal("missing actual protocol definition owner")}
	owner:=definition.Description.Folds
	if owner.Current().Generation!=0||len(owner.Current().Entries)!=0{t.Fatal("Parse evaluated producer or children")}
	bind("visibility_first")
	first:=registry.Binding("visibility_first")
	accepted:=owner.Current()
	if first==nil||first.Folds==nil||first.Entry.Description.Folds!=owner||len(accepted.Entries)==0||first.Folds.Generation!=accepted.Generation||&first.Folds.Entries[0]!=&accepted.Entries[0]{t.Fatal("accepted binding lost definition generation/backing")}
	assertGraph:=func(root *plan.ProjectNode){
		t.Helper()
		if root==nil{t.Fatal("missing accepted private graph")}
		found:=0
		var visit func(expr.Expr)
		visit=func(node expr.Expr){
			if call,ok:=node.(plan.InstalledPgvisibilityExpr);ok{
				found++
				if call.Kind!=functions.VisibilityMapBlock||call.CallRoutineReference()!=call.Procedure||call.Ref.Address!=call.Procedure.Address()||call.Properties().Volatility!=expr.Volatile||!call.Properties().StageDependent||call.ResultUsage().Type.CompositeLen()!=2{t.Fatal("accepted graph lost symbolic operation authority")}
				dependency:=false
				for _,dep:=range definition.Description.Analysis.Dependencies{dependency=dependency||dep.Ref==call.Ref&&dep.Revision==call.Procedure.Revision()}
				if !dependency{t.Fatal("analysis omitted genuine executable dependency")}
				for _,entry:=range accepted.Entries{if entry.Callable==call.Procedure{t.Fatal("volatile visibility producer was folded")}}
			}
			plan.ExpressionChildren(node,visit)
		}
		for _,expression:=range root.Expressions{visit(expression)}
		if found!=1{t.Fatal("private graph lost selected visibility occurrence",found)}
	}
	if first.Resource==nil{t.Fatal("accepted binding omitted query resource")}
	firstRoot:=first.Resource.Root
	assertGraph(firstRoot)
	bind("visibility_second")
	second:=registry.Binding("visibility_second")
	if second==nil||second==first||second.Entry.Description.Folds!=owner||owner.Current().Generation!=accepted.Generation||&second.Folds.Entries[0]!=&accepted.Entries[0]||&owner.Current().Entries[0]!=&accepted.Entries[0]{t.Fatal("repeated binding replaced accepted owner/backing")}
	assertGraph(firstRoot)
	command(func(scope *core.StatementScope)error{return scope.CloseProtocolStatement("visibility_definition")})
	if owner.Live(){t.Fatal("retired definition kept publication authority")}
	parse();bind("visibility_new")
	replacement:=registry.Lookup("visibility_definition").Description.Folds
	replacementAccepted:=replacement.Current()
	if replacement==owner||replacementAccepted.Generation==0||len(replacementAccepted.Entries)==0{t.Fatal("replacement reused retired definition authority")}
	command(func(scope *core.StatementScope)error{_,err:=scope.ExecuteProtocolPortal("visibility_first",0,func([]session.ProtocolField)error{return nil},nil,nil,core.CopyProtocolHooks{});return err})
	if replacement.Current().Generation!=replacementAccepted.Generation||&replacement.Current().Entries[0]!=&replacementAccepted.Entries[0]||owner.Live()||first.Folds.Generation!=accepted.Generation||&first.Folds.Entries[0]!=&accepted.Entries[0]{t.Fatal("old accepted binding published into replacement or changed retained backing")}
	assertGraph(firstRoot)
	for _,name:=range []string{"visibility_first","visibility_second","visibility_new"}{command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal(name)})}
	command(func(scope *core.StatementScope)error{return scope.CloseProtocolStatement("visibility_definition")})
	enginetest.SessionSQL(t,s,"ROLLBACK")
}
