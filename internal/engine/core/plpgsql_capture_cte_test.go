package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Exercise the actual Capture adapter, executor transfer, and routine command
// finish together. The probes inspect live private owners, not SQL outcomes.
func TestPLpgSQLCaptureCTECommandLifetime(t *testing.T) {
	for _, consumed := range []bool{false, true} {
		for _, outcome := range []string{"success", "qualification_error", "cancel"} {
			name := "unreferenced/" + outcome
			if consumed { name = "consumed/" + outcome }
			t.Run(name, func(t *testing.T) {
				e, s, read, relation, body := triggerUnitFixture(t, "BEGIN RETURN NULL; END")
				integer, _ := read.LookupType(sqltype.Int4)
				boolean, _ := read.LookupType(sqltype.Bool)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var parent uint64
				var owner *exec.CTEInvocation
				var child uint64
				accepted, qualified := 0, 0
				failure := errors.New("capture qualification")
				rowParts := triggerUnitDefinition(t, read, relation, body, "r", stored.TriggerInsert).Parts()
				rowParts.Timing = stored.TriggerAfter
				rowParts.When = queryProbe(boolean.Usage(), func(*exec.Context) (sqltype.Value, error) {
					accepted++
					child, _ = core.ScopeEffectTestIDs(s)
					owner = core.ScopeCTEInvocation(s)
					if child == parent || owner == nil || owner.Closed {
						t.Fatal("modifier borrowed retired command/CTE owner", child, parent, owner)
					}
					return sqltype.Boolean(boolean, true), nil
				})
				rowParts.Binding, rowParts.Presentation = &analyze.Binding{}, &stored.View{}
				row, err := stored.NewTrigger(rowParts)
				if err != nil { t.Fatal(err) }
				statementParts := rowParts
				statementParts.Name, statementParts.OID = "s", rowParts.OID+1
				statementParts.Level = stored.TriggerStatement
				statementParts.When = queryProbe(boolean.Usage(), func(*exec.Context) (sqltype.Value, error) {
					qualified++
					active, _ := core.ScopeEffectTestIDs(s)
					if active != child || core.ScopeAfterTestFrames(s)[child].Rows != 1 {
						t.Fatal("producer qualified after command retirement", active, child)
					}
					if outcome == "qualification_error" { return sqltype.Value{}, failure }
					if outcome == "cancel" { cancel(); return sqltype.Value{}, ctx.Err() }
					return sqltype.Boolean(boolean, false), nil
				})
				statement, err := stored.NewTrigger(statementParts)
				if err != nil { t.Fatal(err) }
				triggerUnitInstall(t, e, relation, row, statement)
				query := "WITH x AS (INSERT INTO trigger_unit_table VALUES(1,2) RETURNING id) SELECT 7 INTO v;"
				if consumed { query = "WITH x AS (INSERT INTO trigger_unit_table VALUES(1,2) RETURNING id) SELECT id INTO v FROM x;" }
				d := triggerUnitProcedure(t, e, s, "capture_owner", 9500004, integer, proc.OrdinaryInvocation,
					"DECLARE v integer; BEGIN " + query + " RETURN v; END")
				read = e.CatalogRead()
				// Direct fixture publication is setup, not a concurrent catalog
				// replacement of an executing statement. Admit the actual capture
				// only after every descriptor and trigger has been installed.
				_ = s.Finish(errors.New("finish catalog fixture setup"))
				op, done, err := enginetest.Operation(e, ctx)
				if err != nil { t.Fatal(err) }
				t.Cleanup(done)
				s, err = e.StartStatement(op)
				if err != nil { t.Fatal(err) }
				t.Cleanup(func() { _ = s.Finish(errors.New("discard owner proof")) })
				// A PL host borrows an already admitted outer command. Starting
				// a scope alone leaves TxRef/LockTicket unset: acquireRelationTarget
				// correctly rejects that missing owner, not the installed catalog.
				enclosing := enginetest.Statement(t, "SELECT 1")
				description, err := s.Describe(enclosing)
				if err != nil { t.Fatal(err) }
				s.Expected = &description
				result, err := s.Execute(enclosing, 0, nil)
				if err != nil { t.Fatal(err) }
				if result.Rows == nil { t.Fatal("missing enclosing producer") }
				fields := make([]sqltype.Value, len(result.Rows.Schema()))
				if present, err := result.Rows.Next(ctx, fields); err != nil || !present {
					t.Fatal("enclosing first row", present, err)
				}
				if present, err := result.Rows.Next(ctx, fields); err != nil || present {
					t.Fatal("enclosing EOF", present, err)
				}
				if err := result.Rows.Close(); err != nil { t.Fatal(err) }
				if s.TxRef.Epoch() != read.Epoch() || s.LockTicket == nil || core.ScopeAuthority(s) == nil || core.ScopeAuthority(s).Host == nil {
					t.Fatal("missing admitted enclosing command ownership")
				}
				parent, _ = core.ScopeEffectTestIDs(s)
				previousCTE := core.ScopeCTEInvocation(s)
				previousLexical := core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s))
				input := proc.NewFrame(nil)
				defer input.Close()
				f := proc.NewPLFrame(d.SQL.PL, d.SQL, input, sqltype.CollationRef{})
				defer f.Close()
				value, flow, err := core.ScopeNewPLRun(s, d, f, read).Block(d.SQL.PL.Root)
				switch outcome {
				case "success":
					want := int64(7)
					if consumed { want = 1 }
					got, _ := value.Integer()
					if err != nil || flow.Kind != proc.PLReturn || got != want { t.Fatal(value, flow, err) }
				case "qualification_error":
					if !errors.Is(err, failure) { t.Fatal("lost completion failure", err) }
				case "cancel":
					if !errors.Is(err, context.Canceled) { t.Fatal("lost cancellation", err) }
				}
				var fatal *proc.PLExceptionCleanupError
				if errors.As(err, &fatal) { t.Fatal("execution cause misclassified as cleanup", err) }
				active, _ := core.ScopeEffectTestIDs(s)
				if accepted != 1 || qualified != 1 || owner == nil || !owner.Closed || active != parent || len(core.ScopeAfterTestFrames(s)) != 0 || core.ScopeCTEInvocation(s) != previousCTE || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != previousLexical {
					t.Fatal("capture leaked/replayed producer, frame, or activation", accepted, qualified, active, core.ScopeAfterTestFrames(s))
				}
				if len(e.Storage.Relations[relation.Address()].Versions) != 0 { t.Fatal("capture published private stage") }
				_ = s.Finish(errors.New("discard owner proof"))
			})
		}
	}
}
