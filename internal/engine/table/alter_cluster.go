package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// ClusterStage is the existing statement-private catalog candidate. No descriptor
// returned by StageRead is mutated; all replacements publish with the statement.
type ClusterStage interface {
	StageRead() catalog.Read
	StageBase() catalog.Read
	Stage() ddl.Stage
	AddDomainBase(catalog.Descriptor)
}

// ClusterActionsOnly bypasses inheritance shape planning for root-only metadata.
func ClusterActionsOnly(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		switch action.Action {
		case parser.ClusterOn, parser.SetWithoutCluster, parser.SetWithoutOIDs:
		default:
			return false
		}
	}
	return true
}

func ClusterCandidate(stage ClusterStage, original, parent *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	view := stage.StageRead()
	var selected ids.ObjectAddress
	if action.Action == parser.ClusterOn {
		name := action.ClusterIndex.Text
		address, exists := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name})
		if !exists {
			return nil, sqlError("42704", fmt.Sprintf(`index "%s" for table "%s" does not exist`, name, parent.Name()))
		}
		relation, ok := view.Relation(address)
		if !ok || relation.SQLIndex == nil {
			return nil, sqlError("42809", fmt.Sprintf(`"%s" is not an index`, name))
		}
		def := relation.SQLIndex
		if def.Parent != parent.Address() {
			return nil, sqlError("42809", fmt.Sprintf(`"%s" is not an index for table "%s"`, name, parent.Name()))
		}
		if capable, _ := index.Property(view, def, true, 0, "clusterable"); !capable {
			return nil, sqlError("0A000", fmt.Sprintf(`cannot cluster on index "%s" because access method does not support clustering`, name))
		}
		if def.Predicate != nil {
			return nil, sqlError("0A000", fmt.Sprintf(`cannot cluster on partial index "%s"`, name))
		}
		if !def.Valid {
			return nil, sqlError("0A000", fmt.Sprintf(`cannot cluster on invalid index "%s"`, name))
		}
		selected = address
		if base := stage.StageBase().Get(address); base != nil {
			stage.AddDomainBase(base)
		}
	}
	if parent.RelationKind == catalog.PartitionedRelation {
		return nil, sqlError("0A000", "cannot mark index clustered in partitioned table")
	}
	changed := false
	for address, relation := range view.Relations() {
		if relation.SQLIndex == nil || relation.SQLIndex.Parent != parent.Address() || relation.SQLIndex.Clustered == (address == selected) {
			continue
		}
		next, definition := *relation, *relation.SQLIndex
		definition.Clustered = address == selected
		next.SQLIndex = &definition
		if base := stage.StageBase().Get(address); base == nil {
			// Companion ADD created this identity in the private candidate.
			stage.Stage().UpdateDescriptor(&next)
		} else {
			if relation.Revision() == base.Revision() {
				if err := BumpAlterRevision(relation, &next); err != nil {
					return nil, err
				}
			}
			if err := stage.Stage().Replace(&next); err != nil {
				return nil, err
			}
		}
		changed = true
	}
	if !changed {
		return parent, nil
	}
	next := *parent
	// Companion actions may already have advanced the parent in this stage.
	if parent.Revision() == original.Revision() {
		if err := BumpAlterRevision(parent, &next); err != nil {
			return nil, err
		}
	}
	if err := stage.Stage().Replace(&next); err != nil {
		return nil, err
	}
	return &next, nil
}
