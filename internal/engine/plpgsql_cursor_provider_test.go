package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLCursorProviderValidation(t *testing.T) {
	e, s := plUnitScope(t)
	source := cursorOwnerRoutine(t, e, s, "DECLARE c CURSOR(a integer) FOR SELECT a AS i; BEGIN FOR r IN c(7) LOOP RETURN r.i; END LOOP; RETURN 0; END")
	source.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 900001}
	source.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	source.ObjectRevision = 1
	source.RoutineKind, source.Volatility = catalog.FunctionRoutine, expr.Volatile
	source.Result = ids.OID(source.SQL.Result.Type.ID())
	read := e.CatalogRead()
	state := catalog.CloneState(read.State())
	state.SetDescriptor(source.Address(), source)
	state.BindName(catalog.DescriptorNameKey(source), source.Address())
	view := catalog.NewRead(read.Owner(), state)
	provider := stored.ProcedureProvider{}
	if err := provider.Validate(view, source); err != nil {
		t.Fatal("valid cursor program rejected", err)
	}
	body := source.SQL.PL.Root.Statements[0].Block
	cursorSlot := body.Bindings["c"]
	argumentSlot := source.SQL.PL.Variables[cursorSlot].Cursor.Arguments[0]
	loopSlot := body.Statements[0].Cursor.LoopSlot
	foreign := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		foreign[typ.ID()] = typ
	}
	for _, kind := range []string{"foreign_argument_usage", "foreign_found_usage", "missing_argument_role", "redirected_argument_slot", "typed_loop_record", "argument_query", "argument_source", "argument_coercion"} {
		t.Run(kind, func(t *testing.T) {
			candidate := *source
			definition := *source.SQL
			program := *source.SQL.PL
			program.Variables = slices.Clone(program.Variables)
			definition.PL = &program
			candidate.SQL = &definition
			switch kind {
			case "foreign_argument_usage":
				program.Variables[argumentSlot].Usage.Type = foreign[sqltype.Int4]
			case "foreign_found_usage":
				program.Variables[program.FoundSlot].Usage.Type = foreign[sqltype.Bool]
			case "missing_argument_role":
				program.Variables[argumentSlot].Role = 0
			case "redirected_argument_slot":
				recipe := *program.Variables[cursorSlot].Cursor
				recipe.Arguments = slices.Clone(recipe.Arguments)
				recipe.Arguments[0] = program.FoundSlot
				program.Variables[cursorSlot].Cursor = &recipe
			case "typed_loop_record":
				program.Variables[loopSlot].Usage = source.SQL.Result
			case "argument_query", "argument_source", "argument_coercion":
				root := *program.Root
				root.Statements = slices.Clone(root.Statements)
				block := *root.Statements[0].Block
				block.Statements = slices.Clone(block.Statements)
				instruction := *block.Statements[0].Cursor
				instruction.Arguments = slices.Clone(instruction.Arguments)
				block.Statements[0].Cursor = &instruction
				root.Statements[0].Block, program.Root = &block, &root
				switch kind {
				case "argument_query":
					instruction.ArgumentQuery = nil
				case "argument_source":
					instruction.Arguments[0].Start.Character++
				case "argument_coercion":
					query := *instruction.ArgumentQuery
					query.Targets = slices.Clone(query.Targets)
					// A destination-dependent replacement is not the parsed,
					// uncoerced source projection, even with identical arity.
					query.Targets[0] = parser.CastExpr{Child: query.Targets[0], TypeName: parser.TypeName{Name: "integer"}, Pos: query.Targets[0].SourcePosition()}
					instruction.ArgumentQuery = &query
				}
			}
			if err := provider.Validate(view, &candidate); err == nil {
				t.Fatal("forged/foreign cursor metadata accepted")
			}
		})
	}
	if source.SQL.PL.Variables[argumentSlot].Usage.Type == foreign[sqltype.Int4] || source.SQL.PL.Variables[cursorSlot].Cursor.Arguments[0] != argumentSlot {
		t.Fatal("validation fixtures mutated source")
	}
	if err := provider.Validate(view, source); err != nil {
		t.Fatal("candidate forgery mutated source program", err)
	}
}

func TestPLpgSQLCursorOperandIdentityProvider(t *testing.T) {
	for _, tc := range []struct{ name, statement string }{
		{"open_static", "OPEN c FOR SELECT 7;"},
		{"open_dynamic", "OPEN c FOR EXECUTE 'SELECT $1::integer' USING 7;"},
		{"fetch", "FETCH c INTO x;"},
		{"move", "MOVE c;"},
		{"close", "CLOSE c;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := plUnitScope(t)
			source := cursorOwnerRoutine(t, e, s, "DECLARE c refcursor; ordinary refcursor; x integer; BEGIN "+tc.statement+" RETURN 0; END")
			source.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 900002}
			source.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			source.ObjectRevision = 1
			source.RoutineKind, source.Volatility = catalog.FunctionRoutine, expr.Volatile
			source.Result = ids.OID(source.SQL.Result.Type.ID())
			read := e.CatalogRead()
			refcursor, ok := read.LookupType(sqltype.Refcursor)
			if !ok {
				t.Fatal("missing refcursor")
			}
			domain, err := sqltype.Domain(96002, "cursor_domain", "cursor_domain", refcursor.Usage())
			if err != nil {
				t.Fatal(err)
			}
			if !domain.Usage().Valid() || domain.Usage().Typmod != -1 || domain.BaseType() != refcursor {
				t.Fatal("invalid domain Usage fixture")
			}
			state := catalog.CloneState(read.State())
			descriptor := &catalog.Type{SQLType: domain, ObjectNamespace: source.Namespace(), ObjectRevision: 1}
			state.SetDescriptor(descriptor.Address(), descriptor)
			state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
			state.SetDescriptor(source.Address(), source)
			state.BindName(catalog.DescriptorNameKey(source), source.Address())
			view := catalog.NewRead(read.Owner(), state)
			installed, ok := view.LookupType(domain.ID())
			if !ok || installed != domain {
				t.Fatal("domain fixture is not canonical")
			}
			provider := stored.ProcedureProvider{}
			if err := provider.Validate(view, source); err != nil {
				t.Fatal("valid refcursor program rejected", err)
			}
			body := source.SQL.PL.Root.Statements[0].Block
			candidate := *source
			definition := *source.SQL
			program := *source.SQL.PL
			program.Variables = slices.Clone(program.Variables)
			definition.PL, candidate.SQL = &program, &definition
			// The same canonical domain remains valid as an ordinary local.
			program.Variables[body.Bindings["ordinary"]].Usage = domain.Usage()
			if err := provider.Validate(view, &candidate); err != nil {
				t.Fatal("ordinary domain local rejected", err)
			}
			program.Variables[body.Bindings["c"]].Usage = domain.Usage()
			if err := provider.Validate(view, &candidate); err == nil {
				t.Fatal("domain-backed cursor operand entered executable provider state")
			}
			if source.SQL.PL.Variables[body.Bindings["c"]].Usage.Type != refcursor || source.SQL.PL.Variables[body.Bindings["ordinary"]].Usage.Type != refcursor {
				t.Fatal("forgery mutated source")
			}
			if err := provider.Validate(view, source); err != nil {
				t.Fatal("source program changed", err)
			}
		})
	}
}
