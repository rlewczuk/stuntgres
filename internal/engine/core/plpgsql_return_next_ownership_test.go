package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func returnNextOwnerEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	for _, sql := range []string{
		"CREATE TYPE next_owner_row AS (id integer)",
		"CREATE FUNCTION next_owner_named() RETURNS SETOF next_owner_row LANGUAGE plpgsql AS $$ DECLARE r next_owner_row; BEGIN SELECT 1 INTO r; RETURN NEXT r; SELECT 2 INTO r; RETURN NEXT r; END $$",
		"CREATE FUNCTION next_owner_record() RETURNS SETOF next_owner_row LANGUAGE plpgsql AS $$ DECLARE r record; BEGIN SELECT 1 AS other_name INTO r; RETURN NEXT r; SELECT 2 AS other_name INTO r; RETURN NEXT r; END $$",
		"CREATE FUNCTION next_owner_empty() RETURNS SETOF next_owner_row LANGUAGE plpgsql AS $$ BEGIN RETURN; END $$",
		"CREATE FUNCTION next_owner_fail() RETURNS SETOF next_owner_row LANGUAGE plpgsql AS $$ DECLARE r next_owner_row; BEGIN SELECT 1 INTO r; RETURN NEXT r; PERFORM 1/0; END $$",
	} {
		enginetest.Run(t, e, sql)
	}
	return e
}

// Each scope owns a real transaction, operation and prepared executable. No SQL
// result transport or substitute interpreter is used to inspect its private sink.
func returnNextOwnerScope(t *testing.T, e *core.Engine, name string) (*core.StatementScope, *catalog.Procedure, catalog.Read, func()) {
	t.Helper()
	sess := enginetest.StartSession(t, e, nil)
	op := enginetest.TransactionOperation(t, sess)
	ref, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	s, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	s.TxRef = ref
	core.SetScopeWorked(s, true)
	read := e.CatalogRead()
	d := checkedDefaultOwnerProcedure(t, read, name)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err == nil {
		d, err = e.PreparePLRoutineLocked(op, auth, d)
	}
	e.Mu.Unlock()
	if err != nil {
		_ = s.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	return s, d, read, func() { _ = s.Finish(errors.New("ownership cleanup")); op.Done() }
}

func returnNextOwnerRows(t *testing.T, rows []sqltype.Value) {
	t.Helper()
	if len(rows) != 2 {
		t.Fatalf("private sink length = %d, want 2", len(rows))
	}
	for i, row := range rows {
		if row.CompositeLen() != 1 {
			t.Fatal("lost composite payload", row)
		}
		n, err := row.CompositeField(0).Integer()
		if err != nil || n != int64(i+1) {
			t.Fatal("retained row changed", i, n, err)
		}
	}
}

func TestPLpgSQLReturnNextOwnership(t *testing.T) {
	for _, name := range []string{"next_owner_named", "next_owner_record"} {
		t.Run(name, func(t *testing.T) {
			e := returnNextOwnerEngine(t)
			s, d, read, done := returnNextOwnerScope(t, e, name)
			defer done()
			program := d.SQL.PL
			body := program.Root.Statements[0].Block
			variables := slices.Clone(program.Variables)
			instructions := slices.Clone(body.Statements)
			preparedUnchanged := returnNextAcceptedOwner(t, e, name)
			input := proc.NewFrame(nil)
			defer input.Close()
			var first, second []sqltype.Value
			for _, sink := range []*[]sqltype.Value{&first, &second} {
				if _, err := core.ScopeInvokePLRoutineSet(s, d, input, read, sqltype.CollationRef{}, sink); err != nil {
					t.Fatal(err)
				}
				returnNextOwnerRows(t, *sink)
			}
			if &first[0] == &second[0] {
				t.Fatal("invocations share accumulator backing")
			}
			retained := first[0]
			// Invoke only the actual compiled NEXT instruction with two successive
			// values in its actual lexical slot, retaining the first payload handle.
			frame := proc.NewPLFrame(program, d.SQL, input, sqltype.CollationRef{})
			defer frame.Close()
			frame.Enter(program.Root)
			defer frame.Leave(program.Root)
			frame.Enter(body)
			defer frame.Leave(body)
			run := core.ScopeNewPLRun(s, d, frame, read)
			var appended []sqltype.Value
			run.Values = &appended
			slot := body.Bindings["r"]
			for _, index := range []int{1, 3} {
				instruction := body.Statements[index]
				if instruction.ReturnNext == nil || instruction.ReturnNext.Slot != slot || instruction.Syntax.ReturnNext == nil || instruction.Syntax != &body.Syntax.Statements[index] {
					t.Fatal("NEXT lost checked lexical slot or immutable source owner")
				}
			}
			shape := d.SQL.Result.Type
			if program.Variables[slot].Record {
				var err error
				shape, err = sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "other_name", Usage: shape.CompositeMember(0).Usage}})
				if err != nil {
					t.Fatal(err)
				}
			}
			integer := shape.CompositeMember(0).Usage.Type
			var old sqltype.Value
			for i := int64(1); i <= 2; i++ {
				field, err := sqltype.Integer(integer, i)
				if err != nil {
					t.Fatal(err)
				}
				value, err := sqltype.CompositeValue(shape, []sqltype.Value{field})
				if err != nil {
					t.Fatal(err)
				}
				frame.Values[slot] = value
				_, flow, err := run.Statements(body.Statements[1:2])
				if err != nil || flow.Kind != proc.PLNormal || len(appended) != int(i) {
					t.Fatal("NEXT did not append and continue", flow, err)
				}
				if i == 1 {
					old = appended[0]
					if !program.Variables[slot].Record && old != value {
						t.Fatal("matching named descriptor unnecessarily replaced immutable payload")
					}
				} else if appended[0] != old {
					t.Fatal("slot replacement replaced retained earlier backing")
				}
			}
			returnNextOwnerRows(t, appended)
			returnNextOwnerRows(t, first)
			if first[0] != retained || d.SQL.PL != program || program.Root.Statements[0].Block != body || !reflect.DeepEqual(program.Variables, variables) || !reflect.DeepEqual(body.Statements, instructions) {
				t.Fatal("invocation changed published instruction/variable owner or old result backing")
			}
			preparedUnchanged()
		})
	}
}

