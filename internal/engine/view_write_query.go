package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func (e *Engine) attachViewWriteSourcesLocked(scope *StatementScope, b *analyze.Binding) error {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
	auth.Recapture(auth.Catalog)
	original := len(ViewWriteOf(b.ViewWrite).Logical.QuerySources)
	for i := original; i < len(b.QuerySources); i++ {
		rewritten := false
		for _, ordinal := range ViewWriteOf(b.ViewWrite).QueryOccurrences {
			if ordinal == i {
				rewritten = true
				break
			}
		}
		if !rewritten {
			continue
		}
		source := &b.QuerySources[i]
		source.Storage = e.privateStorageLocked(scope.owner.Catalog, source.Ref.Address)
		for key, ordinal := range ViewWriteOf(b.ViewWrite).QueryOccurrences {
			if ordinal != i {
				continue
			}
			for _, layer := range ViewWriteOf(b.ViewWrite).Layers {
				if stored.ViewOf(layer.Relation) != key.Definition {
					continue
				}
				current := auth.Catalog.MustRelation(layer.Ref.Address)
				source.Principal = current.Owner
				if stored.ViewOf(current).Invoker() {
					source.Principal = auth.State.CurrentUser
				}
				break
			}
		}
		if len(scope.mutations) == 0 {
			scope.querySources = append(scope.querySources, *source)
		}
	}
	return nil
}

// Raw prefixes must be selected before frozen-layout bridges are installed.
// Detach bundle ownership while replacing occurrences, as stored view opening
// does; the returned head owns every input and each bridge's private scratch.
func freezeViewWriteSources(scope *StatementScope, source *exec.Source, leaves []exec.Leaf) *exec.Source {
	sources := source.Sources
	source.Sources = nil
	for i, leaf := range leaves {
		child := source
		if sources != nil {
			child = sources[i]
		}
		child = frozenViewSource(scope, child, leaf.FrozenAttributes, leaf.Relation.Attributes)
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

func frozenViewSource(scope *StatementScope, source *exec.Source, attributes, live []int32) *exec.Source {
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
	bridge := &exec.Source{Ref: source.Ref, Input: source, Layout: layout, Scratch: make([]sqltype.Value, len(layout)), Authority: scope.authority, Locks: source.Locks, Occurrence: source.Occurrence}
	borrowQueryAuthority(bridge, scope)
	return bridge
}
