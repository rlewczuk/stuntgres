package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A pending definition is private to one catalog stage. Raw bound datums are
// transformed in demand order; every Eval occurs outside Engine.mu.
type pendingPartitionDefinition struct {
	stage                  *CatalogStage
	parent, child          *relationDescriptor
	bound                  *partitionMembership
	rawBounds              parser.PartitionBound
	auth                   AuthorizationView
	view                   *VisibilityView
	defaultLeaves          []ObjectRef
	unsupportedCombination bool
}

func (e *Engine) schemaPartitionTableLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, syntax *parser.CreateTable, namespace ObjectAddress) (*pendingPartitionDefinition, error) {
	op := scope.op
	for _, element := range syntax.Elements {
		foreign := element.ForeignKey != nil
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				foreign = foreign || constraint.ForeignKey != nil
			}
		}
		if foreign {
			return nil, sqlError("0A000", "foreign keys involving partition hierarchies are unsupported")
		}
	}
	for _, observed := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, observed)
	}
	var parent *relationDescriptor
	if syntax.PartitionOf != nil {
		parentAuth, err := e.partitionParentAuthorization(op, auth, syntax)
		if err != nil {
			return nil, err
		}
		ref, err := e.resolveRelationReference(parentAuth, syntax.PartitionOf.Name, true)
		if err != nil {
			return nil, err
		}
		parent = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if ref != scope.lockedRef || parent != scope.lockedDescriptor {
			return nil, ErrCatalogReplaced
		}
		if err = parentAuth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, err
		}
		if parent.partition == nil {
			return nil, sqlError("42P17", fmt.Sprintf(`"%s" is not partitioned`, parent.name))
		}
	}
	if parent != nil {
		if _, err := e.schemaPartitionChildLocked(scope, auth, stage, parent, syntax, namespace); err != nil {
			return nil, err
		}
	} else {
		if err := e.schemaTableLocked(op, auth, stage, syntax, namespace, auth.state.CurrentUser, scope.input, scope.foreignKeyTargets); err != nil {
			return nil, err
		}
	}
	name := syntax.Name.Parts[len(syntax.Name.Parts)-1].Text
	address := stage.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: name}]
	child := stage.descriptors[address].(*relationDescriptor)
	keyAuth := auth
	keyAuth.catalog = e.stageRead(stage)
	keyAuth.recaptureCatalog(keyAuth.catalog)
	partition, err := e.bindPartitionKeys(op, keyAuth, child, syntax.PartitionBy, scope.input)
	if err != nil {
		return nil, err
	}
	if partition != nil {
		for _, constraint := range child.checkConstraints {
			if constraint.NoInherit {
				return nil, sqlError("42P16", fmt.Sprintf(`cannot add NO INHERIT constraint to partitioned table "%s"`, child.name))
			}
		}
		child.partition = partition
		child.relationKind = partitionedRelation
	}
	pending := &pendingPartitionDefinition{stage: stage, parent: parent, child: child, auth: auth}
	if parent != nil {
		pending.rawBounds = syntax.PartitionOf.Bound
		pending.bound, err = preparePartitionBounds(parent, pending.rawBounds)
		if err != nil {
			return nil, err
		}
		child.membership = pending.bound
		stage.domainBases = append(stage.domainBases, parent)
		if pending.bound.defaultBound && parent.partition.defaultChild != (ObjectAddress{}) {
			existing := auth.catalog.state.descriptors[parent.partition.defaultChild].(*relationDescriptor)
			return nil, expressionError("42P17", fmt.Sprintf(`partition "%s" conflicts with existing default partition "%s"`, name, existing.name), syntax.PartitionOf.Bound.Pos)
		}
		if !pending.bound.defaultBound && parent.partition.defaultChild != (ObjectAddress{}) {
			defaultRelation := auth.catalog.state.descriptors[parent.partition.defaultChild].(*relationDescriptor)
			err = partitionPhysicalDescendants(auth.catalog, defaultRelation, func(leaf *relationDescriptor) error {
				ref := e.domain.Ref(auth.catalog.Epoch(), leaf.address)
				pending.defaultLeaves = append(pending.defaultLeaves, ref)
				stage.domainBases = append(stage.domainBases, leaf)
				storage := e.privateStorageLocked(scope.owner.catalog, leaf.address)
				if storage == nil {
					return ErrCatalogReplaced
				}
				stage.partitionReads = append(stage.partitionReads, partitionReadFence{address: leaf.address, storage: storage, length: len(storage.versions), ends: storage.ends, generation: storage.generation})
				return nil
			})
			if err != nil {
				return nil, err
			}
			pending.view, err = scope.acquireDataViewsLocked(e, pending.defaultLeaves)
			if err != nil {
				return nil, err
			}
		}
	}
	// Invalid keys/bounds and inherited definitions win over this declared
	// implementation boundary; neither path publishes allocated auxiliaries.
	pending.unsupportedCombination = len(child.generations) != 0 || len(child.identities) != 0
	for _, element := range syntax.Elements {
		if element.Column != nil && serialTypeName(element.Column.TypeName) != "" {
			pending.unsupportedCombination = true
		}
	}
	return pending, nil
}

