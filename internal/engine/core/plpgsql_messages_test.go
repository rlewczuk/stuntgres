package core_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLAssertRuntime(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{"ASSERT true,missing_message();", ""},
		{"ASSERT false,'failed';", "P0004"},
		{"ASSERT NULL,NULL;", "P0004"},
		{"ASSERT 1/0=0;", "22012"},
		{"ASSERT false,1/0;", "22012"},
		{"ASSERT missing_condition(),missing_message();", "42883"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			e, s := plUnitScope(t)
			d := cursorOwnerRoutine(t, e, s, "BEGIN "+tc.body+" RETURN 7; END")
			input := proc.NewFrame(nil)
			defer input.Close()
			read := e.CatalogRead()
			setting, err := e.Settings.Lookup("plpgsql.check_asserts")
			if err != nil {
				t.Fatal("assert setting not registered", err)
			}
			originalRoutine, originalDepth := core.ScopeSQLRoutine(s), core.ScopeActiveSQLRoutineDepth(s)
			for _, enabled := range []bool{false, true, false, true} {
				var inputSetting *string
				off := "off"
				if !enabled {
					inputSetting = &off
				}
				if _, err := core.EngineApplySetting(e, s.Op, s.Op.Session.Settings, setting, inputSetting, false); err != nil {
					t.Fatal(err)
				}
				value, err := core.ScopeInvokePLRoutine(s, d, input, read, sqltype.CollationRef{})
				want := tc.code
				if !enabled {
					want = ""
				}
				if want == "" {
					if err != nil {
						t.Fatal(err)
					}
					if n, err := value.Integer(); err != nil || n != 7 {
						t.Fatal("assert changed return", n, err)
					}
				} else {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != want {
						t.Fatal("reached assertion diagnostic", want, err)
					}
				}
				if core.ScopeActiveSQLRoutineDepth(s) != originalDepth || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != core.RoutineDemandLexical(&originalRoutine) {
					t.Fatal("assert leaked lexical activation or depth")
				}
			}
		})
	}
}

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
