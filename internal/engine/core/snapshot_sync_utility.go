package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// NoteUtilityExpressionDemandLocked consumes an already cooked executable
// recipe. It never binds, folds, authorizes or evaluates it. Engine.mu is held.
func (s *StatementScope) NoteUtilityExpressionDemandLocked(read catalog.Read, node expr.Expr) {
	if s.exportRootDemand || node == nil { return }
	plan.WalkExpression(node,func(child expr.Expr){s.noteUtilityNodeDemandLocked(read,child)})
}

func (s *StatementScope) noteUtilityNodeDemandLocked(read catalog.Read, node expr.Expr) {
	if s.exportRootDemand { return }
	s.exportRootDemand=expressionExportRootDemand(read,node)
	s.NoteUtilityTypeDemandLocked(read,node.ResultUsage().Type)
}

func (s *StatementScope) NoteUtilityTypeDemandLocked(read catalog.Read, typ *sqltype.Descriptor) {
	if s.exportRootDemand { return }
	var active [16]*sqltype.Descriptor
	s.exportRootDemand=utilityTypeExportDemand(read,typ,active[:0])
}

func utilityTypeExportDemand(read catalog.Read, typ *sqltype.Descriptor, active []*sqltype.Descriptor) bool {
	if typ == nil || !sqltype.ContainsDomainConstraints(typ) { return false }
	for _, previous := range active { if previous == typ { return false } }
	active=append(active,typ)
	if typ.IsDomain() {
		if descriptor,ok:=read.Get(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(typ.ID())}).(*catalog.Type);ok {
			for _,check:=range descriptor.DomainChecks {
				found:=false
				plan.WalkExpression(check.Expression,func(node expr.Expr){found=found||expressionExportRootDemand(read,node)})
				if found { return true }
				for _,dependency:=range check.Dependencies { if routine,ok:=read.Procedure(dependency.Ref.Address);ok&&exec.RoutineExportRootDemand(read,routine){return true} }
			}
		}
		return utilityTypeExportDemand(read,typ.DomainBase().Type,active)
	}
	if typ.IsArray(){return utilityTypeExportDemand(read,typ.Element(),active)}
	if typ.IsComposite(){for i:=range typ.CompositeLen(){if utilityTypeExportDemand(read,typ.CompositeMember(i).Usage.Type,active){return true}}}
	return false
}

func (s *StatementScope) NoteUtilityRoutineDemandLocked(read catalog.Read, address ids.ObjectAddress) {
	if s.exportRootDemand { return }
	if routine,ok:=read.Procedure(address);ok { s.exportRootDemand=exec.RoutineExportRootDemand(read,routine) }
}

func (s *StatementScope) NoteUtilityWriteDemandLocked(read catalog.Read, defaults []expr.Expr, generations map[int32]catalog.ColumnGeneration, checks []catalog.CheckConstraint) {
	if s.exportRootDemand { return }
	_ = analyze.VisitRetainedWriteExpressions(defaults,generations,checks,func(node expr.Expr)error{
		s.noteUtilityNodeDemandLocked(read,node)
		if s.exportRootDemand{return errExportRootDemand}
		return nil
	})
}

// NoteOpaqueUtilityExportDemandLocked is only for specifically still-unbound
// executable descendant recipes or not-yet-selectable index rebuild execution
// at a genuine executor boundary. It is not a flag for ALTER TABLE in general.
func (s *StatementScope) NoteOpaqueUtilityExportDemandLocked() { s.exportRootDemand=true }

// AcquireUtilityExpressionSnapshot is called unlocked immediately before the
// first runtime preflight/Eval, after the complete possible recipe closure was
// classified. Historical ALTER scan/rewrite frontiers remain independent: this
// is a genuine active expression pair, never roots reconstructed for that cut.
func (s *StatementScope) AcquireUtilityExpressionSnapshot() error {
	if s.exportRootDemand && s.currentSnapshot != nil && s.exportCapture == nil && !s.Owner.Isolation.Stable() { return ids.ErrCatalogReplaced }
	return s.snapshotDataPhase()
}

func (s *StatementScope) NoteUtilityIndexDemandLocked(read catalog.Read, relation *catalog.Relation) {
	definition:=relation.SQLIndex
	if definition==nil{return}
	for _,key:=range definition.Keys {
		if key.Derived!=nil {
			s.NoteUtilityExpressionDemandLocked(read,key.Derived.Execution)
			for _,dependency:=range key.Derived.Dependencies{s.NoteUtilityRoutineDemandLocked(read,dependency.Address)}
		}
		for slot:=1;slot<=4;slot++ {
			support:=key.Class.Support(slot)
			if support.Routine!=0{s.NoteUtilityRoutineDemandLocked(read,ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:ids.OID(support.Routine)})}
		}
	}
	if definition.Predicate!=nil {
		s.NoteUtilityExpressionDemandLocked(read,definition.Predicate.Execution)
		for _,dependency:=range definition.Predicate.Dependencies{s.NoteUtilityRoutineDemandLocked(read,dependency.Address)}
	}
}

func (h *statementHost) PrepareUtilityIndexSnapshot(read catalog.Read, indexes []*catalog.Relation) error {
	s:=h.scope()
	e:=s.Engine()
	e.Mu.Lock()
	executable:=s.exportRootDemand
	for _,relation:=range indexes{
		s.NoteUtilityIndexDemandLocked(read,relation)
		if definition:=relation.SQLIndex;definition!=nil{
			executable=executable||definition.Predicate!=nil
			for _,key:=range definition.Keys{executable=executable||key.Derived!=nil}
		}
	}
	e.Mu.Unlock()
	if !executable&&!s.exportRootDemand{return nil}
	return s.AcquireUtilityExpressionSnapshot()
}
