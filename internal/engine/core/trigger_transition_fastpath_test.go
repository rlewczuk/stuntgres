package core_test

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestTriggerTransitionFastPath(t *testing.T) {
	for _, kind := range []string{"none", "irrelevant", "unregistered-transition", "statement-transition"} {
		t.Run(kind, func(t *testing.T) {
			e, s, read, relation, procedure := triggerUnitFixture(t, "BEGIN RETURN NULL; END")
			if kind != "none" {
				p := triggerUnitDefinition(t, read, relation, procedure, "s", stored.TriggerDelete).Parts()
				p.Timing, p.Level, p.OldTable = stored.TriggerAfter, stored.TriggerStatement, "x"
				if kind == "statement-transition" || kind == "unregistered-transition" {
					p.Events, p.OldTable, p.NewTable = stored.TriggerInsert, "", "x"
				}
				definition, err := stored.NewTrigger(p)
				if err != nil {
					t.Fatal(err)
				}
				relation = triggerUnitInstall(t, e, relation, definition)
			}
			triggerUnitAdmit(t, s, relation)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			probe := &afterAdmissionProbe{Context: s.Op.Ctx}
			s.Op.Ctx = probe
			binding := analyze.Binding{Ref: ref, Relation: relation, Targets: []int{0, 1}}
			// RETURNING requests the same canonical incremental producer in
			// every metadata case, independently of ROW-trigger relevance.
			outputs := make([]expr.Expr, len(relation.Columns))
			for i, column := range relation.Columns {
				outputs[i] = plan.ColumnExpr{Info: expr.Info{Usage: column.Usage}, Scope: 1, Frame: 1, Slot: i}
			}
			binding.Returning = &analyze.Returning{Schema: relation.Columns, Expressions: outputs}
			if kind != "unregistered-transition" {
				if err := dml.RegisterAfterStatement(s.WriteHost(), binding, stored.TriggerInsert); err != nil {
					t.Fatal(err)
				}
			}
			dml.BeginInsertReturning(s.WriteHost(), binding)
			if s.InsertReturning == nil {
				t.Fatal("missing canonical incremental producer")
			}
			defer s.InsertReturning.Rows.Close()
			if _, err := dml.BindUniqueWrite(s.WriteHost(), ref); err != nil {
				t.Fatal(err)
			}
			for i := range 2 {
				accepted, stale, err := dml.AppendAcceptedInsert(s.WriteHost(), ref, &index.WriteCandidate{Values: transitionRuntimeRow(t, read, int64(i+1), 5)})
				if err != nil || !accepted || stale {
					t.Fatal(accepted, stale, err)
				}
			}
			if len(s.Stage.Rows[ref.Address]) != 2 {
				t.Fatal("witness lacks real accepted producer writes")
			}
			frames := core.ScopeAfterTestFrames(s)
			if kind == "statement-transition" {
				frame := frames[0]
				if probe.admissions.Load() == 0 || frame.Rows != 0 || frame.Statements != 1 || len(frame.Segments) != 1 || len(frame.Segments[0].New) != 2 {
					t.Fatal("statement-only capture omitted accepted images", frame)
				}
			} else if probe.admissions.Load() != 0 || len(frames) != 0 {
				t.Fatal("irrelevant producer entered AFTER admission or allocated capture state", probe.admissions.Load(), frames)
			}
		})
	}
}
