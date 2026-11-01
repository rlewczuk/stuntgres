package lock

import (
	"context"

	"stuntgres/internal/engine/ids"
)

// TupleMaintenance is caller-owned detached scratch for one exact physical
// identity. Present includes raw xmax work even when Pgrowlocks filters that
// tuple as an own updater or private insertion. No owner authority is returned.
type TupleMaintenance struct {
	Tuple MultiTuple
	Present bool
}

// CaptureTupleMaintenance runs outside Engine/owner mutexes and takes only
// Manager.mu. It never assigns identity, settles queues, touches contribution
// ownership, or changes raw history. The caller discards scratch on failure.
func (m *Manager) CaptureTupleMaintenance(ctx context.Context, caller *Ticket, tuples []TupleMaintenance) error {
	if err:=ctx.Err();err!=nil{return err}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(caller)||caller.OwnershipEnded(){return ids.ErrCatalogReplaced}
	for i:=range tuples {
		if err:=ctx.Err();err!=nil{return err}
		key:=tuples[i].Tuple
		if !m.validMultiTuple(caller,key){return ids.ErrCatalogReplaced}
		row:=m.multiRows[key]
		tuples[i].Present=len(row.members)!=0||len(row.inheritedMembers)!=0
	}
	return nil
}
