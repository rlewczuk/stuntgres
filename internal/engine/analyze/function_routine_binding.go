package analyze

// Binder side of builtin routine references: capturing the selected
// declaration's descriptor and its SQL-body closure as binding dependencies.
// The declaration tables and lookups live in routine_catalog.go and
// routine_body.go.

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func (b *Binder) builtinRoutineInfo(info expr.Info, provider functions.RoutineProvider, name string, inputs ...sqltype.ID) (expr.Info, error) {
	declaration, err := routine.LookupBuiltinRoutineDeclaration(provider, name, inputs)
	if err != nil {
		return expr.Info{}, err
	}
	return b.selectedBuiltinRoutineInfo(info, declaration)
}

func (b *Binder) builtinRoutineReference(provider functions.RoutineProvider, name string, inputs ...sqltype.ID) (ids.ObjectRef, error) {
	declaration, err := routine.LookupBuiltinRoutineDeclaration(provider, name, inputs)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	_, ref, err := b.captureBuiltinRoutineDeclaration(declaration)
	return ref, err
}

func (b *Binder) selectedBuiltinRoutineInfo(info expr.Info, declaration *catalog.BuiltinRoutineDeclaration) (expr.Info, error) {
	d, _, err := b.captureBuiltinRoutineDeclaration(declaration)
	if err != nil {
		return expr.Info{}, err
	}
	info.RoutineReference = d
	return info, nil
}

func (b *Binder) captureBuiltinRoutineDeclaration(declaration *catalog.BuiltinRoutineDeclaration) (*catalog.Procedure, ids.ObjectRef, error) {
	view := b.BindingCatalog()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID}
	descriptor, present := catalog.DescriptorAt(view, b.DefinitionReader(), address)
	d, ok := descriptor.(*catalog.Procedure)
	if !present || !ok || d.Builtin != declaration {
		return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: missing owned builtin routine declaration for %s", declaration.Identity.Name)
	}
	ref := view.Owner().Domain.Ref(view.Epoch(), address)
	b.dependency(ref, d.Revision())
	if err := b.captureRoutineBody(d); err != nil {
		return nil, ids.ObjectRef{}, err
	}
	return d, ref, nil
}

// Capture at binding, before descriptors can be remapped or published with a new
// ACL. Optional polymorphic casts are selected from the real argument, not all
// possible casts and not ordinary I/O helper identities.
func (b *Binder) capturePolymorphicBody(node expr.Expr) error {
	outer, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || outer == nil {
		return err
	}
	argument := exec.PolymorphicBodyArgument(node, outer.Address().ObjectID)
	for _, edge := range outer.Body {
		if exec.BodyCastMatches(edge, argument) {
			if _, _, err := b.captureBuiltinRoutineDeclaration(edge.Routine.Builtin); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Binder) captureRoutineBody(outer *catalog.Procedure) error {
	for _, edge := range outer.Body {
		if edge.Demand == catalog.BodyBooleanText || edge.Demand == catalog.BodyNetworkText {
			continue
		}
		if edge.Routine == nil || edge.Routine.Builtin == nil {
			return fmt.Errorf("stuntgres: invalid SQL body declaration")
		}
		if _, _, err := b.captureBuiltinRoutineDeclaration(edge.Routine.Builtin); err != nil {
			return err
		}
	}
	return nil
}
