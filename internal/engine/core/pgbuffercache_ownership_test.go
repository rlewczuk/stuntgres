package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
)

// These witnesses inspect actual retained protocol graph/source owners. They do
// not create substitute providers, fake source inspection APIs or SQL goldens.
func bufferUnitPortal(t *testing.T, e *core.Engine, s *session.Session, name, query string) (*core.ProtocolPortal, *exec.Source, func(func(*core.StatementScope) error)) {
	t.Helper()
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol(name, query, nil); return err })
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal(name, name, &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	binding := core.PayloadOf(s).PreparedSQL.Binding(name)
	portal := core.ProtocolPortalOf(binding)
	if portal == nil || binding.Resource == nil || binding.Resource.Root == nil {
		t.Fatal("real accepted portal lacks private graph")
	}
	command(func(scope *core.StatementScope) error {
		result, err := scope.ExecuteProtocolPortal(name, 1, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		if err == nil && !result.Suspended {
			t.Fatal("actual portal did not suspend")
		}
		return err
	})
	producer := core.PortalProducer(portal)
	if producer == nil || producer.Source == nil {
		t.Fatal("suspended portal lost capture owner")
	}
	return portal, producer.Source, command
}
func bufferUnitReleased(t *testing.T, source *exec.Source) {
	t.Helper()
	if source.ReadLease != nil || source.View != nil || source.Host != nil || source.Authority != nil || source.NestedStage != nil || source.NestedRoot != nil || source.Sources != nil || source.HashIndexes != nil || source.BloomIndexes != nil {
		t.Fatal("terminal source retained borrowed/live resources")
	}
}

func TestPgBuffercacheSourceOwnership(t *testing.T) {
	t.Run("two_sessions_and_same_session_aliases", func(t *testing.T) {
		e := bufferUnitEngine(t)
		for _, q := range []string{"CREATE TABLE buffer_pins(k integer)", "INSERT INTO buffer_pins VALUES(1),(2)"} {
			enginetest.Run(t, e, q)
		}
		a, b, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		node := bufferUnitNode(t, e, "buffer_pins")
		_, left, closeA := bufferUnitPortal(t, e, a, "buffer_a", "SELECT a.k FROM buffer_pins a CROSS JOIN buffer_pins b")
		if len(left.Sources) != 2 || left.Sources[0].ReadLease == nil || left.Sources[1].ReadLease == nil || left.Sources[0].ReadLease == left.Sources[1].ReadLease {
			t.Fatal("real aliases share an invocation/capture")
		}
		alias0, alias1 := left.Sources[0], left.Sources[1]
		bufferUnitFact(t, e, observer, node, 2, 1)
		_, right, closeB := bufferUnitPortal(t, e, b, "buffer_b", "SELECT k FROM buffer_pins")
		bufferUnitFact(t, e, observer, node, 3, 2)
		detached := bufferUnitCursor(t, e, observer)
		defer detached.Close()
		closeA(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("buffer_a") })
		bufferUnitReleased(t, left)
		bufferUnitReleased(t, alias0)
		bufferUnitReleased(t, alias1)
		bufferUnitFact(t, e, observer, node, 3, 1)
		closeB(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("buffer_b") })
		bufferUnitReleased(t, right)
		bufferUnitFact(t, e, observer, node, 3, 0)
		found := false
		for {
			row, present, err := detached.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				break
			}
			if row.CompositeField(1).IsNull() {
				continue
			}
			oid, err := row.CompositeField(1).OID()
			if err != nil {
				t.Fatal(err)
			}
			if uint32(node) == oid {
				pins, err := row.CompositeField(8).Integer()
				if err != nil || pins != 2 {
					t.Fatal("snapshot retained mutable lease state", pins, err)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("retained snapshot lost occupied scalar identity")
		}
		enginetest.SessionSQL(t, a, "ROLLBACK")
		enginetest.SessionSQL(t, b, "ROLLBACK")
	})
	for _, ending := range []string{"execution_error", "cancellation", "session_end"} {
		t.Run(ending, func(t *testing.T) {
			e := bufferUnitEngine(t)
			for _, q := range []string{"CREATE TABLE buffer_terminal(k integer)", "INSERT INTO buffer_terminal VALUES(1),(2)"} {
				enginetest.Run(t, e, q)
			}
			owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			portal, source, command := bufferUnitPortal(t, e, owner, "buffer_terminal", "SELECT k FROM buffer_terminal")
			root := source
			node := bufferUnitNode(t, e, "buffer_terminal")
			bufferUnitFact(t, e, observer, node, 1, 1)
			if ending == "execution_error" {
				failure := errors.New("buffercache source consumer failure")
				var observed error
				command(func(scope *core.StatementScope) error {
					_, observed = scope.ExecuteProtocolPortal("buffer_terminal", 0, func([]session.ProtocolField) error { return failure }, nil, nil, core.CopyProtocolHooks{})
					return nil
				})
				if !errors.Is(observed, failure) {
					t.Fatal("actual producer failure was replaced", observed)
				}
				bufferUnitReleased(t, root)
				bufferUnitFact(t, e, observer, node, 1, 0)
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("buffer_terminal") })
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			} else if ending == "cancellation" {
				ctx, cancel := context.WithCancel(context.Background())
				op, _, err := owner.AcquireCommand(ctx)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				scope, err := e.StartProtocolCommand(op, owner.Transactions.ProtocolCycle)
				if err != nil {
					op.Done()
					cancel()
					t.Fatal(err)
				}
				_, err = scope.ExecuteProtocolPortal("buffer_terminal", 0, func([]session.ProtocolField) error { cancel(); return context.Canceled }, nil, nil, core.CopyProtocolHooks{})
				finished := scope.Finish(err)
				op.Done()
				cancel()
				if !errors.Is(err, context.Canceled) || !errors.Is(finished, context.Canceled) {
					t.Fatal("admitted cancellation lost ordinary boundary", err, finished)
				}
				bufferUnitReleased(t, root)
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			} else {
				owner.RequestEnd(session.CleanClose)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := owner.WaitEnd(ctx)
				cancel()
				if err != nil {
					t.Fatal("registered physical session did not release portal", err)
				}
			}
			bufferUnitReleased(t, root)
			if core.PortalProducer(portal) != nil {
				t.Fatal("terminal portal retained producer")
			}
			bufferUnitFact(t, e, observer, node, 1, 0)
		})
	}
}