// Cancel deterministically only after an actual append; this checks the ordinary
// context error identity without sleeps, SQL cancellation, or a second executor.
type returnNextOwnerCancel struct {
	context.Context
	cancel context.CancelFunc
	values *[]sqltype.Value
}

func (c returnNextOwnerCancel) Err() error {
	if len(*c.values) != 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPLpgSQLReturnNextOwnershipFailureAndCancel(t *testing.T) {
	e := returnNextOwnerEngine(t)
	for _, canceled := range []bool{false, true} {
		name := "next_owner_fail"
		if canceled {
			name = "next_owner_named"
		}
		s, d, read, done := returnNextOwnerScope(t, e, name)
		input := proc.NewFrame(nil)
		var candidate []sqltype.Value
		original := s.Op.Ctx
		ctx, cancel := context.WithCancel(s.Op.Context())
		if canceled {
			s.Op.Ctx = returnNextOwnerCancel{Context: ctx, cancel: cancel, values: &candidate}
		}
		_, err := core.ScopeInvokePLRoutineSet(s, d, input, read, sqltype.CollationRef{}, &candidate)
		cancel()
		s.Op.Ctx = original
		if err == nil || len(candidate) != 1 || canceled && !errors.Is(err, context.Canceled) {
			t.Fatal("candidate error lost private prefix/cancellation identity", canceled, candidate, err)
		}
		old := candidate[0]
		if core.ScopeActiveSQLRoutineDepth(s) != 0 || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != nil {
			t.Fatal("failed invocation retained activation/frame")
		}
		// Reuse the scope and input with an independent sink: no abandoned prefix.
		good := checkedDefaultOwnerProcedure(t, read, "next_owner_named")
		var next []sqltype.Value
		if _, err := core.ScopeInvokePLRoutineSet(s, good, input, read, sqltype.CollationRef{}, &next); err != nil {
			t.Fatal(err)
		}
		returnNextOwnerRows(t, next)
		if candidate[0] != old || &candidate[0] == &next[0] {
			t.Fatal("new invocation adopted or mutated abandoned sink")
		}
		empty := checkedDefaultOwnerProcedure(t, read, "next_owner_empty")
		var zero []sqltype.Value
		if _, err := core.ScopeInvokePLRoutineSet(s, empty, input, read, sqltype.CollationRef{}, &zero); err != nil || len(zero) != 0 {
			t.Fatal("empty invocation inherited prior sink", zero, err)
		}
		input.Close()
		if canceled {
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary cancellation acquired SQLSTATE", err)
			}
		}
		done()
	}
	result, err := enginetest.Execute(e, t.Context(), enginetest.Statement(t, "SELECT * FROM next_owner_fail()"), nil)
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("uncaught failure published a public cursor")
	}
	if err == nil {
		t.Fatal("uncaught candidate unexpectedly succeeded")
	}
}

