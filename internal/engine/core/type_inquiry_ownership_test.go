package core_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Stable inquiry results are deliberately not retained constants. Their demanded
// immutable child is the distinguishing real fold-publication witness.
const typeDescribeOwnedQuery = "SELECT pg_typeof(COALESCE(-(7::integer),0)),format_type(23,COALESCE(-(7::integer),0)),pg_basetype(23::oid::regtype),pg_typeof(COALESCE('x'::text,'y'::text)::unknown::anyelement::unknown),pg_typeof(-(11::integer)),pg_typeof(VARIADIC 1),pg_basetype(VARIADIC 23::oid),format_type(23,VARIADIC NULL::integer),pg_typeof(COALESCE('x'::text,'y'::text)::unknown::anyelement::unknown::text),pg_typeof('x'::unknown::anycompatible::unknown::text)"

func TestTypeDescribePreparedOwnership(t *testing.T) {
	e := engine.New()
	t.Run("SelectedReentry", func(t *testing.T) {
		selectedEngine := engine.New()
		selectedSession := enginetest.StartSession(t, selectedEngine, nil)
		enginetest.SessionSQL(t, selectedSession, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
		enginetest.SessionSQL(t, selectedSession, "CREATE FUNCTION public.pg_typeof(text) RETURNS regtype LANGUAGE SQL AS $$SELECT 'integer'::regtype$$")
		binder := enginetest.ExpressionBinder(t, context.Background(), selectedEngine, sqltype.InputEnvironment{}, nil)
		bind := func(query string) expr.Expr {
			t.Helper()
			raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
			node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			return node
		}
		for _, query := range []string{"SELECT pg_catalog.pg_typeof('x'::anyelement)", "SELECT pg_catalog.pg_typeof(pg_catalog.pg_typeof('x'::anyelement))", "SELECT pg_catalog.pg_typeof('x'::unknown::anyelement)", "SELECT pg_catalog.pg_typeof(pg_catalog.pg_typeof('x'::unknown::anyelement))"} {
			node := bind(query)
			captured := 0
			expr.Walk(node, plan.ExpressionChildren, func(child expr.Expr) {
				if literal, ok := child.(plan.LiteralExpr); ok && literal.UnknownInput != nil {
					captured++
					if literal.UnknownInput.CastType == nil || literal.UnknownInput.CastType.ID() != 2283 {
						t.Fatal("selected reentry lost resolved pseudo authority")
					}
				}
			})
			if captured != 1 {
				t.Fatal("nested selection lost or duplicated unknown input occurrence", captured)
			}
		}
		for _, query := range []string{"SELECT pg_catalog.pg_typeof('x'::text::unknown)", "SELECT pg_catalog.pg_typeof(pg_catalog.pg_typeof('x'::bpchar(4)::unknown))", "SELECT pg_catalog.pg_typeof('x'::text::unknown::anyelement::unknown)", "SELECT pg_catalog.pg_typeof(pg_catalog.pg_typeof('x'::bpchar(4)::unknown::unknown))"} {
			node := bind(query)
			captured := 0
			expr.Walk(node, plan.ExpressionChildren, func(child expr.Expr) {
				if c, ok := child.(plan.CoercionExpr); ok && c.UnknownInputCast != nil {
					captured++
					logical, err := c.LogicalInputCast()
					if err != nil || logical.Target.Type.ID() != sqltype.Unknown || !c.Plan.IsUnknownInputTransport() {
						t.Fatal("selected reentry lost IO authority", err)
					}
				}
			})
			if captured != 1 {
				t.Fatal("nested selected IO carrier lost or duplicated source", captured)
			}
		}
		for _, tc := range []struct {
			query string
			oid   ids.OID
		}{
			{"SELECT pg_catalog.pg_typeof(VARIADIC 1)", 1619},
			{"SELECT pg_catalog.pg_basetype(VARIADIC 23::oid)", 6315},
			{"SELECT pg_catalog.format_type(23,VARIADIC NULL::integer)", 1081},
		} {
			node := bind(tc.query)
			declaration, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || declaration == nil || declaration.Address().ObjectID != tc.oid {
				t.Fatal("fixed VARIADIC lost genuine selected provider", tc.oid, err)
			}
		}
		user := bind("SELECT public.pg_typeof('x'::anyelement)")
		if _, ok := user.(plan.SQLRoutineExpr); !ok {
			t.Fatal("user routine winner was replaced by builtin inquiry")
		}
		expr.Walk(user, plan.ExpressionChildren, func(child expr.Expr) {
			if literal, ok := child.(plan.LiteralExpr); ok && literal.UnknownInput != nil {
				t.Fatal("builtin logical witness leaked into user routine arguments")
			}
		})
	})
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("td_owner", typeDescribeOwnedQuery, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("td_owner")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual definition/analysis owner")
	}
	owner := definition.Description.Folds
	if len(owner.Current().Entries) != 0 || owner.Current().Generation != 0 {
		t.Fatal("Parse evaluated children")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "td_owner", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("td_first")
	first := registry.Binding("td_first")
	accepted := owner.Current()
	if first == nil || first.Folds == nil || len(accepted.Entries) == 0 || first.Entry.Description.Folds != owner || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("binding did not consume exact accepted generation/backing")
	}
	retained := append([]exec.FoldConstant(nil), accepted.Entries...)
	type acceptedIOCast struct {
		marker  *plan.UnknownInputCast
		operand *expr.Expr
		source  sqltype.Usage
		target  *sqltype.Descriptor
		chain   *expr.TransparentInputCast
	}
	ioBacking := map[*core.PreparedBinding]acceptedIOCast{}
	assertGraph := func(binding *core.PreparedBinding) {
		t.Helper()
		if binding.Resource == nil || binding.Resource.Root == nil || len(binding.Resource.Root.Expressions) != 10 {
			t.Fatal("missing accepted private graph")
		}
		for i, node := range binding.Resource.Root.Expressions {
			declaration, err := exec.ExpressionRoutineDescriptor(node)
			want := []ids.OID{1619, 1081, 6315, 1619, 1619, 1619, 6315, 1081, 1619, 1619}[i]
			if err != nil || declaration == nil || declaration.Address().ObjectID != want || node.Properties().Volatility != expr.Stable {
				t.Fatal("selected provider authority/properties lost", i, err)
			}
			if _, literal := node.(plan.LiteralExpr); literal {
				t.Fatal("stable inquiry result was retained")
			}
			if i >= 8 {
				// Execution-private folding may replace the identity wrapper;
				// the accepted inquiry must still validate concrete logical25.
				children := node.(expr.Composite).Children()
				if len(children) != 1 || children[0].ResultUsage().Type.ID() != sqltype.Text {
					t.Fatal("accepted TEXT exit lost concrete operand", i)
				}
				if err := node.(interface{ ValidateStoredProvider() error }).ValidateStoredProvider(); err != nil {
					t.Fatal("accepted TEXT exit lost logical/provider generation", i, err)
				}
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
				value, err := node.Eval(&evaluation)
				oid, oidErr := value.TypeOID()
				if err != nil || oidErr != nil || oid != 25 {
					t.Fatal("accepted private generation lost concrete logical25", i, oid, err, oidErr)
				}
			}
			if i == 3 {
				children := node.(expr.Composite).Children()
				transport, ok := children[0].(plan.CoercionExpr)
				if !ok || transport.UnknownInputCast == nil || !transport.Plan.IsUnknownInputTransport() {
					t.Fatal("accepted graph lost StringIO plan")
				}
				logical, err := transport.LogicalInputCast()
				if err != nil || logical.Target.Type.ID() != sqltype.Unknown || logical.Source.ResultUsage() != transport.UnknownInputCast.Source {
					t.Fatal("accepted IO graph lost exact source/target authority", err)
				}
				if len(logical.Transparent) != 2 || logical.Transparent[0].Target.ID() != 2283 || logical.Transparent[1].Target.ID() != sqltype.Unknown {
					t.Fatal("accepted generation lost composed logical authority")
				}
				if saved, ok := ioBacking[binding]; ok {
					if saved.marker != transport.UnknownInputCast || saved.operand != &children[0] || saved.source != transport.UnknownInputCast.Source || saved.target != transport.UnknownInputCast.Target || saved.chain != &transport.UnknownInputCast.Transparent[0] {
						t.Fatal("rejected publication replaced accepted IO backing")
					}
				} else {
					ioBacking[binding] = acceptedIOCast{transport.UnknownInputCast, &children[0], transport.UnknownInputCast.Source, transport.UnknownInputCast.Target, &transport.UnknownInputCast.Transparent[0]}
				}
			}
		}
		found := false
		// COALESCE intentionally relabels its chosen child with the conditional
		// occurrence. The separate direct demanded fold keeps the actual retained
		// computation's source/occurrence, without dropping conditional coverage.
		expr.Walk(binding.Resource.Root.Expressions[4], plan.ExpressionChildren, func(operand expr.Expr) {
			if literal, ok := operand.(plan.LiteralExpr); ok {
				for _, entry := range accepted.Entries {
					if literal.Source == entry.Source && literal.Occurrence == entry.Occurrence && literal.Value == entry.Value && literal.ResultUsage() == entry.Result {
						found = true
					}
				}
			}
		})
		if !found {
			t.Fatal("actual inquiry child lost retained fold operand/provenance")
		}
	}
	assertGraph(first)
	firstRoot := first.Resource.Root
	bind("td_second")
	second := registry.Binding("td_second")
	if second == first || second.Resource == first.Resource || second.Resource.Root == firstRoot || second.Folds.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("repeated Bind shared mutable graph or replaced immutable backing")
	}
	assertGraph(second)
	t.Run("SemanticGeneration", func(t *testing.T) {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		baseline := definition.Description.Analysis
		integer := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
		successor := *integer
		successor.ObjectRevision = integer.Revision() + 1
		state := catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(successor.Address(), &successor)
		current := e.Catalog.Read(state)
		if baseline.Valid(current, environment) || !baseline.FoldsValid(current, environment) {
			t.Fatal("presentation stamp confused analysis with unchanged executable codec")
		}
		codec := *integer.SQLType
		successor.SQLType = &codec
		state = catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(successor.Address(), &successor)
		if baseline.FoldsValid(e.Catalog.Read(state), environment) {
			t.Fatal("same-OID replacement codec retained old executable generation")
		}
		if owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
			t.Fatal("semantic probes mutated accepted artifacts")
		}
	})
	for _, cause := range []string{"cancellation", "retirement", "generation"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			local := cloneFoldDescription(t, definition.Description)
			before := local.Folds.Current()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			environment, err := e.PreparedEnvironmentLocked(auth, true)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			publication, err := core.BeginFoldPublication(&local, definition.Body, environment, auth.Catalog, nil)
			if err != nil || publication == nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal("missing publication", err)
			}
			publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
			publication.Group.Private = true
			publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
			switch cause {
			case "cancellation":
				cancel()
			case "retirement":
				local.Retire()
			case "generation":
				local.Folds.Invalidate(true)
			}
			fenced := local.Folds.Current()
			err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
			e.Mu.Unlock()
			_ = scope.Finish(err)
			if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed {
				t.Fatal("rejected publication acquired rights", err)
			}
			after := local.Folds.Current()
			if after.Generation != fenced.Generation || &after.Entries[0] != &before.Entries[0] || !reflect.DeepEqual(after.Entries, retained) || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] || first.Resource.Root != firstRoot {
				t.Fatal("failed publication changed accepted artifacts")
			}
			assertGraph(first)
			assertGraph(second)
		})
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("td_owner") })
	if owner.Live() || first.Retired || second.Retired || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("definition retirement mutated accepted binding")
	}
	assertGraph(first)
	assertGraph(second)
	for _, name := range []string{"td_first", "td_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !first.Retired || !second.Retired || !reflect.DeepEqual(accepted.Entries, retained) {
		t.Fatal("terminal detachment mutated saved immutable artifacts")
	}
	typeDescribeFinishCycle(t, e, s, cycle)
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

