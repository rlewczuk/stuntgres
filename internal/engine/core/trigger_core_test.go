package core_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func triggerUnitAdmit(t *testing.T, s *core.StatementScope, relation *catalog.Relation) {
	t.Helper()
	statement := &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: relation.Name()}}}}
	if err := s.Gate(statement); err != nil {
		t.Fatal(err)
	}
	if err := core.ScopeAcquireRelationTarget(s, statement, statement, true, false); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerCoreCatalog(t *testing.T) {
	e, _, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	a := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerInsert|stored.TriggerUpdate)
	p := a.Parts()
	p.Name = "z"
	p.OID++
	p.Arguments = []string{"3"}
	p.UpdateColumns = []int32{2}
	column := relation.Address()
	column.SubID = 2
	p.References = append(p.References, column)
	z, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Arguments[0] = "99"
	p.UpdateColumns[0] = 1
	if z.Parts().Arguments[0] != "3" || z.Parts().UpdateColumns[0] != 2 {
		t.Fatal("constructor retained mutable input")
	}
	original := triggerUnitInstall(t, e, relation, a, z)
	if err := original.Triggers.Validate(e.CatalogRead(), original); err != nil {
		t.Fatal(err)
	}
	if z.Fires(stored.TriggerUpdate, []int32{1}) || !z.Fires(stored.TriggerUpdate, []int32{2}) || !z.Fires(stored.TriggerInsert, nil) || z.Fires(stored.TriggerDelete, nil) {
		t.Fatal("event/UPDATE OF contract")
	}
	if err := stored.TriggersPayload([]stored.Trigger{z, a}).Validate(read, original); err == nil {
		t.Fatal("unordered names accepted")
	}
	if err := stored.TriggersPayload([]stored.Trigger{a, a}).Validate(read, original); err == nil {
		t.Fatal("duplicate names accepted")
	}
	changed := *original
	changed.Triggers = stored.TriggersPayload([]stored.Trigger{z})
	if len(stored.TriggersOf(original)) != 2 || len(stored.TriggersOf(&changed)) != 1 {
		t.Fatal("replacement mutated retained list")
	}
	if stored.TriggersPayload(nil) != nil || (&catalog.Relation{}).HasTriggers() {
		t.Fatal("nil fast path")
	}
	foreign := a.Parts()
	foreign.Relation.Address.ObjectID++
	bad, err := stored.NewTrigger(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.TriggersPayload([]stored.Trigger{bad}).Validate(read, relation); err == nil {
		t.Fatal("wrong parent accepted")
	}
	missing := a.Parts()
	missing.Procedure.Address.ObjectID++
	missing.References = []ids.ObjectAddress{missing.Procedure.Address}
	bad, err = stored.NewTrigger(missing)
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.TriggersPayload([]stored.Trigger{bad}).Validate(read, relation); err == nil {
		t.Fatal("missing function accepted")
	}
}

