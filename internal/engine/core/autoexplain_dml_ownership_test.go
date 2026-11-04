package core_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func autoExplainDMLPresentationOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE ae_presentation_source(id integer)",
		"INSERT INTO ae_presentation_source VALUES(1),(2)",
		"CREATE TABLE ae_presentation_sink(id integer)",
		"CREATE FUNCTION ae_presentation_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
		"CREATE TRIGGER ae_presentation_before BEFORE INSERT ON ae_presentation_sink FOR EACH STATEMENT EXECUTE FUNCTION ae_presentation_before()",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	autoExplainComposedConfigure(t, s)
	enginetest.SessionSQL(t, s, "SET auto_explain.log_analyze=off")
	const query = "WITH a AS (INSERT INTO ae_presentation_sink SELECT id FROM ae_presentation_source RETURNING id) SELECT id FROM a"
	op := enginetest.TransactionOperation(t, s)
	op.OriginalSQL, op.SQLAvailable = query, true
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	explainExecutionCleanup(t, scope)
	result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
	}
	observation, nodes, retired := core.AutoExplainWitnessForTest(scope)
	automatic, explicit, _ := core.AutoExplainDMLRecordsForTest(scope)
	if observation == nil || len(automatic) != 0 || len(explicit) != 0 {
		t.Fatal("presentation-only allocated mutation measurement records")
	}
	for role := exec.DMLInsertInput; role <= exec.DMLCursorLocator; role++ {
		if actual, known := observation.DMLActual(role); known || actual != (exec.Actual{}) {
			t.Fatal("presentation-only created role counters/clocks", role, actual)
		}
	}
	for _, node := range nodes {
		if observation.Known(node) {
			t.Fatal("presentation-only created a source execution record")
		}
	}
	if err := scope.Finish(nil); err != nil || !retired() || len(op.Notices) != 1 {
		t.Fatal("presentation-only actual child capture/lifetime", err, op.Notices)
	}
}

func autoExplainDMLNestedObserverOwnership(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprintf("automatic=%t", automatic), func(t *testing.T) {
			e, s, provider := explainExecutionFixture(t)
			provider.declineExecution = true
			enginetest.SessionSQL(t, s, "CREATE FUNCTION ae_nested_dml(key integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO executed_explain_local SELECT k FROM executed_explain_data WHERE k=key; RETURN key; END$$")
			if automatic {
				autoExplainComposedConfigure(t, s)
				enginetest.SessionSQL(t, s, "SET auto_explain.log_nested_statements=on")
			}
			const query = "EXPLAIN (ANALYZE,FORMAT JSON,TIMING OFF) SELECT ae_nested_dml(1),ae_nested_dml(2)"
			op := enginetest.TransactionOperation(t, s)
			op.OriginalSQL, op.SQLAvailable = query, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			explainExecutionCleanup(t, scope)
			var observed []*exec.Observation
			provider.check = func() error {
				observation, nodes := core.ExplainObservationForTest(scope)
				if observation == nil || len(nodes) == 0 {
					return errors.New("nested accepted source lost its explicit observer")
				}
				for _, previous := range observed {
					if previous == observation {
						return errors.New("independent nested executions adopted the outer explicit observer")
					}
				}
				for _, node := range nodes {
					if !observation.Known(node) {
						return errors.New("nested source presentation did not borrow its actual accepted graph")
					}
				}
				if automatic {
					auto, _, _ := core.AutoExplainWitnessForTest(scope)
					if auto != observation {
						return errors.New("same nested canonical command did not share its explicit/automatic observer")
					}
				}
				observed = append(observed, observation)
				return nil
			}
			result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			if result.Rows != nil {
				if err := result.Rows.Close(); err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
			}
			if len(observed) != 2 {
				t.Fatal("witness did not reach both genuine nested source executions", len(observed))
			}
			if err := scope.Finish(nil); err != nil || provider.begins != provider.ends {
				t.Fatal("nested source/observer did not retire", err)
			}
		})
	}
}

// Snapshot presentation-independent contents and identities of the actual graph;
// the nodes themselves remain borrowed from their accepted resource.
func autoExplainAcceptedGraphState(root *plan.ProjectNode) string {
	if root == nil {
		return ""
	}
	var state strings.Builder
	plan.VisitExecutableQuery(root, func(node plan.Node) {
		fmt.Fprintf(&state, "%p:%#v\n", node, node)
	}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(child expr.Expr) { fmt.Fprintf(&state, "%#v\n", child) })
	})
	return state.String()
}

