package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Schema introspection holds the genuine relation lifetime without demanding
// SELECT authority. Data reads still pass ordinary SELECT binding and ACL/RLS.
func (s *StatementScope) lockXMLMappingRelation(read catalog.Read,relation *catalog.Relation)(catalog.Read,*catalog.Relation,error){
	e:=s.Engine();ref:=e.Catalog.Domain.Ref(read.Epoch(),relation.Address())
	policy:=lock.Policy{TimeoutSlot:&s.Op.LockWait,Timeout:s.Op.LockTimeout,StatementDeadline:s.Op.StatementDeadline,Source:s.Op.SourceContext}
	request,err:=e.Locks.StartAcquire(s.Op.Context(),s.LockTicket,lock.Resource{ObjectRef:ref},lock.AccessShare,policy)
	if request!=nil{s.admissionWaited=true;if s.ReplacementRead{e.ReplacementMu.RUnlock();s.ReplacementRead=false};err=request.Wait()}
	if err!=nil{return read,nil,err}
	s.Owner.Mu.Lock();implicit:=s.Owner.State==session.TxImplicit;s.Owner.Mu.Unlock()
	if implicit&&!s.ReplacementRead{e.ReplacementMu.RLock();s.ReplacementRead=true}
	current:=xmlMappingRead(s)
	if current.Epoch()!=ref.Epoch{return current,nil,ids.ErrCatalogReplaced}
	d,ok:=current.Relation(ref.Address);if !ok{return current,nil,SQLError("XX000",fmt.Sprintf("could not open relation with OID %d",ref.Address.ObjectID))}
	return current,d,nil
}

// table_open accepts relation-backed rows, not index or composite-type storage.
// The data operation reaches this opening through its generated SPI SELECT.
func xmlMappingTableEligibility(relation *catalog.Relation,text string,schema bool)error{
	if relation==nil{return nil}
	kind:=""
	switch relation.RelationKind{case catalog.IndexRelation:kind="indexes";case catalog.CompositeRelation:kind="composite types";default:return nil}
	d:=SQLError("42809",fmt.Sprintf("cannot open relation \"%s\"",relation.Name())).(*sqlstate.Error)
	d.Detail="This operation is not supported for "+kind+"."
	if !schema{d.InternalPosition,d.InternalQuery="15",text}
	return d
}