func incomingVPCSource(t *testing.T, query string) (*core.StatementScope, expr.Expr, *exec.Context) {
	t.Helper()
	e := sepgsqlInitializationEngine(t)
	enginetest.Run(t, e, "CREATE EXTENSION pg_buffercache")
	enginetest.Run(t, e, "CREATE EXTENSION pg_visibility")
	enginetest.Run(t, e, "INSERT INTO init_owner_rows VALUES(9)")
	_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, query)
	if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
		t.Fatal(err)
	}
	from, ok := project.Child.(*plan.FunctionFromNode)
	if !ok || len(from.Calls) != 1 {
		t.Fatal("missing actual incoming source", project.Child)
	}
	return scope, from.Calls[0], evaluation
}

func TestSepgsqlSimulationIncomingVPC(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		row         bool
	}{
		{"buffer", "SELECT bufferid FROM pg_buffercache_pages() AS b(bufferid integer,node oid,space oid,db oid,fork smallint,block bigint,dirty boolean,usage smallint,pins integer)", true},
		{"map", "SELECT blkno FROM pg_visibility_map('init_owner_rows')", true},
		{"page", "SELECT blkno FROM pg_visibility('init_owner_rows')", true},
		{"visible", "SELECT t_ctid FROM pg_check_visible('init_owner_rows')", false},
		{"frozen", "SELECT t_ctid FROM pg_check_frozen('init_owner_rows')", false},
		{"truncate", "SELECT value FROM pg_truncate_visibility_map('init_owner_rows') AS t(value)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, call, evaluation := incomingVPCSource(t, tc.query)
			open := call.(expr.SourceOpening)
			advance := call.(expr.SourceAdvancing)
			site := sepgsqlInitializationSite(t, evaluation, call)
			mark := scope.MarkSepgsqlObservations()
			cursor, err := open.OpenSource(evaluation)
			if err != nil {
				t.Fatal("argument-only open", err)
			}
			defer cursor.Close()
			if cursor.Values != nil || scope.MarkSepgsqlObservations() != mark {
				t.Fatal("metadata open performed body work", cursor)
			}
			if _, present, err := advance.AdvanceSource(t.Context(), evaluation, &cursor); err != nil || present != tc.row {
				t.Fatal("first actual row or DONE", present, tc.row, err)
			}
			if scope.Op.Session.Sepgsql.Current() != "A" {
				t.Fatal("first invocation leaked its subject")
			}
			if _, err := core.HostOf(scope).ValidateRoutineInitialization(evaluation.RoutineInitialization, site); err != nil {
				t.Fatal("actual invocation consumed reusable initialization", err)
			}
			if !tc.row && (!cursor.Exhausted || cursor.State != nil) {
				t.Fatal("empty actual DONE retained snapshot", cursor)
			}
			if tc.row && tc.name != "truncate" {
				lost := *evaluation
				lost.RoutineInitialization = exec.RoutineInitialization{}
				if _, _, err := advance.AdvanceSource(t.Context(), &lost, &cursor); err == nil || !cursor.Exhausted || cursor.State != nil {
					t.Fatal("actual next row/DONE reused retained first-call authority", err, cursor)
				}
			}
			cursor.Close()
			if _, present, err := advance.AdvanceSource(t.Context(), nil, &cursor); err != nil || present || cursor.State != nil {
				t.Fatal("closed source attempted to borrow an evaluator", present, err)
			}
			cursor, err = open.OpenSource(evaluation)
			if err != nil {
				t.Fatal(err)
			}
			lost := *evaluation
			lost.RoutineInitialization = exec.RoutineInitialization{}
			if _, _, err := advance.AdvanceSource(t.Context(), &lost, &cursor); err == nil || !cursor.Exhausted || cursor.State != nil {
				t.Fatal("missing owner adopted live scope", err, cursor)
			}
			cursor, err = open.OpenSource(evaluation)
			if err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := advance.AdvanceSource(canceled, nil, &cursor); !errors.Is(err, context.Canceled) || !cursor.Exhausted || cursor.State != nil {
				t.Fatal("pre-canceled demand entered body or retained inputs", err, cursor)
			}
			cursor, err = open.OpenSource(evaluation)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, foreign := sepgsqlInitializationBinding(t, sepgsqlInitializationEngine(t), "SELECT abs(v) FROM init_owner_rows")
			if _, _, err := advance.AdvanceSource(t.Context(), foreign, &cursor); err == nil || !cursor.Exhausted || cursor.State != nil {
				t.Fatal("foreign evaluator adopted original source", err, cursor)
			}
			cursor, err = open.OpenSource(evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if _, _, err := advance.AdvanceSource(t.Context(), evaluation, &cursor); err == nil || !cursor.Exhausted || cursor.State != nil {
				t.Fatal("finished statement supplied first-call authority", err, cursor)
			}
		})
		for _, first := range []bool{false, true} {
			name := tc.name + "/retired_before_first"
			if first {
				name = tc.name + "/retired_after_first"
			}
			t.Run(name, func(t *testing.T) {
				scope, call, evaluation := incomingVPCSource(t, tc.query)
				advance := call.(expr.SourceAdvancing)
				cursor, err := call.(expr.SourceOpening).OpenSource(evaluation)
				if err != nil {
					t.Fatal(err)
				}
				defer cursor.Close()
				if first {
					if _, present, err := advance.AdvanceSource(t.Context(), evaluation, &cursor); err != nil || present != tc.row {
						t.Fatal("actual pre-retirement demand", present, err)
					}
				}
				extension := "pg_visibility"
				if tc.name == "buffer" {
					extension = "pg_buffercache"
				}
				enginetest.Run(t, scope.Engine(), "DROP EXTENSION "+extension+" CASCADE")
				_, present, err := advance.AdvanceSource(t.Context(), evaluation, &cursor)
				detached := first && (!tc.row || tc.name == "truncate")
				if present || (!detached && err == nil) || (detached && err != nil) || !cursor.Exhausted || cursor.State != nil {
					t.Fatal("retired routine supplied new authority or detached tail lost EOF", first, detached, present, err, cursor)
				}
			})
		}
	}
	for _, tc := range []struct {
		name, query string
		scalar      bool
	}{
		{"map", "SELECT blkno FROM pg_visibility_map(NULL::regclass)", false},
		{"page", "SELECT blkno FROM pg_visibility(NULL::regclass)", false},
		{"visible", "SELECT t_ctid FROM pg_check_visible(NULL::regclass)", false},
		{"frozen", "SELECT t_ctid FROM pg_check_frozen(NULL::regclass)", false},
		{"truncate", "SELECT result FROM pg_truncate_visibility_map(NULL::regclass) AS t(result)", true},
	} {
		t.Run(tc.name+"/strict_null_detached", func(t *testing.T) {
			scope, call, evaluation := incomingVPCSource(t, tc.query)
			mark := scope.MarkSepgsqlObservations()
			cursor, err := call.(expr.SourceOpening).OpenSource(evaluation)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			advance := call.(expr.SourceAdvancing)
			value, present, err := advance.AdvanceSource(t.Context(), nil, &cursor)
			if err != nil || present != tc.scalar || tc.scalar && (!value.IsNull() || value.Type() != call.ResultUsage().Type) || cursor.State != nil || scope.MarkSepgsqlObservations() != mark {
				t.Fatal("strict NULL source borrowed authority or changed cardinality/type", value, present, err, cursor)
			}
			if _, present, err := advance.AdvanceSource(t.Context(), nil, &cursor); err != nil || present || !cursor.Exhausted || cursor.State != nil {
				t.Fatal("strict NULL tail borrowed authority", present, err, cursor)
			}
		})
	}
}

