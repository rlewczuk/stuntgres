package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
)

// ReplaceDatabaseTemplate shares ordinary replacement's validation, graph remap
// and installation, but copies TEMP catalog metadata without source session
// attachment or physical TEMP contents. The caller protects the exact source
// with replacement read admission and its database-template lease.
func (e *Engine) ReplaceDatabaseTemplate(ctx context.Context, source *Engine, replacement catalog.Read) (ChangeSet, error) {
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	if source == nil || source == e || replacement.Owner() != &source.Catalog.Owner || replacement.State() == nil || e.Catalog.Domain == source.Catalog.Domain {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid database template source")
	}
	e.ReplacementMu.Lock()
	e.Mu.Lock()
	if e.Cluster != nil || len(e.Storage.Relations) != 0 || len(e.Storage.Sequences) != 0 || e.prepared.Len() != 0 {
		e.Mu.Unlock()
		e.ReplacementMu.Unlock()
		return ChangeSet{}, fmt.Errorf("stuntgres: database template destination is not fresh and private")
	}
	// The constructor's capability base must not become retained destination
	// TEMP authority. Source TEMP descriptors enter through the import graph,
	// not through preserveTemporaryLocked's live-destination graft.
	base, err := control.DatabaseTemplateCatalog(e.Catalog.Current(), false)
	if err == nil {
		e.Catalog.PublishLocked(base.State())
	}
	e.Mu.Unlock()
	e.ReplacementMu.Unlock()
	if err != nil {
		return ChangeSet{}, err
	}
	return e.replaceCatalogContents(ctx, source, replacement, nil, true, nil)
}

// prepareDatabaseTemplateMetadataLocked operates only on the private imported
// candidate. Opaque detached tokens preserve namespace/toast grouping without
// granting a real destination session authority, even at an equal numeric ID.
func (e *Engine) prepareDatabaseTemplateMetadataLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator) error {
	var owners map[ids.SessionToken]ids.SessionToken
	for address, descriptor := range next.Descriptors() {
		namespace, ok := descriptor.(*catalog.Namespace)
		if !ok || namespace.Temporary == (ids.SessionToken{}) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		owner, found := owners[namespace.Temporary]
		if !found {
			if owners == nil {
				owners = make(map[ids.SessionToken]ids.SessionToken)
			}
			owner = e.Catalog.Domain.DetachedSession(namespace.Temporary.ID())
			owners[namespace.Temporary] = owner
		}
		copy := *namespace
		copy.Temporary = owner
		next.SetDescriptor(address, &copy)
		if err := (catalog.NamespaceProvider{}).Validate(e.Catalog.Read(next), &copy); err != nil {
			return err
		}
	}
	for address, relation := range next.Relations() {
		namespace, ok := next.Namespace(relation.Namespace())
		if !ok || !namespace.Temporary.IsDetached() {
			continue
		}
		if relation.RelationKind.Stored() {
			relations[address] = &storage.Relation{}
		}
		if relation.Sequence != nil {
			sequences[address] = sequence.InitialGenerator(*relation.Sequence, nil, relation.Revision())
		}
	}
	return ctx.Err()
}
