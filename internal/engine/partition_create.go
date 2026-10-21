package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A pending definition is private to one catalog stage. Raw bound datums are
// transformed in demand order; every Eval occurs outside Engine.mu.
type pendingPartitionDefinition struct {
	partition.Definition
	stage *CatalogStage
	auth  AuthorizationView
	view  *visibility.View
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
		if err := e.schemaTableLocked(op, auth, stage, syntax, namespace, auth.State.CurrentUser, scope.input, scope.foreignActionsOrZero().KeyTargets); err != nil {
			return nil, err
		}
	}
	name := syntax.Name.Parts[len(syntax.Name.Parts)-1].Text
	address, _ := stage.NameBinding(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	child := stage.Get(address).(*catalog.Relation)
	keyAuth := auth
	keyAuth.Catalog = e.stageRead(stage)
	keyAuth.Recapture(keyAuth.Catalog)
	part, err := e.bindPartitionKeys(op, keyAuth, child, syntax.PartitionBy, scope.input)
	if err != nil {
		return nil, err
	}
	if part != nil {
		for _, constraint := range child.Constraints.Check {
			if constraint.NoInherit {
				return nil, sqlError("42P16", fmt.Sprintf(`cannot add NO INHERIT constraint to partitioned table "%s"`, child.Name()))
			}
		}
		child.Partition = part
		child.RelationKind = catalog.PartitionedRelation
	}
	pending := &pendingPartitionDefinition{Definition: partition.Definition{Parent: parent, Child: child}, stage: stage, auth: auth}
	if parent != nil {
		pending.Raw = syntax.PartitionOf.Bound
		pending.Bound, err = partition.PrepareBounds(parent, pending.Raw)
		if err != nil {
			return nil, err
		}
		child.Membership = pending.Bound
		stage.domainBases = append(stage.domainBases, parent)
		if err = partition.CheckDefaultConflict(auth.Catalog, parent, pending.Bound, name, syntax.PartitionOf.Bound.Pos); err != nil {
			return nil, err
		}
		if !pending.Bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
			defaultRelation := auth.Catalog.MustRelation(parent.Partition.DefaultChild)
			err = partition.PhysicalDescendants(auth.Catalog, defaultRelation, func(leaf *catalog.Relation) error {
				ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), leaf.Address())
				pending.DefaultLeaves = append(pending.DefaultLeaves, ref)
				stage.domainBases = append(stage.domainBases, leaf)
				storage := e.privateStorageLocked(scope.owner.Catalog, leaf.Address())
				if storage == nil {
					return ids.ErrCatalogReplaced
				}
				stage.storageReads = append(stage.storageReads, storageReadFence{address: leaf.Address(), storage: storage, length: len(storage.Versions), ends: storage.Ends, generation: storage.Generation})
				return nil
			})
			if err != nil {
				return nil, err
			}
			pending.view, err = scope.acquireDataViewsLocked(e, pending.DefaultLeaves)
			if err != nil {
				return nil, err
			}
		}
	}
	// Invalid keys/bounds and inherited definitions win over this declared
	// implementation boundary; neither path publishes allocated auxiliaries.
	pending.Unsupported = len(child.ColumnMeta.Generations) != 0 || len(child.ColumnMeta.Identities) != 0
	for _, element := range syntax.Elements {
		if element.Column != nil && sequence.SerialTypeName(element.Column.TypeName) != "" {
			pending.Unsupported = true
		}
	}
	return pending, nil
}

func (p *pendingPartitionDefinition) finish(scope *StatementScope) error {
	if p == nil {
		return nil
	}
	for _, constraint := range p.Child.Constraints.Unique {
		idx := p.stage.Get(constraint.Index).(*catalog.Relation)
		if err := index.PartitionUniqueCoverage(p.Child, idx.SQLIndex); err != nil {
			return err
		}
	}
	for _, constraint := range p.Child.Constraints.Exclusion {
		idx := p.stage.Get(constraint.Index).(*catalog.Relation)
		if err := index.PartitionUniqueCoverage(p.Child, idx.SQLIndex); err != nil {
			return err
		}
	}
	if p.Parent == nil {
		return partition.CheckSupported(&p.Definition)
	}
	ctx := scope.op.Context()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Input: scope.input, Output: scope.output}}
	if !p.Bound.DefaultBound {
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
		if err := partition.CheckSupported(&p.Definition); err != nil {
			return err
		}
		evaluation.Catalog, evaluation.RoutineAuthority = p.auth.Catalog, &p.auth
		for _, ref := range p.DefaultLeaves {
			leaf, ok := view.Relation(ref.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			layout, err := catalog.InheritanceQueryLayout(p.Parent, leaf)
			if err != nil {
				return err
			}
			mapping := exec.InheritanceQuerySegment{Layout: layout}
			if layout != nil {
				mapping.Scratch = make([]sqltype.Value, len(layout))
			}
			source, err := exec.CaptureSource(ctx, p.view, ref)
			if err != nil {
				return err
			}
			for i := range source.Count() {
				var values [32]catalog.PartitionDatum
				key, err := partition.RowKey(p.Parent, mapping.MapValues(source.Row(i)), &evaluation, values[:0])
				if err != nil {
					source.Close()
					return err
				}
				contains, err := partition.Contains(ctx, p.Parent.Partition.Keys, p.Bound, key, scope.input)
				if err != nil {
					source.Close()
					return err
				}
				if contains {
					source.Close()
					defaultRelation := view.MustRelation(p.Parent.Partition.DefaultChild)
					diagnostic := sqlError("23514", fmt.Sprintf(`updated partition constraint for default partition "%s" would be violated by some row`, defaultRelation.Name())).(*sqlstate.Error)
					diagnostic.Schema, diagnostic.Table = view.Get(defaultRelation.Namespace()).Name(), defaultRelation.Name()
					return diagnostic
				}
			}
			source.Close()
		}
	}
	if err := partition.CheckSupported(&p.Definition); err != nil {
		return err
	}
	parent, err := partition.WithChild(p.Parent, p.Child.Address(), p.Bound.DefaultBound)
	if err != nil {
		return err
	}
	if err := p.stage.Replace(parent); err != nil {
		return err
	}
	e := scope.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, index := range index.PartitionIndexDeclarationOrder(p.stage.Base, p.Parent) {
		child := e.stageRead(p.stage).MustRelation(p.Child.Address())
		if _, err := e.stagePartitionIndexChild(scope.op, p.stage, p.Parent, index, child, true); err != nil {
			return err
		}
	}
	return nil
}

