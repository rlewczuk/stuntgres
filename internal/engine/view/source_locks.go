package view

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// SourceWalk describes a statement's view source closure walk: the catalog it
// reads, the current domain and epoch, the locked target and the admitted
// query leaves. Refresh walks a REFRESH MATERIALIZED VIEW target's query.
type SourceWalk struct {
	Catalog catalog.Read
	Domain  *ids.Domain
	Epoch   ids.CatalogEpoch
	Locked  ids.ObjectRef
	Leaves  []exec.Leaf
	Refresh bool
}

// SourceClosure is what a SourceWalk found. Modes holds every visited
// relation with its strongest row mode; Refs are the relations still to
// acquire; Retained are the temporary views whose references the caller
// revalidates after the waits. For a refresh, Sources are the visited
// relations in visit order and MaterializedSource reports a materialized
// view source other than the target.
type SourceClosure struct {
	Modes              map[ids.ObjectRef]lock.Mode
	Refs               []ids.ObjectRef
	Retained           []*catalog.Relation
	Sources            []catalog.Descriptor
	MaterializedSource bool
}

// Closure walks the view definitions reachable from the locked target and the
// query leaves. On error it returns what it found so far.
func (w SourceWalk) Closure() (SourceClosure, error) {
	c := SourceClosure{Modes: make(map[ids.ObjectRef]lock.Mode)}
	var visit func(ids.ObjectRef, lock.Mode) error
	visit = func(ref ids.ObjectRef, mode lock.Mode) error {
		prior, visited := c.Modes[ref]
		if ref == (ids.ObjectRef{}) || visited && exec.StrongerQueryRowMode(prior, mode) == prior {
			return nil
		}
		c.Modes[ref] = exec.StrongerQueryRowMode(prior, mode)
		d, ok := w.Catalog.Relation(ref.Address)
		if ref.Domain() != w.Domain || ref.Epoch != w.Epoch || !ok {
			return ids.ErrCatalogReplaced
		}
		if w.Refresh && !visited {
			c.Sources = append(c.Sources, d)
			if ref != w.Locked && d.RelationKind == catalog.MaterializedViewRelation {
				c.MaterializedSource = true
			}
		}
		admitted := ref == w.Locked
		admittedMode := lock.Mode(0)
		for _, leaf := range w.Leaves {
			if leaf.Ref == ref {
				admitted = true
				admittedMode = exec.StrongerQueryRowMode(admittedMode, leaf.Mark.Mode)
			}
		}
		if !admitted && !visited || admitted && mode != 0 && prior == 0 && admittedMode == 0 {
			c.Refs = append(c.Refs, ref)
		}
		if d.RelationKind == catalog.ViewRelation || w.Refresh && ref == w.Locked && d.RelationKind == catalog.MaterializedViewRelation {
			if stored.ViewOf(d).Temporary() != (ids.SessionToken{}) {
				for old := range stored.ViewOf(d).References() {
					if old.Epoch != w.Epoch {
						c.Retained = append(c.Retained, d)
						break
					}
				}
			}
			for i, leaf := range stored.ViewOf(d).Sources() {
				use, err := stored.ViewUseReference(w.Catalog, d, leaf.Ref)
				if err != nil {
					return err
				}
				if err := visit(use, stored.ViewLeafRowMark(stored.ViewOf(d), i, plan.RowMark{Mode: mode}).Mode); err != nil {
					return err
				}
			}
			for _, dependency := range stored.ViewOf(d).Dependencies() {
				if dependency.Ref.Address.ClassID == ids.RelationClassID {
					child, err := stored.ViewUseReference(w.Catalog, d, dependency.Ref)
					if err != nil {
						return err
					}
					child.Address.SubID = 0
					if err := visit(child, 0); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	err := visit(w.Locked, 0)
	for _, leaf := range w.Leaves {
		if err != nil {
			break
		}
		err = visit(leaf.Ref, leaf.Mark.Mode)
	}
	return c, err
}

// StoredLeaves returns the visited relations other than locked that have
// storage, as query leaves for the physical source collection.
func (c SourceClosure) StoredLeaves(read catalog.Read, locked ids.ObjectRef) []exec.Leaf {
	var leaves []exec.Leaf
	for ref := range c.Modes {
		if ref == locked {
			continue
		}
		relation := read.MustRelation(ref.Address)
		if relation.RelationKind.Stored() {
			leaves = append(leaves, exec.Leaf{Ref: ref})
		}
	}
	return leaves
}

// DefinitionReferences returns the relations b's dependencies and represented
// sequence constants name, once each, without the locked target and the
// admitted query leaves.
func DefinitionReferences(b analyze.Binding, locked ids.ObjectRef, leaves []exec.Leaf) []ids.ObjectRef {
	var refs []ids.ObjectRef
	seen := make(map[ids.ObjectRef]bool)
	add := func(ref ids.ObjectRef) {
		if ref.Address.ClassID != ids.RelationClassID {
			return
		}
		ref.Address.SubID = 0
		if ref == locked || seen[ref] {
			return
		}
		for _, leaf := range leaves {
			if ref == leaf.Ref {
				return
			}
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, dependency := range b.Dependencies {
		add(dependency.Ref)
	}
	for _, constant := range b.Prepared.SequenceConstants {
		if constant.Represented {
			add(constant.Ref)
		}
	}
	return refs
}
