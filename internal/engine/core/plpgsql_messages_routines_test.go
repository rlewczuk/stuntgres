package core_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// Exercise the actual catalog provider, not only the lower checked validator.
func TestPLpgSQLRaiseAssertProvider(t *testing.T) {
	for _, name := range []string{"raise_query", "option_order", "severity", "assert_query", "old_payload"} {
		t.Run(name, func(t *testing.T) {
			e, s := plUnitScope(t)
			source := cursorOwnerRoutine(t, e, s, "BEGIN RAISE NOTICE '%',7 USING DETAIL='d',HINT='h'; ASSERT true,'m'; RETURN 7; END")
			source.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 900003}
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
				t.Fatal("canonical messages rejected", err)
			}
			candidate, definition, program := *source, *source.SQL, *source.SQL.PL
			root := *program.Root
			root.Statements = slices.Clone(root.Statements)
			body := *root.Statements[0].Block
			body.Statements = slices.Clone(body.Statements)
			raise := *body.Statements[0].Raise
			raise.Arguments, raise.Options = slices.Clone(raise.Arguments), slices.Clone(raise.Options)
			assertion := *body.Statements[1].Assert
			body.Statements[0].Raise, body.Statements[1].Assert = &raise, &assertion
			root.Statements[0].Block = &body
			program.Root, definition.PL, candidate.SQL = &root, &program, &definition
			switch name {
			case "raise_query":
				q := *raise.Arguments[0]
				q.Where = q.Targets[0]
				raise.Arguments[0] = &q
			case "option_order":
				raise.Options[0], raise.Options[1] = raise.Options[1], raise.Options[0]
			case "severity":
				raise.Level = parser.PLRaiseException
			case "assert_query":
				assertion.Condition = raise.Arguments[0]
			case "old_payload":
				body.Statements[2].Assert = &assertion
			}
			if err := provider.Validate(view, &candidate); err == nil {
				t.Fatal("provider admitted forged messages", name)
			}
			if !reflect.DeepEqual(program.Variables, source.SQL.PL.Variables) || source.SQL.PL.SameLayout(&program) {
				t.Fatal("forged semantic layout compared equal or changed usages")
			}
			if err := provider.Validate(view, source); err != nil {
				t.Fatal("candidate changed published source", err)
			}
		})
	}
}
