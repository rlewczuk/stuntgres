package functions_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func freespaceCaptureFixture(t *testing.T) (*core.Engine, ids.ObjectRef, plan.InstalledPgFreespaceExpr) {
	t.Helper()
	e := engine.New()
	for _, q := range []string{"CREATE EXTENSION pg_freespacemap", "CREATE TABLE freespace_heap(n integer)", "INSERT INTO freespace_heap VALUES(1)"} {
		enginetest.Run(t, e, q)
	}
	return e, enginetest.CatalogRef(t, e, e.CatalogRead(), "freespace_heap"), freespaceCaptureExpression(t, e, true)
}
func freespaceCaptureExpression(t *testing.T, e *core.Engine, block bool) plan.InstalledPgFreespaceExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	args := []parser.Expr{enginetest.StringLiteral("-")}
	if block {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 0}})
	}
	node, err := binder.BindFunctionSource(enginetest.FunctionCall("pg_freespace", args...))
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledPgFreespaceExpr)
}
func freespaceCapture(ctx context.Context, e *core.Engine, s *session.Session, n plan.InstalledPgFreespaceExpr, ref ids.ObjectRef) (storage.FreespaceLayout, error) {
	op, err := s.Acquire(ctx)
	if err != nil {
		return storage.FreespaceLayout{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return storage.FreespaceLayout{}, err
	}
	setup, err := parser.Parse("SELECT 1")
	if err != nil {
		return storage.FreespaceLayout{}, scope.Finish(err)
	}
	result, err := scope.Execute(setup[0], 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	var captured storage.FreespaceLayout
	if err == nil {
		captured, err = core.HostOf(scope).Contrib().PgFreespace(n, uint32(ref.Address.ObjectID), 0)
	}
	return captured, scope.Finish(err)
}

type freespaceCaptureOutcome struct {
	capture storage.FreespaceLayout
	err     error
}

func freespaceWait(t *testing.T, e *core.Engine, ref ids.ObjectRef, s *session.Session, done <-chan freespaceCaptureOutcome) *freespaceCaptureOutcome {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		for resource, state := range e.Locks.Inspect().Resources {
			if resource.ObjectRef != ref || resource.Row != 0 {
				continue
			}
			for _, waiter := range state.Pending {
				if waiter.Ticket.Session() == s.SessionToken && waiter.Mode == lock.AccessShare && !waiter.Terminal {
					return nil
				}
			}
		}
		select {
		case result := <-done:
			return &result
		case <-deadline.C:
			t.Fatal("exact freespace AccessShare waiter not admitted")
		default:
			runtime.Gosched()
		}
	}
}

func TestPgFreespaceCapture(t *testing.T) {
	t.Run("visible_roots_and_compact_results", func(t *testing.T) {
		e, ref, n := freespaceCaptureFixture(t)
		reader, writer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		initial := enginetest.SessionSQL(t, reader, "SELECT n FROM freespace_heap")
		if initial.Rows != nil {
			initial.Rows.Close()
		}
		scope, finish := visibilityScope(t, e, reader)
		h := visibilityHost(scope)
		h.ReacquireOwnerEngine()
		view, local, err := h.RelationDataViewLocked(ref)
		h.ReleaseOwnerEngineLocked()
		if err != nil {
			finish(err)
			t.Fatal(err)
		}
		if local {
			defer view.Release()
		}
		retained := view.Load()
		old := retained.Committed[ref.Address][0]
		original := e.Storage.Relations[ref.Address].Versions[0]
		if old.Version != original.Version || &old.Values[0] != &original.Values[0] {
			t.Fatal("capture copied or replaced genuine immutable datum backing")
		}
		source, err := exec.CaptureSource(t.Context(), view, ref)
		if err != nil {
			finish(err)
			t.Fatal(err)
		}
		defer source.Close()
		if source.Count() != 1 || source.VersionAt(source.RawIndex(0)).Version != original.Version {
			t.Fatal("source lost actual visible version")
		}
		source.Close()
		oldLayout, err := contrib.PgFreespace(h, n, uint32(ref.Address.ObjectID), 0)
		if err != nil || oldLayout != (storage.FreespaceLayout{Blocks: 1, LastAvail: 8096}) {
			t.Fatal(oldLayout, err)
		}
		if err := finish(nil); err != nil {
			t.Fatal(err)
		}
		enginetest.SessionSQL(t, writer, "INSERT INTO freespace_heap VALUES(2)")
		current, err := freespaceCapture(t.Context(), e, writer, n, ref)
		if err != nil || current.LastAvail != 8064 {
			t.Fatal(current, err)
		}
		snapshot, err := freespaceCapture(t.Context(), e, reader, n, ref)
		if err != nil || snapshot != oldLayout {
			t.Fatal("mixed newer roots and older frontier", snapshot, err)
		}
		enginetest.SessionSQL(t, writer, "BEGIN")
		enginetest.SessionSQL(t, writer, "DELETE FROM freespace_heap WHERE n=2")
		own, err := freespaceCapture(t.Context(), e, writer, n, ref)
		if err != nil || own != oldLayout {
			t.Fatal("own deletion failed to reclaim", own, err)
		}
		enginetest.SessionSQL(t, writer, "ROLLBACK")
		rolled, err := freespaceCapture(t.Context(), e, writer, n, ref)
		if err != nil || rolled != current {
			t.Fatal("rollback did not restore population", rolled, err)
		}
		if oldLayout.LastAvail != 8096 || retained.Committed[ref.Address][0].Version != old.Version || &retained.Committed[ref.Address][0].Values[0] != &original.Values[0] {
			t.Fatal("later writes mutated retained roots/results")
		}
		enginetest.SessionSQL(t, reader, "ROLLBACK")
	})
	t.Run("real_index_generator_and_catalog", func(t *testing.T) {
		e, _, n := freespaceCaptureFixture(t)
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE INDEX freespace_index ON freespace_heap(n)", "CREATE SEQUENCE freespace_sequence"} {
			enginetest.Run(t, e, q)
		}
		indexRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "freespace_index")
		sequenceRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "freespace_sequence")
		scope, finish := visibilityScope(t, e, s)
		h := visibilityHost(scope)
		// Already holding Owner -> Engine: use the locked catalog projection,
		// never Engine.CatalogRead, which would recursively acquire Engine.Mu.
		h.ReacquireOwnerEngine()
		view := e.Catalog.Current()
		allocation := h.RelationAllocationLocked(view, view.MustRelation(indexRef.Address))
		generator := h.SequenceGeneratorLocked(sequenceRef.Address)
		genuine := allocation.Index != nil && allocation.Index == e.Storage.IndexRoots[indexRef.Address] && allocation.Parent != nil && generator != nil && generator == e.Storage.Sequences[sequenceRef.Address]
		var before storage.SequenceGenerator
		if generator != nil {
			before = *generator
		}
		h.ReleaseOwnerEngineLocked()
		if !genuine {
			t.Fatal("capture manufactured index/generator identity")
		}
		result, err := contrib.PgFreespace(h, n, uint32(indexRef.Address.ObjectID), 0)
		if err != nil || result.Blocks != 1 || result.LastAvail != 0 {
			t.Fatal("index within-page slack was exposed", result, err)
		}
		result, err = contrib.PgFreespace(h, n, uint32(sequenceRef.Address.ObjectID), 0)
		h.ReacquireOwnerEngine()
		unchanged := h.SequenceGeneratorLocked(sequenceRef.Address) == generator && reflect.DeepEqual(before, *generator)
		h.ReleaseOwnerEngineLocked()
		if err != nil || result.Blocks != 1 || result.LastAvail != 8096 || !unchanged {
			t.Fatal("sequence inquiry changed generator", result, err)
		}
		catalogRef := enginetest.RoleTableRef(t, e, "pg_catalog.pg_class")
		result, err = contrib.PgFreespace(h, n, uint32(catalogRef.Address.ObjectID), 0)
		if err != nil || result.Blocks == 0 {
			t.Fatal("genuine catalog producer missing", result, err)
		}
		if err := finish(nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("partitioned_index_precedes_block", func(t *testing.T) {
		e, ref, n := freespaceCaptureFixture(t)
		enginetest.Run(t, e, "CREATE TABLE freespace_parent(n integer) PARTITION BY RANGE(n)")
		enginetest.Run(t, e, "CREATE INDEX freespace_index ON freespace_heap(n)")
		view := e.CatalogRead()
		parent := enginetest.CatalogRef(t, e, view, "freespace_parent")
		indexRef := enginetest.CatalogRef(t, e, view, "freespace_index")
		target := *view.MustRelation(indexRef.Address)
		definition := *target.SQLIndex
		definition.Parent = parent.Address
		target.SQLIndex = &definition
		target.ObjectRevision = target.Revision() + 1
		before := e.Catalog.State
		t.Cleanup(func() { e.Mu.Lock(); e.Catalog.PublishLocked(before); e.Mu.Unlock() })
		state := catalog.CloneState(before)
		state.SetDescriptor(indexRef.Address, &target)
		e.Mu.Lock()
		e.Catalog.PublishLocked(state)
		e.Mu.Unlock()
		scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
		_, err := contrib.PgFreespace(visibilityHost(scope), n, uint32(indexRef.Address.ObjectID), -1)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "relation \"freespace_index\" does not have storage", Detail: "This operation is not supported for partitioned indexes."}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
			t.Fatal("kind/block precedence", ref, diagnostic, err)
		}
		finish(err)
	})
	for _, phase := range []string{"cancel", "revoke", "replacement"} {
		t.Run(phase, func(t *testing.T) {
			e, ref, n := freespaceCaptureFixture(t)
			for _, q := range []string{"CREATE ROLE freespace_reader", "GRANT USAGE ON SCHEMA public TO freespace_reader", "GRANT EXECUTE ON FUNCTION pg_freespace(regclass,bigint) TO freespace_reader"} {
				enginetest.Run(t, e, q)
			}
			holder, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, observer, "SET SESSION AUTHORIZATION freespace_reader")
			enginetest.SessionSQL(t, holder, "BEGIN")
			enginetest.SessionSQL(t, holder, "LOCK TABLE freespace_heap IN ACCESS EXCLUSIVE MODE")
			defer enginetest.SessionSQL(t, holder, "ROLLBACK")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan freespaceCaptureOutcome, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("freespace capture worker did not join")
					}
				}
			}()
			go func() {
				capture, err := freespaceCapture(ctx, e, observer, n, ref)
				done <- freespaceCaptureOutcome{capture, err}
			}()
			if early := freespaceWait(t, e, ref, observer, done); early != nil {
				joined = true
				t.Fatal("capture bypassed scoped lock admission", early.err)
			}
			switch phase {
			case "cancel":
				cancel()
			case "revoke":
				enginetest.Run(t, e, "REVOKE EXECUTE ON FUNCTION pg_freespace(regclass,bigint) FROM freespace_reader")
			case "replacement":
				if _, err := e.ReplaceCatalog(t.Context(), e, e.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			enginetest.SessionSQL(t, holder, "ROLLBACK")
			var result freespaceCaptureOutcome
			select {
			case result = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("capture failed to finish")
			}
			if result.capture != (storage.FreespaceLayout{}) {
				t.Fatal("failed capture published a result prefix", result)
			}
			switch phase {
			case "cancel":
				if !errors.Is(result.err, context.Canceled) {
					t.Fatal(result.err)
				}
			case "revoke":
				var diagnostic *sqlstate.Error
				if !errors.As(result.err, &diagnostic) || diagnostic.Code != "42501" {
					t.Fatal("current authority not rechecked", result.err)
				}
			case "replacement":
				if !errors.Is(result.err, ids.ErrCatalogReplaced) {
					t.Fatal("old identity silently retargeted", result.err)
				}
			}
			if phase != "replacement" {
				enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION pg_freespace(regclass,bigint) TO freespace_reader")
				next, err := freespaceCapture(t.Context(), e, observer, n, ref)
				if err != nil || next.LastAvail != 8096 {
					t.Fatal("later admission leaked", next, err)
				}
			}
		})
	}
}

