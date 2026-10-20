package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A pending definition is private to one catalog stage. Raw bound datums are
// transformed in demand order; every Eval occurs outside Engine.mu.
type pendingPartitionDefinition struct {
	stage                  *CatalogStage
	parent, child          *catalog.Relation
	bound                  *catalog.PartitionMembership
	rawBounds              parser.PartitionBound
	auth                   AuthorizationView
	view                   *visibility.View
	defaultLeaves          []ids.ObjectRef
	unsupportedCombination bool
}

func (e *Engine) schemaPartitionTableLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, syntax *parser.CreateTable, namespace ids.ObjectAddress) (*pendingPartitionDefinition, error) {
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
	var parent *catalog.Relation
	if syntax.PartitionOf != nil {
		parentAuth, err := e.partitionParentAuthorization(op, auth, syntax)
		if err != nil {
			return nil, err
		}
		ref, err := e.resolveRelationReference(parentAuth, syntax.PartitionOf.Name, true)
		if err != nil {
			return nil, err
		}
		parent = auth.Catalog.MustRelation(ref.Address)
		if ref != scope.lockedRef || parent != scope.lockedDescriptor {
			return nil, ids.ErrCatalogReplaced
		}
		if err = parentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, err
		}
		if parent.Partition == nil {
			return nil, sqlError("42P17", fmt.Sprintf(`"%s" is not partitioned`, parent.Name()))
		}
	}
	if parent != nil {
		if _, err := e.schemaPartitionChildLocked(scope, auth, stage, parent, syntax, namespace); err != nil {
			return nil, err
		}
	} else {
		if err := e.schemaTableLocked(op, auth, stage, syntax, namespace, auth.State.CurrentUser, scope.input, scope.foreignActionsOrZero().keyTargets); err != nil {
			return nil, err
		}
	}
	name := syntax.Name.Parts[len(syntax.Name.Parts)-1].Text
	address, _ := stage.NameBinding(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	child := stage.Get(address).(*catalog.Relation)
	keyAuth := auth
	keyAuth.Catalog = e.stageRead(stage)
	keyAuth.Recapture(keyAuth.Catalog)
	partition, err := e.bindPartitionKeys(op, keyAuth, child, syntax.PartitionBy, scope.input)
	if err != nil {
		return nil, err
	}
	if partition != nil {
		for _, constraint := range child.Constraints.Check {
			if constraint.NoInherit {
				return nil, sqlError("42P16", fmt.Sprintf(`cannot add NO INHERIT constraint to partitioned table "%s"`, child.Name()))
			}
		}
		child.Partition = partition
		child.RelationKind = catalog.PartitionedRelation
	}
	pending := &pendingPartitionDefinition{stage: stage, parent: parent, child: child, auth: auth}
	if parent != nil {
		pending.rawBounds = syntax.PartitionOf.Bound
		pending.bound, err = preparePartitionBounds(parent, pending.rawBounds)
		if err != nil {
			return nil, err
		}
		child.Membership = pending.bound
		stage.domainBases = append(stage.domainBases, parent)
		if pending.bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
			existing := auth.Catalog.MustRelation(parent.Partition.DefaultChild)
			return nil, expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" conflicts with existing default partition "%s"`, name, existing.Name()), syntax.PartitionOf.Bound.Pos)
		}
		if !pending.bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
			defaultRelation := auth.Catalog.MustRelation(parent.Partition.DefaultChild)
			err = partitionPhysicalDescendants(auth.Catalog, defaultRelation, func(leaf *catalog.Relation) error {
				ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), leaf.Address())
				pending.defaultLeaves = append(pending.defaultLeaves, ref)
				stage.domainBases = append(stage.domainBases, leaf)
				storage := e.privateStorageLocked(scope.owner.catalog, leaf.Address())
				if storage == nil {
					return ids.ErrCatalogReplaced
				}
				stage.partitionReads = append(stage.partitionReads, partitionReadFence{address: leaf.Address(), storage: storage, length: len(storage.Versions), ends: storage.Ends, generation: storage.Generation})
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
	pending.unsupportedCombination = len(child.ColumnMeta.Generations) != 0 || len(child.ColumnMeta.Identities) != 0
	for _, element := range syntax.Elements {
		if element.Column != nil && sequence.SerialTypeName(element.Column.TypeName) != "" {
			pending.unsupportedCombination = true
		}
	}
	return pending, nil
}

