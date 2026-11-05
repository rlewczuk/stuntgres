package core_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func backendStateCount(t *testing.T, v sqltype.Value, want int64, overflow bool) {
	t.Helper()
	if v.IsNull() {
		t.Fatal("whole NULL instead of actual cache")
	}
	n, err := v.CompositeField(0).Integer()
	b, be := v.CompositeField(1).Boolean()
	if err != nil || be != nil || n != want || b != overflow {
		t.Fatal("canonical cache projection", n, b, want, overflow, err, be)
	}
}

func TestBackendStateRuntimeLifetime(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []string{"selected", "activity", "identity"} {
			t.Run(mode+"/"+first, func(t *testing.T) {
				e := engine.New()
				observer := enginetest.StartSession(t, e, nil)
				peer := enginetest.StartSession(t, e, nil)
				activityOwnerRun(t, e, observer, "CREATE TABLE bs_owner(v integer)", "CREATE ROLE bs_other", "SET stats_fetch_consistency="+mode, "BEGIN")
				activityOwnerRun(t, e, peer, "BEGIN", "SAVEPOINT parent", "SAVEPOINT child", "INSERT INTO bs_owner VALUES(1)")
				ref := peer.Transactions.Ref
				e.Mu.Lock()
				entry := e.Visibility.Entries[ref]
				e.Mu.Unlock()
				if entry.SubxactCount != 2 || entry.SubxactCache[0] != peer.Transactions.Savepoints[0].FullXID || entry.SubxactCache[1] != peer.Transactions.Savepoints[1].FullXID {
					t.Fatal("actual lazy ancestor assignment", entry)
				}
				birth, _ := e.Statistics.LiveActivity(peer.Token())
				shape := backendCurrentShape(t, e, 6107)
				textType := enginetest.Usage(e, sqltype.Text).Type
				intType := enginetest.Usage(e, sqltype.Int4).Type
				var old sqltype.Value
				backendCurrentBorrow(t, e, observer, func(_ *core.StatementScope, r exec.SessionInquiryRuntime) {
					if first == "activity" {
						if _, err := r.BackendActivity(sqltype.Null(intType), backendCurrentShape(t, e, 2022)); err != nil {
							t.Fatal(err)
						}
					}
					if first == "identity" {
						if _, err := r.BackendIdentityIDs(intType); err != nil {
							t.Fatal(err)
						}
					}
					var err error
					old, err = r.BackendStateValue(functions.BackendStateSubxact, int64(birth.BackendID), shape)
					if err != nil {
						t.Fatal(err)
					}
					backendStateCount(t, old, 2, false)
				})
				reader := reflect.ValueOf(core.PayloadOf(observer)).Elem().FieldByName("statsReader")
				if reader.FieldByName("activity").IsNil() || !reader.FieldByName("stamp").IsZero() {
					t.Fatal("partial or cumulative activity cut")
				}
				retained := enginetest.SessionSQL(t, observer, fmt.Sprintf("SELECT pg_stat_get_backend_subxact(%d)", birth.BackendID))
				if retained.Rows == nil {
					t.Fatal("missing actual detached row owner")
				}
				schema := retained.Rows.Schema()
				datum := make([]sqltype.Value, 1)
				if present, err := retained.Rows.Next(t.Context(), datum); err != nil || !present {
					t.Fatal("detached record row", present, err)
				}
				backendStateCount(t, datum[0], 2, false)
				activityOwnerRun(t, e, peer, "RELEASE child")
				e.Mu.Lock()
				released := e.Visibility.Entries[ref]
				e.Mu.Unlock()
				if released.SubxactCache != entry.SubxactCache || released.SubxactCount != 2 {
					t.Fatal("release changed cache")
				}
				activityOwnerRun(t, e, peer, "ROLLBACK TO parent")
				e.Mu.Lock()
				aborted := e.Visibility.Entries[ref]
				e.Mu.Unlock()
				if aborted.SubxactCount != 0 {
					t.Fatal("released canonical subtree survived abort", aborted)
				}
				activityOwnerRun(t, e, observer, "SET ROLE bs_other")
				backendCurrentBorrow(t, e, observer, func(_ *core.StatementScope, r exec.SessionInquiryRuntime) {
					v, err := r.BackendStateValue(functions.BackendStateSubxact, int64(birth.BackendID), shape)
					if err != nil {
						t.Fatal(err)
					}
					backendStateCount(t, v, 2, false)
					v, err = r.BackendStateValue(functions.BackendStateActivity, int64(birth.BackendID), textType)
					got, _ := v.Text()
					if err != nil || got != "<insufficient privilege>" {
						t.Fatal("cached raw facts froze observer authority", v, err)
					}
				})
				activityOwnerRun(t, e, observer, "RESET ROLE", "SELECT pg_stat_clear_snapshot()")
				backendCurrentBorrow(t, e, observer, func(_ *core.StatementScope, r exec.SessionInquiryRuntime) {
					v, err := r.BackendStateValue(functions.BackendStateSubxact, int64(birth.BackendID), shape)
					if err != nil {
						t.Fatal(err)
					}
					backendStateCount(t, v, 0, false)
				})
				activityOwnerRun(t, e, peer, "INSERT INTO bs_owner VALUES(2)")
				stale := peer.Transactions.Ref
				if err := engine.Reset(t.Context(), e); err != nil {
					t.Fatal(err)
				}
				if err := controlsExecute(t, peer, "ROLLBACK TO parent"); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale epoch retained child authority", err)
				}
				controlsRequire(t, peer, "ROLLBACK")
				controlsRequire(t, observer, "ROLLBACK")
				activityOwnerRun(t, e, peer, "BEGIN")
				successor := peer.Transactions.Ref
				e.Mu.Lock()
				e.Visibility.EndTransactionIdentityLocked(stale, false)
				fresh := e.Visibility.Entries[successor]
				e.Mu.Unlock()
				if fresh.SubxactCount != 0 || fresh.SubxactOverflowed || successor == stale {
					t.Fatal("stale completion touched successor")
				}
				controlsRequire(t, peer, "ROLLBACK")
				backendStateCount(t, old, 2, false)
				if !reflect.DeepEqual(retained.Rows.Schema(), schema) {
					t.Fatal("native replacement mutated detached row descriptor")
				}
				backendStateCount(t, datum[0], 2, false)
				if err := retained.Rows.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	t.Run("same_token_new_birth_wait", backendStateBirthWait)
	t.Run("accepted_prepared_backing", backendStatePreparedOwnership)
	t.Run("prepared_native_replacement", backendStatePreparedReplacement)
}

func backendStateBirthWait(t *testing.T) {
	e := engine.New()
	observer := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, observer, "BEGIN")
	birth, _ := e.Statistics.LiveActivity(peer.Token())
	typ := enginetest.Usage(e, sqltype.Text).Type
	backendCurrentBorrow(t, e, observer, func(_ *core.StatementScope, r exec.SessionInquiryRuntime) {
		if _, err := r.BackendStateValue(functions.BackendStateActivity, int64(birth.BackendID), typ); err != nil {
			t.Fatal(err)
		}
	})
	token := peer.Token()
	statsFunctionRetire(t, peer)
	replacement, err := enginetest.OpenSession(t.Context(), e, session.SessionStart{ID: token.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer statsFunctionRetire(t, replacement)
	current, _ := e.Statistics.LiveActivity(replacement.Token())
	if replacement.Token() != token || current.Incarnation == birth.Incarnation || current.BackendID != birth.BackendID {
		t.Fatal("not an actual same-token successor")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- activityOwnerExecute(e, replacement, ctx, "SELECT pg_sleep(3600)") }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("canceled successor worker did not join")
			}
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		a, _ := e.Statistics.LiveActivity(token)
		if a.WaitType == "Timeout" && a.Wait == "PgSleep" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successor did not publish actual wait")
		}
		runtime.Gosched()
	}
	backendCurrentBorrow(t, e, observer, func(_ *core.StatementScope, r exec.SessionInquiryRuntime) {
		for _, kind := range []functions.BackendStateKind{functions.BackendStateWaitEvent, functions.BackendStateWaitEventType} {
			v, err := r.BackendStateValue(kind, int64(birth.BackendID), typ)
			if err != nil || !v.IsNull() {
				t.Fatal("new physical birth supplied old cut's wait", v, err)
			}
		}
	})
	activityOwnerRun(t, e, observer, "SELECT pg_stat_clear_snapshot()")
	backendCurrentBorrow(t, e, observer, func(_ *core.StatementScope, r exec.SessionInquiryRuntime) {
		v, err := r.BackendStateValue(functions.BackendStateWaitEvent, int64(birth.BackendID), typ)
		got, _ := v.Text()
		if err != nil || got != "PgSleep" {
			t.Fatal("clear did not adopt genuine live successor", v, err)
		}
	})
	cancel()
	select {
	case err := <-done:
		joined = true
		if err == nil {
			t.Fatal("sleep unexpectedly completed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled successor worker did not join")
	}
	activityOwnerRun(t, e, observer, "ROLLBACK")
}

func TestBackendStateConcurrentCapture(t *testing.T) {
	e := engine.New()
	observer := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, observer, "CREATE TABLE bs_race(v integer)")
	shape := backendCurrentShape(t, e, 6107)
	for range 16 {
		peer := enginetest.StartSession(t, e, nil)
		activityOwnerRun(t, e, peer, "BEGIN", "SAVEPOINT parent", "SAVEPOINT child")
		birth, _ := e.Statistics.LiveActivity(peer.Token())
		for _, q := range []string{"INSERT INTO bs_race VALUES(1)", "RELEASE child", "ROLLBACK TO parent", "INSERT INTO bs_race VALUES(2)", "COMMIT"} {
			start, done := make(chan struct{}), make(chan error, 1)
			go func() {
				<-start
				err := activityOwnerExecute(e, peer, t.Context(), q)
				e.Mu.Lock()
				e.Statistics.Reset(time.Now())
				e.Statistics.Replace(time.Now(), nil)
				e.Mu.Unlock()
				done <- err
			}()
			close(start)
			var retained sqltype.Value
			var count int64
			var overflow bool
			backendCurrentBorrow(t, e, observer, func(scope *core.StatementScope, r exec.SessionInquiryRuntime) {
				if _, err := core.HostOf(scope).StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
					t.Fatal(err)
				}
				var err error
				retained, err = r.BackendStateValue(functions.BackendStateSubxact, int64(birth.BackendID), shape)
				if err != nil {
					t.Fatal(err)
				}
				var ce, be error
				count, ce = retained.CompositeField(0).Integer()
				overflow, be = retained.CompositeField(1).Boolean()
				if ce != nil || be != nil || count < 0 || count > 2 || overflow {
					t.Fatal("torn canonical capture", retained, ce, be)
				}
			})
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(q, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("canonical mutation worker did not join")
			}
			backendStateCount(t, retained, count, overflow)
		}
		start, done := make(chan struct{}), make(chan error, 1)
		go func() { <-start; peer.RequestEnd(session.CleanClose); done <- peer.WaitEnd(context.Background()) }()
		close(start)
		backendCurrentBorrow(t, e, observer, func(scope *core.StatementScope, r exec.SessionInquiryRuntime) {
			if _, err := core.HostOf(scope).StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
				t.Fatal(err)
			}
			v, err := r.BackendStateValue(functions.BackendStateSubxact, int64(birth.BackendID), shape)
			if err != nil {
				t.Fatal(err)
			}
			if !v.CompositeField(0).IsNull() {
				backendStateCount(t, v, 0, false)
			}
		})
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("physical retirement worker did not join")
		}
	}
}

func backendStatePreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("state_owned", preparedFoldPoint+",pg_stat_get_backend_subxact(0) AS live", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("state_owned")
	owner := definition.Description.Folds
	if owner == nil || !owner.Live() || owner.Current().Generation != 0 {
		t.Fatal("Parse lost actual definition owner")
	}
	reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
	if !reader.FieldByName("activity").IsNil() || !reader.FieldByName("stamp").IsZero() {
		t.Fatal("Prepare captured runtime state")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "state_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("state_first")
	first := registry.Binding("state_first")
	accepted := owner.Current()
	entries := append([]exec.FoldConstant(nil), accepted.Entries...)
	if len(entries) == 0 || first == nil || first.Resource == nil || first.Resource.Root == nil {
		t.Fatal("missing real accepted graph and folds")
	}
	root := first.Resource.Root
	analysis := definition.Description.Analysis
	call, ok := root.Expressions[1].(functions.BackendState)
	if !ok || call.Kind != functions.BackendStateSubxact || call.RoutineReference == nil || call.RoutineReference.RoutineOID() != 6107 || call.Usage.Type.CompositeLen() != 2 {
		t.Fatal("live record recipe folded or relabeled", root.Expressions[1])
	}
	if !reader.FieldByName("activity").IsNil() {
		t.Fatal("Bind captured runtime cut")
	}
	assertUnchanged := func() {
		t.Helper()
		current := owner.Current()
		if definition.Description.Folds != owner || definition.Description.Analysis != analysis || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(current, accepted) || first.Resource.Root != root || !reflect.DeepEqual(root.Expressions[1], call) {
			t.Fatal("accepted generation/backing/recipe mutated")
		}
		for i, entry := range entries {
			if !enginetest.SamePublishedFold(entry, current.Entries[i]) {
				t.Fatal("accepted payload mutated", i)
			}
		}
	}
	bind("state_second")
	second := registry.Binding("state_second")
	if second.Resource.Root == root || second.Entry.Description.Folds != owner || &second.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("portal did not share original backing")
	}
	for _, name := range []string{"state_first", "state_second"} {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		assertUnchanged()
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var env core.PreparedEnvironment
	if err == nil {
		env, err = e.PreparedEnvironmentLocked(auth, true)
	}
	var publication *core.FoldPublication
	if err == nil {
		publication, err = core.BeginFoldPublication(&definition.Description, definition.Body, env, auth.Catalog, nil)
	}
	if err == nil && publication != nil {
		publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(ctx, auth.Catalog, env, e.Catalog.State.Epoch())
	}
	e.Mu.Unlock()
	op.Done()
	if publication == nil || !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("rejected fold publication became accepted", err)
	}
	assertUnchanged()
	for _, name := range []string{"state_first", "state_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("state_owned") })
	op, _, err = s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

// The held description's public RECORD signature is occurrence-owned, while
// executable analysis and fold backing must rebind to each replacement epoch.
func backendStatePreparedReplacement(t *testing.T) {
	e := engine.New()
	observer := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, observer, "CREATE TABLE bs_prepared_replace(v integer)", "SET extra_float_digits=3")
	var dump bytes.Buffer
	if err := engine.Dump(t.Context(), e, &dump); err != nil {
		t.Fatal(err)
	}
	birth, _ := e.Statistics.LiveActivity(peer.Token())
	source := preparedFoldPoint + fmt.Sprintf(",pg_stat_get_backend_subxact(%d) AS subxact", birth.BackendID)
	statement, description := enginetest.PreparedDescription(t, e, observer, source)
	defer description.Retire()
	publicShape := description.Results[1].Usage.Type
	if publicShape.ID() != 2249 || publicShape.CompositeLen() != 2 {
		t.Fatal("missing held fixed OUT descriptor")
	}
	publicMembers := [2]sqltype.CompositeMember{publicShape.CompositeMember(0), publicShape.CompositeMember(1)}
	currentShape := func() *sqltype.Descriptor {
		t.Helper()
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "subxact_count", Usage: enginetest.Usage(e, sqltype.Int4)},
			{Attribute: 2, Name: "subxact_overflowed", Usage: enginetest.Usage(e, sqltype.Bool)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return shape
	}
	execute := func() core.Result {
		t.Helper()
		result, err := enginetest.PreparedExecute(e, observer, statement, &description)
		if err != nil {
			t.Fatal("held builtin RECORD failed to rebind", err)
		}
		if result.Rows == nil || len(result.Rows.Schema()) != 2 {
			t.Fatal("missing actual prepared row owner")
		}
		return result
	}
	read := func(result core.Result, count int64, expected *sqltype.Descriptor) sqltype.Value {
		t.Helper()
		defer result.Rows.Close()
		schema := result.Rows.Schema()[1].Usage
		if !sqltype.SameRecordUsage(schema, expected.Usage()) {
			t.Fatal("prepared Rows schema lost its execution-generation OUT contract")
		}
		row := make([]sqltype.Value, 2)
		if present, err := result.Rows.Next(t.Context(), row); err != nil || !present {
			t.Fatal("prepared detached record row", present, err)
		}
		backendStateCount(t, row[1], count, false)
		if !sqltype.SameRecordUsage(row[1].Type().Usage(), expected.Usage()) ||
			row[1].Type() != schema.Type || !row[1].ValidResult(schema) {
			t.Fatal("prepared datum lost its execution-generation OUT contract")
		}
		for i := range 2 {
			if row[1].CompositeField(i).Type() != expected.CompositeMember(i).Usage.Type {
				t.Fatal("prepared field borrowed a foreign primitive generation", i)
			}
		}
		retained := row[1]
		if present, err := result.Rows.Next(t.Context(), row); err != nil || present {
			t.Fatal("prepared scalar cardinality", present, err)
		}
		return retained
	}
	for _, restore := range []bool{false, true} {
		oldText, freshText, freshDigits := "(0.10000000000000002,1)", "(0.1,1)", "0"
		if restore {
			activityOwnerRun(t, e, observer, "CREATE TABLE bs_prepared_replace(v integer)")
			// The preceding Reset execution accepted the low-precision fold.
			// SET alone must not refold it: Restore, not a second invalidator,
			// must replace that backing and observe the opposite presentation.
			oldText, freshText, freshDigits = "(0.1,1)", "(0.10000000000000002,1)", "3"
		}
		activityOwnerRun(t, e, peer, "BEGIN", "SAVEPOINT child", "INSERT INTO bs_prepared_replace VALUES(99)")
		oldShape := currentShape()
		oldRows := execute()
		defer oldRows.Rows.Close()
		owner := description.Folds
		old := enginetest.SnapshotFoldDescription(description)
		entries := append([]exec.FoldConstant(nil), old.Accepted.Entries...)
		oldAnalysis := description.Analysis
		if owner == nil || !owner.Live() || len(entries) == 0 || oldAnalysis == nil {
			t.Fatal("missing actual accepted prepared definition backing")
		}
		enginetest.PreparedFoldEntry(t, old, sqltype.Text, oldText)
		oldType := oldRows.Rows.Schema()[1].Usage.Type
		var err error
		if restore {
			err = engine.Restore(t.Context(), e, bytes.NewReader(dump.Bytes()))
		} else {
			err = engine.Reset(t.Context(), e)
		}
		if err != nil {
			t.Fatal(err)
		}
		if oldRows.Rows.Schema()[1].Usage.Type != oldType {
			t.Fatal("native replacement mutated detached row metadata")
		}
		retained := read(oldRows, 1, oldShape)
		activityOwnerRun(t, e, peer, "ROLLBACK")
		activityOwnerRun(t, e, observer, "SET extra_float_digits="+freshDigits)
		freshShape := currentShape()
		for i := range 2 {
			if freshShape.CompositeMember(i).Usage.Type == oldShape.CompositeMember(i).Usage.Type {
				t.Fatal("replacement did not exercise a genuine new primitive generation", i)
			}
		}
		read(execute(), 0, freshShape)
		current := description.Folds.Current()
		if description.Folds != owner || description.Analysis == oldAnalysis ||
			description.Folds.Analysis != description.Analysis ||
			description.Folds.Environment.Epoch == old.Environment.Epoch ||
			current.Generation <= old.Accepted.Generation || len(current.Entries) == 0 ||
			&current.Entries[0] == &old.Accepted.Entries[0] {
			t.Fatal("replacement reused retired analysis, generation or fold backing")
		}
		currentCatalog := e.CatalogRead()
		callable := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6107}
		if description.Analysis.Domain != currentCatalog.Owner().Domain ||
			description.Analysis.Epoch != currentCatalog.Epoch() ||
			description.Analysis.FoldCallables[callable] != currentCatalog.MustProcedure(callable) {
			t.Fatal("reanalysis did not capture the actual current callable authority")
		}
		if description.Results[1].Usage.Type != publicShape {
			t.Fatal("reanalysis replaced the held public signature")
		}
		for i, member := range publicMembers {
			if publicShape.CompositeMember(i) != member {
				t.Fatal("replacement mutated original held public members", i)
			}
		}
		for i, entry := range entries {
			if !enginetest.SamePublishedFold(entry, old.Accepted.Entries[i]) {
				t.Fatal("replacement mutated prior accepted immutable backing", i)
			}
		}
		enginetest.PreparedFoldEntry(t, old, sqltype.Text, oldText)
		enginetest.PreparedFoldEntry(t, description, sqltype.Text, freshText)
		backendStateCount(t, retained, 1, false)
		if !sqltype.SameRecordUsage(retained.Type().Usage(), oldShape.Usage()) {
			t.Fatal("fresh execution retagged the detached old datum")
		}
		read(execute(), 0, freshShape)
		reused := description.Folds.Current()
		if reused.Generation != current.Generation || &reused.Entries[0] != &current.Entries[0] {
			t.Fatal("unchanged post-replacement execution did not reuse accepted backing")
		}
	}
}