func TestPgBuffercacheBorrowedSnapshot(t *testing.T) {
	scope, call, evaluation := incomingVPCSource(t, "SELECT bufferid FROM pg_buffercache_pages() AS b(bufferid integer,node oid,space oid,db oid,fork smallint,block bigint,dirty boolean,usage smallint,pins integer)")
	e := scope.Engine()
	for _, query := range []string{
		"CREATE TABLE buffer_before_demand(k integer)",
		"CREATE TABLE buffer_after_demand(k integer)",
		"INSERT INTO buffer_before_demand VALUES(1)",
		"INSERT INTO buffer_after_demand VALUES(2)",
	} {
		enginetest.Run(t, e, query)
	}
	before := bufferUnitNode(t, e, "buffer_before_demand")
	after := bufferUnitNode(t, e, "buffer_after_demand")
	open := call.(expr.SourceOpening)
	advance := call.(expr.SourceAdvancing)
	cursor, err := open.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	if cursor.Position != -1 || cursor.State != nil || cursor.Values != nil {
		t.Fatal("metadata open captured buffer occupancy", cursor)
	}
	enginetest.Run(t, e, "SELECT k FROM buffer_before_demand")
	value, present, err := advance.AdvanceSource(t.Context(), evaluation, &cursor)
	if err != nil || !present || cursor.Position != 1 || cursor.State == nil || cursor.Values != nil {
		t.Fatal("first borrowed invocation did not capture and emit its first row", value, present, err, cursor)
	}
	enginetest.Run(t, e, "SELECT k FROM buffer_after_demand")
	var foundBefore, foundAfter bool
	var rows int64
	for {
		if !value.CompositeField(1).IsNull() {
			node, err := value.CompositeField(1).OID()
			if err != nil {
				t.Fatal(err)
			}
			foundBefore = foundBefore || node == uint32(before)
			foundAfter = foundAfter || node == uint32(after)
		}
		rows++
		value, present, err = advance.AdvanceSource(t.Context(), evaluation, &cursor)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
	}
	if !foundBefore || foundAfter || rows <= 0 || !cursor.Exhausted || cursor.State != nil || cursor.Values != nil {
		t.Fatal("snapshot was eager, mutable, materialized, or retained after DONE", foundBefore, foundAfter, rows, cursor)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if _, present, err := advance.AdvanceSource(t.Context(), nil, &cursor); err != nil || present {
		t.Fatal("detached EOF requested finished-scope authority", present, err)
	}
}
