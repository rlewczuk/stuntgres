package routine

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
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

func indexBuiltinRoutineFormals() map[builtinRoutineKey]*catalog.BuiltinRoutineDeclaration {
	byDeclaration := make(map[builtinRoutineKey]*catalog.BuiltinRoutineDeclaration, len(BuiltinRoutineDeclarations))
	for i := range BuiltinRoutineDeclarations {
		d := &BuiltinRoutineDeclarations[i]
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

// BuiltinRoutineCost returns the immutable released cost used when seeding a
// builtin declaration, independently of its mutable public descriptor.
func BuiltinRoutineCost(d *catalog.BuiltinRoutineDeclaration) float32 {
	display, ok := builtinRoutineDisplay[d.Identity.ID]
	if !ok || display.Language.String() == "" {
		// View-authority declarations deliberately stay outside the scalar
		// inventory, but retain their genuine internal-language default.
		for i := range sharedMemoryDeclarations {
			if d == &sharedMemoryDeclarations[i] {
				return 1
			}
		}
		panic("stuntgres: missing builtin routine display declaration")
	}
	if display.Cost != 0 {
		return display.Cost
	}
	if display.Language == catalog.SQLRoutineLanguage || display.Language == catalog.PLpgSQLRoutineLanguage {
		return 100
	}
	return 1
}

// BuiltinProcedures returns a fresh descriptor for every builtin routine
// declaration, in declaration order, with SQL bodies attached. The engine
// installs them; descriptor pointers retain the returned backing array.
// Immutable cells remain stable; later ACL/owner changes still publish
// ordinary COW records.
func BuiltinProcedures(owner authorization.PrincipalRef) []catalog.Procedure {
	procedures := slices.Clone(builtinProcedureTemplate())
	for i := range procedures {
		procedures[i].Owner = owner
	}
	// Body edges point into this backing array, so each copy attaches its own.
	attachBuiltinRoutineBodies(procedures)
	return procedures
}

// builtinProcedureTemplate holds every field of BuiltinProcedures' result
// except the owner and the SQL bodies, built once per process: the binary
// fixes them, and the cells they share are immutable.
var builtinProcedureTemplate = sync.OnceValue(func() []catalog.Procedure {
	if len(builtinRoutineDisplay) != len(BuiltinRoutineDeclarations) {
		panic("stuntgres: incomplete builtin routine display inventory")
	}
	procedures := make([]catalog.Procedure, len(BuiltinRoutineDeclarations))
	for i := range BuiltinRoutineDeclarations {
		declaration := &BuiltinRoutineDeclarations[i]
		display, ok := builtinRoutineDisplay[declaration.Identity.ID]
		if !ok || display.Language.String() == "" {
			panic("stuntgres: missing builtin routine display declaration")
		}
		procedures[i] = catalog.Procedure{
			ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID},
			ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: declaration.Identity.Namespace},
			ObjectRevision:  1, ObjectName: declaration.Identity.Name, Signature: declaration.Identity.Signature,
			RoutineKind: declaration.Kind, Builtin: declaration,
			Display:         builtinRoutineDisplayValue(declaration.Identity.ID, display),
			DeclarationBody: builtinRoutineDeclarationBodies[declaration.Identity.ID],
			Cost:            BuiltinRoutineCost(declaration),
			Result:          declaration.Result, Volatility: declaration.Volatility, Strict: declaration.Strict,
			LiteralDefaults: builtinRoutineLiteralDefaults[declaration.Identity.ID],
			InputNames:      builtinRoutineInputNames[declaration.Identity.ID],
		}
	}
	return procedures
})

// Called only after a concrete existing provider has successfully selected its
// declaration. Physical child usages are not substituted for declared formals.
// Metadata borrows the already-owned immutable descriptor pointer; the existing
// value dependency manifest separately owns the scoped lifetime reference.
func LookupBuiltinRoutineDeclaration(provider functions.RoutineProvider, name string, inputs []sqltype.ID) (*catalog.BuiltinRoutineDeclaration, error) {
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
