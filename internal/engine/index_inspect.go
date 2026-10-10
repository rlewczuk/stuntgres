package engine

import (
	"context"
	"fmt"
)

// InspectIndex projects authoritative immutable descriptor flags without
// deparsing definitions or allocating metadata vectors.
func (e *Engine) InspectIndex(ctx context.Context, schema, name string) (bool, bool, bool, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, false, false, false, err
	}
	e.mu.Lock()
	catalog := CatalogRead{owner: e, state: e.state}
	namespace, ok := catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: schema}]
	if !ok {
		e.mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: index schema %s does not exist", quoteIdentifier(schema))
	}
	address, ok := catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: name}]
	if !ok {
		e.mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: index %s.%s does not exist", quoteIdentifier(schema), quoteIdentifier(name))
	}
	index, ok := catalog.state.descriptors[address].(*relationDescriptor)
	if !ok || index.sqlIndex == nil {
		e.mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: relation %s.%s is not an index", quoteIdentifier(schema), quoteIdentifier(name))
	}
	ready, valid, live := index.sqlIndex.publicationState()
	wait, admitted := e.indexBuildWaits[address]
	admitted = admitted && wait.owner.owner == e && wait.owner.epoch == e.state.epoch && live && !valid
	older := admitted && ready && wait.kind == indexWaitOlderSnapshots
	writers := admitted && wait.kind == indexWaitWriters
	e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, false, false, false, false, err
	}
	return ready, valid, live, older, writers, nil
}
