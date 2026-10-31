package index

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// Detached pg_index-shaped values. Analyzed expression nodes are immutable;
// the expression list and all vectors belong to the projection, not descriptors.
type Metadata struct {
	IndexRelID, IndRelID                       ids.OID
	IndNatts, IndNKeyAtts                      int16
	IndIsUnique, IndNullsNotDistinct           bool
	IndIsPrimary, IndIsExclusion, IndImmediate bool
	IndIsClustered, IndIsValid, IndCheckXmin   bool
	IndIsReady, IndIsLive, IndIsReplIdent      bool
	IndKey                                     []int16
	IndClass, IndCollation                     []ids.OID
	IndOption                                  []int16
	IndexExpressions                           []expr.Expr
	Predicate                                  expr.Expr
}

type IndexesMetadata struct {
	SchemaName, TableName, IndexName string
	Tablespace                       *string
	IndexDef                         string
}

// ProjectIndexFacts captures pg_index's structural vectors and original frozen
// nodes. A newer compatible SQL body does not make these catalog facts invalid.
func ProjectIndexFacts(ctx context.Context, view catalog.Read, ref ids.ObjectRef) (Metadata, error) {
	facts, _, _, err := projectIndexFacts(ctx, view, ref)
	return facts, err
}

// Both projections use exactly this capture. Callers requesting pg_indexes SQL
// retain strict definition/deparse validation; pg_index never demands that SQL.
func ProjectIndexMetadata(ctx context.Context, view catalog.Read, ref ids.ObjectRef) (Metadata, IndexesMetadata, error) {
	facts, index, parent, err := projectIndexFacts(ctx, view, ref)
	if err != nil {
		return Metadata{}, IndexesMetadata{}, err
	}
	sql, err := DefinitionSQL(ctx, view, index, Presentation{})
	if err != nil {
		return Metadata{}, IndexesMetadata{}, err
	}
	names := IndexesMetadata{SchemaName: view.Get(parent.Namespace()).Name(), TableName: parent.Name(), IndexName: index.Name(), IndexDef: sql}
	return facts, names, nil
}

