package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCTEDMLBindingOwner(t *testing.T) {
	e := engine.New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, session, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	scope, op := cteDMLUnitScope(t, session, context.Background())
	ast := enginetest.Statement(t, "WITH one AS (UPDATE t SET v=v+$1 WHERE id=1 RETURNING id,v), two AS (UPDATE t SET v=v+$2 WHERE id=2 RETURNING id,v) UPDATE t SET v=v+$3 WHERE id=3 RETURNING id,v")
	description, err := scope.Describe(ast)
	if err != nil {
		t.Fatal(err)
	}
	if description.Parameters.Count != 3 || len(description.Parameters.Types) != 3 || len(description.Results) != 2 {
		t.Fatal(description)
	}
	for _, usage := range description.Parameters.Types {
		if usage.Type.ID() != sqltype.Int4 {
			t.Fatal(usage)
		}
	}
	if len(core.ScopeMutations(scope)) != 3 || core.DescriptionAnalysis(&description) == nil || len(core.PreparedAnalysisTargets(core.DescriptionAnalysis(&description))) != 3 {
		t.Fatal("occurrences were conflated", len(core.ScopeMutations(scope)))
	}
	seen := make(map[parser.Statement]bool)
	for i := range core.ScopeMutations(scope) {
		mutation := core.ScopeMutations(scope)[i]
		if i == scope.ActiveMutation {
			mutation = scope.StatementMutation
		}
		if seen[mutation.Statement] || mutation.LockedRef != ref || core.PreparedAnalysisTargets(core.DescriptionAnalysis(&description))[mutation.Statement] != ref {
			t.Fatal("wrong exact target occurrence", i, mutation.LockedRef)
		}
		seen[mutation.Statement] = true
		if mutation.InsertReturning != nil || mutation.UniqueBinding != nil || mutation.PartitionInsert != nil {
			t.Fatal("Prepare retained executable target state")
		}
	}
	if scope.Stage.Rows != nil || scope.Stage.Deletes != nil || scope.View != nil || core.ScopeCTEInvocation(scope) != nil {
		t.Fatal("Prepare executed a modifier or captured data")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, session, "SELECT v FROM t ORDER BY id", 10, 20, 30)
	enginetest.LockEmpty(t, e)
	for _, kind := range []string{"frozen-select", "frozen-update", "frozen-foreign", "stale-child", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			session := sessionStart(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE frozen_child(v integer)",
				"CREATE TABLE frozen_descendant() INHERITS(frozen_child)",
				"CREATE TABLE frozen_main(v integer)",
			} {
				sessionSQL(t, session, q)
			}
			body := "WITH m AS (UPDATE frozen_child SET v=1 RETURNING v) SELECT v FROM m"
			if kind == "frozen-update" {
				body = "WITH m AS (UPDATE frozen_child SET v=1 RETURNING v) UPDATE frozen_main SET v=2 RETURNING v"
			}
			if kind == "frozen-foreign" {
				// Metadata-only admission proof: no provider is opened here.
				// Public FDW scenarios own actual remote writes and lifetimes.
				for _, q := range []string{
					"CREATE FOREIGN DATA WRAPPER frozen_reader NO HANDLER NO VALIDATOR",
					"CREATE SERVER frozen_server FOREIGN DATA WRAPPER frozen_reader",
					"CREATE FOREIGN TABLE frozen_foreign(v integer) SERVER frozen_server",
					"CREATE TABLE frozen_source(v integer)",
				} {
					sessionSQL(t, session, q)
				}
				body = "WITH m AS (UPDATE frozen_foreign SET v=(SELECT v FROM frozen_source) RETURNING v) SELECT v FROM m"
			}
			sessionSQL(t, session, "CREATE FUNCTION frozen_admission() RETURNS integer LANGUAGE SQL BEGIN ATOMIC "+body+"; END")
			read := e.CatalogRead()
			addresses := read.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "frozen_admission")
			if len(addresses) != 1 {
				t.Fatal("missing actual frozen program")
			}
			procedure, ok := read.Procedure(addresses[0])
			if !ok {
				t.Fatal("missing actual frozen procedure")
			}
			program, ok := procedure.SQLBody.(*stored.RoutineProgram)
			if !ok {
				t.Fatal("missing actual frozen payload")
			}
			action := program.Action(0)
			if kind == "stale-child" {
				action = action.With(func(parts *stored.RuleActionParts) {
					parts.Binding.CTEs = slices.Clone(parts.Binding.CTEs)
					definition := *parts.Binding.CTEs[0]
					original, ok := analyze.MutationBinding(&definition)
					if !ok {
						t.Fatal("missing actual modifier")
					}
					mutation := *original
					mutation.Ref.Epoch++
					definition.Mutation = &mutation
					parts.Binding.CTEs[0] = &definition
				})
				program = stored.NewRoutineProgram([]stored.RuleAction{action})
			}
			child := enginetest.CatalogRef(t, e, read, "frozen_child")
			descendant := enginetest.CatalogRef(t, e, read, "frozen_descendant")
			if kind == "frozen-foreign" {
				child = enginetest.CatalogRef(t, e, read, "frozen_foreign")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, op := cteDMLUnitScope(t, session, ctx)
			// Establish a real statement ticket without lexically admitting the
			// frozen modifier. The bound admission owner must obtain its grants.
			if _, err := scope.Describe(enginetest.Statement(t, "SELECT 1")); err != nil {
				t.Fatal(err)
			}
			restore := scope.ActivateSQLRoutine(procedure.SQL.Inputs, read)
			restored := false
			t.Cleanup(func() {
				if !restored {
					_ = restore(errors.New("unit cleanup"))
				}
			})
			if kind == "canceled" {
				cancel()
			}
			err := core.ScopeAdmitSQLRoutineCommand(scope, procedure, program, 0)
			switch kind {
			case "stale-child":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale immutable child target admitted", err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled immutable child admission", err)
				}
			default:
				if err != nil {
					t.Fatal("actual frozen child admission", err)
				}
				mutations := core.ScopeMutations(scope)
				if len(mutations) != 2 || scope.ActiveMutation != 0 || scope.Statement != action.Syntax() {
					t.Fatal("missing exact main/child mutation contexts")
				}
				if mutations[1].Statement != action.Binding().CTEs[0].Statement || mutations[1].LockedRef != child {
					t.Fatal("child target lost immutable identity", mutations[1])
				}
				if scope.LockTicket == nil || !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: child}, lock.RowExclusive) {
					t.Fatal("child context lacks its actual target grant")
				}
				if kind == "frozen-foreign" {
					source := enginetest.CatalogRef(t, e, read, "frozen_source")
					if !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: source}, lock.AccessShare) {
						t.Fatal("local foreign-command source lacks its actual read grant")
					}
					if scope.View == nil || scope.View.Data().GenerationAt(source.Address) == nil || scope.View.Data().GenerationAt(child.Address) != nil {
						t.Fatal("foreign target entered the local heap manifest or local source was omitted")
					}
					if len(mutations[1].InheritedTargets) != 0 || mutations[1].LockedStorage != nil {
						t.Fatal("foreign admission manufactured local target storage")
					}
				} else {
					if !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: descendant}, lock.RowExclusive) {
						t.Fatal("child context lacks actual hierarchy grant")
					}
					if len(mutations[1].InheritedTargets) != 1 || mutations[1].InheritedTargets[0].Ref != descendant {
						t.Fatal("bound child hierarchy belongs to wrong occurrence")
					}
				}
				if kind == "frozen-update" {
					main := enginetest.CatalogRef(t, e, read, "frozen_main")
					if scope.LockedRef != main || !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: main}, lock.RowExclusive) {
						t.Fatal("main DML target lost its own grant/context")
					}
				} else if scope.LockedRef != (ids.ObjectRef{}) {
					t.Fatal("main SELECT borrowed its child's target")
				}
			}
			if len(scope.Stage.Rows) != 0 || len(scope.Stage.Deletes) != 0 || core.ScopeCTEInvocation(scope) != nil {
				t.Fatal("admission executed child effects")
			}
			err = restore(err)
			restored = true
			if finishErr := scope.Finish(err); err == nil && finishErr != nil {
				t.Fatal(finishErr)
			}
			op.Done()
			enginetest.LockEmpty(t, e)
		})
	}
}
