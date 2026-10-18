package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Fixed formal keys index the same immutable declaration table. They avoid
// formatting signature strings or repeating overload resolution during binding;
// this is neither an execution dispatcher nor a permission/identity cache.
type builtinRoutineKey struct {
	name   string
	inputs [7]ids.OID
	arity  uint8
}

var builtinRoutineByDeclaration = indexBuiltinRoutineFormals()

func indexBuiltinRoutineFormals() map[builtinRoutineKey]*builtinRoutineDeclaration {
	byDeclaration := make(map[builtinRoutineKey]*builtinRoutineDeclaration, len(builtinRoutineDeclarations))
	for i := range builtinRoutineDeclarations {
		d := &builtinRoutineDeclarations[i]
		key := builtinRoutineKey{name: d.Identity.Name}
		remaining := string(d.Identity.Signature)
		for remaining != "" {
			word, rest, _ := strings.Cut(remaining, ",")
			id, err := strconv.ParseUint(word, 10, 32)
			if err != nil || int(key.arity) == len(key.inputs) {
				panic("stuntgres: invalid builtin routine formal declaration")
			}
			key.inputs[key.arity] = ids.OID(id)
			key.arity++
			remaining = rest
		}
		byDeclaration[key] = d
	}
	return byDeclaration
}

func (e *Engine) initializeBuiltinRoutines() {
	owner := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	// Catalog pointers retain this engine-owned backing array. Immutable cells
	// remain stable; later ACL/owner changes still publish ordinary COW records.
	storage := make([]procedureDescriptor, len(builtinRoutineDeclarations))
	for i := range builtinRoutineDeclarations {
		declaration := &builtinRoutineDeclarations[i]
		d := &storage[i]
		*d = procedureDescriptor{
			ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID},
			ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: declaration.Identity.Namespace},
			ObjectRevision:  1, ObjectName: declaration.Identity.Name, Signature: declaration.Identity.Signature,
			RoutineKind: declaration.Kind, Builtin: declaration, Owner: owner,
			Result: declaration.Result, Volatility: declaration.Volatility, Strict: declaration.Strict,
		}
		e.catalog.state.setDescriptor(d.Address(), d)
		e.catalog.state.bindName(descriptorNameKey(d), d.Address())
	}
	e.initializeBuiltinRoutineBodies()
}

// Called only after a concrete existing provider has successfully selected its
// declaration. Physical child usages are not substituted for declared formals.
// Metadata borrows the already-owned immutable descriptor pointer; the existing
// value dependency manifest separately owns the scoped lifetime reference.
func lookupBuiltinRoutineDeclaration(provider routineProvider, name string, inputs []sqltype.ID) (*builtinRoutineDeclaration, error) {
	if len(inputs) > 7 {
		return nil, fmt.Errorf("stuntgres: unsupported builtin routine formal key")
	}
	key := builtinRoutineKey{name: name, arity: uint8(len(inputs))}
	for i, input := range inputs {
		key.inputs[i] = ids.OID(input)
	}
	declaration := builtinRoutineByDeclaration[key]
	if declaration == nil || declaration.Providers&provider == 0 {
		return nil, fmt.Errorf("stuntgres: missing successful routine association for %s", name)
	}
	return declaration, nil
}

func (b *expressionBinder) builtinRoutineInfo(info expr.Info, provider routineProvider, name string, inputs ...sqltype.ID) (expr.Info, error) {
	declaration, err := lookupBuiltinRoutineDeclaration(provider, name, inputs)
	if err != nil {
		return expr.Info{}, err
	}
	return b.selectedBuiltinRoutineInfo(info, declaration)
}

func (b *expressionBinder) builtinRoutineReference(provider routineProvider, name string, inputs ...sqltype.ID) (ids.ObjectRef, error) {
	declaration, err := lookupBuiltinRoutineDeclaration(provider, name, inputs)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	_, ref, err := b.captureBuiltinRoutineDeclaration(declaration)
	return ref, err
}

func (b *expressionBinder) selectedBuiltinRoutineInfo(info expr.Info, declaration *builtinRoutineDeclaration) (expr.Info, error) {
	d, _, err := b.captureBuiltinRoutineDeclaration(declaration)
	if err != nil {
		return expr.Info{}, err
	}
	info.RoutineReference = d
	return info, nil
}

func (b *expressionBinder) captureBuiltinRoutineDeclaration(declaration *builtinRoutineDeclaration) (*procedureDescriptor, ids.ObjectRef, error) {
	view := b.bindingCatalog()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID}
	d, ok := view.Procedure(address)
	if !ok || d.Builtin != declaration {
		return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: missing owned builtin routine declaration for %s", declaration.Identity.Name)
	}
	ref := view.owner.Domain.Ref(view.Epoch(), address)
	b.dependency(ref, d.Revision())
	if err := b.captureRoutineBody(d); err != nil {
		return nil, ids.ObjectRef{}, err
	}
	return d, ref, nil
}
