package core

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// One last specialization is retained per session and declaration. A nested
// invocation owns its private descriptor even when it replaces that cache entry.
func (s *StatementScope) invokePLRoutineSpecialized(d *catalog.Procedure, input *proc.Frame, read catalog.Read, collation sqltype.CollationRef, result sqltype.Usage) (sqltype.Value, error) {
	needs := d.SQL.Polymorphic() || d.SQL.Result.Type.ID() == 2249
	for _, usage := range d.SQL.Inputs {
		needs = needs || usage.Type.ID() == 2249
	}
	if !needs {
		return s.invokePLRoutine(d, input, read, collation)
	}
	signature := proc.InvocationSignature{Inputs: input.Inputs, Result: result}
	copiedInputs := false
	// Generic RECORD transport can hide a concrete anonymous tuple descriptor.
	// Compile %TYPE copies against that invocation's actual shape, without
	// changing the caller's checked signature or the published declaration.
	for i, usage := range d.SQL.Inputs {
		if usage.Type.ID() != 2249 {
			continue
		}
		actual := input.Parameters[i].ActualRecord()
		if actual.Type() != nil && actual.Type() != signature.Inputs[i].Type {
			if !copiedInputs {
				signature.Inputs = slices.Clone(signature.Inputs)
				copiedInputs = true
			}
			signature.Inputs[i] = actual.Type().Usage()
		}
	}
	e := s.Engine()
	e.Mu.Lock()
	payload := PayloadOf(s.Op.Session)
	retained := payload.sqlRoutines[d.Address()]
	current := retained.plSpecialized
	if retained.definition != d.SQL || current == nil || !retained.signature.Equal(signature) || retained.inputCollation != collation || retained.name != d.Name() {
		definition, err := d.SQL.Specialize(signature)
		if err != nil {
			e.Mu.Unlock()
			return sqltype.Value{}, err
		}
		// Compilation replaces PL and may normalize unshaped OUT transport.
		// Specialize deliberately returns the declaration for unchanged usages.
		if definition == d.SQL {
			owned := *definition
			owned.Formals = slices.Clone(definition.Formals)
			definition = &owned
		}
		definition.PolymorphicResult = sqltype.PolymorphicFamily(d.SQL.Result.Type.ID()) != 0
		for index := range definition.Formals {
			usage := definition.Formals[index].Usage
			if definition.FormalInputs[index] < 0 && usage.Type.ID() == 2249 && !usage.Type.IsComposite() {
				definition.Formals[index].Usage = sqltype.DeferredRecord().Usage()
			}
		}
		private := *d
		private.SQL = definition
		auth, err := s.RoutineAuthorizationLocked()
		if err == nil {
			err = e.CompilePLRoutineLocked(s.Op, auth, &private)
		}
		if err != nil {
			e.Mu.Unlock()
			return sqltype.Value{}, err
		}
		current = &private
		retained = payload.sqlRoutines[d.Address()]
		retained.definition, retained.signature, retained.inputCollation, retained.name = d.SQL, signature, collation, d.Name()
		retained.plSpecialized = current
		payload.sqlRoutines[d.Address()] = retained
	}
	e.Mu.Unlock()
	return s.invokePLRoutine(current, input, read, collation)
}
