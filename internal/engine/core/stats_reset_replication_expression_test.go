package core_test

import (
 "context"
 "fmt"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
)

func TestStatsResetReplicationExpressionAuthority(t *testing.T) {
 e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
 a,b:=resetStatsCreate(t,e,s,"reset_expression_a"),resetStatsCreate(t,e,s,"reset_expression_b")
 beforeA,beforeB:=resetStatsSeed(e,a,1),resetStatsSeed(e,b,11)
 op,err:=s.Acquire(context.Background()); if err!=nil { t.Fatal(err) }
 defer func(){ if op!=nil { op.Done() } }()
 query:=enginetest.BindSelect(t,e,op,"SELECT pg_stat_reset_subscription_stats(NULL::oid)")
 project,ok:=query.Root.(*plan.ProjectNode); if !ok || len(project.Expressions)!=1 { t.Fatal("missing typed project",query.Root) }
 node,ok:=project.Expressions[0].(functions.StatsResetReplication)
 if !ok || node.Validate()!=nil || node.Kind!=functions.ResetSubscriptionStats || node.IntrinsicVolatility()!=expr.Volatile || node.PlanningFold()!=expr.FoldNever || node.PreflightFold()!=expr.PreflightKeep || node.StrictNull()!=expr.StrictNone || node.ClosedDefault(func(expr.Expr)bool{return true}) { t.Fatal("typed volatile protocol",node) }
 original:=node.Argument
 children:=node.Children(); if len(children)!=1 || !reflect.DeepEqual(children[0],original) { t.Fatal("unary ownership") }; children[0]=nil
 if !reflect.DeepEqual(node.Argument,original) { t.Fatal("borrowed mutable children") }
 rebuilt,err:=node.Rebuild(node.Info,[]expr.Expr{original}); if err!=nil || !node.SameGroup(rebuilt,func(a,b expr.Expr)bool{return reflect.DeepEqual(a,b)},false) { t.Fatal("rebuild",rebuilt,err) }
 if _,err:=node.Rebuild(node.Info,nil); err==nil { t.Fatal("invalid reconstruction admitted") }
 rendering,err:=node.Render(); if err!=nil || rendering.Name!="pg_stat_reset_subscription_stats" || len(rendering.Args)!=1 || !reflect.DeepEqual(rendering.Args[0].Expr,original) { t.Fatal("render retained wrong operand",rendering,err) }
 capture,ok,err:=node.Capture(expr.CaptureFunction); if err!=nil || !ok || capture.Count!=1 || !capture.Indexed { t.Fatal("capture",capture,err) }
 if child,err:=node.CaptureOperand(0); err!=nil || !reflect.DeepEqual(child,original) { t.Fatal("capture operand",child,err) }
 if _,err:=node.CaptureOperand(1); err==nil { t.Fatal("invalid capture admitted") }
 if err:=node.ValidateStoredProvider(); err!=nil { t.Fatal(err) }
 op.Done(); op=nil
 if e.Statistics.Read(a)!=beforeA || e.Statistics.Read(b)!=beforeB { t.Fatal("binding evaluated reset") }
 ast,description:=enginetest.PreparedDescription(t,e,s,"SELECT pg_stat_reset_subscription_stats($1::oid)")
 found:=false
 for _,dependency:=range description.Analysis.Dependencies { if dependency.Ref.Address.ObjectID==6232 { found=true } }
 if !found { t.Fatal("routine admission dependency absent") }
 if e.Statistics.Read(a)!=beforeA || e.Statistics.Read(b)!=beforeB { t.Fatal("Prepare/Describe sampled/reset") }
 argument:=fmt.Sprint(a.Object.Address.ObjectID)
 if err:=resetStatsPrepared(e,s,ast,&description,&argument); err!=nil { t.Fatal(err) }; resetStatsZero(t,e,a)
 if e.Statistics.Read(b)!=beforeB { t.Fatal("held argument changed wrong key") }
 argument=fmt.Sprint(b.Object.Address.ObjectID)
 if err:=resetStatsPrepared(e,s,ast,&description,&argument); err!=nil { t.Fatal(err) }; resetStatsZero(t,e,b)
 // An ordinary stored view admits volatile expressions without evaluating
 // them. Its Boolean projection avoids storing the VOID pseudotype itself.
 before:=resetStatsSeed(e,a,17)
 beforeStoredB:=resetStatsSeed(e,b,27)
 statsFunctionExec(t,s,"CREATE VIEW reset_expression_view AS SELECT pg_stat_reset_subscription_stats(NULL::oid) IS NULL AS v")
 read:=e.CatalogRead()
 ref:=enginetest.CatalogRef(t,e,read,"reset_expression_view")
 relation:=read.MustRelation(ref.Address)
 definition:=stored.ViewOf(relation)
 if definition==nil || definition.Query()==nil { t.Fatal("missing accepted stored view") }
 storedRoot,ok:=definition.Query().Root.(*plan.ProjectNode)
 if !ok || len(storedRoot.Expressions)!=1 { t.Fatal("invalid stored projection",definition.Query().Root) }
 storedExpression:=storedRoot.Expressions[0]
 storedBacking:=&storedRoot.Expressions[0]
 storedRevision:=definition.DefinitionRevision()
 var storedNode functions.StatsResetReplication
 typed:=false
 plan.WalkExpression(storedExpression,func(child expr.Expr){ if n,yes:=child.(functions.StatsResetReplication); yes { storedNode=n; typed=true } })
 if !typed || storedNode.ValidateStoredProvider()!=nil || storedNode.RoutineReference==nil || storedNode.RoutineReference.RoutineOID()!=6232 { t.Fatal("stored provider authority",storedExpression) }
 storedCapture,accepted,err:=storedNode.Capture(expr.CaptureFunction)
 if err!=nil || !accepted || storedCapture.Count!=1 || !storedCapture.Indexed { t.Fatal("stored operand capture",storedCapture,err) }
 if child,err:=storedNode.CaptureOperand(0); err!=nil || !reflect.DeepEqual(child,storedNode.Argument) { t.Fatal("stored owned operand",child,err) }
 _,viewDescription:=enginetest.PreparedDescription(t,e,s,"SELECT v FROM reset_expression_view")
 defer e.RetirePreparedDescription(&viewDescription)
 if stored.ViewOf(e.CatalogRead().MustRelation(ref.Address))!=definition || definition.Query().Root!=storedRoot || &storedRoot.Expressions[0]!=storedBacking || definition.DefinitionRevision()!=storedRevision || !reflect.DeepEqual(storedRoot.Expressions[0],storedExpression) { t.Fatal("Prepare mutated accepted stored view backing") }
 if e.Statistics.Read(a)!=before || e.Statistics.Read(b)!=beforeStoredB { t.Fatal("stored CREATE/Prepare executed volatile reset") }
 statsFunctionExec(t,s,"CREATE ROLE reset_expression_reader","SET ROLE reset_expression_reader")
 if err:=resetStatsPrepared(e,s,ast,&description,nil); err==nil { t.Fatal("held statement retained superuser privilege") }
 statsFunctionExec(t,s,"RESET ROLE","GRANT EXECUTE ON FUNCTION pg_stat_reset_subscription_stats(oid) TO reset_expression_reader","SET ROLE reset_expression_reader")
 if err:=resetStatsPrepared(e,s,ast,&description,nil); err!=nil { t.Fatal(err) }
 statsFunctionExec(t,s,"RESET ROLE","REVOKE EXECUTE ON FUNCTION pg_stat_reset_subscription_stats(oid) FROM reset_expression_reader","SET ROLE reset_expression_reader")
 heldBefore:=e.Statistics.Read(a)
 if err:=resetStatsPrepared(e,s,ast,&description,nil); err==nil || e.Statistics.Read(a)!=heldBefore { t.Fatal("revoked held statement mutated provider",err) }
 statsFunctionExec(t,s,"RESET ROLE")
 if node.RoutineReference==nil || node.RoutineReference.RoutineOID()!=6232 { t.Fatal("lost executable declaration association") }
}
