package exec

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/sqltype"
)

func (p *TransitionAggregate) remapContextSupport(r functions.ProviderRemap) (functions.AggregateProvider,error) {
 if err:=p.ValidateSelection();err!=nil{return nil,err}
 next:=*p
 mapRoutine:=func(old *catalog.Procedure)(*catalog.Procedure,error){
  mapped,err:=r.Routine(old);if err!=nil{return nil,err};d,ok:=mapped.(*catalog.Procedure);if !ok||d==nil{return nil,fmt.Errorf("stuntgres: invalid aggregate-context routine remap")};return d,nil
 }
 mapUsage:=func(source sqltype.Usage)(sqltype.Usage,error){
  if source.Type.ID()!=2281{return r.Usage(source)}
  if source!=source.Type.Usage()||r.Type==nil{return sqltype.Usage{},fmt.Errorf("stuntgres: invalid internal aggregate-state remap")}
  mapped:=r.Type(source.Type)
  if mapped==nil||mapped.ID()!=2281{return sqltype.Usage{},fmt.Errorf("stuntgres: missing internal aggregate-state type")}
  return mapped.Usage(),nil
 }
 var err error
 if next.Aggregate,err=mapRoutine(p.Aggregate);err!=nil{return nil,err}
 if next.Support,err=mapRoutine(p.Support);err!=nil{return nil,err}
 if next.Final,err=mapRoutine(p.Final);err!=nil{return nil,err}
 if next.StateUsage,err=mapUsage(p.StateUsage);err!=nil{return nil,err}
 if next.Result,err=r.Usage(p.Result);err!=nil{return nil,err}
 mapUsages:=func(source []sqltype.Usage)([]sqltype.Usage,error){
  result:=make([]sqltype.Usage,len(source));for i,u:=range source{var e error;result[i],e=mapUsage(u);if e!=nil{return nil,e}};return result,nil
 }
 if next.Inputs,err=mapUsages(p.Inputs);err!=nil{return nil,err}
 next.Formals=append([]sqltype.Usage(nil),next.Inputs...)
 if next.DirectInputs,err=mapUsages(p.DirectInputs);err!=nil{return nil,err}
 if next.FinalInputs,err=mapUsages(p.FinalInputs);err!=nil{return nil,err}
 next.FinalFormals=append([]sqltype.Usage(nil),next.FinalInputs...)
 next.ContextOrder=make([]plan.SortKey,len(p.ContextOrder))
 keys:=append([]functions.AggregateSortKey(nil),p.ContextSupport.Keys()...)
 for i,key:=range p.ContextOrder{
  next.ContextOrder[i]=key
  next.ContextOrder[i].Expression=nil
  source:=p.ContextSupport.Rows()[i]
  source.Collation,_=key.Comparison.InputCollation()
  usage,err:=r.Usage(source);if err!=nil{return nil,err}
  comparison,err:=functions.AggregateOrdering(usage,keys[i].Key.Pos,false);if err!=nil{return nil,err}
  next.ContextOrder[i].Comparison=comparison
  keys[i].Expression=nil
  keys[i].Comparison=comparison
 }
 if p.InputCollation.ID!=0{
  found:=false
  for i,u:=range p.Inputs{if u.Collation==p.InputCollation{next.InputCollation=next.Inputs[i].Collation;found=true;break}}
  if !found{for i,u:=range p.DirectInputs{if u.Collation==p.InputCollation{next.InputCollation=next.DirectInputs[i].Collation;found=true;break}}}
  if !found{return nil,fmt.Errorf("stuntgres: aggregate-context collation has no owner")}
 }
 next.ContextSupport,err=functions.NewAggregateContextSupport(uint32(next.Support.Address().ObjectID),uint32(next.Final.Address().ObjectID),p.ContextSupport.FinalExtra(),p.ContextSupport.Hypothetical(),next.Result,next.Inputs[1:],next.DirectInputs,keys)
 if err!=nil{return nil,err}
 if err=next.ValidateSelection();err!=nil{return nil,err}
 return &next,nil
}
