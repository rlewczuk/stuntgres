package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// triggerReplacement authorizes local-child replacement only for the exact
// CREATE OR REPLACE definition and its descendant clone lineage in this stage.
type triggerReplacement struct {
	Relation ids.ObjectAddress
	OID      ids.OID
}

// synchronizeStageTriggersLocked maintains partition clone provenance at catalog
// publication, including CREATE/ATTACH/DETACH and parent replacement. Runtime
// dispatch reads only the physical relation's immutable list. Engine.mu is held.
func (e *Engine) synchronizeStageTriggersLocked(stage *CatalogStage) error {
	read := e.stageRead(stage)
	relevant := false
	for address, descriptor := range stage.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		old, _ := stage.Base.Relation(address)
		if relation.HasTriggers() || old != nil && old.HasTriggers() {
			relevant = true
			break
		}
		if relation.Membership != nil {
			if parent, ok := read.Relation(relation.Membership.Parent); ok && parent.HasTriggers() {
				relevant = true
				break
			}
		}
	}
	if !relevant {
		return nil
	}
	// Cloning projects relation rows, not routine declarations or principal
	// lifetimes. Every capture uses the same checked current declaration.
	procedure := catalog.ProcedureRemapper(func(original *catalog.Procedure) (*catalog.Procedure, error) {
		if original == nil {
			return nil, nil
		}
		return exec.ValidateStoredRoutineDescriptor(read, original)
	})
	var replacementSources map[ids.OID]bool
	replacement := stage.triggerReplacement
	if root, ok := read.Relation(replacement.Relation); ok && root.Partition != nil && len(root.Partition.Children) != 0 {
		for _, definition := range stored.TriggersOf(root) {
			if definition.OID() == replacement.OID {
				replacementSources = map[ids.OID]bool{replacement.OID: true}
				break
			}
		}
	}
	visited := make(map[ids.ObjectAddress]bool)
	visiting := make(map[ids.ObjectAddress]bool)
	var sync func(*catalog.Relation) error
	sync = func(child *catalog.Relation) error {
		address := child.Address()
		if visited[address] {
			return nil
		}
		if visiting[address] {
			return fmt.Errorf("stuntgres: cyclic partition trigger ownership")
		}
		visiting[address] = true
		var parent *catalog.Relation
		if child.Membership != nil {
			parent, _ = read.Relation(child.Membership.Parent)
			if parent == nil {
				return fmt.Errorf("stuntgres: missing partition trigger parent")
			}
			if err := sync(parent); err != nil {
				return err
			}
			read = e.stageRead(stage)
			parent = read.MustRelation(parent.Address())
		}
		list := stored.TriggersOf(child)
		var next []stored.Trigger
		changed := false
		for _, t := range list {
			if t.Parts().Parent.ObjectID == 0 {
				next = append(next, t)
			} else {
				changed = true
			}
		}
		if parent != nil {
			for _, source := range stored.TriggersOf(parent) {
				prior := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Name() == source.Name() })
				replaceLocal := replacementSources[source.OID()]
				var oid ids.OID
				if prior >= 0 {
					p := list[prior].Parts()
					sameParent := p.Parent == (ids.ObjectAddress{ClassID: 2620, ObjectID: source.OID()})
					if !sameParent {
						if p.Parent != (ids.ObjectAddress{}) || !replaceLocal {
							return sqlError("42710", fmt.Sprintf("trigger %q for relation %q already exists", source.Name(), child.Name()))
						}
						next = slices.DeleteFunc(next, func(t stored.Trigger) bool { return t.OID() == p.OID })
					}
					oid = p.OID
					if sameParent && p.ParentRevision == parent.Revision() {
						if replaceLocal {
							replacementSources[oid] = true
						}
						next = append(next, list[prior])
						continue
					}
				} else {
					var err error
					oid, err = e.catalog.allocateOIDLocked()
					if err != nil {
						return err
					}
				}
				if replaceLocal {
					replacementSources[oid] = true
				}
				cloned, err := stored.CloneTrigger(read, source, parent, child, oid, procedure)
				if err != nil {
					return err
				}
				next = append(next, cloned)
				changed = true
			}
		}
		if changed {
			slices.SortFunc(next, func(a, b stored.Trigger) int { return strings.Compare(a.Name(), b.Name()) })
			// Retain descriptor identity when every inherited definition is intact.
			same := len(next) == len(list)
			if same {
				for i, t := range next {
					p, q := t.Parts(), list[i].Parts()
					if p.OID != q.OID || p.Parent != q.Parent || p.ParentRevision != q.ParentRevision {
						same = false
						break
					}
				}
			}
			if !same {
				copy := *child
				copy.Triggers = stored.TriggersPayload(next)
				var err error
				copy.ObjectRevision, err = e.catalog.revisionLocked()
				if err != nil {
					return err
				}
				// Trigger-only publication does not change or reconstruct the row type.
				if stage.Base.Get(address) == nil {
					stage.Candidate.UpdateDescriptor(&copy)
				} else if err := stage.Candidate.Replace(&copy); err != nil {
					return err
				}
				read = e.stageRead(stage)
			}
		}
		delete(visiting, address)
		visited[address] = true
		return nil
	}
	for _, descriptor := range read.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			if err := sync(relation); err != nil {
				return err
			}
		}
	}
	return nil
}
