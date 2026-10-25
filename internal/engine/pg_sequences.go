package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func (e *Engine) capturePGSequencesLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	base := e.Catalog.State
	if scope.Owner.SequenceCatalog != nil {
		base = scope.Owner.SequenceCatalog
	}
	own := scope.Owner.Catalog
	namespace := func(address ids.ObjectAddress) catalog.Descriptor {
		descriptor := base.Get(address)
		if own != nil {
			if current, overridden := own.Descriptors[address]; overridden {
				descriptor = current
			}
		}
		return descriptor
	}
	// The walk itself checks nothing; each definition reads the statement
	// context afresh, since held COPY input may mask a pending cancel.
	rows, addresses, err := pgcatalog.CapturePGSequences(pgcatalog.PGSequencesInput{
		Source: source,
		Err:    func() error { return scope.Op.Context().Err() },
		Visit: func(visit func(catalog.Descriptor) error) error {
			return visitSequenceOverlay(base, own, nil, visit)
		},
		Namespace: namespace, Temporary: scope.Op.Session.SessionToken, Roles: scope.Inquiry.commentRoles,
	})
	if err != nil {
		return nil, err
	}
	provider := &exec.PGSequencesSource{Host: HostOf(scope)}
	for _, address := range addresses {
		provider.Refs = append(provider.Refs, e.Catalog.Domain.Ref(base.Epoch(), address))
	}
	return &exec.Source{Ref: ref, Anonymous: rows, PGSequences: provider}, nil
}

// CompletePGSequence fills a pg_sequences row's last_value: it acquires the
// sequence lock, then reads the generator under Engine.mu.
func (h *statementHost) CompletePGSequence(ctx context.Context, ref ids.ObjectRef, row []sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := h.scope()
	if err := s.AcquireSequence(ref, lock.RowExclusive, true); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	auth.Catalog = e.PrivateCatalogLocked(s.Owner.Catalog)
	auth.Recapture(auth.Catalog)
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok || relation.RelationKind != catalog.SequenceRelation {
		return SQLError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
	}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return err
	}
	generator := e.SequenceGeneratorLocked(s, ref.Address)
	if generator == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	if rights&(authorization.SelectPrivilege|authorization.UsagePrivilege) != 0 && generator.Called {
		row[10], err = sqltype.Integer(row[10].Type(), generator.Last)
	}
	return err
}