func (p *pendingPartitionDefinition) finish(scope *StatementScope) error {
	if p == nil {
		return nil
	}
	for _, constraint := range p.child.uniqueConstraints {
		index := p.stage.descriptors[constraint.Index].(*relationDescriptor)
		if err := partitionUniqueCoverage(p.child, index.sqlIndex); err != nil {
			return err
		}
	}
	for _, constraint := range p.child.exclusionConstraints {
		index := p.stage.descriptors[constraint.Index].(*relationDescriptor)
		if err := partitionUniqueCoverage(p.child, index.sqlIndex); err != nil {
			return err
		}
	}
	if p.parent == nil {
		if p.unsupportedCombination {
			return sqlError("0A000", "partitioned table constraint combination is unsupported")
		}
		return nil
	}
	ctx := scope.op.Context()
	evaluation := EvalContext{Context: ctx, statementScope: scope, Input: scope.input, Output: scope.output}
	if !p.bound.defaultBound {
		if err := p.evaluateBounds(scope, &evaluation); err != nil {
			return err
		}
		if err := p.validateBounds(scope); err != nil {
			return err
		}
		catalog := p.stage.base
		if err := p.checkOverlap(scope); err != nil {
			return err
		}
		if p.unsupportedCombination {
			return sqlError("0A000", "partitioned table constraint combination is unsupported")
		}
		evaluation.catalog, evaluation.routineAuthority = p.auth.catalog, &p.auth
		for _, ref := range p.defaultLeaves {
			leaf, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
			if !ok {
				return ErrCatalogReplaced
			}
			layout, err := inheritanceQueryLayout(p.parent, leaf)
			if err != nil {
				return err
			}
			mapping := inheritanceQuerySegment{layout: layout}
			if layout != nil {
				mapping.scratch = make([]sqltype.Value, len(layout))
			}
			source, err := captureQuerySource(ctx, p.view, ref)
			if err != nil {
				return err
			}
			for i := range source.count() {
				var values [32]partitionDatum
				key, err := partitionRowKey(p.parent, mapping.mapValues(source.row(i)), &evaluation, values[:0])
				if err != nil {
					source.close()
					return err
				}
				contains, err := partitionContains(ctx, p.parent.partition.keys, p.bound, key, scope.input)
				if err != nil {
					source.close()
					return err
				}
				if contains {
					source.close()
					defaultRelation := catalog.state.descriptors[p.parent.partition.defaultChild].(*relationDescriptor)
					diagnostic := sqlError("23514", fmt.Sprintf(`updated partition constraint for default partition "%s" would be violated by some row`, defaultRelation.name)).(*sqlstate.Error)
					diagnostic.Schema, diagnostic.Table = catalog.state.descriptors[defaultRelation.namespace].Name(), defaultRelation.name
					return diagnostic
				}
			}
			source.close()
		}
	}
	if p.unsupportedCombination {
		return sqlError("0A000", "partitioned table constraint combination is unsupported")
	}
	if p.parent.revision == DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	parent := *p.parent
	definition := *p.parent.partition
	definition.children = append(slices.Clone(definition.children), p.child.address)
	if p.bound.defaultBound {
		definition.defaultChild = p.child.address
	}
	parent.partition = &definition
	parent.revision++
	if err := p.stage.Replace(&parent); err != nil {
		return err
	}
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, index := range partitionIndexDeclarationOrder(p.stage.base, p.parent) {
		child := e.stageRead(p.stage).state.descriptors[p.child.address].(*relationDescriptor)
		if _, err := e.stagePartitionIndexChild(scope.op, p.stage, p.parent, index, child, true); err != nil {
			return err
		}
	}
	return nil
}

func partitionTupleSQL(ctx context.Context, tuple []partitionDatum) (string, error) {
	var text strings.Builder
	for i, datum := range tuple {
		if i > 0 {
			text.WriteString(", ")
		}
		switch datum.kind {
		case -1:
			text.WriteString("MINVALUE")
		case 1:
			text.WriteString("MAXVALUE")
		default:
			value, err := datum.value.Type().SQLLiteral(ctx, datum.value)
			if err != nil {
				return "", err
			}
			text.WriteString(value)
		}
	}
	return text.String(), nil
}

type partitionReadFence struct {
	address    ObjectAddress
	storage    *relationStorage
	length     int
	ends       *deletionRoot
	generation uint64
}

func (e *Engine) partitionReadsValidLocked(stage *CatalogStage) bool {
	for _, read := range stage.partitionReads {
		current := e.stageStorageLocked(stage, read.address)
		if current != read.storage || current == nil || len(current.versions) != read.length || current.ends != read.ends || current.generation != read.generation {
			return false
		}
	}
	return true
}

