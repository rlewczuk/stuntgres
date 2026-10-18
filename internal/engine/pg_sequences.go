package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// Definitions are snapshot-owned; completion uses the exact current incarnation.
type pgSequencesSource struct {
	scope      *StatementScope
	refs       []ids.ObjectRef
	predicates []expr.Expr
}

func (e *Engine) capturePGSequencesLocked(scope *StatementScope, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	base := e.catalog.state
	if scope.owner.sequenceCatalog != nil {
		base = scope.owner.sequenceCatalog
	}
	own := scope.owner.catalog
	rows := make([][]sqltype.Value, 0)
	provider := &pgSequencesSource{scope: scope}
	appendDefinition := func(descriptor Descriptor) error {
		if err := scope.op.Context().Err(); err != nil {
			return err
		}
		relation, ok := descriptor.(*relationDescriptor)
		if !ok || relation.RelationKind != sequenceRelation {
			return nil
		}
		namespace := base.Get(relation.namespace)
		if own != nil {
			if current, overridden := own.descriptors[relation.namespace]; overridden {
				namespace = current
			}
		}
		ns, ok := namespace.(*namespaceDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: sequence namespace missing")
		}
		if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != scope.op.session.token {
			return nil
		}
		owner, err := scope.commentRoles.Descriptor(relation.Owner)
		if err != nil {
			return err
		}
		row := make([]sqltype.Value, 11)
		row[0] = sqltype.String(source.Columns[0].Usage.Type, ns.name)
		row[1] = sqltype.String(source.Columns[1].Usage.Type, relation.name)
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
		provider.refs = append(provider.refs, e.catalog.Domain.Ref(base.epoch, relation.address))
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
	return &querySource{ref: ref, anonymous: rows, pgSequences: provider}, nil
}

func (p *pgSequencesSource) complete(ctx context.Context, ref ids.ObjectRef, row []sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.scope.acquireSequence(ref, lock.RowExclusive, true); err != nil {
		return err
	}
	e := p.scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.epoch {
		return ids.ErrCatalogReplaced
	}
	auth, err := e.authorizationLocked(p.scope.op)
	if err != nil {
		return err
	}
	auth.catalog = e.privateCatalogLocked(p.scope.owner.catalog)
	auth.recaptureCatalog(auth.catalog)
	relation, ok := auth.catalog.Relation(ref.Address)
	if !ok || relation.RelationKind != sequenceRelation {
		return sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
	}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return err
	}
	generator := e.sequenceGeneratorLocked(p.scope, ref.Address)
	if generator == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	if rights&(authorization.SelectPrivilege|authorization.UsagePrivilege) != 0 && generator.called {
		row[10], err = sqltype.Integer(row[10].Type(), generator.last)
	}
	return err
}

func openPGSequencesStage(state *queryState, source *querySource, schema []ColumnDescriptor) indexedStage {
	p := source.pgSequences
	out := indexedStage{source: source, schema: schema, selection: []int{}}
	position := 0
	evaluation := state.evaluation(nil)
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		for position < len(p.refs) {
			i := position
			position++
			evaluation.setContext(ctx)
			evaluation.Row = source.anonymous[i]
			admitted := true
			for _, predicate := range p.predicates {
				value, err := predicate.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				truth, err := TruthOf(value)
				if err != nil {
					return false, err
				}
				if truth != True {
					admitted = false
					break
				}
			}
			if !admitted {
				continue
			}
			if err := p.complete(ctx, p.refs[i], source.anonymous[i]); err != nil {
				return false, err
			}
			out.selection = append(out.selection, i)
			return true, ctx.Err()
		}
		return false, ctx.Err()
	}
	return out
}
