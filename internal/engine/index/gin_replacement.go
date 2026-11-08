package index

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// This is a replacement-owner certificate for an exact retained TEMP source,
// not a way for detached class values to refresh their captured authority.
func rebindRetainedGin(ctx context.Context, before, after catalog.Read, parent, idx *catalog.Relation, root *storage.IndexRoot) (*catalog.Relation, *storage.IndexRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if before.Owner() != after.Owner() || before.Get(idx.Address()) != idx || after.Get(idx.Address()) != idx || before.Get(parent.Address()) != parent || after.Get(parent.Address()) != parent || root == nil || root.Gin == nil {
		return nil, nil, ids.ErrCatalogReplaced
	}
	prior, err := BindGinIndex(before, parent, idx.SQLIndex)
	if err != nil {
		return nil, nil, err
	}
	for _, columns := range [2][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
		if len(columns) != len(prior) && len(columns) != 0 {
			return nil, nil, ids.ErrCatalogReplaced
		}
		for i, column := range columns {
			if !prior[i].Same(column.Binding) {
				return nil, nil, fmt.Errorf("stuntgres: stale retained GIN source binding")
			}
		}
	}
	if len(root.Gin.Columns) != len(prior) {
		return nil, nil, ids.ErrCatalogReplaced
	}
	for _, binding := range prior {
		typ, ok := after.LookupType(binding.Usage.Type.ID())
		if !ok || typ != binding.Usage.Type {
			return nil, nil, fmt.Errorf("stuntgres: retained GIN replacement changed concrete type authority")
		}
		if binding.Usage.Collation.ID != 0 {
			address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(binding.Usage.Collation.ID)}
			collation, ok := after.Get(address).(*catalog.Collation)
			if !ok {
				return nil, nil, ids.ErrCatalogReplaced
			}
			if err := (catalog.CollationProvider{}).Validate(after, collation); err != nil {
				return nil, nil, err
			}
			current, err := collation.Usage()
			if err != nil {
				return nil, nil, err
			}
			current.Derivation = binding.Usage.Collation.Derivation
			if current != binding.Usage.Collation {
				return nil, nil, fmt.Errorf("stuntgres: retained GIN replacement changed collation authority")
			}
		}
	}
	definition := *idx.SQLIndex
	var keys []catalog.IndexKey
	for i, key := range definition.Keys {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		current, err := retainedGinReplacementClass(before, after, key.Class)
		if err != nil {
			return nil, nil, err
		}
		if current != key.Class {
			if keys == nil {
				keys = slices.Clone(definition.Keys)
			}
			keys[i].Class = current
		}
	}
	if keys == nil {
		return idx, root, nil
	}
	definition.Keys = keys
	bindings, err := BindGinIndex(after, parent, &definition)
	if err != nil {
		return nil, nil, err
	}
	for i, binding := range bindings {
		comparison := binding
		comparison.Class = prior[i].Class
		if !comparison.Same(prior[i]) {
			return nil, nil, fmt.Errorf("stuntgres: retained GIN replacement changed token semantics")
		}
	}
	descriptor := *idx
	descriptor.SQLIndex = &definition
	next, payload := *root, *root.Gin
	payload.Columns = slices.Clone(payload.Columns)
	payload.Pending = slices.Clone(payload.Pending)
	for i, binding := range bindings {
		payload.Columns[i].Binding = binding
		if len(payload.Pending) != 0 {
			payload.Pending[i].Binding = binding
		}
	}
	next.Gin = &payload
	next.Construction = transportConstruction(root, idx, &descriptor)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return &descriptor, &next, nil
}