func TestPLpgSQLReturnNextOwnershipRestore(t *testing.T) {
	source := returnNextOwnerEngine(t)
	s, original, sourceRead, done := returnNextOwnerScope(t, source, "next_owner_named")
	input := proc.NewFrame(nil)
	var retained []sqltype.Value
	if _, err := core.ScopeInvokePLRoutineSet(s, original, input, sourceRead, sqltype.CollationRef{}, &retained); err != nil {
		t.Fatal(err)
	}
	input.Close()
	done()
	old := retained[0]
	variables := slices.Clone(original.SQL.PL.Variables)
	instructions := slices.Clone(original.SQL.PL.Root.Statements[0].Block.Statements)
	var stream bytes.Buffer
	if err := engine.Dump(t.Context(), source, &stream); err != nil {
		t.Fatal(err)
	}
	destination := returnNextOwnerEngine(t)
	previous := checkedDefaultOwnerProcedure(t, destination.CatalogRead(), "next_owner_named")
	for range 2 {
		if err := engine.Restore(t.Context(), destination, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatal(err)
		}
		s, next, view, done := returnNextOwnerScope(t, destination, "next_owner_named")
		if next == previous || next == original || next.SQL == original.SQL || next.SQL.Result.Type == original.SQL.Result.Type || next.SQL.Result.Type == previous.SQL.Result.Type {
			t.Fatal("restored executable borrowed source/prior descriptor generation")
		}
		for _, variable := range next.SQL.PL.Variables {
			if variable.Name == "r" && variable.Usage.Type != next.SQL.Result.Type {
				t.Fatal("restored lexical row usage not owned by destination result generation")
			}
		}
		input := proc.NewFrame(nil)
		var rows []sqltype.Value
		_, err := core.ScopeInvokePLRoutineSet(s, next, input, view, sqltype.CollationRef{}, &rows)
		input.Close()
		done()
		if err != nil {
			t.Fatal(err)
		}
		returnNextOwnerRows(t, rows)
		returnNextOwnerRows(t, retained)
		if rows[0].Type() != next.SQL.Result.Type || retained[0] != old || retained[0].Type() != original.SQL.Result.Type || !reflect.DeepEqual(original.SQL.PL.Variables, variables) || !reflect.DeepEqual(original.SQL.PL.Root.Statements[0].Block.Statements, instructions) {
			t.Fatal("restore mutated old backing or failed to reown new result")
		}
		if sourceRead.Get(original.Address()).(*catalog.Procedure).SQL.Result.Type != original.SQL.Result.Type {
			t.Fatal("restore modified retained source catalog")
		}
		previous = next
	}
}

// Retain the real registry's accepted graph and folds, not a reconstructed
// description, while invocations replace lexical slots and accumulator backing.
func returnNextAcceptedOwner(t *testing.T, e *core.Engine, name string) func() {
	t.Helper()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "BEGIN")
	t.Cleanup(func() { enginetest.SessionSQL(t, session, "ROLLBACK") })
	cycle := session.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, session, cycle, fn)
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("next_owned", "SELECT COALESCE(-(7::integer),0) AS folded, r.id FROM "+name+"() AS r", nil)
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("next_before", "next_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	registry := core.PayloadOf(session).PreparedSQL
	definition := registry.Lookup("next_owned")
	analysis, owner := definition.Description.Analysis, definition.Description.Folds
	portal := registry.Binding("next_before")
	if analysis == nil || owner == nil || portal.Resource.Root == nil || portal.Folds == nil {
		t.Fatal("missing accepted prepared owners")
	}
	graph, group, accepted := portal.Resource.Root, *portal.Folds, owner.Current()
	if len(accepted.Entries) == 0 || len(group.Entries) == 0 {
		t.Fatal("missing distinguishing accepted fold backing")
	}
	entries := slices.Clone(accepted.Entries)
	dependencies := slices.Clone(analysis.Dependencies)
	return func() {
		t.Helper()
		if definition.Description.Analysis != analysis || definition.Description.Folds != owner || portal.Resource.Root != graph || portal.Folds.Generation != group.Generation || &portal.Folds.Entries[0] != &group.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(accepted.Entries, entries) || !reflect.DeepEqual(analysis.Dependencies, dependencies) {
			t.Fatal("invocation replaced or mutated accepted prepared backing")
		}
	}
}