// Both CREATE and ATTACH use the same typed deciding-component diagnostic.
// ATTACH transforms datums before opening its candidate, then invokes this
// validation after that existing relation's eligibility/schema checks.
func (p *pendingPartitionDefinition) validateBounds(scope *StatementScope) error {
	if p.bound.defaultBound {
		return nil
	}
	if p.bound.strategy == parser.PartitionHash {
		return nil
	}
	ctx := scope.op.Context()
	order, deciding, err := comparePartitionTuple(ctx, p.parent.partition.keys, p.bound.lower, p.bound.upper, scope.input)
	if err != nil {
		return err
	}
	if order >= 0 {
		lower, err := partitionTupleSQL(ctx, p.bound.lower)
		if err != nil {
			return err
		}
		upper, err := partitionTupleSQL(ctx, p.bound.upper)
		if err != nil {
			return err
		}
		d := expressionError("42P17", fmt.Sprintf(`empty range bound specified for partition "%s"`, p.child.name), p.bound.lower[deciding].pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("Specified lower bound (%s) is greater than or equal to upper bound (%s).", lower, upper)
		return d
	}
	return nil
}

// partitionBoundary is a statement-local projection of the one immutable child
// topology. Tuples and descriptors are referenced, not copied or persisted.
type partitionBoundary struct {
	tuple    []partitionDatum
	relation *relationDescriptor
	lower    bool
}

func (p *pendingPartitionDefinition) checkOverlap(scope *StatementScope) error {
	if p.bound.strategy == parser.PartitionHash {
		return p.checkHashOverlap(scope)
	}
	count := len(p.parent.partition.children)
	if p.parent.partition.defaultChild != (ObjectAddress{}) {
		count--
	}
	if count == 0 {
		return nil
	}
	// Most hierarchies need no heap allocation. Larger ones require one bounded
	// projection to reproduce the ordered search's last-comparison diagnostic.
	var local [16]partitionBoundary
	bounds := local[:0]
	if count > len(local)/2 {
		bounds = make([]partitionBoundary, 0, 2*count)
	}
	for _, address := range p.parent.partition.children {
		sibling := p.stage.base.state.descriptors[address].(*relationDescriptor)
		if sibling.membership.defaultBound {
			continue
		}
		bounds = append(bounds,
			partitionBoundary{sibling.membership.lower, sibling, true},
			partitionBoundary{sibling.membership.upper, sibling, false})
	}
	compare := func(a, b partitionBoundary) (int, int, error) {
		order, component, err := comparePartitionTuple(scope.op.Context(), p.parent.partition.keys, a.tuple, b.tuple, scope.input)
		if err == nil && order == 0 && a.lower != b.lower {
			order = -1
			if a.lower {
				order = 1
			}
		}
		return order, component, err
	}
	var sortErr error
	slices.SortFunc(bounds, func(a, b partitionBoundary) int {
		if sortErr != nil {
			return 0
		}
		order, _, err := compare(a, b)
		sortErr = err
		return order
	})
	if sortErr != nil {
		return sortErr
	}
	// A shared endpoint is represented by its exclusive upper boundary, as in
	// PostgreSQL's PartitionBoundInfo. That boundary's following partition is
	// identified by the next upper endpoint, not by a second membership map.
	unique := bounds[:0]
	for _, bound := range bounds {
		if len(unique) != 0 {
			order, _, err := comparePartitionTuple(scope.op.Context(), p.parent.partition.keys, unique[len(unique)-1].tuple, bound.tuple, scope.input)
			if err != nil {
				return err
			}
			if order == 0 {
				continue
			}
		}
		unique = append(unique, bound)
	}
	bounds = unique
	probe := partitionBoundary{tuple: p.bound.lower, lower: true}
	lo, hi := -1, len(bounds)-1
	lastOrder, lastComponent := 0, 0
	for lo < hi {
		mid := (lo + hi + 1) / 2
		order, component, err := compare(bounds[mid], probe)
		if err != nil {
			return err
		}
		lastOrder, lastComponent = order, component
		if order <= 0 {
			lo = mid
			if order == 0 {
				break
			}
		} else {
			hi = mid - 1
		}
	}
	if lo+1 == len(bounds) {
		return nil
	}
	next := bounds[lo+1]
	position := p.bound.lower[lastComponent].pos
	if !next.lower {
		// The lower endpoint is already inside a sibling. PostgreSQL exposes
		// the search's final comparison component, with equality at the first.
		if lastOrder == 0 {
			position = p.bound.lower[0].pos
		}
	} else {
		// The lower endpoint is in a gap. Only its next partition can be the
		// first overlap; point to the deciding new upper component.
		order, component, err := compare(next, partitionBoundary{tuple: p.bound.upper})
		if err != nil {
			return err
		}
		if order >= 0 {
			return nil
		}
		position = p.bound.upper[component].pos
	}
	return expressionError("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, p.child.name, next.relation.name), position)
}
