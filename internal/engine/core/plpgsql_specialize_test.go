package core

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// No composed statement dispatcher is needed: bare RETURN exercises the real PL
// compiler, frame and output exporter without issuing a SQL statement. Observing
// the actual private session cell here avoids a second cache or inspection API.
func TestPLpgSQLProcedureInvocationSpecialization(t *testing.T) {
	e, err := NewEngine(nil, nil, "postgres", false)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := e.ResolveLogin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	admission, err := e.AdmitDatabase(context.Background(), principal, DatabaseSelector{})
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Release()
	s, err := e.StartSession(context.Background(), admission, session.SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if err := scope.Gate(&parser.Call{}); err != nil {
		t.Fatal(err)
	}
	read := e.CatalogRead()
	usage := func(id sqltype.ID) sqltype.Usage {
		typ, ok := read.LookupType(id)
		if !ok {
			t.Fatal("missing seeded type", id)
		}
		return typ.Usage()
	}
	language, ok := read.LookupName(ids.NameKey{ClassID: ids.LanguageClassID, Name: "plpgsql"})
	if !ok {
		t.Fatal("missing seeded PL language")
	}
	compile := func(d *catalog.Procedure) {
		t.Helper()
		e.Mu.Lock()
		auth, err := scope.RoutineAuthorizationLocked()
		if err == nil {
			err = e.CompilePLRoutineLocked(op, auth, d)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	newDeclaration := func(formals []proc.Formal) *catalog.Procedure {
		t.Helper()
		d := &proc.Definition{Procedure: true, Kind: proc.PLpgSQLBody, Name: "private_projection"}
		if err := d.SetFormals(formals); err != nil {
			t.Fatal(err)
		}
		d.Source = `DECLARE "copy" answer%TYPE; transitive "copy"%TYPE; inputcopy ` + d.Formals[d.InputFormals[0]].Name + "%TYPE; inputtransitive inputcopy%TYPE; BEGIN RETURN; END"
		tuple, err := d.OutputTuple()
		if err != nil {
			t.Fatal(err)
		}
		d.Result = tuple.Usage()
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, ObjectName: d.Name, RoutineKind: catalog.ProcedureRoutine, Language: language, Result: 2249, SQL: d}
		compile(p)
		return p
	}
	invoke := func(d *catalog.Procedure, signature proc.InvocationSignature, values []sqltype.Value, collation sqltype.CollationRef) *catalog.Procedure {
		t.Helper()
		if !signature.ValidFor(d.SQL) {
			t.Fatal("unchecked invocation fixture")
		}
		input := proc.NewFrame(signature.Inputs)
		defer input.Close()
		for i, in := range signature.Inputs {
			value := sqltype.Null(in.Type)
			if values != nil {
				value = values[i]
			}
			if err := input.Bind(i, value, in); err != nil {
				t.Fatal(err)
			}
		}
		before := slices.Clone(signature.Inputs)
		value, err := scope.invokePLRoutineSpecialized(d, input, read, collation, signature.Result, nil)
		if err != nil {
			t.Fatal(err)
		}
		if value.Type() != signature.Result.Type || !slices.Equal(signature.Inputs, before) || !slices.Equal(input.Inputs, before) {
			t.Fatal("invocation changed caller signature or output descriptor")
		}
		current := PayloadOf(s).sqlRoutines[d.Address()].plSpecialized
		if current == nil || current == d || current.SQL == d.SQL || current.SQL.PL == d.SQL.PL || &current.SQL.Formals[0] == &d.SQL.Formals[0] {
			t.Fatal("compiler mutated published backing")
		}
		if err := current.SQL.PL.ValidateDefinition(current.SQL); err != nil {
			t.Fatal(err)
		}
		for i, index := range current.SQL.OutputFormals {
			member := signature.Result.Type.CompositeMember(i)
			if current.SQL.Formals[index].Usage != member.Usage || current.SQL.PL.Variables[index].Usage != member.Usage || current.SQL.PL.Variables[index].Record != (member.Usage.Type.ID() == 2249) {
				t.Fatal("OUT slot is not concrete invocation member", i)
			}
		}
		body := current.SQL.PL.Root.Statements[0].Block
		for _, name := range []string{"copy", "transitive"} {
			if current.SQL.PL.Variables[body.Bindings[name]].Usage != signature.Result.Type.CompositeMember(0).Usage {
				t.Fatal("transitive TYPE copy lost member", name)
			}
		}
		for i, index := range current.SQL.InputFormals {
			if current.SQL.PL.Variables[index].Usage != current.SQL.Inputs[i] {
				t.Fatal("input slot lost compilation signature", i)
			}
		}
		for _, name := range []string{"inputcopy", "inputtransitive"} {
			if current.SQL.PL.Variables[body.Bindings[name]].Usage != current.SQL.Inputs[0] {
				t.Fatal("transitive input TYPE copy lost actual shape", name)
			}
		}
		if _, ok := current.SQL.PL.Root.Bindings["$0"]; ok {
			t.Fatal("procedure acquired scalar result placeholder")
		}
		return current
	}
	for _, tc := range []struct {
		name, mode string
		pseudo     sqltype.ID
		actual     []sqltype.ID
	}{
		{"sole", "i", 2283, []sqltype.ID{sqltype.Int4, sqltype.Text}},
		{"multiple", "i", 2283, []sqltype.ID{sqltype.Int4, sqltype.Text}},
		{"inout", "b", 2283, []sqltype.ID{sqltype.Int4, sqltype.Text}},
		{"array", "i", 2277, []sqltype.ID{1007, 1009}},
		{"array_multiple", "i", 2277, []sqltype.ID{1007, 1009}},
		{"array_inout", "b", 2277, []sqltype.ID{1007, 1009}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			formals := []proc.Formal{{Mode: tc.mode, Name: "x", Usage: usage(tc.pseudo)}, {Mode: "o", Name: "answer", Usage: usage(tc.pseudo)}}
			if tc.name == "multiple" || tc.name == "array_multiple" {
				formals = append(formals, proc.Formal{Mode: "o", Name: "other", Usage: usage(tc.pseudo)})
			}
			// The first output must be the one copied by the local declarations.
			if tc.mode == "b" {
				formals[0].Name = "answer"
				formals[1].Name = "other"
			}
			d := newDeclaration(formals)
			published, original := d.SQL.PL, slices.Clone(d.SQL.Formals)
			signature := func(id sqltype.ID) proc.InvocationSignature {
				result, err := d.SQL.ResolveResult(func(sqltype.Usage) sqltype.Usage { return usage(id) })
				if err != nil {
					t.Fatal(err)
				}
				return proc.InvocationSignature{Inputs: []sqltype.Usage{usage(id)}, Result: result}
			}
			integer, text := signature(tc.actual[0]), signature(tc.actual[1])
			first := invoke(d, integer, nil, sqltype.CollationRef{})
			if again := invoke(d, integer, nil, sqltype.CollationRef{}); again != first {
				t.Fatal("identical signature did not reuse actual cache entry")
			}
			second := invoke(d, text, nil, sqltype.CollationRef{})
			if second == first || invoke(d, integer, nil, sqltype.CollationRef{}) == first {
				t.Fatal("last specialization did not replace entry")
			}
			if first.SQL.Result != integer.Result || first.SQL.PL.Variables[d.SQL.OutputFormals[0]].Usage != usage(tc.actual[0]) || d.SQL.PL != published || !slices.Equal(d.SQL.Formals, original) {
				t.Fatal("cache replacement mutated prior or published activation")
			}
			// An activation saved before the replacement remains executable.
			frame := proc.NewFrame(first.SQL.Inputs)
			defer frame.Close()
			if err := frame.Bind(0, sqltype.Null(first.SQL.Inputs[0].Type), first.SQL.Inputs[0]); err != nil {
				t.Fatal(err)
			}
			if value, err := scope.invokePLRoutine(first, frame, read, sqltype.CollationRef{}, nil); err != nil || value.Type() != integer.Result.Type {
				t.Fatal("prior specialization no longer usable", err)
			}
			last := invoke(d, integer, nil, sqltype.CollationRef{})
			renamed := *d
			renamed.ObjectName = "renamed_projection"
			if invoke(&renamed, integer, nil, sqltype.CollationRef{}) == last {
				t.Fatal("live name omitted from key")
			}
			redefined := *d
			definition := *d.SQL
			redefined.SQL = &definition
			last = invoke(d, integer, nil, sqltype.CollationRef{})
			if invoke(&redefined, integer, nil, sqltype.CollationRef{}) == last {
				t.Fatal("definition identity omitted from key")
			}
			c, err := sqltype.RegisteredCollation(950, 950)
			if err != nil {
				t.Fatal(err)
			}
			base := invoke(d, integer, nil, sqltype.CollationRef{})
			if invoke(d, integer, nil, c) == base {
				t.Fatal("input collation omitted from key")
			}
			if tc.mode == "i" {
				textBase := invoke(d, text, nil, sqltype.CollationRef{})
				changed := text
				changed.Inputs = slices.Clone(text.Inputs)
				changed.Inputs[0].Collation = c
				if invoke(d, changed, nil, sqltype.CollationRef{}) == textBase {
					t.Fatal("input Usage collation omitted from signature key")
				}
			}
		})
	}
	t.Run("unchanged_definition", func(t *testing.T) {
		d := newDeclaration([]proc.Formal{{Mode: "i", Name: "x", Usage: usage(sqltype.Int4)}, {Mode: "o", Name: "answer", Usage: usage(sqltype.Int4)}})
		program := d.SQL.PL
		signature := proc.InvocationSignature{Inputs: d.SQL.Inputs, Result: d.SQL.Result}
		private := invoke(d, signature, nil, sqltype.CollationRef{})
		if private.SQL.Result != d.SQL.Result || d.SQL.PL != program || private.SQL.PolymorphicResult {
			t.Fatal("compilation-only ownership changed declaration")
		}
	})
	t.Run("result_shape", func(t *testing.T) {
		d := newDeclaration([]proc.Formal{{Mode: "i", Name: "x", Usage: usage(sqltype.Int4)}, {Mode: "o", Name: "answer", Usage: sqltype.DeferredRecord().Usage()}})
		var previous *catalog.Procedure
		for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Int4} {
			// Same-OID children distinguish descriptor generations independently
			// of the fixed concrete input.
			child := *usage(id).Type
			shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "field", Usage: child.Usage()}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := d.SQL.ResolveResult(func(sqltype.Usage) sqltype.Usage { return shape.Usage() })
			if err != nil {
				t.Fatal(err)
			}
			signature := proc.InvocationSignature{Inputs: d.SQL.Inputs, Result: result}
			current := invoke(d, signature, nil, sqltype.CollationRef{})
			if current == previous || current.SQL.Formals[1].Usage != shape.Usage() || current.SQL.Formals[1].Usage.Type.IsDeferredRecord() {
				t.Fatal("result member shape or generation lost")
			}
			if invoke(d, signature, nil, sqltype.CollationRef{}) != current {
				t.Fatal("identical shaped result did not reuse cache entry")
			}
			previous = current
		}
	})
	// RECORD transport carries the caller's descriptor unchanged; only the
	// compilation signature is unwrapped. Distinct shapes/generations must miss.
	t.Run("record_input", func(t *testing.T) {
		d := newDeclaration([]proc.Formal{{Mode: "i", Name: "x", Usage: usage(2249)}, {Mode: "o", Name: "answer", Usage: usage(sqltype.Int4)}})
		var previous *catalog.Procedure
		for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Int4} {
			shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "field", Usage: usage(id)}})
			if err != nil {
				t.Fatal(err)
			}
			row, err := sqltype.OwnedCompositeValue(shape, []sqltype.Value{sqltype.Null(usage(id).Type)})
			if err != nil {
				t.Fatal(err)
			}
			transport := sqltype.DeferredRecord().Usage()
			wrapped, err := sqltype.RecordValue(transport.Type, row)
			if err != nil {
				t.Fatal(err)
			}
			signature := proc.InvocationSignature{Inputs: []sqltype.Usage{transport}, Result: d.SQL.Result}
			current := invoke(d, signature, []sqltype.Value{wrapped}, sqltype.CollationRef{})
			if current == previous || current.SQL.Inputs[0] != shape.Usage() || current.SQL.PL.Variables[0].Usage != shape.Usage() || signature.Inputs[0] != transport {
				t.Fatal("actual RECORD shape or cache distinction lost")
			}
			previous = current
		}
	})
}
