package core_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTriggerBeforeStatementQueryOwnership(t *testing.T) {
	t.Run("actual_cte_swap", beforeStatementCTEOwner)
	e, s, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	p := triggerUnitDefinition(t, read, relation, d, "before", stored.TriggerInsert|stored.TriggerUpdate).Parts()
	p.Level = stored.TriggerStatement
	// A disabled first producer must still own the relation/event opportunity.
	p.Mode = 'D'
	before, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Name = "queued"
	p.OID++
	p.Timing = stored.TriggerAfter
	p.Mode = 'O'
	after, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, relation, before, after)
	triggerUnitAdmit(t, s, relation)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	h := s.WriteHost()
	if err := h.AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.BeforeStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	first := core.ScopeBeforeTestMap(s)
	if len(first) != 1 {
		t.Fatal("disabled first producer did not mark query", first)
	}
	var key core.BeforeStatementTestKey
	for k := range first {
		key = k
	}
	lexical := s.Command
	s.Command = dml.Command{}
	if err := h.BeforeStatement(ref, stored.TriggerInsert, []int32{999}); err != nil {
		t.Fatal(err)
	}
	if len(core.ScopeBeforeTestMap(s)) != 1 {
		t.Fatal("lexical mutation swapped query ownership")
	}
	delete(first, key)
	if len(core.ScopeBeforeTestMap(s)) != 0 {
		t.Fatal("lexical scratch selected a copied fired map")
	}
	first[key] = struct{}{}
	s.Command = lexical
	mark, err := s.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.BeforeStatement(ref, stored.TriggerUpdate, []int32{relation.Attributes[1]}); err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatal("different event was deduplicated")
	}
	finish, err := s.BeginRoutineCommand()
	if err != nil {
		t.Fatal(err)
	}
	nested, serial := core.ScopeEffectTestIDs(s)
	if err := h.BeforeStatement(ref, stored.TriggerInsert, nil); err != nil {
		t.Fatal(err)
	}
	child := core.ScopeBeforeTestMap(s)
	if len(child) != 1 {
		t.Fatal("nested query did not get fresh fired state")
	}
	delete(child, key)
	if len(first) != 2 {
		t.Fatal("nested query shares parent backing")
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := core.ScopeAfterTestFrames(s)[nested]; ok {
		t.Fatal("nested completion retained frame")
	}
	if f := core.ScopeAfterTestFrames(s)[0]; f.Statements != 1 || f.StatementCursor != 0 {
		t.Fatal("nested retirement drained parent AFTER", f)
	}
	if err := s.EndPLException(mark, true, errors.New("caught suffix")); err != nil {
		t.Fatal(err)
	}
	restored := core.ScopeBeforeTestMap(s)
	if len(restored) != 1 {
		t.Fatal("checkpoint lost saved relation/event state")
	}
	delete(first, key)
	if len(restored) != 1 {
		t.Fatal("checkpoint shares mutated live map backing")
	}
	_, now := core.ScopeEffectTestIDs(s)
	if now < serial {
		t.Fatal("checkpoint rewound effect serial")
	}
	if err := s.Finish(errors.New("unit veto")); err == nil {
		t.Fatal("missing finish veto")
	}
	if len(core.ScopeAfterTestFrames(s)) != 0 {
		t.Fatal("Finish retained BEFORE state")
	}
}

func TestTriggerBeforeStatementNativeProtocol(t *testing.T) {
	if err := core.BeforeStatementNativeTest(dml.BeforeResult{Proceed: false}, nil); err != nil {
		t.Fatal("nil tuple must not suppress or reject statement", err)
	}
	// Even a nonnil zero-width row denotes a returned native tuple pointer.
	for _, row := range [][]sqltype.Value{{}, make([]sqltype.Value, 1)} {
		err := core.BeforeStatementNativeTest(dml.BeforeResult{Row: row, Proceed: true}, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "39P01" || diagnostic.Message != "BEFORE STATEMENT trigger cannot return a value" {
			t.Fatal("native tuple protocol", err)
		}
	}
	primary := errors.New("original native helper rejection")
	if err := core.BeforeStatementNativeTest(dml.BeforeResult{Row: make([]sqltype.Value, 1)}, primary); err != primary {
		t.Fatal("protocol replaced helper error", err)
	}
}

type beforeStatementAdmissionProbe struct {
	context.Context
	admissions atomic.Int64
}

func (p *beforeStatementAdmissionProbe) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".(*statementHost).BeforeStatement") {
			p.admissions.Add(1)
			break
		}
		if !more {
			break
		}
	}
	return p.Context.Err()
}
func TestTriggerBeforeStatementFastPath(t *testing.T) {
	t.Run("truncate_authority", beforeStatementTruncateAuthority)
	for _, kind := range []string{"none", "row", "after", "other_event", "applicable"} {
		t.Run(kind, func(t *testing.T) {
			e, s, read, relation, d := triggerUnitFixture(t, "BEGIN PERFORM 1/0; RETURN NULL; END")
			if kind != "none" {
				p := triggerUnitDefinition(t, read, relation, d, "candidate", stored.TriggerUpdate).Parts()
				p.Level = stored.TriggerStatement
				switch kind {
				case "row":
					p.Level = stored.TriggerRow
				case "after":
					p.Timing = stored.TriggerAfter
					p.Events = stored.TriggerInsert
				case "other_event":
					p.Events = stored.TriggerInsert
				}
				definition, err := stored.NewTrigger(p)
				if err != nil {
					t.Fatal(err)
				}
				relation = triggerUnitInstall(t, e, relation, definition)
			}
			triggerUnitAdmit(t, s, relation)
			probe := &beforeStatementAdmissionProbe{Context: s.Op.Ctx}
			s.Op.Ctx = probe
			out, err := s.Execute(enginetest.Statement(t, "UPDATE public.trigger_unit_table SET v=v WHERE false"), 0, nil)
			if out.Rows != nil {
				out.Rows.Close()
				t.Fatal("unexpected returning rows")
			}
			if out.Count != 0 {
				t.Fatal("empty operation reported effects", out.Count)
			}
			if kind == "applicable" {
				var state interface{ SQLState() string }
				if !errors.As(err, &state) || state.SQLState() != "22012" || probe.admissions.Load() == 0 || len(core.ScopeBeforeTestMap(s)) != 1 {
					t.Fatal("applicable zero-row boundary did not execute body", err)
				}
			} else if err != nil || probe.admissions.Load() != 0 || core.ScopeBeforeTestMap(s) != nil || len(core.ScopeAfterTestFrames(s)) != 0 || len(s.BeforeCaptures) != 0 || len(s.BeforeTargets) != 0 {
				t.Fatal("irrelevant path admitted or allocated statement state", kind, err)
			}
		})
	}
}