func (p *pendingPartitionDefinition) finish(scope *StatementScope) error {
	if p == nil {
		return nil
	}
	for _, constraint := range p.child.Constraints.Unique {
		index := p.stage.Get(constraint.Index).(*catalog.Relation)
		if err := partitionUniqueCoverage(p.child, index.SQLIndex); err != nil {
			return err
		}
	}
	for _, constraint := range p.child.Constraints.Exclusion {
		index := p.stage.Get(constraint.Index).(*catalog.Relation)
		if err := partitionUniqueCoverage(p.child, index.SQLIndex); err != nil {
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
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Input: scope.input, Output: scope.output}}
	if !p.bound.DefaultBound {
		if err := p.evaluateBounds(scope, &evaluation); err != nil {
			return err
		}
		if err := p.validateBounds(scope); err != nil {
			return err
		}
		view := p.stage.Base
		if err := p.checkOverlap(scope); err != nil {
			return err
		}
		if p.unsupportedCombination {
			return sqlError("0A000", "partitioned table constraint combination is unsupported")
		}
		evaluation.Catalog, evaluation.RoutineAuthority = p.auth.Catalog, &p.auth
		for _, ref := range p.defaultLeaves {
			leaf, ok := view.Relation(ref.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			layout, err := inheritanceQueryLayout(p.parent, leaf)
			if err != nil {
				return err
			}
			mapping := exec.InheritanceQuerySegment{Layout: layout}
			if layout != nil {
				mapping.Scratch = make([]sqltype.Value, len(layout))
			}
			source, err := captureQuerySource(ctx, p.view, ref)
			if err != nil {
				return err
			}
			for i := range source.Count() {
				var values [32]catalog.PartitionDatum
				key, err := partitionRowKey(p.parent, mapping.MapValues(source.Row(i)), &evaluation, values[:0])
				if err != nil {
					source.Close()
					return err
				}
				contains, err := partitionContains(ctx, p.parent.Partition.Keys, p.bound, key, scope.input)
				if err != nil {
					source.Close()
					return err
				}
				if contains {
					source.Close()
					defaultRelation := view.MustRelation(p.parent.Partition.DefaultChild)
					diagnostic := sqlError("23514", fmt.Sprintf(`updated partition constraint for default partition "%s" would be violated by some row`, defaultRelation.Name())).(*sqlstate.Error)
					diagnostic.Schema, diagnostic.Table = view.Get(defaultRelation.Namespace()).Name(), defaultRelation.Name()
					return diagnostic
				}
			}
			source.Close()
		}
	}
	if p.unsupportedCombination {
		return sqlError("0A000", "partitioned table constraint combination is unsupported")
	}
	if p.parent.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	parent := *p.parent
	definition := *p.parent.Partition
	definition.Children = append(slices.Clone(definition.Children), p.child.Address())
	if p.bound.DefaultBound {
		definition.DefaultChild = p.child.Address()
	}
	parent.Partition = &definition
	parent.ObjectRevision++
	if err := p.stage.Replace(&parent); err != nil {
		return err
	}
	e := scope.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, index := range partitionIndexDeclarationOrder(p.stage.Base, p.parent) {
		child := e.stageRead(p.stage).MustRelation(p.child.Address())
		if _, err := e.stagePartitionIndexChild(scope.op, p.stage, p.parent, index, child, true); err != nil {
			return err
		}
	}
	return nil
}

func partitionTupleSQL(ctx context.Context, tuple []catalog.PartitionDatum) (string, error) {
	var text strings.Builder
	for i, datum := range tuple {
		if i > 0 {
			text.WriteString(", ")
		}
		switch datum.Kind {
		case -1:
			text.WriteString("MINVALUE")
		case 1:
			text.WriteString("MAXVALUE")
		default:
			value, err := datum.Value.Type().SQLLiteral(ctx, datum.Value)
			if err != nil {
				return "", err
			}
			text.WriteString(value)
		}
	}
	return text.String(), nil
}

type partitionReadFence struct {
	address    ids.ObjectAddress
	storage    *storage.Relation
	length     int
	ends       *storage.DeletionRoot
	generation uint64
}

func (partitionReadFence) Kind() session.FenceKind { return fenceStorageRead }

func (f partitionReadFence) currentLocked(e *Engine, _ *catalogChanges) error {
	s := e.storage.Relations[f.address]
	if s != f.storage || s != nil && (len(s.Versions) != f.length || s.Ends != f.ends || s.Generation != f.generation) {
		return catalog.Conflict{}
	}
	return nil
}

func (e *Engine) partitionReadsValidLocked(stage *CatalogStage) bool {
	for _, read := range stage.partitionReads {
		current := e.stageStorageLocked(stage, read.address)
		if current != read.storage || current == nil || len(current.Versions) != read.length || current.Ends != read.ends || current.Generation != read.generation {
			return false
		}
	}
	return true
}

// Both CREATE and ATTACH use the same typed deciding-component diagnostic.
// ATTACH transforms datums before opening its candidate, then invokes this
// validation after that existing relation's eligibility/schema checks.
func (p *pendingPartitionDefinition) validateBounds(scope *StatementScope) error {
	if p.bound.DefaultBound {
		return nil
	}
	if p.bound.Strategy == parser.PartitionHash {
		return nil
	}
	ctx := scope.op.Context()
	order, deciding, err := comparePartitionTuple(ctx, p.parent.Partition.Keys, p.bound.Lower, p.bound.Upper, scope.input)
	if err != nil {
		return err
	}
	if order >= 0 {
		lower, err := partitionTupleSQL(ctx, p.bound.Lower)
		if err != nil {
			return err
		}
		upper, err := partitionTupleSQL(ctx, p.bound.Upper)
		if err != nil {
			return err
		}
		d := expr.ErrorAt("42P17", fmt.Sprintf(`empty range bound specified for partition "%s"`, p.child.Name()), p.bound.Lower[deciding].Pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("Specified lower bound (%s) is greater than or equal to upper bound (%s).", lower, upper)
		return d
	}
	return nil
}

// partitionBoundary is a statement-local projection of the one immutable child
// topology. Tuples and descriptors are referenced, not copied or persisted.
type partitionBoundary struct {
	tuple    []catalog.PartitionDatum
	relation *catalog.Relation
	lower    bool
}

func (p *pendingPartitionDefinition) checkOverlap(scope *StatementScope) error {
	if p.bound.Strategy == parser.PartitionHash {
		return p.checkHashOverlap(scope)
	}
	count := len(p.parent.Partition.Children)
	if p.parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
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
	for _, address := range p.parent.Partition.Children {
		sibling := p.stage.Base.MustRelation(address)
		if sibling.Membership.DefaultBound {
			continue
		}
		bounds = append(bounds,
			partitionBoundary{sibling.Membership.Lower, sibling, true},
			partitionBoundary{sibling.Membership.Upper, sibling, false})
	}
	compare := func(a, b partitionBoundary) (int, int, error) {
		order, component, err := comparePartitionTuple(scope.op.Context(), p.parent.Partition.Keys, a.tuple, b.tuple, scope.input)
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
			order, _, err := comparePartitionTuple(scope.op.Context(), p.parent.Partition.Keys, unique[len(unique)-1].tuple, bound.tuple, scope.input)
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
	probe := partitionBoundary{tuple: p.bound.Lower, lower: true}
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
	position := p.bound.Lower[lastComponent].Pos
	if !next.lower {
		// The lower endpoint is already inside a sibling. PostgreSQL exposes
		// the search's final comparison component, with equality at the first.
		if lastOrder == 0 {
			position = p.bound.Lower[0].Pos
		}
	} else {
		// The lower endpoint is in a gap. Only its next partition can be the
		// first overlap; point to the deciding new upper component.
		order, component, err := compare(next, partitionBoundary{tuple: p.bound.Upper})
		if err != nil {
			return err
		}
		if order >= 0 {
			return nil
		}
		position = p.bound.Upper[component].Pos
	}
	return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, p.child.Name(), next.relation.Name()), position)
}
