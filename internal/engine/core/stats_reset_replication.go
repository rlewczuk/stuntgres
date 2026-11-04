package core

import (
 "fmt"
 "time"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/statistics"
 "stuntgres/internal/sqltype"
)

// Ordinary routine admission owns EXECUTE. This runtime borrows the current
// operation and cluster under their shared mutex; provider locks remain leaves.
func (h *statementHost) StatsResetReplication(kind functions.StatsResetReplicationKind, argument sqltype.Value, result *sqltype.Descriptor) (sqltype.Value,error) {
 s:=h.scope()
 if s==nil || s.Op==nil || s.Op.Session==nil || result==nil || result.ID()!=sqltype.Void || kind.Name()=="" || argument.Type()==nil || argument.Type().ID()!=kind.ArgumentType() { return sqltype.Value{},fmt.Errorf("stuntgres: invalid replication statistics reset authority") }
 if err:=h.Context().Err(); err!=nil { return sqltype.Value{},err }
 e:=s.Engine(); e.Mu.Lock(); defer e.Mu.Unlock()
 _,err:=s.RoutineAuthorizationLocked(); if err!=nil { return sqltype.Value{},err }
 at:=time.Now().UTC().Truncate(time.Microsecond)
 if kind==functions.ResetReplicationSlot && !argument.IsNull() {
  name,err:=argument.Text(); if err!=nil { return sqltype.Value{},err }
  slot:=e.Cluster.logical.slots[name]
  if slot==nil { return sqltype.Value{},SQLError("22023",fmt.Sprintf(`replication slot "%s" does not exist`,name)) }
  slot.statsReset=at
  return sqltype.VoidValue(result),nil
 }
 var oid uint32
 if kind==functions.ResetSubscriptionStats && !argument.IsNull() {
  oid,err=argument.OID(); if err!=nil { return sqltype.Value{},err }
  if oid==0 { return sqltype.Value{},SQLError("22023","invalid subscription OID 0") }
 }
 category:=statistics.Subscription; if kind==functions.ResetReplicationSlot { category=statistics.ReplicationSlot }
 if argument.IsNull() {
  // Each engine owns its one actual provider, including private real creations.
  e.Statistics.ResetCategory(category,at)
  if kind==functions.ResetReplicationSlot && e.Cluster!=nil { for _,slot:=range e.Cluster.logical.slots { slot.statsReset=at } }
  if e.Cluster!=nil { for _,target:=range e.Cluster.Targets { if target!=e { target.Statistics.ResetCategory(category,at) } } }
  return sqltype.VoidValue(result),nil
 }
 // Existing shared records, not caller-private or committed descriptors, own
 // reset eligibility. Provisional CREATE and staged DROP retain that authority.
 if !e.Statistics.ResetSubscriptionOID(ids.OID(oid),at) && e.Cluster!=nil {
  for _,target:=range e.Cluster.Targets {
   if target!=e && target.Statistics.ResetSubscriptionOID(ids.OID(oid),at) { break }
  }
 }
 return sqltype.VoidValue(result),nil
}
