package core_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLCursorDiagnosticLedgerProvider(t *testing.T) {
	for _, tc := range []struct {
		name, declarations, statements, body string
		datum, forged                        int
		loop                                 bool
	}{
		{name: "omitted_argument_row", declarations: "c CURSOR(a integer) FOR SELECT a;", datum: 4, forged: 3},
		{name: "omitted_scalar_fetch_row", declarations: "c refcursor; x integer;", statements: "FETCH c INTO x;", datum: 4, forged: 3},
		{name: "spurious_record_fetch_row", declarations: "c refcursor; r record;", statements: "FETCH c INTO r;", datum: 3, forged: 4},
		{name: "omitted_query_field", declarations: "c refcursor; r record;", statements: "OPEN c FOR SELECT r.f1+r.f1;", datum: 4, forged: 3},
		{name: "wrong_outer_record_parent", body: "DECLARE r record; c CURSOR(a integer) FOR SELECT a AS f1; BEGIN FOR r IN c(r.f1) LOOP CASE 2 WHEN true THEN RETURN r.f1; ELSE RETURN 2; END CASE; END LOOP; RETURN 0; END", datum: 8, forged: 7, loop: true},
		{name: "coherent_width_change", declarations: "c refcursor; r record;", statements: "FETCH c INTO r.f1,r.f2,r.f3,r.f4,r.f5,r.f6;", datum: 10, forged: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope := plUnitScope(t)
			body := tc.body
			if body == "" {
				body = "DECLARE " + tc.declarations + " BEGIN " + tc.statements + " CASE 2 WHEN true THEN RETURN 1; ELSE RETURN 2; END CASE; END"
			}
			source := cursorOwnerRoutine(t, e, scope, body)
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
				t.Fatal("canonical ledger rejected", err)
			}
			variables := slices.Clone(source.SQL.PL.Variables)
			candidate, definition, program := *source, *source.SQL, *source.SQL.PL
			root := *program.Root
			root.Statements = slices.Clone(root.Statements)
			block := *root.Statements[0].Block
			block.Statements = slices.Clone(block.Statements)
			root.Statements[0].Block = &block
			program.Root, definition.PL, candidate.SQL = &root, &program, &definition
			var instruction *proc.PLInstruction
			if tc.loop {
				cursor := *block.Statements[0].Cursor
				loop := *cursor.Loop
				loop.Statements = slices.Clone(loop.Statements)
				loopBody := *loop.Statements[0].Block
				loopBody.Statements = slices.Clone(loopBody.Statements)
				block.Statements[0].Cursor, cursor.Loop, loop.Statements[0].Block = &cursor, &loop, &loopBody
				instruction = &loopBody.Statements[0]
			} else {
				instruction = &block.Statements[len(block.Statements)-1]
			}
			original := instruction.Case
			conditional := *original
			conditional.Arms = slices.Clone(conditional.Arms)
			instruction.Case = &conditional
			want := fmt.Sprintf("__Case__Variable_%d__", tc.datum)
			if original.SearchName != want {
				t.Fatal("independent datum expectation", original.SearchName, want)
			}
			conditional.SearchName = fmt.Sprintf("__Case__Variable_%d__", tc.forged)
			delta := len(conditional.SearchName) - len(original.SearchName)
			for i := range conditional.Arms {
				arm := &conditional.Arms[i]
				arm.DiagnosticQuery = strings.Replace(arm.DiagnosticQuery, original.SearchName, conditional.SearchName, 1)
				arm.DiagnosticOperator += delta
				arm.DiagnosticItems += delta
			}
			if err := provider.Validate(view, &candidate); err == nil {
				t.Fatal("coherent cursor ledger forgery admitted by provider")
			}
			if program.SearchCount != source.SQL.PL.SearchCount || conditional.SearchSlot != original.SearchSlot || !reflect.DeepEqual(program.Variables, variables) {
				t.Fatal("display-only forgery changed executable layout/full Usage")
			}
			if original.SearchName != want || source.SQL.PL.SameLayout(&program) || !reflect.DeepEqual(source.SQL.PL.Variables, variables) {
				t.Fatal("private candidate altered original identity or typed metadata")
			}
			if err := provider.Validate(view, source); err != nil {
				t.Fatal("forgery corrupted published source", err)
			}
		})
	}
}

func plUnitScope(t *testing.T) (*engine.Engine, *engine.StatementScope) {
	t.Helper()
	e := engine.New()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	return e, scope
}

func cursorOwnerRoutine(t *testing.T, e *engine.Engine, s *engine.StatementScope, source string) *catalog.Procedure {
	t.Helper()
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, proc.PLReturnPolicy(integer.Usage(), 0, false))
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{ObjectName: "cursor_owner_unit", Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "cursor_owner_unit", Source: source, Result: integer.Usage(), PLSyntax: raw}}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = e.CompilePLRoutineLocked(s.Op, auth, d)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
