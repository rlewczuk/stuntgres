package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// InspectIndex projects authoritative immutable descriptor flags without
// deparsing definitions or allocating metadata vectors.
func (e *Engine) InspectIndex(ctx context.Context, schema, name string) (bool, bool, bool, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, false, false, false, err
	}
	e.mu.Lock()
	catalog := e.catalog.read(e.catalog.state)
	namespace, ok := catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema})
	if !ok {
		e.mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: index schema %s does not exist", quoteIdentifier(schema))
	}
	address, ok := catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	if !ok {
		e.mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: index %s.%s does not exist", quoteIdentifier(schema), quoteIdentifier(name))
	}
	index, ok := catalog.Relation(address)
	if !ok || index.sqlIndex == nil {
		e.mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: relation %s.%s is not an index", quoteIdentifier(schema), quoteIdentifier(name))
	}
	ready, valid, live := index.sqlIndex.publicationState()
	wait, admitted := e.indexBuildWaits[address]
	admitted = admitted && wait.owner.Domain() == e.catalog.domain && wait.owner.Epoch() == e.catalog.state.epoch && live && !valid
	older := admitted && ready && wait.kind == indexWaitOlderSnapshots
	writers := admitted && wait.kind == indexWaitWriters
	e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, false, false, false, false, err
	}
	return ready, valid, live, older, writers, nil
}
