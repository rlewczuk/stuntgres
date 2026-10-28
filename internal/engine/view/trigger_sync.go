package view

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// SynchronizeTriggersLocked maintains partition clone provenance at catalog
// publication, including CREATE/ATTACH/DETACH and parent replacement. Runtime
// dispatch reads only the physical relation's immutable list. stageRead reads
// the candidate's current state, alloc issues clone OIDs and revisions, and
// replacement names the CREATE OR REPLACE definition. Engine.mu is held.
func SynchronizeTriggersLocked(c *catalog.Candidate, stageRead func() catalog.Read, alloc ddl.Allocator, replacement TriggerReplacement) error {
	read := stageRead()
	relevant := false
	for address, descriptor := range c.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		old, _ := c.Base.Relation(address)
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
			read = stageRead()
			parent = read.MustRelation(parent.Address())
		}
		list := stored.TriggersOf(child)
		var next []stored.Trigger
		changed := false
		for _, t := range list {
			if t.Parts().Parent.ObjectID == 0 {
				next = append(next, t)
			} else if parent != nil && slices.ContainsFunc(stored.TriggersOf(parent), func(source stored.Trigger) bool {
				return source.OID() == t.Parts().Parent.ObjectID && source.Parts().Level == stored.TriggerStatement
			}) {
				// Replacing a parent row definition with a statement definition
				// does not recurse into its existing row clones.
				next = append(next, t)
			} else {
				changed = true
			}
		}
		if parent != nil {
			baseParent, _ := c.Base.Relation(parent.Address())
			var baseSources []stored.Trigger
			if baseParent != nil { baseSources = stored.TriggersOf(baseParent) }
			var basePositions map[ids.OID]int
		sourceLoop:
			for sourceIndex, source := range stored.TriggersOf(parent) {
				if source.Parts().Level != stored.TriggerRow {
					continue
				}
				parentAddress := ids.ObjectAddress{ClassID: 2620, ObjectID: source.OID()}
				prior := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Parts().Parent == parentAddress })
				occupant := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Name() == source.Name() })
				if occupant >= 0 && prior >= 0 && occupant != prior {
					return sqlError("42710", fmt.Sprintf("trigger %q for relation %q already exists", source.Name(), child.Name()))
				}
				if prior < 0 {
					prior = occupant
				}
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
					// A mode-only replacement retains row-shape and recipe
					// backing; clone provenance needs no WHEN rebinding.
					if !replaceLocal && sameParent && baseParent != nil && len(parent.Columns) == len(baseParent.Columns) && len(parent.Attributes) == len(baseParent.Attributes) && (len(parent.Columns) == 0 || &parent.Columns[0] == &baseParent.Columns[0]) && (len(parent.Attributes) == 0 || &parent.Attributes[0] == &baseParent.Attributes[0]) {
						at := sourceIndex
						if at >= len(baseSources) || baseSources[at].OID() != source.OID() {
							if basePositions == nil {
								basePositions = make(map[ids.OID]int, len(baseSources))
								for i, prior := range baseSources { basePositions[prior.OID()] = i }
							}
							var found bool
							at, found = basePositions[source.OID()]
							if !found { at = -1 }
						}
						if at >= 0 && source.SameExecutable(baseSources[at]) {
							next = append(next, list[prior].WithParentRevision(parent.Revision()))
							changed = true
							continue sourceLoop
						}
					}
				} else {
					var err error
					oid, err = alloc.AllocateOID()
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
				if prior >= 0 && list[prior].Parts().Parent == parentAddress {
					p := cloned.Parts()
					p.AutoExtensions = list[prior].Parts().AutoExtensions
					if !replaceLocal {
						p.Mode = list[prior].Mode()
					}
					cloned, err = stored.NewTrigger(p)
					if err != nil {
						return err
					}
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
				copy.ObjectRevision, err = alloc.Revision()
				if err != nil {
					return err
				}
				// Trigger-only publication does not change or reconstruct the row type.
				if c.Base.Get(address) == nil {
					c.UpdateDescriptor(&copy)
				} else if err := c.Replace(&copy); err != nil {
					return err
				}
				read = stageRead()
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
