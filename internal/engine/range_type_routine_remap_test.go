package engine

import (
 "context"
 "testing"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/routine"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/engine/typedef"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func rangeTypeUnitRemappedConstructor(t *testing.T) (*Engine,*catalog.Procedure,*catalog.Procedure) {
 t.Helper()
 source,original,_:=rangeTypeUnitGeneratedCast(t)
 originalType:=original.RangeConstructor.Type
 next:=catalog.CloneState(source.State())
 target:=New()
 roles:=authorization.Seed(source.Owner().Principals)
 sourceRoles:=roles.Read(source.Owner().Principals.Principal(authorization.BootstrapPrincipalID))
 targetRoles:=target.RoleStore.PublishedRead(&target.Catalog.Owner,target.CatalogRead())
 next,principal,err:=func()(*catalog.State,catalog.ProcedureRemapper,error){target.Mu.Lock();defer target.Mu.Unlock();return target.remapPreparedCatalog(next,sourceRoles,targetRoles,nil)}()
 if err!=nil {t.Fatal(err)}
 if _,err:=typedef.ReownDomainTypes(next,nil,true,routine.RangeTypeRebinder(next,nil));err!=nil {t.Fatal(err)}
 view:=target.Catalog.Read(next)
 remap:=stored.NewRoutineRemap(view,nil,nil,principal,false)
 if err:=routine.RebindSQLDefinitions(next,remap);err!=nil {t.Fatal("constructor declaration stabilization reapplied source principal policy",err)}
 current:=next.MustProcedure(original.Address())
 again,err:=remap.Procedure(original)
 if err!=nil||again!=current {t.Fatal("original and catalog consumers lost the operation memo",err)}
 if current.Owner!=target.Catalog.Owner.Principals.Principal(authorization.BootstrapPrincipalID)||original.Owner==current.Owner {t.Fatal("constructor owner was not reprovenanced exactly once")}
 if current.RangeConstructor.Type==originalType||original.RangeConstructor.Type!=originalType {t.Fatal("constructor type generation was transplanted or source mutated")}
 if err:=catalog.ValidateRangeConstructor(view,current);err!=nil {t.Fatal(err)}
 target.Catalog.PublishLocked(next)
 return target,original,current
}

func TestRangeTypeConstructorOriginalRemap(t *testing.T) {
 _,_,_ = rangeTypeUnitRemappedConstructor(t)
}

func TestRangeTypeGeneratedCastFrozenSlots(t *testing.T) {
 target,_,_:=rangeTypeUnitRemappedConstructor(t)
 for _,sql:=range []string{`SELECT ('[1,3]'::public.unit_span)::public.unit_spans`,`SELECT (NULL::public.unit_span)::public.unit_spans`} {
  binder:=newTestExpressionBinder(t,context.Background(),target,sqltype.InputEnvironment{},nil)
  binder.Params.NoParameters=true
  raw:=statement(t,sql).(*parser.Select).Targets[0]
  bound,err:=binder.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext})
  if err!=nil {t.Fatal(err)}
  if err:=binder.CaptureViewLiterals(raw,bound,analyze.ViewTargetLiteral,0);err!=nil {t.Fatal("internal negative slots leaked into frozen cast children",err)}
 }
}
