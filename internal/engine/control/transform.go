package control

import (
 "context"
 "fmt"
 "slices"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/dump"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
)

// transformEvents uses the ordinary declaration graph. Temporal promises are
// private replay operations on actual surviving converter identities: a source
// may legally ALTER a referenced converter VOLATILE after defining its pair.
// Public CREATE validation is never bypassed, and final VOLATILE metadata is
// restored before any affected or default host consumer can be declared/run.
func (s *dumpScript) transformEvents() error {
 if len(s.Transforms)==0 { return nil }
 var completion dump.Event
 var affected []dump.Event
 for _,pair:=range s.Transforms {
  event:=dump.Event{OID:pair.Address().ObjectID}
  s.Graph[event]=nil
  var failure error
  pair.Dependencies(func(address ids.ObjectAddress)bool {
   predecessor,err:=(Resolver{}).Predecessor(s.CatalogView,address,pair.Address())
   if err!=nil { failure=err; return false }
   if predecessor!=0 { s.Graph.Add(event,dump.Event{OID:predecessor}) }
   return true
  })
  if failure!=nil { return failure }
  if catalog.TransformBootstrapEquivalent(s.CatalogView,pair) { continue }
  needsPromise:=false
  for _,address:=range [...]ids.ObjectAddress{pair.From,pair.To} {
   if address.ObjectID==0 { continue }
   p,ok:=s.CatalogView.Procedure(address)
   if !ok { return fmt.Errorf("stuntgres: missing transform replay callback") }
   if p.Volatility!=expr.Volatile { continue }
   if p.HostConversion==nil || catalog.ValidateHostConversion(s.CatalogView,p)!=nil {
    return fmt.Errorf("stuntgres: volatile external transform replay callback is unavailable")
   }
   ns,ok:=s.CatalogView.Namespace(p.Namespace())
   if !ok { return fmt.Errorf("stuntgres: missing transform replay callback namespace") }
   sql:="ALTER FUNCTION "+catalog.QuoteIdentifier(ns.Name())+"."+catalog.QuoteIdentifier(p.Name())+"(pg_catalog.internal) "
   id:=p.Address().ObjectID
   if s.transformPromises==nil {
    s.transformPromises=make(map[ids.OID]string)
    s.transformFinalVolatility=make(map[ids.OID]string)
   }
   s.transformPromises[id]=sql+"STABLE;\n"
   s.transformFinalVolatility[id]=sql+"VOLATILE;\n"
   promise:=dump.Event{OID:id,Phase:dump.TransformPromise}
   s.Graph[promise]=nil
   s.Graph.Add(event,promise)
   if completion.OID==0 || id<completion.OID { completion=dump.Event{OID:id,Phase:dump.TransformCompletion} }
   needsPromise=true
  }
  if needsPromise { affected=append(affected,event) }
 }
 if completion.OID==0 { return nil }
 // One real callback anchors the shared completion event. Its prerequisites
 // are every pair that needs a bounded promise, not an invented object/OID.
 s.Graph[completion]=affected
 for event,predecessors:=range s.Graph {
  if event==completion || event.Phase==dump.TransformPromise { continue }
  if _,pair:=s.CatalogView.Get(ids.ObjectAddress{ClassID:ids.TransformClassID,ObjectID:event.OID}).(*catalog.Transform); pair { continue }
  needsCompletion:=false
  for _,predecessor:=range predecessors {
   if _,pair:=s.CatalogView.Get(ids.ObjectAddress{ClassID:ids.TransformClassID,ObjectID:predecessor.OID}).(*catalog.Transform); pair { needsCompletion=true; break }
  }
  if p,ok:=s.CatalogView.Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:event.OID}); ok && p.Host!=nil { needsCompletion=true }
  if needsCompletion { s.Graph.Add(event,completion) }
 }
 return nil
}
func emitTransformPromise(_ context.Context,s *dumpScript,w *dumpWriter,event dump.Event)error {
 return w.emit(s.transformPromises[event.OID])
}
func emitTransformCompletion(_ context.Context,s *dumpScript,w *dumpWriter,_ dump.Event)error {
 // Map traversal must not make the logical stream depend on Go iteration.
 // Providers are keyed by real OIDs; use source catalog order only through a
 // sorted list, just like the ordinary provider identity replay planner.
 addresses:=make([]ids.OID,0,len(s.transformFinalVolatility))
 for id:=range s.transformFinalVolatility { addresses=append(addresses,id) }
 slices.Sort(addresses)
 for _,id:=range addresses { if err:=w.emit(s.transformFinalVolatility[id]); err!=nil { return err } }
 return nil
}
