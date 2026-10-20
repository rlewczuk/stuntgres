package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

func (e *Engine) capturePGSequencesLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
	base := e.catalog.state
	if scope.owner.sequenceCatalog != nil {
		base = scope.owner.sequenceCatalog
	}
	own := scope.owner.catalog
	rows := make([][]sqltype.Value, 0)
	provider := &pgSequencesSource{Host: hostOf(scope)}
	appendDefinition := func(descriptor catalog.Descriptor) error {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.SequenceRelation {
			return nil
		}
		namespace := base.Get(relation.Namespace())
		if own != nil {
			if current, overridden := own.descriptors[relation.Namespace()]; overridden {
				namespace = current
			}
		}
		ns, ok := namespace.(*catalog.Namespace)
		if !ok {
			return fmt.Errorf("stuntgres: sequence namespace missing")
		}
		if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != scope.op.session.token {
			return nil
		}
		owner, err := scope.inquiry.commentRoles.Descriptor(relation.Owner)
		if err != nil {
			return err
		}
		row := make([]sqltype.Value, 11)
		row[0] = sqltype.String(source.Columns[0].Usage.Type, ns.Name())
		row[1] = sqltype.String(source.Columns[1].Usage.Type, relation.Name())
		row[2] = sqltype.String(source.Columns[2].Usage.Type, owner.Name)
		row[3], err = sqltype.RegtypeValue(source.Columns[3].Usage.Type, uint32(relation.Sequence.SQLType))
		if err != nil {
			return err
		}
		for i, value := range [4]int64{relation.Sequence.Start, relation.Sequence.Min, relation.Sequence.Max, relation.Sequence.Increment} {
			row[i+4], err = sqltype.Integer(source.Columns[i+4].Usage.Type, value)
			if err != nil {
				return err
			}
		}
		row[8] = sqltype.Boolean(source.Columns[8].Usage.Type, relation.Sequence.Cycle)
		row[9], err = sqltype.Integer(source.Columns[9].Usage.Type, relation.Sequence.Cache)
		if err != nil {
			return err
		}
		row[10] = sqltype.Null(source.Columns[10].Usage.Type)
		rows = append(rows, row)
		provider.Refs = append(provider.Refs, e.catalog.Domain.Ref(base.Epoch(), relation.Address()))
		return nil
	}
	for address, descriptor := range base.Descriptors() {
		if own != nil {
			if _, overridden := own.descriptors[address]; overridden {
				continue
			}
		}
		if err := appendDefinition(descriptor); err != nil {
			return nil, err
		}
	}
	if own != nil {
		for _, descriptor := range own.descriptors {
			if err := appendDefinition(descriptor); err != nil {
				return nil, err
			}
		}
	}
	return &querySource{Ref: ref, Anonymous: rows, PGSequences: provider}, nil
}

// CompletePGSequence fills a pg_sequences row's last_value: it acquires the
// sequence lock, then reads the generator under Engine.mu.
func (h *statementHost) CompletePGSequence(ctx context.Context, ref ids.ObjectRef, row []sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := h.scope()
	if err := s.acquireSequence(ref, lock.RowExclusive, true); err != nil {
		return err
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	auth.Catalog = e.privateCatalogLocked(s.owner.catalog)
	auth.Recapture(auth.Catalog)
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok || relation.RelationKind != catalog.SequenceRelation {
		return sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
	}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return err
	}
	generator := e.sequenceGeneratorLocked(s, ref.Address)
	if generator == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	if rights&(authorization.SelectPrivilege|authorization.UsagePrivilege) != 0 && generator.Called {
		row[10], err = sqltype.Integer(row[10].Type(), generator.Last)
	}
	return err
}
