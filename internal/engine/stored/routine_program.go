package stored

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// RoutineProgram retains immutable analyzed recipes, never an opened executor,
// data snapshot, session frame, or permission decision.
type RoutineProgram struct {
	actions            []RuleAction
	actionDependencies [][]catalog.ExpressionDependency
	dependencies       []catalog.ExpressionDependency
}

func NewRoutineProgram(actions []RuleAction) *RoutineProgram {
	p := &RoutineProgram{actions: slices.Clone(actions), actionDependencies: make([][]catalog.ExpressionDependency, len(actions))}
	for i, action := range actions {
		var dependencies []catalog.ExpressionDependency
		if action.binding.Relation != nil {
			dependencies = append(dependencies, catalog.ExpressionDependency{Ref: action.binding.Ref, Revision: action.binding.Relation.Revision()})
		}
		for _, source := range action.binding.QuerySources {
			if source.Relation != nil {
				dependencies = append(dependencies, catalog.ExpressionDependency{Ref: source.Ref, Revision: source.Relation.Revision()})
			}
		}
		dependencies = append(dependencies, action.binding.Dependencies...)
		if action.presentation != nil {
			dependencies = append(dependencies, action.presentation.Dependencies()...)
		}
		for _, presentation := range action.modifierPresentations {
			if presentation != nil {
				dependencies = append(dependencies, presentation.Dependencies()...)
			}
		}
		p.actionDependencies[i] = dependencies
		p.dependencies = append(p.dependencies, dependencies...)
	}
	p.dependencies = routineDependencySet(p.dependencies)
	return p
}
func RoutineProgramOf(d *catalog.Procedure) (*RoutineProgram, error) {
	p, ok := d.SQLBody.(*RoutineProgram)
	if !ok || p == nil || d.SQL == nil || !d.SQL.Standard || p.Len() != len(d.SQL.Statements) {
		return nil, fmt.Errorf("stuntgres: invalid SQL-standard routine program")
	}
	return p, nil
}
func (p *RoutineProgram) Len() int                                     { return len(p.actions) }
func (p *RoutineProgram) Action(i int) RuleAction                      { return p.actions[i] }
func (p *RoutineProgram) Dependencies() []catalog.ExpressionDependency { return p.dependencies }
func (p *RoutineProgram) StatementSQL(ctx context.Context, view catalog.Read, i int) (string, error) {
	if err := p.ValidateAction(view, i); err != nil {
		return "", err
	}
	return RuleActionSQL(ctx, view, p.actions[i])
}
func (p *RoutineProgram) Validate(view catalog.Read) error {
	if len(p.actionDependencies) != len(p.actions) {
		return fmt.Errorf("stuntgres: incomplete routine dependency capture")
	}
	for i := range p.actions {
		if err := p.ValidateAction(view, i); err != nil {
			return err
		}
	}
	return p.validateDependencySet()
}

// ValidateAction checks only the reached immutable command against current
// catalog facts. Authorization remains the invoker admission owner's job.
func (p *RoutineProgram) ValidateAction(view catalog.Read, i int) error {
	if i < 0 || i >= len(p.actions) || i >= len(p.actionDependencies) {
		return fmt.Errorf("stuntgres: invalid routine command index")
	}
	for _, dependency := range p.actionDependencies[i] {
		ref := dependency.Ref
		if ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		address := ref.Address
		address.SubID = 0
		d, ok := view.Find(address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if ref.Address.SubID != 0 {
			relation, ok := d.(*catalog.Relation)
			if !ok || !relation.HasAttribute(ref.Address.SubID) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	action := p.actions[i]
	if action.syntax == nil || action.presentation == nil {
		return fmt.Errorf("stuntgres: incomplete routine command capture")
	}
	if err := validateRoutineActionShape(action); err != nil {
		return err
	}
	if err := validateRoutineActionScalars(action, view); err != nil {
		return err
	}
	if err := validateRoutinePresentation(view, action.presentation); err != nil {
		return err
	}
	for _, presentation := range action.modifierPresentations {
		if presentation == nil {
			return fmt.Errorf("stuntgres: incomplete routine modifier capture")
		}
		if err := validateRoutinePresentation(view, presentation); err != nil {
			return err
		}
	}
	return nil
}
func (p *RoutineProgram) Rebind(view catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress) (catalog.RoutineProgram, error) {
	if err := p.validateDependencySet(); err != nil {
		return nil, err
	}
	var invalidReference bool
	var domain *ids.Domain
	var epoch ids.CatalogEpoch
	if len(p.dependencies) != 0 {
		domain, epoch = p.dependencies[0].Ref.Domain(), p.dependencies[0].Ref.Epoch
	}
	// Address projections (schema origins and alias datum OIDs) carry no
	// lifetime authority. Keep them out of the checked ObjectRef boundary.
	addressMapper := func(address ids.ObjectAddress) ids.ObjectAddress {
		base := address
		base.SubID = 0
		if mapped, ok := mapping[base]; ok {
			address.ClassID, address.ObjectID = mapped.ClassID, mapped.ObjectID
		}
		return address
	}
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) {
			return old
		}
		if domain != nil && (old.Domain() != domain || old.Epoch != epoch) {
			invalidReference = true
		}
		return view.Owner().Domain.Ref(view.Epoch(), addressMapper(old.Address))
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}
		if mapped, ok := mapping[address]; ok {
			address = mapped
		}
		current, _ := view.LookupType(sqltype.ID(address.ObjectID))
		return current
	}
	actions := slices.Clone(p.actions)
	for i := range actions {
		a := &actions[i]
		if err := validateRoutineActionShape(*a); err != nil {
			return nil, err
		}
		var err error
		if a.presentation == nil {
			return nil, fmt.Errorf("stuntgres: missing frozen routine formal namespace")
		}
		if err := validateRoutineScalars(a.presentation, catalog.Read{}); err != nil {
			return nil, err
		}
		if err := validateRoutineActionScalars(*a, catalog.Read{}); err != nil {
			return nil, err
		}
		a.binding, err = remapRuleBinding(view, a.binding, ref, typ, nil, len(a.presentation.routineInputs), addressMapper)
		if err != nil {
			return nil, err
		}
		a.presentation, err = remapView(view, a.presentation, mapping, ref, typ, nil, addressMapper)
		if err != nil {
			return nil, err
		}
		a.modifierPresentations = maps.Clone(a.modifierPresentations)
		for statement, presentation := range a.modifierPresentations {
			if err := validateRoutineScalars(presentation, catalog.Read{}); err != nil {
				return nil, err
			}
			a.modifierPresentations[statement], err = remapView(view, presentation, mapping, ref, typ, nil, addressMapper)
			if err != nil {
				return nil, err
			}
		}
	}
	if invalidReference {
		return nil, ids.ErrCatalogReplaced
	}
	next := NewRoutineProgram(actions)
	return next, next.Validate(view)
}