func TestTriggerCoreFrame(t *testing.T) {
	_, _, read, relation, d := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	integer, _ := read.LookupType(sqltype.Int4)
	boolean, _ := read.LookupType(sqltype.Bool)
	program := d.SQL.PL
	if program.FoundSlot != proc.TriggerSlotCount || len(program.Variables) != proc.TriggerSlotCount+1 || len(program.Root.Bindings) != proc.TriggerSlotCount {
		t.Fatal("canonical trigger prefix and single implicit FOUND layout")
	}
	for slot, name := range [...]string{"old", "new", "tg_name", "tg_when", "tg_level", "tg_op", "tg_relid", "tg_table_name", "tg_table_schema", "tg_relname", "tg_nargs", "tg_argv"} {
		bound, exists := program.Root.Bindings[name]
		if !exists || bound != slot || program.Variables[slot].Name != name || program.Variables[slot].Role != proc.PLOrdinarySlot {
			t.Fatal("trigger datum renumbered or replaced by FOUND", name, bound)
		}
	}
	found := program.Variables[program.FoundSlot]
	if found.Name != "found" || found.Role != proc.PLFoundSlot || found.Record || found.Usage != boolean.Usage() {
		t.Fatal("trigger FOUND lacks canonical BOOL ownership")
	}
	for _, name := range [][]string{{"found"}, {program.Function, "found"}} {
		if slot, exists := program.Lookup(name, []*proc.PLBlock{program.Root}); !exists || slot != program.FoundSlot {
			t.Fatal("trigger implicit FOUND lookup replaced canonical TG namespace", name)
		}
	}
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	rel, err := core.PLTriggerRelation(read, relation)
	if err != nil {
		t.Fatal(err)
	}
	f, err := proc.NewPLTriggerFrame(d.SQL, rel, "a", "INSERT", []string{"x", "7"}, nil, row)
	if err != nil {
		t.Fatal(err)
	}
	if d.SQL.PL.FormalCount != 0 || len(d.SQL.Formals) != 0 || d.SQL.PL.Invocation != proc.DMLTriggerInvocation || !f.Values[0].IsNull() || f.Values[0].Type() != f.Values[1].Type() {
		t.Fatal("trigger/formal or absent-record contract")
	}
	if len(f.Values[11].ArrayDimensions()) != 1 || f.Values[11].ArrayDimensions()[0].Lower != 0 {
		t.Fatal("TG_ARGV lower bound")
	}
	for i, want := range []string{"x", "7"} {
		got, err := f.Values[11].ArrayElement(i).Text()
		if err != nil || got != want {
			t.Fatal(got, err)
		}
	}
	initialFound, err := f.Values[program.FoundSlot].Boolean()
	if err != nil || initialFound {
		t.Fatal("trigger FOUND did not start false", err)
	}
	f.SetFound(true)
	retained := f.Values[1]
	row[1] = triggerUnitInt(t, integer, 99)
	f.Close()
	got, err := retained.CompositeField(1).Integer()
	if err != nil || got != 5 {
		t.Fatal("frame borrowed producer scratch or erased returned composite", got, err)
	}
	noargs, err := proc.NewPLTriggerFrame(d.SQL, rel, "b", "DELETE", nil, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer noargs.Close()
	if !noargs.Values[1].IsNull() || !noargs.Values[11].IsNull() {
		t.Fatal("absent NEW or noargs TG_ARGV lost typed NULL")
	}
	count, err := noargs.Values[10].Integer()
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	nextFound, err := noargs.Values[program.FoundSlot].Boolean()
	if err != nil || nextFound || noargs.Values[program.FoundSlot].Type() != boolean {
		t.Fatal("trigger FOUND leaked across invocations", err)
	}
	broken := *d.SQL.PL
	broken.Variables = append([]proc.PLVariable(nil), broken.Variables...)
	broken.Variables[10].Usage = integer.Usage()
	broken.Variables[2].Usage = integer.Usage()
	definition := *d.SQL
	definition.PL = &broken
	if err := broken.ValidateDefinition(&definition); err == nil {
		t.Fatal("forged implicit slot type accepted")
	}
	for _, kind := range []string{"redirect_found", "wrong_found_usage", "wrong_found_role", "found_in_canonical_map", "redirect_old", "trigger_flag"} {
		t.Run(kind, func(t *testing.T) {
			candidate := *program
			candidate.Variables = append([]proc.PLVariable(nil), program.Variables...)
			root := *program.Root
			root.Bindings = maps.Clone(root.Bindings)
			candidate.Root = &root
			switch kind {
			case "redirect_found":
				candidate.FoundSlot = root.Bindings["old"]
			case "wrong_found_usage":
				candidate.Variables[program.FoundSlot].Usage = integer.Usage()
			case "wrong_found_role":
				candidate.Variables[program.FoundSlot].Role = proc.PLOrdinarySlot
			case "found_in_canonical_map":
				root.Bindings["found"] = program.FoundSlot
			case "redirect_old":
				root.Bindings["old"] = root.Bindings["new"]
			case "trigger_flag":
				candidate.Invocation = proc.OrdinaryInvocation
			}
			definition := *d.SQL
			definition.PL = &candidate
			if err := candidate.ValidateDefinition(&definition); err == nil {
				t.Fatal("forged trigger/FOUND composition accepted")
			}
			if err := program.ValidateDefinition(d.SQL); err != nil {
				t.Fatal("fixture candidate mutated canonical trigger program", err)
			}
		})
	}
	t.Run("case_state_after_trigger_prefix", func(t *testing.T) {
		_, _, read, relation, routine := triggerUnitFixture(t, "BEGIN CASE 1 WHEN true THEN RETURN NEW; ELSE RETURN OLD; END CASE; END")
		program := routine.SQL.PL
		conditional := program.Root.Statements[0].Block.Statements[0].Case
		if program.SearchCount != 1 || conditional.SearchName != "__Case__Variable_13__" || conditional.SearchSlot < 0 || conditional.SearchSlot >= program.SearchCount {
			t.Fatal("trigger diagnostic count displaced native CASE search ownership")
		}
		rel, err := core.PLTriggerRelation(read, relation)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := proc.NewPLTriggerFrame(routine.SQL, rel, "case", "INSERT", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer frame.Close()
		if len(frame.Searches) != program.SearchCount || len(frame.Values) != proc.TriggerSlotCount+1 || frame.Searches[conditional.SearchSlot].Usage != (sqltype.Usage{}) || frame.Searches[conditional.SearchSlot].Value.Type() != nil {
			t.Fatal("CASE invocation state became a trigger datum or retained a value")
		}
		if slot, exists := program.SearchSlot(proc.PLSearchName(conditional.SearchSlot)); !exists || slot != conditional.SearchSlot {
			t.Fatal("trigger CASE search lost its native canonical index")
		}
		if err := program.ValidateDefinition(routine.SQL); err != nil {
			t.Fatal("canonical trigger CASE program rejected", err)
		}
	})
}

func TestTriggerCoreReturn(t *testing.T) {
	_, s, read, relation, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	shape, _ := read.LookupType(relation.RowType)
	integer, _ := read.LookupType(sqltype.Int4)
	text, _ := read.LookupType(sqltype.Text)
	one := triggerUnitInt(t, integer, 1)
	for _, tc := range []struct {
		name    string
		members []sqltype.CompositeMember
		values  []sqltype.Value
		detail  string
	}{
		{"width", []sqltype.CompositeMember{{Attribute: 1, Name: "other", Usage: integer.Usage()}}, []sqltype.Value{one}, "Number of returned columns (1) does not match expected column count (2)."},
		{"type", []sqltype.CompositeMember{{Attribute: 1, Name: "x", Usage: integer.Usage()}, {Attribute: 2, Name: "y", Usage: text.Usage()}}, []sqltype.Value{one, sqltype.String(text, "bad")}, "Returned type text does not match expected type integer in column \"v\" (position 2)."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rowtype, err := sqltype.RecordShape(tc.members)
			if err != nil {
				t.Fatal(err)
			}
			value, err := sqltype.OwnedCompositeValue(rowtype, tc.values)
			if err != nil {
				t.Fatal(err)
			}
			_, err = proc.TriggerReturn(shape, value)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "42804" || d.Detail != tc.detail {
				t.Fatal(err)
			}
			if tc.name == "width" {
				ordinary, err := core.ScopeConvertPLValue(s, value, shape.Usage())
				if err != nil || ordinary.CompositeLen() != 2 || !ordinary.CompositeField(1).IsNull() {
					t.Fatal("ordinary row assignment no longer NULL-fills", err)
				}
			}
		})
	}
	if _, err := proc.TriggerReturn(shape, one); err == nil {
		t.Fatal("scalar accepted")
	}
	value, err := proc.TriggerReturn(shape, sqltype.Null(integer))
	if err != nil || !value.IsNull() || value.Type() != shape {
		t.Fatal("whole NULL rejected", err)
	}
	other, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "different1", Usage: integer.Usage()}, {Attribute: 2, Name: "different2", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	value, err = sqltype.OwnedCompositeValue(other, []sqltype.Value{one, one})
	if err != nil {
		t.Fatal(err)
	}
	value, err = proc.TriggerReturn(shape, value)
	if err != nil || value.Type() != shape {
		t.Fatal("compatible positional record rejected", err)
	}
}

func TestTriggerCoreRuntime(t *testing.T) {
	e, s, read, relation, d := triggerUnitFixture(t, "BEGIN NEW.v := NEW.v+1; RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerInsert)
	relation = triggerUnitInstall(t, e, relation, definition)
	integer, _ := read.LookupType(sqltype.Int4)
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	request := dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}
	if _, err := s.WriteHost().BeforeRow(request); err == nil {
		t.Fatal("unadmitted trigger entry accepted")
	}
	triggerUnitAdmit(t, s, relation)
	result, err := s.WriteHost().BeforeRow(request)
	if err != nil || !result.Proceed {
		t.Fatal(err)
	}
	got, err := result.Row[1].Integer()
	if err != nil || got != 6 || core.ScopeActiveSQLRoutineDepth(s) != 0 {
		t.Fatal("unlocked invocation/depth", got, err)
	}
	if len(s.BeforeCaptures) != 1 {
		t.Fatal("command capture not retained")
	}
	result, err = s.WriteHost().BeforeRow(request)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = result.Row[1].Integer()
	if got != 6 || len(s.BeforeCaptures) != 1 {
		t.Fatal("frame leaked prior row or capture was lost")
	}
	// The PL expression above acquires Engine.mu through ordinary binding;
	// successful completion proves the callback was not run under that mutex.
	if !e.Mu.TryLock() {
		t.Fatal("trigger retained engine mutex")
	}
	e.Mu.Unlock()
	input := proc.NewFrame(nil)
	defer input.Close()
	if _, err := core.ScopeInvokeSQLRoutine(s, d, input, d.SQL.Result, nil, sqltype.CollationRef{}, false); err == nil {
		t.Fatal("ordinary call acquired trigger entry")
	}
}

func TestRoutineTriggerLiveAttributeCapture(t *testing.T) {
	e, s, read, relation, d := triggerUnitFixture(t, "BEGIN IF current_setting('application_name')='live-trigger' THEN NEW.v := NEW.v+10; ELSE NEW.v := NEW.v+1; END IF; RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, d, "live", stored.TriggerInsert)
	relation = triggerUnitInstall(t, e, relation, definition)
	triggerUnitAdmit(t, s, relation)
	if err := core.ScopeSnapshotDataPhase(s); err != nil {
		t.Fatal(err)
	}
	leavePolicy := core.ActivateRoutineCommandPolicy(s, &catalog.Procedure{Volatility: expr.Stable})
	defer leavePolicy()
	callerDemand := core.ScopeSQLRoutine(s)
	integer, _ := read.LookupType(sqltype.Int4)
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	request := dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}
	first, err := s.WriteHost().BeforeRow(request)
	if err != nil || !first.Proceed {
		t.Fatal(err)
	}
	oldCapture := s.BeforeCaptures[dml.BeforeKey{Relation: ref, Event: dml.BeforeInsert}]
	changed := *d
	changed.ObjectRevision++
	changed.Strict = true
	changed.Configuration = catalog.NewRoutineConfiguration([]string{"application_name=live-trigger"})
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(d.Address(), &changed)
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	second, err := s.WriteHost().BeforeRow(request)
	if err != nil || !second.Proceed {
		t.Fatal(err)
	}
	got, _ := second.Row[1].Integer()
	retained, _ := first.Row[1].Integer()
	scratch, _ := row[1].Integer()
	capture := s.BeforeCaptures[dml.BeforeKey{Relation: ref, Event: dml.BeforeInsert}]
	if got != 15 || retained != 6 || scratch != 5 || capture.Relation != oldCapture.Relation || capture.Catalog != oldCapture.Catalog || capture.Catalog.Get(d.Address()) != d || d.Configuration != nil {
		t.Fatal("firing lost live attributes, retained relation roots or immutable row backing", got, retained, scratch)
	}
	if !core.SameSQLRoutineDemand(callerDemand, core.ScopeSQLRoutine(s)) {
		t.Fatal("firing replaced caller frame/diagnostic or coherent policy backing")
	}
	if core.ScopeActiveSQLRoutineDepth(s) != 0 || s.Op.EffectiveUser != nil {
		t.Fatal("live revision leaked firing depth/actor")
	}
}

func TestTriggerCoreAtomicStage(t *testing.T) {
	for _, phase := range []string{"finish_veto", "cancel", "close_veto"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e, s, read, relation, d := triggerUnitFixtureContext(t, ctx, "BEGIN RETURN NEW; END")
			definition := triggerUnitDefinition(t, read, relation, d, "a", stored.TriggerInsert)
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, s, relation)
			h := s.WriteHost()
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			integer, _ := read.LookupType(sqltype.Int4)
			row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
			binding := analyze.Binding{Ref: ref, Relation: relation, Targets: []int{0, 1}}
			dml.BeginInsertReturning(h, binding)
			if _, err := dml.BindUniqueWrite(h, ref); err != nil {
				t.Fatal(err)
			}
			veto := errors.New("trigger statement Finish veto")
			if phase == "close_veto" {
				e.Mu.Lock()
				source, err := e.CaptureQuerySourcesLocked(s, nil, true)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				defer source.Close()
				if err := exec.SelectQuerySources(ctx, source); err != nil {
					t.Fatal(err)
				}
				project := &plan.ProjectNode{QuerySchema: plan.QuerySchema(relation.Columns), Child: &plan.SingletonNode{}, Frame: 1, Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: row[0]}, plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: row[1]}}}
				state := &exec.State{Source: source, SourceMode: exec.SourceBorrowed}
				state.Input, state.Output = h.Environments()
				cursor, err := exec.OpenQueryProjectStage(ctx, state, project)
				if err != nil {
					t.Fatal(err)
				}
				closeVeto := errors.New("required trigger producer Close")
				finish := cursor.Finish
				cursor.Finish = func() error {
					if finish != nil {
						if err := finish(); err != nil {
							return err
						}
					}
					return closeVeto
				}
				_, veto = dml.AssembleInsertRows(ctx, binding, &cursor, -1, "public", state.Input, state.Output, h, false, nil, state)
				if !errors.Is(veto, closeVeto) {
					t.Fatal("required Close did not veto accepted trigger row", veto)
				}
			} else {
				accepted, stale, err := dml.AppendAcceptedInsert(h, ref, &index.WriteCandidate{Values: row})
				if err != nil || !accepted || stale {
					t.Fatal("real stage admission", accepted, stale, err)
				}
			}
			staged := s.Stage.Rows[relation.Address()]
			if len(staged) != 1 || staged[0].Row == 0 || staged[0].Version == 0 {
				t.Fatal("stage did not allocate genuine row/version identities")
			}
			if target, _ := e.Locks.ReplacementGrants(s.LockTicket, ref, nil); !target {
				t.Fatal("missing real target grant")
			}
			if phase == "cancel" {
				cancel()
				_, veto = h.BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row})
				if !errors.Is(veto, context.Canceled) {
					t.Fatal("canceled trigger entry", veto)
				}
			}
			if err := s.Finish(veto); !errors.Is(err, veto) {
				t.Fatal(err)
			}
			if len(e.Storage.Relations[relation.Address()].Versions) != 0 {
				t.Fatal("veto published accepted trigger row")
			}
			if _, err := h.BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err == nil {
				t.Fatal("finished scope admitted trigger")
			}
		})
	}
}

func triggerUnitDefinition(t *testing.T, read catalog.Read, relation *catalog.Relation, d *catalog.Procedure, name string, event stored.TriggerEvent) stored.Trigger {
	t.Helper()
	p := stored.TriggerParts{OID: 9500010, Name: name, Relation: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Procedure: read.Owner().Domain.Ref(read.Epoch(), d.Address()), Events: event, References: []ids.ObjectAddress{d.Address()}}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func triggerUnitInstall(t *testing.T, e *core.Engine, relation *catalog.Relation, definitions ...stored.Trigger) *catalog.Relation {
	t.Helper()
	copy := *relation
	copy.Triggers = stored.TriggersPayload(definitions)
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(copy.Address(), &copy)
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	return &copy
}
