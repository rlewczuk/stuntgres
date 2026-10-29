package tablecmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// stageReplicaIdentity owns no published payload. The ordinary ALTER stage
// publishes the parent and all changed selection bits together.
func stageReplicaIdentity(e *core.Engine, stage *core.CatalogStage, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	view := e.StageRead(stage)
	var selected ids.ObjectAddress
	if action.ReplicaIdentity == parser.ReplicaIdentityIndex {
		address, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: old.Namespace(), Name: action.ReplicaIndex.Text})
		if !ok {
			return nil, core.SQLError("42704", fmt.Sprintf(`index "%s" for table "%s" does not exist`, action.ReplicaIndex.Text, old.Name()))
		}
		idx, ok := view.Relation(address)
		if !ok || idx.SQLIndex == nil {
			return nil, core.SQLError("42809", fmt.Sprintf(`"%s" is not an index`, action.ReplicaIndex.Text))
		}
		if err := index.ReplicaIdentityEligibility(old, idx); err != nil {
			return nil, err
		}
		selected = address
	}
	next := *old
	next.ReplicaIdentity = action.ReplicaIdentity
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != old.Address() {
			continue
		}
		flag := idx.Address() == selected
		if idx.SQLIndex.ReplicaIdentity == flag {
			continue
		}
		replacement, definition := *idx, *idx.SQLIndex
		definition.ReplicaIdentity = flag
		replacement.SQLIndex = &definition
		if err := table.BumpAlterRevision(idx, &replacement); err != nil {
			return nil, err
		}
		if stage.Base.Get(replacement.Address()) == nil {
			// ADD and TYPE can create this index earlier in the same private
			// stage. Preserve its creation/name observations; Replace requires
			// an actual base predecessor and would reject this new identity.
			stage.UpdateDescriptor(&replacement)
		} else if err := stage.Replace(&replacement); err != nil {
			return nil, err
		}
	}
	return &next, nil
}