func retainedGinReplacementClass(before, after catalog.Read, captured sqltype.IndexClass) (sqltype.IndexClass, error) {
	old, err := routine.BindClassSupport(before, captured)
	if err != nil {
		return sqltype.IndexClass{}, err
	}
	current, ok := after.IndexClass(ids.OID(old.OID()))
	if !ok || !old.IsGin() || !old.SameIdentity(current) {
		return sqltype.IndexClass{}, ids.ErrCatalogReplaced
	}
	current, err = routine.BindClassSupport(after, current)
	if err != nil {
		return sqltype.IndexClass{}, err
	}
	oldClass, oldOK := before.OperatorClass(ids.OID(old.OID()))
	newClass, newOK := after.OperatorClass(ids.OID(old.OID()))
	if !oldOK || !newOK {
		return sqltype.IndexClass{}, ids.ErrCatalogReplaced
	}
	class := *newClass
	class.ObjectName, class.ObjectNamespace, class.Owner, class.ObjectRevision = oldClass.ObjectName, oldClass.ObjectNamespace, oldClass.Owner, oldClass.ObjectRevision
	if class != *oldClass {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: retained GIN replacement changed class authority")
	}
	familyAddress := ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(old.FamilyOID())}
	oldFamily, oldOK := before.OperatorFamily(familyAddress)
	newFamily, newOK := after.OperatorFamily(familyAddress)
	if !oldOK || !newOK {
		return sqltype.IndexClass{}, ids.ErrCatalogReplaced
	}
	family := *newFamily
	family.ObjectName, family.ObjectNamespace, family.Owner, family.ObjectRevision = oldFamily.ObjectName, oldFamily.ObjectNamespace, oldFamily.Owner, oldFamily.ObjectRevision
	if family != *oldFamily {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: retained GIN replacement changed family authority")
	}
	method := ids.OID(old.MethodOID())
	oldMethod, oldOK := before.AccessMethod(method)
	newMethod, newOK := after.AccessMethod(method)
	if !oldOK || !newOK || oldMethod.Native != catalog.GinAccessMethod || newMethod.Native != oldMethod.Native || oldMethod.Handler != 333 || newMethod.Handler != oldMethod.Handler {
		return sqltype.IndexClass{}, ids.ErrCatalogReplaced
	}
	if err := (catalog.AccessMethodProvider{}).Validate(before, oldMethod); err != nil {
		return sqltype.IndexClass{}, err
	}
	if err := (catalog.AccessMethodProvider{}).Validate(after, newMethod); err != nil {
		return sqltype.IndexClass{}, err
	}
	if err := retainedGinReplacementProcedure(before, after, oldMethod.Handler, false); err != nil {
		return sqltype.IndexClass{}, err
	}
	input, familyID := ids.OID(old.InputOID()), ids.OID(old.FamilyOID())
	profile, profiled := routine.LookupGinProfile(familyID)
	if profiled {
		input = profile.Input
	}
	for slot := int16(1); slot <= 6; slot++ {
		var declared routine.GinProfileSupport
		required := false
		if profiled {
			declared, required = profile.SupportFor(slot)
		} else {
			expected, needed := catalog.GinSupportRoutine(providerFamilyOf(input), slot)
			required = needed
			declared = routine.GinProfileSupport{Slot: slot, Routine: expected, Intrinsic: true}
		}
		left, leftOK := before.FamilySupport(familyID, input, input, slot)
		right, rightOK := after.FamilySupport(familyID, input, input, slot)
		if leftOK != rightOK || leftOK != required || leftOK && (*left != *right || left.Function != declared.Routine) {
			return sqltype.IndexClass{}, fmt.Errorf("stuntgres: retained GIN replacement changed support membership")
		}
		if !required {
			continue
		}
		if declared.Intrinsic {
			if err := retainedGinReplacementProcedure(before, after, declared.Routine, true); err != nil {
				return sqltype.IndexClass{}, err
			}
		} else {
			// The ordinary jsonb_path_ops slot-1 comparator is installed under an
			// ordinary routine identity, not a GinSupport declaration capability.
			if err := retainedGinReplacementProcedure(before, after, declared.Routine, false); err != nil {
				return sqltype.IndexClass{}, err
			}
			proc, ok := before.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declared.Routine})
			if !ok || proc.GinSupport != 0 {
				return sqltype.IndexClass{}, fmt.Errorf("stuntgres: retained GIN replacement changed support authority")
			}
		}
	}
	checkStrategy := func(leftType, rightType ids.OID, number int16) error {
		left, leftOK := before.FamilyOperator(familyID, leftType, rightType, number, 's')
		right, rightOK := after.FamilyOperator(familyID, leftType, rightType, number, 's')
		if !leftOK || !rightOK || *left != *right {
			return fmt.Errorf("stuntgres: retained GIN replacement changed search membership")
		}
		address := ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: left.Operator}
		oldOperator, oldOK := before.Get(address).(*catalog.Operator)
		newOperator, newOK := after.Get(address).(*catalog.Operator)
		if !oldOK || !newOK || *oldOperator != *newOperator {
			return fmt.Errorf("stuntgres: retained GIN replacement changed search operator")
		}
		if err := (routine.OperatorProvider{}).Validate(before, oldOperator); err != nil {
			return err
		}
		if err := (routine.OperatorProvider{}).Validate(after, newOperator); err != nil {
			return err
		}
		if oldOperator.Function == 0 {
			// A released seeded search row can be lookup-only (Function and
			// Result zero). Its strict member/operator certificates above are
			// authority; it does not claim a callable implementation provider.
			if oldOperator.Result != 0 || !routine.OperatorSeeded(oldOperator) || !routine.OperatorSeeded(newOperator) {
				return fmt.Errorf("stuntgres: retained GIN replacement has unowned lookup-only search metadata")
			}
			return nil
		}
		if routine.GinMetadataOnlyOperator(before, oldOperator) == nil && routine.GinMetadataOnlyOperator(after, newOperator) == nil {
			// Seeded operators 3247/3248/3249 name lookup-only released
			// identities 4047/4048/4049 with no installed procedure descriptor.
			return nil
		}
		return retainedGinReplacementProcedure(before, after, oldOperator.Function, false)
	}
	if profiled {
		for _, strategy := range profile.Strategies {
			if err := checkStrategy(strategy.Left, strategy.Right, int16(strategy.Number)); err != nil {
				return sqltype.IndexClass{}, err
			}
		}
	} else {
		rightType, strategies := input, int16(4)
		if input == 3614 {
			rightType, strategies = 3615, 2
		}
		for number := int16(1); number <= strategies; number++ {
			if err := checkStrategy(input, rightType, number); err != nil {
				return sqltype.IndexClass{}, err
			}
		}
	}
	return current, nil
}

