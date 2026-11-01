package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// The caller holds the exact heap-statistics owner cohort, then Engine.Mu.
// RC's command pair belongs to StatementScope, not Owner.SnapshotSet/Frontier.
// Unknown advertised peers remain conservative; Xmin is never compared to Born.
func (h *statementHost) PgvisibilityHorizonLocked(owners []*session.TransactionOwner) storage.VisibilityHorizon {
	s,e:=h.scope(),h.scope().Engine()
	known:=make(map[ids.TxRef]ids.CommitSeq,len(owners))
	if s.currentSnapshot!=nil {known[s.TxRef]=s.snapshotFrontier}
	for _,owner:=range owners {
		if owner!=s.Owner && owner.Isolation.Stable() && owner.SnapshotSet && owner.CurrentSnapshot!=nil && (owner.State==session.TxActive||owner.State==session.TxImplicit) && owner.Ref.Domain()==s.TxRef.Domain() && owner.Ref.Epoch()==s.TxRef.Epoch() {known[owner.Ref]=owner.Frontier}
	}
	var result storage.VisibilityHorizon
	for ref,entry:=range e.Visibility.Entries {
		if !entry.Snapshot||ref.Domain()!=s.TxRef.Domain()||ref.Epoch()!=s.TxRef.Epoch(){continue}
		frontier,ok:=known[ref]
		if !ok {result.Unknown=true;continue}
		if !result.Protected||frontier<result.Frontier {result.Frontier=frontier}
		result.Protected=true
	}
	return result
}

// Entered with no owner/engine mutex. The ordinary catalog capture selects its
// genuine immutable catalog/RoleRead roots and registered producer, with no SQL
// execution, SELECT gate or fake physical version identity. Zero Keep forbids
// inheriting the outer query's pg_class/pg_proc projection pruning.
func (h *statementHost) PgvisibilityCatalogBytes(ref ids.ObjectRef, target *catalog.Relation) (int64,error) {
	s,e:=h.scope(),h.scope().Engine()
	e.Mu.Lock()
	if e.Catalog.State.Epoch()!=ref.Epoch||e.PrivateCatalogLocked(s.PendingCatalog()).Get(ref.Address)!=target {e.Mu.Unlock();return 0,ids.ErrCatalogReplaced}
	if err:=e.CaptureCommentSnapshotLocked(s);err!=nil {e.Mu.Unlock();return 0,err}
	source,err:=e.openCatalogSourceLocked(s,ref,target,pgcatalog.Keep{})
	if err!=nil {return 0,err}
	defer source.Close()
	var bytes int64
	for i:=range source.Count() {
		if err:=s.Op.Context().Err();err!=nil {return 0,err}
		n,err:=storage.RowAllocation(s.Op.Context(),source.Row(i));if err!=nil{return 0,err}
		n,err=storage.AddSize(n,4);if err!=nil{return 0,err}
		bytes,err=storage.AddSize(bytes,n);if err!=nil{return 0,err}
	}
	return bytes,nil
}
