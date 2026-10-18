package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
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
		key := builtinRoutineKey{name: d.identity.name}
		remaining := string(d.identity.signature)
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
			address:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.identity.id},
			namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: declaration.identity.namespace},
			revision:  1, name: declaration.identity.name, signature: declaration.identity.signature,
			routineKind: declaration.kind, builtin: declaration, owner: owner,
			result: declaration.result, volatility: declaration.volatility, strict: declaration.strict,
		}
		e.catalog.state.descriptors[d.address] = d
		e.catalog.state.names[descriptorNameKey(d)] = d.address
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
	if declaration == nil || declaration.providers&provider == 0 {
		return nil, fmt.Errorf("stuntgres: missing successful routine association for %s", name)
	}
	return declaration, nil
}

func (b *expressionBinder) builtinRoutineInfo(info expressionInfo, provider routineProvider, name string, inputs ...sqltype.ID) (expressionInfo, error) {
	declaration, err := lookupBuiltinRoutineDeclaration(provider, name, inputs)
	if err != nil {
		return expressionInfo{}, err
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

func (b *expressionBinder) selectedBuiltinRoutineInfo(info expressionInfo, declaration *builtinRoutineDeclaration) (expressionInfo, error) {
	d, _, err := b.captureBuiltinRoutineDeclaration(declaration)
	if err != nil {
		return expressionInfo{}, err
	}
	info.RoutineReference = d
	return info, nil
}

func (b *expressionBinder) captureBuiltinRoutineDeclaration(declaration *builtinRoutineDeclaration) (*procedureDescriptor, ids.ObjectRef, error) {
	view := b.bindingCatalog()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.identity.id}
	d, ok := view.Procedure(address)
	if !ok || d.builtin != declaration {
		return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: missing owned builtin routine declaration for %s", declaration.identity.name)
	}
	ref := view.owner.domain.Ref(view.Epoch(), address)
	b.dependency(ref, d.revision)
	if err := b.captureRoutineBody(d); err != nil {
		return nil, ids.ObjectRef{}, err
	}
	return d, ref, nil
}