// providerFamilyOf maps a released GIN input type to its builtin family.
func providerFamilyOf(input ids.OID) ids.OID {
	if input == 3614 {
		return 3659
	}
	return 2745
}

func retainedGinReplacementProcedure(before, after catalog.Read, address ids.OID, support bool) error {
	key := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: address}
	old, oldOK := before.Procedure(key)
	current, currentOK := after.Procedure(key)
	if !oldOK || !currentOK || !exec.SameRoutineDeclaration(current, old) || old.GinSupport != current.GinSupport {
		return fmt.Errorf("stuntgres: retained GIN replacement changed callable declaration")
	}
	if old.Builtin == nil && old.GinSupport == 0 && old.AccessMethod == catalog.NoAccessMethod && current != old {
		return fmt.Errorf("stuntgres: retained GIN replacement substituted user callable authority")
	}
	if support {
		if err := catalog.ValidateGinSupport(old); err != nil {
			return err
		}
		if err := catalog.ValidateGinSupport(current); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		view      catalog.Read
		procedure *catalog.Procedure
	}{{before, old}, {after, current}} {
		if item.view.Owner() == nil || item.view.Owner().Provider(ids.ProcedureClassID) == nil {
			return ids.ErrCatalogReplaced
		}
		if err := item.view.Owner().Provider(ids.ProcedureClassID).Validate(item.view, item.procedure); err != nil {
			return err
		}
	}
	return nil
}
