package core_test

import (
	"context"
	"fmt"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Keep failure diagnostics bounded while retaining phase changes rather than
// spending the entire sample window on admission's initially empty scratch.
type xmlScopePolls struct{
	calls,changes int
	last string
	recent [16]string
}
func xmlScopePollSnapshot(s *core.StatementScope)string{
	if s==nil{return "nil"}
	width,name:=0,"";if s.Expected!=nil{width=len(s.Expected.Results);if width!=0{name=s.Expected.Results[0].Name}}
	var frame,parent *session.EffectiveUserInvocation
	var owner authorization.PrincipalRef
	if s.Op!=nil{frame=s.Op.EffectiveUser;if frame!=nil{owner,parent=frame.Owner,frame.Parent}}
	return fmt.Sprintf("scope=%p op=%p depth=%d expected=%t/%d/%q frame=%p owner=%v parent=%p identity=%v routineActor=%v catalog=%t view=%t",s,s.Op,core.ScopeActiveSQLRoutineDepth(s),s.Expected!=nil,width,name,frame,owner.ID(),parent,s.Identity.CurrentPrincipal.ID(),s.RoutineAuth.State.CurrentUser.ID(),s.RoutineAuth.Catalog.State()!=nil,s.View!=nil)
}
func (p *xmlScopePolls)observe(command *core.StatementScope,portal *core.ProtocolPortal){
	p.calls++
	state:=fmt.Sprintf("command{%s} portal=%t",xmlScopePollSnapshot(command),portal!=nil)
	if portal!=nil{
		producer:=core.PortalProducer(portal)
		state+=fmt.Sprintf(" producer=%p",producer)
		if producer!=nil{
			authority:=core.CursorAuthority(producer)
			state+=fmt.Sprintf(" stage=%p authority=%p",core.CursorStage(producer),authority)
			if authority!=nil{state+=fmt.Sprintf(" host=%T live{%s}",authority.Host,xmlScopePollSnapshot(core.ScopeOf(authority.Host)))}
		}
	}
	if state!=p.last{p.recent[p.changes%len(p.recent)]=fmt.Sprintf("%d %s",p.changes,state);p.changes++;p.last=state}
}
func (p *xmlScopePolls)String()string{return fmt.Sprintf("polls=%d transitions=%d recent=%q",p.calls,p.changes,p.recent)}

type xmlScopeActorContext struct{
	context.Context
	scope *core.StatementScope
	actor authorization.PrincipalRef
	parent *session.EffectiveUserInvocation
	portal *core.ProtocolPortal
	liveScope *core.StatementScope
	target ids.ObjectAddress
	seen bool
	wrong string
	polls xmlScopePolls
}
func (c *xmlScopeActorContext)Err()error{
	c.polls.observe(c.scope,c.portal)
	// Resolve the actual producer's live host through the existing portal and
	// cursor seams; cursor admission attaches that exact executor authority.
	if c.portal==nil{return c.Context.Err()}
	producer:=core.PortalProducer(c.portal);if producer==nil{return c.Context.Err()}
	authority:=core.CursorAuthority(producer);if authority==nil||authority.Host==nil{return c.Context.Err()}
	s:=core.ScopeOf(authority.Host)
	// Generated SELECT installs descriptor a before execution and then captures
	// current authority/identity. Its preceding empty activation scratch and the
	// outer view's descriptor v are not admitted target observations.
	if s!=nil&&core.ScopeActiveSQLRoutineDepth(s)>0&&s.Expected!=nil&&len(s.Expected.Results)==1&&s.Expected.Results[0].Name=="a"&&s.RoutineAuth.Catalog.State()!=nil&&s.Identity.CurrentPrincipal.ID()!=0{
		c.seen=true;c.liveScope=s;frame:=s.Op.EffectiveUser
		target,present:=s.RoutineAuth.Catalog.Relation(c.target)
		if c.wrong==""{switch{
		case s.Op!=c.scope.Op:c.wrong="producer changed operation owner"
		case !present||len(target.Columns)!=1:c.wrong="admitted target catalog identity"
		case s.Expected.Results[0].Usage!=target.Columns[0].Usage:c.wrong="admitted target descriptor"
		case frame==nil:c.wrong="missing borrowed frame"
		case frame.Owner!=c.actor:c.wrong="borrowed frame actor"
		case frame.Parent!=c.parent:c.wrong="borrowed frame parent"
		case frame.Restricted!=s.Op.Restricted:c.wrong="borrowed frame restricted owner"
		case s.ForeignActionAuth!=nil:c.wrong="foreign actor not detached"
		case s.Identity.CurrentPrincipal!=c.actor:c.wrong="admitted SELECT expression identity"
		case s.RoutineAuth.State.CurrentUser!=c.actor:c.wrong="admitted SELECT authority"
		}}
	}
	return c.Context.Err()
}

func xmlMapScopeExpressionActorOwnership(t *testing.T){
	e:=engine.NewWithSettings(nil,"xml_scope_actor",false);s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{`CREATE ROLE xs_actor_reader`,`CREATE ROLE xs_actor_view_owner`,`CREATE SCHEMA xs_actor_private`,`CREATE TABLE xs_actor_private.t(a int)`,`INSERT INTO xs_actor_private.t VALUES(1)`,`GRANT USAGE ON SCHEMA xs_actor_private TO xs_actor_reader`,`GRANT SELECT ON xs_actor_private.t TO xs_actor_reader`,`CREATE VIEW public.xs_actor_outer AS SELECT schema_to_xml('xs_actor_private',false,false,'') AS v`,`ALTER VIEW public.xs_actor_outer OWNER TO xs_actor_view_owner`,`GRANT USAGE ON SCHEMA public TO xs_actor_reader`,`GRANT SELECT ON public.xs_actor_outer TO xs_actor_reader`,`SET ROLE xs_actor_reader`}{enginetest.SessionSQL(t,s,q)}
	transaction:=enginetest.TransactionBegin(t,s);defer enginetest.TransactionEnd(t,s,transaction,false)
	cycle:=s.NewProtocolCycle();command:=func(fn func(*core.StatementScope)error){enginetest.PreparedFoldProtocolCommand(t,e,s,cycle,fn)}
	command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("scope_actor",`SELECT v FROM public.xs_actor_outer`,nil);return err})
	command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal("scope_actor_portal","scope_actor",&enginetest.ProtocolReader{Data:[]byte{0,0,0,0,0,0}});return err})
	actor,ok:=e.Roles().Lookup("xs_actor_reader");if !ok{t.Fatal("missing actual expression actor")};viewOwner,ok:=e.Roles().Lookup("xs_actor_view_owner");if !ok||viewOwner.Ref==actor.Ref{t.Fatal("nondistinguishing actual view owner")}
	read:=e.CatalogRead();namespace:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"xs_actor_private"});target:=read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:namespace,Name:"t"})
	if target==(ids.ObjectAddress{}){t.Fatal("missing real target relation")}
	binding:=core.PayloadOf(s).PreparedSQL.Binding("scope_actor_portal")
	if binding==nil||binding.Resource==nil{t.Fatal("missing real actor portal resource")}
	portal,ok:=binding.Resource.Closer.(*core.ProtocolPortal);if !ok{t.Fatalf("actor resource closer %T is not the actual protocol portal",binding.Resource.Closer)}
	observed:=&xmlScopeActorContext{Context:context.Background(),actor:actor.Ref,target:target,portal:portal}
	op,_,err:=s.AcquireCommand(context.Background());if err!=nil{t.Fatal(err)};scope,err:=e.StartProtocolCommand(op,cycle);if err!=nil{op.Done();t.Fatal(err)};observed.scope=scope;observed.parent=op.EffectiveUser;foreign:=scope.ForeignActionAuth
	// Acquisition wraps its parent in a standard cancellation child. Observe
	// that admitted child itself: a parent's custom Err is not its poll hook.
	observed.Context=op.Context();op.Ctx=observed
	count:=0;_,err=scope.ExecuteProtocolPortal("scope_actor_portal",0,func(_ []session.ProtocolField)error{count++;return nil},nil,nil,core.CopyProtocolHooks{})
	if err!=nil||count!=1||!observed.seen||observed.wrong!=""||op.EffectiveUser!=observed.parent||scope.ForeignActionAuth!=foreign||core.ScopeActiveSQLRoutineDepth(scope)!=0||observed.liveScope!=nil&&core.ScopeActiveSQLRoutineDepth(observed.liveScope)!=0{scope.Finish(err);op.Done();t.Fatal("actual view expression actor borrow/restoration",observed.seen,observed.wrong,count,err,observed.polls.String())}
	err=scope.Finish(err);op.Done();if err!=nil{t.Fatal(err)}
	command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal("scope_actor_portal")})
}
