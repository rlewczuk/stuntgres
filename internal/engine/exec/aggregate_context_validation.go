package exec

import (
 "fmt"
 "slices"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func (p *TransitionAggregate) validateContextSelection() error {
 invalid:=func()error{return fmt.Errorf("stuntgres: invalid aggregate-context specialization")}
 recipe:=p.ContextSupport
 definition:=p.Aggregate.Aggregate.Definition
 if definition.Kind=="n"||p.Support.Builtin==nil||p.Final.Builtin==nil||p.Support.Builtin.Providers&functions.AggregateContextRoutineProvider==0||p.Final.Builtin.Providers&functions.AggregateContextRoutineProvider==0||p.Support.Signature!="2281,2276"||p.Final.Signature!="2281,2276"||len(p.Inputs)==0||p.Inputs[0]!=p.StateUsage||!slices.Equal(p.Inputs,p.Formals)||!slices.Equal(p.Inputs[1:],recipe.Rows())||!slices.Equal(p.DirectInputs,recipe.Direct())||len(p.ContextOrder)!=len(recipe.Keys())||len(p.ContextOrder)!=len(recipe.Rows())||len(p.FinalConversions)!=0||!slices.Equal(p.FinalInputs,p.FinalFormals){return invalid()}
 count:=1+len(p.DirectInputs)
 if definition.FinalExtra{count+=len(p.Inputs)-1}
 if len(p.FinalInputs)!=count{return invalid()}
 for i,input:=range p.FinalInputs{
  expected:=p.StateUsage
  if i>0&&i<=len(p.DirectInputs){expected=p.DirectInputs[i-1]}
  if i>len(p.DirectInputs){expected=p.Inputs[i-len(p.DirectInputs)]}
  if input!=expected{return invalid()}
 }
 for i,key:=range p.ContextOrder{
  source:=recipe.Keys()[i]
  nullsFirst:=source.Key.Nulls==parser.NullsFirst||source.Key.Nulls==parser.NullsDefault&&source.Key.Desc
  if key.Slot!=i||key.Cache!=i||key.Desc!=source.Key.Desc||key.NullsFirst!=nullsFirst||!key.Comparison.Equal(source.Comparison)||!key.Comparison.OrderingBound(){return invalid()}
  usage:=recipe.Rows()[i]
  usage.Collation,_=key.Comparison.InputCollation()
  rebound,err:=sqltype.BindOrdering(usage)
  if err!=nil||!rebound.Equal(key.Comparison){return invalid()}
 }
 return nil
}