func TestPgFreespaceMutationCapture(t *testing.T) {
	for _, tc := range []struct{ name, columns, values string }{
		{"default", "v smallint DEFAULT pg_freespace('freespace_heap'::regclass,0)", "DEFAULT VALUES"},
		{"check", "v smallint CHECK(pg_freespace('freespace_heap'::regclass,0)>=0)", "VALUES(0)"},
		{"default_and_check", "v smallint DEFAULT pg_freespace('freespace_heap'::regclass,0), CHECK(v>=0 AND pg_freespace('freespace_heap'::regclass,0)>=0)", "DEFAULT VALUES"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, ref, _ := freespaceCaptureFixture(t)
			enginetest.Run(t, e, "CREATE TABLE freespace_consumer("+tc.columns+")")
			store := e.Storage.Relations[ref.Address]
			original := store.Versions[0]
			s := enginetest.StartSession(t, e, nil)
			op, err := s.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !scope.Finished {
					scope.Finish(context.Canceled)
				}
			}()
			result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO freespace_consumer "+tc.values), 0, nil)
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if scope.View == nil || scope.View.Load() == nil {
				t.Fatal("mutation omitted its original source snapshot")
			}
			root, ok := scope.View.Load().DynamicRoots[ref.Address]
			if !ok || root.Storage != store || root.Relation != e.CatalogRead().Get(ref.Address) || len(root.Versions) != 1 || root.Versions[0].Version != original.Version || &root.Versions[0].Values[0] != &original.Values[0] {
				t.Fatal("stored mutation manifest lost genuine dynamic root/backing")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPgFreespaceAdmittedIdentity(t *testing.T) {
	for _, block := range []bool{true, false} {
		t.Run(strconv.FormatBool(block), func(t *testing.T) {
			e, ref, _ := freespaceCaptureFixture(t)
			n := freespaceCaptureExpression(t, e, block)
			retained := e.CatalogRead()
			regclass := n.Args[0].ResultUsage().Type
			n.Args = append([]expr.Expr(nil), n.Args...)
			n.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, strconv.FormatUint(uint64(ref.Address.ObjectID), 10))}
			// Current COW presentation is not a replacement of the captured
			// declaration. The wrapper's genuine scalar slot moves independently.
			for _, q := range []string{
				"CREATE SCHEMA freespace_moved",
				"ALTER FUNCTION pg_freespace(regclass,bigint) RENAME TO freespace_scalar",
				"ALTER FUNCTION freespace_scalar(regclass,bigint) SET SCHEMA freespace_moved",
				"ALTER FUNCTION pg_freespace(regclass) RENAME TO freespace_wrapper",
				"ALTER FUNCTION freespace_wrapper(regclass) SET SCHEMA freespace_moved",
			} {
				enginetest.Run(t, e, q)
			}
			current := e.CatalogRead()
			if current.Get(n.Ref.Address) == n.Procedure {
				t.Fatal("fixture did not replace presentation descriptor")
			}
			next := catalog.CloneState(current.State())
			next.SetEpoch(current.Epoch() + 1)
			e.Mu.Lock()
			e.Catalog.PublishLocked(next)
			e.Mu.Unlock()
			scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
			e.Mu.Lock()
			auth := enginetest.CatalogAuthorization(e, e.Catalog.Current())
			e.Mu.Unlock()
			dependencies := []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(next.Epoch(), n.Ref.Address), Revision: n.Procedure.Revision()}}
			evaluation := exec.Context{
				EvalStatement: exec.EvalStatement{Context: t.Context(), Host: core.HostOf(scope), Catalog: retained, Input: scope.Input, Output: scope.Output},
				EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: dependencies},
			}
			if block {
				value, err := n.Eval(&evaluation)
				if err != nil {
					t.Fatal("admitted scalar identity", err)
				}
				free, failure := value.Integer()
				if failure != nil || free != 8096 {
					t.Fatal("admitted scalar identity", value, failure)
				}
			} else {
				cursor, err := n.OpenSource(&evaluation)
				if err != nil {
					t.Fatal("admitted wrapper identity", err)
				}
				if !cursor.Initialized() || cursor.Exhausted || cursor.State == nil {
					t.Fatal("wrapper did not retain an argument-only source", cursor)
				}
				value, present, err := n.AdvanceSource(t.Context(), &evaluation, &cursor)
				if err != nil || !present || value.CompositeLen() != 2 {
					t.Fatal("admitted wrapper first actual row", value, present, err)
				}
				block, blockErr := value.CompositeField(0).Integer()
				free, freeErr := value.CompositeField(1).Integer()
				if blockErr != nil || freeErr != nil || block != 0 || free != 8096 {
					t.Fatal("wrapper lost selected scalar capture", block, free, blockErr, freeErr)
				}
				if _, present, err = n.AdvanceSource(t.Context(), &evaluation, &cursor); err != nil || present || !cursor.Exhausted || cursor.State != nil {
					t.Fatal("wrapper did not consume terminal DONE and detach", present, err, cursor)
				}
				cursor.Close()
				foreignCursor, err := n.OpenSource(&evaluation)
				if err != nil {
					t.Fatal(err)
				}
				foreign := n
				foreign.Occurrence++
				if _, _, err = foreign.AdvanceSource(t.Context(), &evaluation, &foreignCursor); err == nil || !foreignCursor.Exhausted || foreignCursor.State != nil {
					t.Fatal("another source occurrence adopted the cursor", err, foreignCursor)
				}
				canceledCursor, err := n.OpenSource(&evaluation)
				if err != nil {
					t.Fatal(err)
				}
				canceled, cancel := context.WithCancel(t.Context())
				cancel()
				if _, _, err = n.AdvanceSource(canceled, &evaluation, &canceledCursor); !errors.Is(err, context.Canceled) || !canceledCursor.Exhausted || canceledCursor.State != nil {
					t.Fatal("canceled pull retained source state or entered a body", err, canceledCursor)
				}
			}
			if n.Ref.Epoch != retained.Epoch() || n.Procedure != retained.Get(n.Ref.Address) || n.CallRoutineReference() != n.Procedure {
				t.Fatal("invocation mutated retained descriptor/epoch")
			}
			stale := evaluation
			stale.RoutineChecks = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			if block {
				if _, err := n.Eval(&stale); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale scalar epoch gained authority", err)
				}
			} else {
				if _, err := n.OpenSource(&stale); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale wrapper epoch gained authority", err)
				}
			}
			if err := finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPgFreespaceWrapperParameterDemand(t *testing.T) {
	e, _, _ := freespaceCaptureFixture(t)
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	bound, err := binder.BindFunctionSource(enginetest.FunctionCall("pg_freespace", enginetest.StringLiteral("-")))
	if err != nil {
		t.Fatal(err)
	}
	n := bound.(plan.InstalledPgFreespaceExpr)
	retained := e.CatalogRead()
	originalManifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	regclass := n.Args[0].ResultUsage().Type
	children := []expr.Expr{plan.ParameterExpr{Info: expr.Info{Usage: regclass.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}}
	info := expr.InfoOf(n)
	info.Property = expr.MergeProperties(info.Property, children[0].Properties())
	rebuilt, err := n.Rebuild(info, children)
	if err != nil {
		t.Fatal(err)
	}
	n = rebuilt.(plan.InstalledPgFreespaceExpr)
	original := n
	if retained.Get(n.Ref.Address) != n.Procedure {
		t.Fatal("binding did not retain its genuine original catalog")
	}
	for _, q := range []string{
		"CREATE ROLE freespace_reader",
		"GRANT USAGE ON SCHEMA public TO freespace_reader",
		"ALTER FUNCTION pg_freespace(regclass,bigint) RENAME TO freespace_scalar",
		"ALTER FUNCTION pg_freespace(regclass) RENAME TO freespace_wrapper",
		"GRANT EXECUTE ON FUNCTION freespace_wrapper(regclass) TO freespace_reader",
	} {
		enginetest.Run(t, e, q)
	}
	query := enginetest.Statement(t, "SELECT * FROM freespace_wrapper($1::regclass)")
	s := enginetest.StartSession(t, e, nil)
	// Source reconstruction is independent of retained-recipe admission. Its
	// real command owner must win over an opposite lexical parameter policy.
	reconstruct := func(scope *core.StatementScope, source *exec.Source, generic bool) (exec.Context, error) {
		if source.Authority == nil || source.Authority.PreserveParameterDemand != generic {
			return exec.Context{}, fmt.Errorf("source lost admitted invocation policy")
		}
		state := exec.State{
			Source: source, Parameters: []sqltype.Value{sqltype.Null(regclass)}, Input: scope.Input, Output: scope.Output,
			Outer: &exec.Context{EvalStatement: exec.EvalStatement{Catalog: retained, PreserveParameterDemand: !generic}},
		}
		evaluation := state.Evaluation(t.Context())
		if evaluation.PreserveParameterDemand != generic {
			return exec.Context{}, fmt.Errorf("query reconstruction borrowed outer policy")
		}
		// Detaching a genuine source changes its live capabilities, not its
		// command policy. A derived copy borrows that same authority slot.
		detached, detachErr := func() (exec.Context, error) {
			host := source.Authority.Host
			source.Authority.Host = nil
			defer func() { source.Authority.Host = host }()
			derived := *source
			evaluation := (&exec.State{Source: &derived}).Evaluation(t.Context())
			if evaluation.LiveHost() != nil {
				return exec.Context{}, fmt.Errorf("detached shared authority retained a live host")
			}
			return evaluation, nil
		}()
		if detachErr != nil {
			return exec.Context{}, detachErr
		}
		if detached.PreserveParameterDemand != generic {
			return exec.Context{}, fmt.Errorf("detachment changed invocation policy")
		}
		nested := (&exec.State{Outer: &evaluation}).Evaluation(t.Context())
		if nested.PreserveParameterDemand != generic || nested.Outer != &evaluation {
			return exec.Context{}, fmt.Errorf("nested evaluation lost lexical policy ownership")
		}
		return evaluation, nil
	}
	for _, phase := range []string{"outer_only", "both", "neither"} {
		switch phase {
		case "both":
			enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION freespace_scalar(regclass,bigint) TO freespace_reader")
		case "neither":
			enginetest.Run(t, e, "REVOKE EXECUTE ON FUNCTION freespace_scalar(regclass,bigint) FROM freespace_reader")
			enginetest.Run(t, e, "REVOKE EXECUTE ON FUNCTION freespace_wrapper(regclass) FROM freespace_reader")
		}
		for _, generic := range []bool{false, true} {
			mode := "force_custom_plan"
			if generic {
				mode = "force_generic_plan"
			}
			enginetest.SessionSQL(t, s, "SET plan_cache_mode="+mode)
			// Real parameterized wrapper startup owns the command's policy.
			// Eager FinalRows deliberately detach their authority; borrow the
			// original command slot through the existing source-capture seam,
			// not by patching finalized output or installing a new authority.
			if err := func() (failure error) {
				op, err := s.Acquire(t.Context())
				if err != nil {
					return err
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					return err
				}
				defer func() { failure = scope.Finish(failure) }()
				result, err := scope.Execute(query, 1, func(_ context.Context, signature analyze.ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
					frame[0] = sqltype.Null(signature.Types[0].Type)
					return nil
				})
				if result.Rows != nil {
					defer result.Rows.Close()
				}
				if err != nil {
					return err
				}
				h := visibilityHost(scope)
				h.ReacquireOwnerEngine()
				source, err := e.CaptureQuerySourcesLocked(scope, nil, true)
				genuine := source != nil && source.Authority != nil && source.Authority.Host == core.HostOf(scope)
				h.ReleaseOwnerEngineLocked()
				if source != nil {
					defer source.Close()
				}
				if err != nil {
					return err
				}
				if !genuine {
					return fmt.Errorf("source did not borrow the real wrapper command authority")
				}
				_, err = reconstruct(scope, source, generic)
				return err
			}(); err != nil {
				t.Fatal("production wrapper source ownership", phase, mode, err)
			}
			enginetest.SessionSQL(t, s, "SET SESSION AUTHORIZATION freespace_reader")
			failure := func() (failure error) {
				scope, finish := visibilityScope(t, e, s)
				defer func() { failure = finish(failure) }()
				h := visibilityHost(scope)
				h.ReacquireOwnerEngine()
				auth, err := scope.RoutineAuthorizationLocked()
				// Like TestPgFreespaceAdmittedIdentity, this private fixture
				// supplies the original binding manifest and current principal
				// separately. Preserve original revisions, admitting only refs
				// from this owner's current catalog; no source/scope manifest
				// is patched after capture to masquerade as fresh SQL.
				checks := append([]catalog.ExpressionDependency(nil), originalManifest...)
				if err == nil {
					for i := range checks {
						checks[i].Ref = auth.Ref(checks[i].Ref.Address)
					}
				}
				var source *exec.Source
				if err == nil {
					source, err = e.CaptureQuerySourcesLocked(scope, nil, true)
				}
				h.ReleaseOwnerEngineLocked()
				if source != nil {
					defer source.Close()
				}
				if err != nil {
					return err
				}
				reconstructed, err := reconstruct(scope, source, generic)
				if err != nil {
					return err
				}
				// Initialize authenticated recipe admission before invoking the
				// original carrier. SELECT 1 supplied only the command policy,
				// not a stored recipe or a fresh binding of this old declaration.
				evaluation := exec.Context{
					EvalStatement: reconstructed.EvalStatement, EvalFrame: reconstructed.EvalFrame,
					EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: checks},
				}
				// The command source owns its initializer/header snapshot. This
				// separately admitted retained recipe owns its original catalog;
				// neither may overwrite the other's immutable carrier.
				evaluation.Catalog = retained
				if evaluation.Catalog.Get(n.Ref.Address) != original.Procedure {
					return fmt.Errorf("reconstruction lost the original retained catalog")
				}
				if _, changed, err := exec.FoldStrictParameterNull(&evaluation, n); err != nil || changed {
					return fmt.Errorf("non-STRICT wrapper eliminated: changed=%t err=%v", changed, err)
				}
				cursor, err := n.OpenSource(&evaluation)
				defer cursor.Close()
				if err == nil {
					var present bool
					_, present, err = n.AdvanceSource(t.Context(), &evaluation, &cursor)
					if err == nil && (present || !cursor.Exhausted || cursor.State != nil) {
						return fmt.Errorf("NULL wrapper did not consume an empty detached source")
					}
				}
				want := ""
				if phase == "neither" {
					want = "permission denied for function freespace_wrapper"
				} else if phase == "outer_only" && generic {
					want = "permission denied for function freespace_scalar"
				}
				if want != "" {
					var diagnostic *sqlstate.Error
					context := ""
					if phase == "outer_only" {
						context = "SQL function \"freespace_wrapper\" statement 1"
					}
					if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != want || diagnostic.Context != context {
						return fmt.Errorf("wrapper authority demand: want=%q context=%q err=%v", want, context, err)
					}
				} else if err != nil || cursor.Stop != 0 || cursor.Type != n.ResultUsage().Type {
					return fmt.Errorf("custom elimination/authorized generic empty: cursor=%v err=%v", cursor, err)
				}
				if !reflect.DeepEqual(n, original) || n.CallRoutineReference() != original.Procedure || !reflect.DeepEqual(binder.Dependencies, originalManifest) {
					return fmt.Errorf("invocation mutated the original carrier or lifetime manifest")
				}
				return nil
			}()
			enginetest.SessionSQL(t, s, "RESET SESSION AUTHORIZATION")
			if failure != nil {
				t.Fatal("retained wrapper parameter demand", phase, mode, failure)
			}
		}
	}
}
