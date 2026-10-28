package partitioncmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func stageDetachBound(e *core.Engine, s *core.StatementScope, auth core.AuthorizationView, stage *core.CatalogStage, parent, child *catalog.Relation) (*catalog.Relation, error) {
	if child.Membership.Strategy == parser.PartitionHash {
		return child, nil
	}
	proven, err := partition.ConstraintProven(s.Op.Context(), s.Input, stage.Base, parent, child, child.Membership, false)
	if err != nil || proven {
		return child, err
	}
	raw, err := partition.DetachBoundCheck(s.Op.Context(), stage.Base, parent, child)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	host := tablecmds.AlterHost{TableHost: tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: s.Op, CatalogStage: stage}}, Scope: s}
	order := table.InheritanceAlterOrder(stage.Base, child)
	var root *catalog.Relation
	for i, old := range order {
		if err := s.Op.Context().Err(); err != nil {
			return nil, err
		}
		currentAuth := auth
		currentAuth.Recapture(e.StageRead(stage))
		var inheritance *table.InheritanceAlterPlan
		if i != 0 {
			inheritance = &table.InheritanceAlterPlan{Partition: true, Descendant: true}
		}
		prepared, err := table.PrepareAddedChecks(host, inheritance, currentAuth, old, []*parser.CheckConstraint{raw})
		if err != nil {
			return nil, err
		}
		next := *prepared.Next
		if err := table.AssignDefinitionOIDs(host, &next); err != nil {
			return nil, err
		}
		next.ObjectRevision, err = e.Catalog.RevisionLocked()
		if err != nil {
			return nil, err
		}
		next.History.DetachPredecessor = old
		next.History.DetachPublication = next.Revision()
		next.History.DetachRoot = child.Address()
		if err := stage.Replace(&next); err != nil {
			return nil, err
		}
		if i == 0 {
			root = &next
			// Reuse the root binder's actual generated name for inherited CHECKs.
			for _, c := range next.Constraints.Check {
				if c.Ordinal >= prepared.Ordinal {
					copy := *raw
					copy.Name = parser.Identifier{Text: c.Name}
					raw = &copy
					break
				}
			}
		}
	}
	return root, nil
}
