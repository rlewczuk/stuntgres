package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

type DerivedIndexKey struct {
	Definition, Execution expr.Expr
	Dependencies          []ids.ObjectRef
	Attributes            []int32 // Immutable parent layout used by both bound row frames.
}

func (d *IndexDefinition) HasRuntimeDomainConversion() bool {
	if d.Predicate != nil && d.Predicate.Execution.Properties().RuntimeDomainConversion {
		return true
	}
	return d.HasRuntimeDomainKeyConversion()
}

// Callable index recipes must run on the produced tuple before RETURNING.
// Reuse the tracked insertion output so publication does not call them twice.
// Plain-column indexes retain their allocation-free insertion path.
func (d *IndexDefinition) RequiresInsertionOutput() bool {
	if d.HasRuntimeDomainConversion() {
		return true
	}
	if d.Predicate != nil {
		for _, ref := range d.Predicate.Dependencies {
			if ref.Address.ClassID == ids.ProcedureClassID {
				return true
			}
		}
	}
	for _, key := range d.Keys {
		if key.Derived == nil {
			continue
		}
		for _, ref := range key.Derived.Dependencies {
			if ref.Address.ClassID == ids.ProcedureClassID {
				return true
			}
		}
	}
	return false
}

func (d *IndexDefinition) HasRuntimeDomainKeyConversion() bool {
	for _, key := range d.Keys {
		if key.Derived != nil && key.Derived.Execution.Properties().RuntimeDomainConversion {
			return true
		}
	}
	return false
}