func projectIndexFacts(ctx context.Context, view catalog.Read, ref ids.ObjectRef) (Metadata, *catalog.Relation, *catalog.Relation, error) {
	var m Metadata
	if err := ctx.Err(); err != nil {
		return m, nil, nil, err
	}
	if view.Owner() == nil || view.State() == nil || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address.SubID != 0 {
		return m, nil, nil, fmt.Errorf("stuntgres: invalid index metadata capture")
	}
	index, ok := view.Relation(ref.Address)
	if !ok || index.SQLIndex == nil {
		return m, nil, nil, fmt.Errorf("stuntgres: index metadata target is missing")
	}
	if index.RelationKind != catalog.IndexRelation || index.Owner.ID() == 0 || index.Owner != view.Owner().Principals.Principal(index.Owner.ID()) {
		return m, nil, nil, fmt.Errorf("stuntgres: invalid index metadata identity")
	}
	parent, err := validateIndexStructure(view, index)
	if err != nil {
		return m, nil, nil, err
	}
	if _, ok := view.Namespace(parent.Namespace()); !ok {
		return m, nil, nil, fmt.Errorf("stuntgres: missing index metadata namespace")
	}
	definition := index.SQLIndex
	if predicate := definition.Predicate; predicate != nil {
		if err := stored.ValidateIndexMetadataExpression(ctx, view, parent, predicate.Definition, predicate.Dependencies); err != nil {
			return m, nil, nil, err
		}
	}
	for _, key := range definition.Keys {
		if key.Derived != nil {
			if err := stored.ValidateIndexMetadataExpression(ctx, view, parent, key.Derived.Definition, key.Derived.Dependencies); err != nil {
				return m, nil, nil, err
			}
		}
	}
	n := len(definition.Keys)
	ready, valid, live := definition.PublicationState()
	m = Metadata{IndexRelID: index.Address().ObjectID, IndRelID: parent.Address().ObjectID, IndNatts: int16(n + len(definition.Include)), IndNKeyAtts: int16(n), IndImmediate: true, IndIsValid: valid, IndIsReady: ready, IndIsLive: live, IndKey: make([]int16, n+len(definition.Include)), IndClass: make([]ids.OID, n), IndCollation: make([]ids.OID, n), IndOption: make([]int16, n)}
	m.IndIsUnique = definition.Unique
	m.IndNullsNotDistinct = definition.NullsNotDistinct
	m.IndIsClustered = definition.Clustered
	m.IndIsReplIdent = definition.ReplicaIdentity
	m.IndIsPrimary = definition.Constraint != nil && definition.Constraint.Kind == catalog.UniqueBackingKind && definition.Constraint.Primary
	if c := UniqueDefinition(parent, index); c != nil {
		m.IndImmediate = !c.Deferrable
	}
	if definition.IsExclusion() {
		m.IndIsExclusion = true
		if c := ExclusionDefinition(parent, index); c != nil {
			m.IndImmediate = !c.Deferrable
		}
	}
	if predicate := definition.Predicate; predicate != nil {
		m.Predicate = predicate.Definition
	}
	for i, key := range definition.Keys {
		if err := ctx.Err(); err != nil {
			return Metadata{}, nil, nil, err
		}
		attribute, node, err := CatalogKey(parent, key)
		if err != nil {
			return Metadata{}, nil, nil, err
		}
		if node != nil {
			if m.IndexExpressions == nil {
				m.IndexExpressions = make([]expr.Expr, 0, n-i)
			}
			m.IndexExpressions = append(m.IndexExpressions, node)
		} else {
			if attribute < 1 || attribute > 32767 {
				return Metadata{}, nil, nil, fmt.Errorf("stuntgres: index attribute cannot be represented in int2vector")
			}
			m.IndKey[i] = int16(attribute)
		}
		usage, err := KeyUsage(parent, key)
		if err != nil {
			return Metadata{}, nil, nil, err
		}
		m.IndClass[i] = ids.OID(key.Class.OID())
		if m.IndClass[i] == 0 {
			return Metadata{}, nil, nil, fmt.Errorf("stuntgres: index class has no released identity")
		}
		m.IndCollation[i] = ids.OID(usage.Collation.ID)
		if definition.Method != catalog.BtreeMethod {
			continue
		}
		if key.Desc {
			m.IndOption[i] |= 1
		}
		if key.NullsFirst {
			m.IndOption[i] |= 2
		}
	}
	for i, attribute := range definition.Include {
		if err := ctx.Err(); err != nil {
			return Metadata{}, nil, nil, err
		}
		if attribute < 1 || attribute > 32767 {
			return Metadata{}, nil, nil, fmt.Errorf("stuntgres: included index attribute cannot be represented in int2vector")
		}
		m.IndKey[n+i] = int16(attribute)
	}
	return m, index, parent, nil
}

// ACL-neutral structural enumeration for internal metadata consumers.
// The SQL pg_indexes source separately applies its invoker disclosure policy.
func CatalogMetadata(ctx context.Context, view catalog.Read) ([]Metadata, []IndexesMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if view.Owner() == nil || view.State() == nil {
		return nil, nil, fmt.Errorf("stuntgres: invalid index metadata capture")
	}
	var indexes []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int { return cmp.Compare(a.Address().ObjectID, b.Address().ObjectID) })
	records := make([]Metadata, len(indexes))
	names := make([]IndexesMetadata, len(indexes))
	for i, index := range indexes {
		var err error
		records[i], names[i], err = ProjectIndexMetadata(ctx, view, view.Owner().Domain.Ref(view.Epoch(), index.Address()))
		if err != nil {
			return nil, nil, err
		}
	}
	return records, names, nil
}