func TestTypeDescribeConcurrentReuse(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("td_concurrent", typeDescribeOwnedQuery, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	contexts := make([]exec.Context, 2)
	candidates := make([]exec.FoldCaptureCandidate, 2)
	bindings := make([]*core.PreparedBinding, 2)
	for i, name := range []string{"td_left", "td_right"} {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "td_concurrent", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		bindings[i] = registry.Binding(name)
		producer := core.PortalProducer(core.ProtocolPortalOf(bindings[i]))
		if producer == nil {
			t.Fatal("accepted portal has no actual producer")
		}
		state := core.CursorState(producer)
		contexts[i] = state.Evaluation(context.Background())
		candidates[i] = bindings[i].Folds.Candidate()
		contexts[i].FoldCaptures = &candidates[i]
	}
	owner := registry.Lookup("td_concurrent").Description.Folds
	accepted := owner.Current()
	if len(accepted.Entries) == 0 || bindings[0].Resource.Root == bindings[1].Resource.Root || contexts[0].Query == contexts[1].Query || contexts[0].Query.Source == contexts[1].Query.Source || contexts[0].FoldCaptures == contexts[1].FoldCaptures {
		t.Fatal("accepted invocations share mutable graph, frame or capture candidate")
	}
	before := append([]exec.FoldConstant(nil), accepted.Entries...)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for i := range contexts {
		workers.Add(1)
		go func(i int, local exec.Context) {
			defer workers.Done()
			<-start
			// Both actual invocation frames borrow the same immutable accepted
			// pg_typeof node. Its sibling catalog calls retain their own graph;
			// detached row contexts cannot recapture statement catalog authority.
			for range 8 {
				for _, index := range []int{0, 3, 4} {
					if _, err := bindings[0].Resource.Root.Expressions[index].Eval(&local); err != nil {
						results <- err
						return
					}
				}
			}
			results <- nil
		}(i, contexts[i])
	}
	close(start)
	workers.Wait()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(accepted.Entries, before) {
		t.Fatal("parallel invocation published or mutated accepted fold backing")
	}
	for _, binding := range bindings {
		if binding.Folds.Generation != accepted.Generation || &binding.Folds.Entries[0] != &accepted.Entries[0] {
			t.Fatal("parallel invocation adopted a private fold candidate")
		}
	}
	for _, name := range []string{"td_left", "td_right"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("td_concurrent") })
	typeDescribeFinishCycle(t, e, s, cycle)
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

func typeDescribeFinishCycle(t *testing.T, e *core.Engine, s *session.Session, cycle *session.ProtocolCycle) {
	t.Helper()
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
