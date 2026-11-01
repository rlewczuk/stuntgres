package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RelationPendingPreflight adds the caller's retained-cursor safety policy to
// constraint invocation/event checks. Admission owns all of these sources.
func (s *StatementScope) RelationPendingPreflight(view catalog.Read, address ids.ObjectAddress, command string) error {
	if err := s.relationCursorPreflight(view, address, command); err != nil {
		return err
	}
	return s.ConstraintPendingPreflight(view, address, command)
}

// ConstraintPendingPreflight preserves executing-constraint-before-pending
// precedence without changing the caller's cursor-use policy. Native eager
// cursor input remains compatible with metadata-only ALTER operations.
// The caller owns statement admission and may hold Engine.Mu.
func (s *StatementScope) ConstraintPendingPreflight(view catalog.Read, address ids.ObjectAddress, command string) error {
	for _, active := range s.activeConstraintTriggers {
		if active == address {
			return relationActiveQueryError(view, address, command)
		}
	}
	if s.HasPendingConstraintEvents(address) {
		return SQLError("55006", fmt.Sprintf(`cannot %s "%s" because it has pending trigger events`, command, view.MustRelation(address).Name()))
	}
	return nil
}

func relationActiveQueryError(view catalog.Read, address ids.ObjectAddress, command string) error {
	return SQLError("55006", fmt.Sprintf(`cannot %s "%s" because it is being used by active queries in this session`, command, view.MustRelation(address).Name()))
}


// CheckPendingRelationDrops follows the stage's actual retirement closure, not
// the spelling of the utility or every object a trigger references. This also
// covers relations retired by schema/dependency/partition cascades.
func (s *StatementScope) CheckPendingRelationDrops(stage *CatalogStage) error {
	for address := range stage.Removals() {
		relation, ok := stage.Base.Relation(address)
		if !ok || relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation {
			continue
		}
		if !stage.closesCursors {
			if err := s.relationCursorPreflight(stage.Base, address, "DROP TABLE"); err != nil {
				return err
			}
		}
		if err := s.ConstraintPendingPreflight(stage.Base, address, "DROP TABLE"); err != nil {
			return err
		}
	}
	return nil
}