// Both CREATE and ATTACH use the same typed deciding-component diagnostic.
// ATTACH transforms datums before opening its candidate, then invokes this
// validation after that existing relation's eligibility/schema checks.
func (p *pendingPartitionDefinition) validateBounds(scope *StatementScope) error {
	if p.Bound.DefaultBound {
		return nil
	}
	if p.Bound.Strategy == parser.PartitionHash {
		return nil
	}
	ctx := scope.op.Context()
	order, deciding, err := partition.CompareTuple(ctx, p.Parent.Partition.Keys, p.Bound.Lower, p.Bound.Upper, scope.input)
	if err != nil {
		return err
	}
	if order >= 0 {
		lower, err := partition.TupleSQL(ctx, p.Bound.Lower)
		if err != nil {
			return err
		}
		upper, err := partition.TupleSQL(ctx, p.Bound.Upper)
		if err != nil {
			return err
		}
		d := expr.ErrorAt("42P17", fmt.Sprintf(`empty range bound specified for partition "%s"`, p.Child.Name()), p.Bound.Lower[deciding].Pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("Specified lower bound (%s) is greater than or equal to upper bound (%s).", lower, upper)
		return d
	}
	return nil
}

func (p *pendingPartitionDefinition) checkOverlap(scope *StatementScope) error {
	if p.Bound.Strategy == parser.PartitionHash {
		return p.checkHashOverlap(scope)
	}
	count := len(p.Parent.Partition.Children)
	if p.Parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
		count--
	}
	if count == 0 {
		return nil
	}
	// Most hierarchies need no heap allocation. Larger ones require one bounded
	// projection to reproduce the ordered search's last-comparison diagnostic.
	var local [16]partition.Boundary
	bounds := local[:0]
	if count > len(local)/2 {
		bounds = make([]partition.Boundary, 0, 2*count)
	}
	for _, address := range p.Parent.Partition.Children {
		sibling := p.stage.Base.MustRelation(address)
		if sibling.Membership.DefaultBound {
			continue
		}
		bounds = append(bounds,
			partition.Boundary{Tuple: sibling.Membership.Lower, Relation: sibling, Lower: true},
			partition.Boundary{Tuple: sibling.Membership.Upper, Relation: sibling})
	}
	compare := func(a, b partition.Boundary) (int, int, error) {
		order, component, err := partition.CompareTuple(scope.op.Context(), p.Parent.Partition.Keys, a.Tuple, b.Tuple, scope.input)
		if err == nil && order == 0 && a.Lower != b.Lower {
			order = -1
			if a.Lower {
				order = 1
			}
		}
		return order, component, err
	}
	var sortErr error
	slices.SortFunc(bounds, func(a, b partition.Boundary) int {
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
			order, _, err := partition.CompareTuple(scope.op.Context(), p.Parent.Partition.Keys, unique[len(unique)-1].Tuple, bound.Tuple, scope.input)
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
	probe := partition.Boundary{Tuple: p.Bound.Lower, Lower: true}
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
	position := p.Bound.Lower[lastComponent].Pos
	if !next.Lower {
		// The lower endpoint is already inside a sibling. PostgreSQL exposes
		// the search's final comparison component, with equality at the first.
		if lastOrder == 0 {
			position = p.Bound.Lower[0].Pos
		}
	} else {
		// The lower endpoint is in a gap. Only its next partition can be the
		// first overlap; point to the deciding new upper component.
		order, component, err := compare(next, partition.Boundary{Tuple: p.Bound.Upper})
		if err != nil {
			return err
		}
		if order >= 0 {
			return nil
		}
		position = p.Bound.Upper[component].Pos
	}
	return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, p.Child.Name(), next.Relation.Name()), position)
}
