package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// InspectIndex projects authoritative immutable descriptor flags without
// deparsing definitions or allocating metadata vectors.
func (e *Engine) InspectIndex(ctx context.Context, schema, name string) (bool, bool, bool, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, false, false, false, err
	}
	e.Mu.Lock()
	catalogView := e.Catalog.Current()
	namespace, ok := catalogView.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema})
	if !ok {
		e.Mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: index schema %s does not exist", catalog.QuoteIdentifier(schema))
	}
	address, ok := catalogView.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	if !ok {
		e.Mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: index %s.%s does not exist", catalog.QuoteIdentifier(schema), catalog.QuoteIdentifier(name))
	}
	idx, ok := catalogView.Relation(address)
	if !ok || idx.SQLIndex == nil {
		e.Mu.Unlock()
		return false, false, false, false, false, fmt.Errorf("stuntgres: relation %s.%s is not an index", catalog.QuoteIdentifier(schema), catalog.QuoteIdentifier(name))
	}
	ready, valid, live := idx.SQLIndex.PublicationState()
	wait, admitted := e.IndexWaits.LookupLocked(address)
	admitted = admitted && wait.Owner.Domain() == e.Catalog.Domain && wait.Owner.Epoch() == e.Catalog.State.Epoch() && live && !valid
	older := admitted && ready && wait.Kind == index.WaitOlderSnapshots
	writers := admitted && wait.Kind == index.WaitWriters
	e.Mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, false, false, false, false, err
	}
	return ready, valid, live, older, writers, nil
}
