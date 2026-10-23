package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
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

func triggerUnitAdmit(t *testing.T, s *StatementScope, relation *catalog.Relation) {
	t.Helper()
	statement := &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: relation.Name()}}}}
	if err := s.gate(statement); err != nil {
		t.Fatal(err)
	}
	if err := s.acquireRelationTarget(statement, statement, true, false); err != nil {
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
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	f, err := newPLTriggerFrame(d, read, relation, "a", "INSERT", []string{"x", "7"}, nil, row)
	if err != nil {
		t.Fatal(err)
	}
	if d.SQL.PL.FormalCount != 0 || len(d.SQL.Formals) != 0 || !d.SQL.PL.Trigger || !f.Values[0].IsNull() || f.Values[0].Type() != f.Values[1].Type() {
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
	retained := f.Values[1]
	row[1] = triggerUnitInt(t, integer, 99)
	f.Close()
	got, err := retained.CompositeField(1).Integer()
	if err != nil || got != 5 {
		t.Fatal("frame borrowed producer scratch or erased returned composite", got, err)
	}
	noargs, err := newPLTriggerFrame(d, read, relation, "b", "DELETE", nil, row, nil)
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
	broken := *d.SQL.PL
	broken.Variables = append([]proc.PLVariable(nil), broken.Variables...)
	broken.Variables[10].Usage = integer.Usage()
	broken.Variables[2].Usage = integer.Usage()
	definition := *d.SQL
	definition.PL = &broken
	if err := broken.ValidateDefinition(&definition); err == nil {
		t.Fatal("forged implicit slot type accepted")
	}
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
				ordinary, err := s.convertPLValue(value, shape.Usage())
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
	if _, err := s.writeHost().BeforeRow(request); err == nil {
		t.Fatal("unadmitted trigger entry accepted")
	}
	triggerUnitAdmit(t, s, relation)
	result, err := s.writeHost().BeforeRow(request)
	if err != nil || !result.Proceed {
		t.Fatal(err)
	}
	got, err := result.Row[1].Integer()
	if err != nil || got != 6 || s.activeSQLRoutineDepth != 0 {
		t.Fatal("unlocked invocation/depth", got, err)
	}
	if len(s.BeforeCaptures) != 1 {
		t.Fatal("command capture not retained")
	}
	result, err = s.writeHost().BeforeRow(request)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = result.Row[1].Integer()
	if got != 6 || len(s.BeforeCaptures) != 1 {
		t.Fatal("frame leaked prior row or capture was lost")
	}
	// The PL expression above acquires Engine.mu through ordinary binding;
	// successful completion proves the callback was not run under that mutex.
	if !e.mu.TryLock() {
		t.Fatal("trigger retained engine mutex")
	}
	e.mu.Unlock()
	input := proc.NewFrame(nil)
	defer input.Close()
	if _, err := s.invokeSQLRoutine(d, input, d.SQL.Result, nil, sqltype.CollationRef{}, false); err == nil {
		t.Fatal("ordinary call acquired trigger entry")
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
			h := s.writeHost()
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
				e.mu.Lock()
				source, err := e.captureQuerySourcesLocked(s, nil, true)
				e.mu.Unlock()
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
			staged := s.stage.rows[relation.Address()]
			if len(staged) != 1 || staged[0].Row == 0 || staged[0].Version == 0 {
				t.Fatal("stage did not allocate genuine row/version identities")
			}
			if target, _ := e.locks.ReplacementGrants(s.ticket, ref, nil); !target {
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
			if len(e.storage.Relations[relation.Address()].Versions) != 0 {
				t.Fatal("veto published accepted trigger row")
			}
			if _, err := h.BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row}); err == nil {
				t.Fatal("finished scope admitted trigger")
			}
		})
	}
}
