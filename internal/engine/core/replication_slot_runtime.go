package core

import (
 "fmt"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/replication"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// ReplicationSlot borrows current authority under Engine -> Session ordering.
// It publishes no slot, ownership or WAL state, even for temporary requests.
func (h *statementHost) ReplicationSlot(kind functions.ReplicationSlotKind, arity uint8, values [5]sqltype.Value) error {
 s:=h.scope(); e:=s.Engine()
 e.Mu.Lock()
 auth,err:=s.RoutineAuthorizationLocked()
 if err!=nil { e.Mu.Unlock(); return err }
 actor,err:=auth.Roles.Descriptor(auth.State.CurrentUser)
 e.Mu.Unlock()
 if err!=nil { return err }
 if !actor.Superuser && !actor.Replication { return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42501",Message:"permission denied to use replication slots",Detail:"Only roles with the REPLICATION attribute may use replication slots."} }
 name,err:=values[0].Text(); if err!=nil { return err }
 switch kind {
 case functions.SlotCreatePhysical,functions.SlotCreateLogical:
  if err:=replication.ValidateSlotName(name); err!=nil { return err }
  if kind==functions.SlotCreatePhysical { return SQLError("0A000","pg_create_physical_replication_slot physical replication slots and WAL reservation are unsupported in embedded mode") }
  temporary,failover:=false,false
  if values[2].Type()!=nil { temporary,err=values[2].Boolean(); if err!=nil { return err } }
  if values[4].Type()!=nil { failover,err=values[4].Boolean(); if err!=nil { return err } }
  if temporary && failover { return SQLError("0A000","cannot enable failover for a temporary replication slot") }
  plugin,err:=values[1].Text(); if err!=nil { return err }
  list,_:=s.Op.Setting("output_plugin_libraries")
  if !replication.PluginAllowed(list,plugin) { return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42501",Message:fmt.Sprintf(`library "%s" may not be used as an output plugin`,plugin),Hint:`If it is safe for all REPLICATION users to use this library as an output plugin, add it to "output_plugin_libraries" and reload the server configuration.`} }
  return SQLError("0A000","pg_create_logical_replication_slot logical decoding and WAL providers are unsupported in embedded mode")
 case functions.SlotAdvance:
  lsn,err:=values[1].PGLSN(); if err!=nil { return err }; if lsn==0 { return SQLError("22023","invalid target WAL LSN") }
 case functions.SlotDrop,functions.SlotCopyPhysical,functions.SlotCopyLogical:
 default: return fmt.Errorf("stuntgres: invalid replication slot operation %d/%d",kind,arity)
 }
 return SQLError("42704",fmt.Sprintf(`replication slot "%s" does not exist`,name))
}
