package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
)

// DeclarationBodyRoutine resolves a typed slot of an already selected immutable
// released recipe. It is metadata/lifetime selection, never a SQL name lookup,
// permission check or claim that the selected kernel was actually invoked.
func DeclarationBodyRoutine(view catalog.Read, outer *catalog.Procedure, part proc.DeclarationPart) (*catalog.Procedure, error) {
	current, err := ValidateStoredRoutineDescriptor(view, outer)
	if err != nil {
		return nil, err
	}
	if current.DeclarationBody == nil || current.DeclarationBody != outer.DeclarationBody {
		return nil, ids.ErrCatalogReplaced
	}
	var selected *catalog.Procedure
	switch part.Kind {
	case proc.DeclarationRoutine:
		identity, ok := catalog.BuiltinRoutineIdentityBySignature(part.Name, catalog.ProcedureSignature(part.Signature))
		if !ok {
			return nil, fmt.Errorf("stuntgres: unknown released declaration routine %s(%s)", part.Name, part.Signature)
		}
		selected, ok = view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID})
		if !ok || selected.Builtin == nil || selected.Builtin.Identity != identity {
			return nil, ids.ErrCatalogReplaced
		}
	case proc.DeclarationOperator:
		operator, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: ids.OID(part.OID)})
		original := routine.OperatorRoutine(part.Name, ids.OID(part.Arguments[0]), ids.OID(part.Arguments[1]))
		if !ok || original == nil || operator.Left != ids.OID(part.Arguments[0]) || operator.Right != ids.OID(part.Arguments[1]) || operator.Function != original.Identity.ID {
			return nil, ids.ErrCatalogReplaced
		}
		selected, ok = view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
		if !ok || selected.Builtin != original {
			return nil, ids.ErrCatalogReplaced
		}
	case proc.DeclarationExtensionMember:
		descriptor, failure := extension.DeclarationMember(view, current, part)
		if failure != nil {
			return nil, failure
		}
		var ok bool
		selected, ok = descriptor.(*catalog.Procedure)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
	case proc.DeclarationSelfRoutine:
		selected = current
	default:
		return nil, fmt.Errorf("stuntgres: declaration part is not a callable source")
	}
	return selected, nil
}

// NativeDeclarationCall exposes the same immutable preorder ordinal to the real
// source-specific nested body owner (Earth/pageinspect/freespace/xml2), not an event
// substitute for that owner's argument, strictness and actual invocation work.
func NativeDeclarationCall(c *Context, outer *catalog.Procedure, source expr.Info, ordinal int) (*catalog.Procedure, expr.Info, error) {
	if c == nil || c.RoutineBodyContext.Outer != outer {
		return nil, expr.Info{}, fmt.Errorf("stuntgres: foreign entered declaration body")
	}
	recipe, ok := proc.RoutineDeclarationRecipe(outer.DeclarationBody)
	if !ok || ordinal < 0 || ordinal >= recipe.CallCount() {
		return nil, expr.Info{}, fmt.Errorf("stuntgres: missing declaration call ordinal")
	}
	index, part := recipe.CallPart(ordinal)
	auth, _, err := RoutineInvocationAuthority(c)
	if err != nil {
		return nil, expr.Info{}, err
	}
	selected, err := DeclarationBodyRoutine(auth.Catalog, outer, part)
	if err != nil {
		return nil, expr.Info{}, err
	}
	info := source
	info.RoutineReference = selected
	info.Occurrence = uint32(index + 1)
	return selected, info, nil
}

type nativeDeclarationDemand uint8

const (
	retainedNativeDeclarationCalls nativeDeclarationDemand = iota
	customNullFreespaceScalarElided
)

func initializeNativeDeclarationBody(c *Context, outer *catalog.Procedure, source expr.Info, demand nativeDeclarationDemand) error {
	recipe, ok := proc.RoutineDeclarationRecipe(outer.DeclarationBody)
	if !ok {
		return fmt.Errorf("stuntgres: missing selected native declaration recipe")
	}
	var elided *catalog.Procedure
	if demand != retainedNativeDeclarationCalls {
		if demand != customNullFreespaceScalarElided {
			return fmt.Errorf("stuntgres: invalid native declaration demand")
		}
		auth, _, err := RoutineInvocationAuthority(c)
		if err != nil {
			return err
		}
		current, err := ValidateStoredRoutineDescriptor(auth.Catalog, outer)
		if err != nil {
			return err
		}
		if extension.PgFreespaceProviderOf(current) != extension.StockPgFreespace || current.Origin == nil || current.Origin.Declaration != 1 {
			return fmt.Errorf("stuntgres: foreign custom NULL declaration demand")
		}
		elided, err = extension.PgFreespaceScalar(auth.Catalog, current)
		if err != nil {
			return err
		}
	}
	for ordinal := range recipe.CallCount() {
		selected, info, err := NativeDeclarationCall(c, outer, source, ordinal)
		if err != nil {
			return err
		}
		if elided != nil && selected.Address() == elided.Address() && SameRoutineDeclaration(selected, elided) {
			continue
		}
		if err = InitializeNativeBodyCall(c, selected, info); err != nil {
			return err
		}
	}
	return nil
}
