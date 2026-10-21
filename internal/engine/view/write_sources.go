package view

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// FreezeWriteSources installs the frozen-layout bridges of a view write's
// leaves. Raw prefixes must be selected before the bridges are installed.
// Detach bundle ownership while replacing occurrences, as stored view opening
// does; the returned head owns every input and each bridge's private scratch.
//
// Each bridge borrows auth, or host when auth is nil, as root query admission
// does for every source it opens.
func FreezeWriteSources(source *exec.Source, leaves []exec.Leaf, auth *exec.Authority, host exec.Host) *exec.Source {
	sources := source.Sources
	source.Sources = nil
	for i, leaf := range leaves {
		child := source
		if sources != nil {
			child = sources[i]
		}
		child = frozenSource(child, leaf.FrozenAttributes, leaf.Relation.Attributes, auth, host)
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
	return source
}

func frozenSource(source *exec.Source, attributes, live []int32, auth *exec.Authority, host exec.Host) *exec.Source {
	if attributes == nil {
		return source
	}
	unchanged := len(attributes) == len(live)
	if unchanged {
		for i, a := range attributes {
			if live[i] != a {
				unchanged = false
				break
			}
		}
	}
	if unchanged {
		return source
	}
	layout := make([]int, len(attributes))
	for i, a := range attributes {
		layout[i] = -1
		for j, b := range live {
			if a == b {
				layout[i] = j
				break
			}
		}
	}
	bridge := &exec.Source{Ref: source.Ref, Input: source, Layout: layout, Scratch: make([]sqltype.Value, len(layout)), Authority: auth, Locks: source.Locks, Occurrence: source.Occurrence}
	if auth == nil {
		bridge.Host = host
	}
	return bridge
}