// Parameterized definitions cannot own reusable folds: Bind owns each genuine
// accepted group, including its constant siblings. Keep the direct-import proof
// in the callers and inspect the actual protocol registry/resource here.
func autoExplainParameterizedFoldOwnership(t *testing.T, e *core.Engine, s *session.Session, query string) {
	t.Helper()
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) []sqlstate.Error {
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartProtocolCommand(op, cycle)
		if err != nil {
			t.Fatal(err)
		}
		explainExecutionCleanup(t, scope)
		if err := scope.Finish(fn(scope)); err != nil {
			t.Fatal(err)
		}
		return op.DetachNotices()
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("ae_fold_definition", query, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("ae_fold_definition")
	description := &definition.Description
	analysis, owner, generation := description.Analysis, description.Folds, description.PlanGeneration
	parameter := &description.Parameters.Types[0]
	definitionFolds := owner.Current()
	type retained struct {
		group   *exec.AcceptedFolds
		saved   exec.AcceptedFolds
		text    string
		backing *exec.FoldConstant
		root    *plan.ProjectNode
		graph   string
	}
	var previous []retained
	assertRetained := func() {
		t.Helper()
		for _, old := range previous {
			if old.group.Generation != old.saved.Generation || fmt.Sprint(*old.group) != old.text ||
				fmt.Sprint(old.saved) != old.text || &old.group.Entries[0] != old.backing || &old.saved.Entries[0] != old.backing {
				t.Fatal("later execution/rejection mutated accepted fold generation/backing")
			}
			if autoExplainAcceptedGraphState(old.root) != old.graph {
				t.Fatal("later execution/rejection mutated retained accepted graph")
			}
		}
		if description.Analysis != analysis || description.Folds != owner || description.PlanGeneration != generation ||
			&description.Parameters.Types[0] != parameter || owner.Current().Generation != definitionFolds.Generation ||
			len(owner.Current().Entries) != len(definitionFolds.Entries) {
			t.Fatal("invocation folds leaked into the immutable parameterized definition")
		}
	}
	for index, argument := range []string{"1", "2", "99", "1"} {
		name := fmt.Sprintf("ae_fold_%d", index)
		command(func(scope *core.StatementScope) error {
			data := append([]byte{0, 0, 0, 1, 0, 0, 0, byte(len(argument))}, argument...)
			data = append(data, 0, 0)
			_, err := scope.BindProtocolPortal(name, "ae_fold_definition", &enginetest.ProtocolReader{Data: data})
			if err == nil && core.AutoExplainActiveForTest(scope) {
				t.Fatal("Bind activated automatic execution")
			}
			return err
		})
		binding := registry.Binding(name)
		if binding == nil || binding.Folds == nil || len(binding.Folds.Entries) == 0 || binding.Resource == nil {
			t.Fatal("witness has no genuine Bind-accepted folds/resource")
		}
		root := binding.Resource.Root
		if strings.HasPrefix(query, "SELECT ") && root == nil {
			t.Fatal("accepted SELECT has no real producer graph")
		}
		for _, old := range previous {
			if binding.Folds == old.group || &binding.Folds.Entries[0] == old.backing || root != nil && root == old.root {
				t.Fatal("parameterized Bind reused another invocation's private fold group/graph")
			}
		}
		accepted := binding.Folds
		previous = append(previous, retained{accepted, *accepted, fmt.Sprint(*accepted), &accepted.Entries[0], root, autoExplainAcceptedGraphState(root)})
		// Reject final publication against this very accepted binding, not a
		// fabricated candidate, and retain its actual graph and group.
		command(func(scope *core.StatementScope) error {
			e.Mu.Lock()
			defer e.Mu.Unlock()
			auth, err := e.AuthorizationLocked(scope.Op)
			if err != nil {
				return err
			}
			environment, err := e.PreparedEnvironmentLocked(auth, scope.RowSecurity)
			if err != nil {
				return err
			}
			publication, err := core.BeginFoldPublication(&binding.Entry.Description, binding.Entry.Body, environment, auth.Catalog, binding)
			if err != nil || publication == nil {
				t.Fatal("missing actual accepted-binding publication", err)
			}
			ctx, cancel := context.WithCancel(scope.Op.Context())
			cancel()
			if err := publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch()); !errors.Is(err, context.Canceled) ||
				binding.Folds != accepted || binding.Resource.Root != root {
				t.Fatal("rejected publication changed accepted artifacts", err)
			}
			return nil
		})
		assertRetained()
		notices := command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		if root != nil && len(notices) != 0 {
			t.Fatal("retained SELECT reported completion before executor disposal", notices)
		}
		assertRetained()
		closed := command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
		if root == nil && len(closed) != 0 {
			t.Fatal("eager mutation reported again on portal disposal", closed)
		}
		notices = append(notices, closed...)
		if len(notices) != 1 || !strings.Contains(notices[0].Message, "$1 = '"+argument+"'") {
			t.Fatal("accepted binding lost its current canonical arguments", notices)
		}
		assertRetained()
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("ae_fold_definition") })
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
}

// Observe the real OpenNode handoff reached by ExecuteMerge before
// AssembleMergeRows enters its membership loop. No production hook is installed.
type autoExplainMergeHandoff struct {
	context.Context
	scope   *core.StatementScope
	record  *exec.DMLObservation
	failure error
}

func (p *autoExplainMergeHandoff) Err() error {
	if p.record == nil && p.failure == nil {
		var pcs [32]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		open, merge, membership := false, false, false
		for {
			frame, more := frames.Next()
			open = open || strings.HasSuffix(frame.Function, "/exec.OpenNode")
			merge = merge || strings.HasSuffix(frame.Function, "/dml.ExecuteMerge")
			membership = membership || strings.HasSuffix(frame.Function, "/dml.AssembleMergeRows")
			if !more {
				break
			}
		}
		if open && merge && !membership {
			automatic, explicit, shared := core.AutoExplainDMLRecordsForTest(p.scope)
			if !shared || len(automatic) != 1 || len(explicit) != 1 || automatic[0] != explicit[0] {
				p.failure = errors.New("actual pre-membership handoff lost shared explicit/automatic records")
			} else {
				p.record = automatic[0]
				if p.record.CommandActual().Loops != 1 || p.record.Role(exec.DMLMergeCandidates).Actual() != (exec.Actual{}) ||
					p.record.Role(exec.DMLMergeTarget).Actual() != (exec.Actual{}) {
					p.failure = errors.New("capture manufactured producer entry before genuine MERGE membership")
				}
			}
		}
	}
	if p.failure != nil {
		return p.failure
	}
	return p.Context.Err()
}
