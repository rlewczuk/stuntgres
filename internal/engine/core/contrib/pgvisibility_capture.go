package contrib

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func pgvisibilityAuthority[A Authority](auth A, n plan.InstalledPgvisibilityExpr) error {
	view:=auth.Access()
	if n.Procedure==nil||n.CallRoutineReference()!=n.Procedure||n.Ref.Domain()!=view.Catalog.Owner().Domain||n.Ref.Epoch!=view.Catalog.Epoch(){return ids.ErrCatalogReplaced}
	current,ok:=view.Catalog.Procedure(n.Ref.Address)
	if !ok||!exec.SameRoutineDeclaration(current,n.Procedure){return ids.ErrCatalogReplaced}
	if err:=extension.ValidatePgvisibilityIdentity(view.Catalog,current,n.Ref);err!=nil{return err}
	boundary:=n.Pgvisibility;boundary.RoutineReference=current
	if err:=extension.ValidatePgvisibilityCall(view.Catalog,current,boundary);err!=nil{return err}
	if err:=auth.Check(access.Requirement{Object:view.Ref(current.Namespace()),Privileges:authorization.UsagePrivilege});err!=nil{return err}
	return auth.Check(access.Requirement{Object:n.Ref,Privileges:authorization.ExecutePrivilege})
}

// pgvisibilityTargetCheckable reads descriptor facts only. Target kind precedes
// block validation; TEMP buffer policy belongs to the captured physical layout.
func pgvisibilityTargetCheckable(v catalog.Read, target *catalog.Relation) error {
	label:=""
	if target.RelationKind==catalog.CatalogRelation && target.CatalogView {label="views"} else {
		switch target.RelationKind {
		case catalog.TableRelation,catalog.MaterializedViewRelation,catalog.CatalogRelation:
		case catalog.PartitionedRelation:label="partitioned tables"
		case catalog.ViewRelation:label="views"
		case catalog.ForeignRelation:label="foreign tables"
		case catalog.SequenceRelation:label="sequences"
		case catalog.IndexRelation:
			label="indexes"
			if target.SQLIndex!=nil {if parent,ok:=v.Relation(target.SQLIndex.Parent);ok&&parent.Partition!=nil {label="partitioned indexes"}}
		default:label="composite types"
		}
	}
	if label=="" {return nil}
	return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42809",Message:fmt.Sprintf("relation %q is of wrong relation kind",target.Name()),Detail:"This operation is not supported for "+label+"."}
}

func Pgvisibility[A Authority](h Host[A], n plan.InstalledPgvisibilityExpr, oid uint32, block int64) (storage.VisibilityMap,error) {
	if err:=h.Active();err!=nil{return storage.VisibilityMap{},err}
	h.ReacquireEngine()
	auth,err:=h.RoutineAuthorizationLocked()
	if err==nil {err=pgvisibilityAuthority(auth,n)}
	var target *catalog.Relation
	var store *storage.Relation
	if err==nil {target,_,err=amcheckTargetIdentity(auth.Access().Catalog,oid)}
	if err==nil {store=h.PrivateStorageLocked(target.Address())}
	epoch:=h.CatalogEpochLocked()
	h.ReleaseEngineLocked()
	if err!=nil{return storage.VisibilityMap{},err}
	ref:=h.CatalogDomain().Ref(epoch,target.Address())
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err:=h.AcquireRelationContribution(ref,lock.AccessShare,&contribution);err!=nil{return storage.VisibilityMap{},err}
	writes,deletes:=h.ColumnProjectionRoots(target.Address())
	owners,err:=h.LockHeapStatisticsCohort()
	if err!=nil{return storage.VisibilityMap{},err}
	auth,err=h.RoutineAuthorizationLocked()
	if err==nil && auth.Access().Catalog.Epoch()!=epoch {err=ids.ErrCatalogReplaced}
	if err==nil {err=pgvisibilityAuthority(auth,n)}
	if err==nil && (auth.Access().Catalog.Get(target.Address())!=target||h.PrivateStorageLocked(target.Address())!=store){err=ids.ErrCatalogReplaced}
	if err==nil {h.MarkTemporaryTargetLocked(ref);err=pgvisibilityTargetCheckable(auth.Access().Catalog,target)}
	if err==nil && n.Kind.Block() && (block<0||block>4294967294){err=sqlError("22023","invalid block number")}
	var facts []storage.HeapTupleFact
	var horizon storage.VisibilityHorizon
	foreign:=false
	if err==nil {
		if namespace,ok:=auth.Access().Catalog.Namespace(target.Namespace());ok {foreign=namespace.Temporary!=(ids.SessionToken{})&&namespace.Temporary!=h.Operation().Session.SessionToken}
		if store!=nil {
			facts=h.HeapStatisticsFactsLocked(owners,store,target.Address(),writes,deletes)
			horizon=h.PgvisibilityHorizonLocked(owners)
		} else if target.RelationKind!=catalog.CatalogRelation {err=ids.ErrCatalogReplaced}
	}
	h.UnlockHeapStatisticsCohortLocked(owners)
	if err!=nil{return storage.VisibilityMap{},err}
	var result storage.VisibilityMap
	if store==nil {
		bytes,failure:=h.PgvisibilityCatalogBytes(ref,target)
		err=failure
		if err==nil {result,err=storage.CatalogVisibility(bytes)}
	} else {
		maintenance:=make([]lock.TupleMaintenance,len(facts))
		for i:=range facts {maintenance[i].Tuple=lock.MultiTuple{Resource:lock.Resource{ObjectRef:ref,Row:facts[i].Version.Row},Version:facts[i].Version.Version}}
		// Manager.mu is never nested under owner/Engine mutexes. Unlike
		// Pgrowlocks this batch includes private/updater/predecessor versions.
		err=h.LockManager().CaptureTupleMaintenance(h.Operation().Context(),h.Ticket(),maintenance)
		if err==nil {
			for i:=range facts {facts[i].LockMaintenance=maintenance[i].Present}
			result,err=storage.CaptureVisibility(h.Operation().Context(),facts,horizon,false)
		}
	}
	if err!=nil{return storage.VisibilityMap{},err}
	h.ReacquireEngine()
	auth,err=h.RoutineAuthorizationLocked()
	if err==nil {err=pgvisibilityAuthority(auth,n)}
	valid:=err==nil&&h.CatalogEpochLocked()==epoch&&auth.Access().Catalog.Get(target.Address())==target&&h.PrivateStorageLocked(target.Address())==store
	h.ReleaseEngineLocked()
	if err!=nil{return storage.VisibilityMap{},err}
	if !valid{return storage.VisibilityMap{},ids.ErrCatalogReplaced}
	if err:=h.Operation().Context().Err();err!=nil{return storage.VisibilityMap{},err}
	if foreign {
		buffer:=n.Kind==functions.VisibilityPageRelation
		if n.Kind.Block() {buffer=block<result.MappedBlocks||n.Kind.Page()&&block<result.Blocks} else {buffer=buffer||result.Blocks!=0&&result.MappedBlocks!=0}
		if buffer{return storage.VisibilityMap{},sqlError("0A000","cannot access temporary tables of other sessions")}
	}
	return result,nil
}
