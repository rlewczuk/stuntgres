package view

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// FreezeStoredSources installs the frozen-layout bridges of a stored view's
// captured sources. leaves are the view's prepared leaves, one per stored
// source of definition, in order. A source whose frozen attributes differ from
// its relation's live attributes reads through a bridge that maps each frozen
// slot to its live position (or -1); a leaf whose stored reference differs
// from its occurrence keeps the stored one as StoredRef. Bundle ownership is
// detached while occurrences are replaced; the returned head owns every input
// and each bridge's private scratch.
//
// Each bridge and the head borrow auth, or host when auth is nil, as root
// query admission does for every source it opens.
func FreezeStoredSources(source *exec.Source, definition *stored.View, leaves []exec.Leaf, auth *exec.Authority, host exec.Host) *exec.Source {
	// Detach bundle ownership before replacing execution-local occurrences.
	sources := source.Sources
	source.Sources = nil
	for i, leaf := range definition.Sources() {
		child := source
		if sources != nil {
			child = sources[i]
		}
		live := leaves[i].Relation.Attributes
		unchanged := len(leaf.Attributes) == len(live)
		if unchanged {
			for slot, attribute := range leaf.Attributes {
				if attribute != live[slot] {
					unchanged = false
					break
				}
			}
		}
		if !unchanged {
			layout := make([]int, len(leaf.Attributes))
			for slot, attribute := range leaf.Attributes {
				layout[slot] = -1
				for current, id := range live {
					if attribute == id {
						layout[slot] = current
						break
					}
				}
			}
			child = &exec.Source{Ref: leaves[i].Ref, Input: child, Layout: layout, Scratch: make([]sqltype.Value, len(layout)), Authority: auth, Locks: child.Locks, Occurrence: child.Occurrence}
			borrowAuthority(child, auth, host)
		}
		if leaf.Ref != leaves[i].Ref {
			child.StoredRef = leaf.Ref
		}
		if sources != nil {
			sources[i] = child
		} else {
			source = child
		}
	}
	if sources != nil {
		source = sources[0]
		source.Sources = sources
	}
	borrowAuthority(source, auth, host)
	return source
}

// borrowAuthority makes s borrow auth, or host when auth is nil.
func borrowAuthority(s *exec.Source, auth *exec.Authority, host exec.Host) {
	s.Authority = auth
	s.Host = nil
	if s.Authority == nil {
		s.Host = host
	}
}
