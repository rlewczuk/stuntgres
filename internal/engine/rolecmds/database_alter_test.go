package rolecmds_test

import (
	"context"
	"errors"
	"maps"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

func TestDatabaseAlterPublication(t *testing.T) {
	t.Run("shared-builtin", databaseAlterSharedPublication)
	t.Run("explicit-shared-repairs", databaseAlterExplicitPublication)
	for _, route := range []string{"direct", "commit", "rollback", "commit-prepared", "rollback-prepared"} {
		t.Run(route, func(t *testing.T) {
			e, seed := engine.New(), engine.New()
			target := databaseCreateUnitInstall(t, e, seed, "source")
			peer := databaseCreateUnitInstall(t, e, seed, "peer")
			original := target.CatalogRead().MustDatabase(target.Catalog.DatabaseAddress())
			domain, store, defaultTarget, frontier, epoch := target.Catalog.Domain, &target.Storage, e.Cluster.DefaultTarget, e.Cluster.NextOID, target.Catalog.State.Epoch()
			roots := map[*core.Engine]*catalog.State{e: e.Catalog.State, target: target.Catalog.State, peer: peer.Catalog.State}
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			apply := func(statement parser.Statement) {
				t.Helper()
				if _, err := e.Execute(op, statement, 0, nil); err != nil {
					t.Fatal(err)
				}
			}
			if route != "direct" {
				apply(&parser.TransactionControl{Kind: parser.KindStart})
			}
			apply(&parser.AlterDatabaseStmt{Name: "source", Action: "rename", NewName: "first"})
			if route != "direct" {
				if e.Cluster.Targets["source"] != target || e.Cluster.Targets["first"] != nil {
					t.Fatal("transaction-private rename escaped")
				}
				waitCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				finished := make(chan struct{})
				var waitingSession *session.Session
				var waitingError error
				go func() {
					admission, err := e.AdmitDatabase(waitCtx, e.Roles().LookupRef("postgres"), ids.SessionID(900100), core.DatabaseSelector{Name: "source", Explicit: true})
					waitingError = err
					if err == nil {
						defer admission.Release()
						waitingSession, waitingError = admission.Target().StartSession(waitCtx, admission, session.SessionStart{ID: ids.SessionID(900100)})
					}
					close(finished)
				}()
				defer func() {
					stop()
					<-finished
					if waitingSession != nil {
						waitingSession.RequestEnd(session.CleanClose)
						_ = waitingSession.WaitEnd(context.Background())
					}
				}()
				for {
					rows, err := e.Locks.PGLocks(e.Catalog.Domain, e.Catalog.State.Epoch())
					if err != nil {
						t.Fatal(err)
					}
					pending := false
					for _, row := range rows {
						if row.Waiting && row.Kind == lock.ObjectRecord && row.Class == uint32(ids.DatabaseClassID) && row.Object == uint32(original.Address().ObjectID) {
							pending = true
						}
					}
					if pending {
						break
					}
					select {
					case <-finished:
						t.Fatal("startup escaped private rename exclusion", waitingError)
					case <-waitCtx.Done():
						t.Fatal(waitCtx.Err())
					default:
						runtime.Gosched()
					}
				}
				mark := parser.Identifier{Text: "rename_mark"}
				apply(&parser.SavepointControl{Kind: parser.CreateSavepoint, Name: mark})
				apply(&parser.AlterDatabaseStmt{Name: "first", Action: "rename", NewName: "discarded"})
				apply(&parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: mark})
				apply(&parser.AlterDatabaseStmt{Name: "first", Action: "rename", NewName: "final"})
				switch route {
				case "commit":
					apply(&parser.TransactionControl{Kind: parser.KindCommit})
				case "rollback":
					apply(&parser.TransactionControl{Kind: parser.KindRollback})
				default:
					apply(&parser.TransactionControl{Kind: parser.KindPrepareTransaction, GID: "database_alter"})
					if e.Cluster.Targets["source"] != target || target.DatabaseName != "source" {
						t.Fatal("PREPARE exposed name")
					}
					select {
					case <-finished:
						t.Fatal("PREPARE released rename exclusion", waitingError)
					default:
					}
					kind := parser.KindCommitPrepared
					if route == "rollback-prepared" {
						kind = parser.KindRollbackPrepared
					}
					apply(&parser.TransactionControl{Kind: kind, GID: "database_alter"})
				}
				select {
				case <-finished:
				case <-waitCtx.Done():
					t.Fatal(waitCtx.Err())
				}
				if route == "rollback" || route == "rollback-prepared" {
					if waitingError != nil || waitingSession == nil || waitingSession.DatabaseName != "source" {
						t.Fatal("rollback did not release original admission", waitingError)
					}
				} else {
					if state, ok := waitingError.(interface{ SQLState() string }); !ok || state.SQLState() != "3D000" || waitingSession != nil {
						t.Fatal("commit redirected old selector", waitingError)
					}
				}
			}
			expected := "final"
			if route == "direct" {
				expected = "first"
			}
			if route == "rollback" || route == "rollback-prepared" {
				expected = "source"
			}
			if e.Cluster.Targets[expected] != target || target.DatabaseName != expected || e.Cluster.Targets["discarded"] != nil {
				t.Fatal("incoherent published registry", expected, e.Cluster.Targets)
			}
			for runtime := range roots {
				descriptor := runtime.Catalog.State.MustDatabase(original.Address())
				if descriptor.Name() != expected || descriptor.Owner != original.Owner || descriptor.ACL.Explicit != original.ACL.Explicit {
					t.Fatal("partial database root publication", descriptor)
				}
			}
			if original.Name() != "source" || target.Catalog.Domain != domain || &target.Storage != store || e.Cluster.DefaultTarget != defaultTarget || e.Cluster.NextOID != frontier || target.Catalog.State.Epoch() != epoch {
				t.Fatal("rename replaced identity/storage/frontier")
			}
			before := e.Cluster.Targets
			apply(&parser.AlterDatabaseStmt{Name: expected, Action: "options", Options: []parser.DatabaseOption{{Name: "is_template", Value: "true"}}})
			// An alias marker distinguishes map retention from equal copied entries.
			e.Mu.Lock()
			before["identity_probe"] = peer
			sameMap := e.Cluster.Targets["identity_probe"] == peer
			delete(before, "identity_probe")
			delete(e.Cluster.Targets, "identity_probe")
			e.Mu.Unlock()
			if !sameMap {
				t.Fatal("metadata publication copied Targets")
			}
		})
	}
	for _, fence := range []string{"cancel", "predecessor", "role", "destination"} {
		t.Run(fence, func(t *testing.T) {
			e, seed := engine.New(), engine.New()
			target := databaseCreateUnitInstall(t, e, seed, "source")
			original := target.CatalogRead().MustDatabase(target.Catalog.DatabaseAddress())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			auth, err := core.OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			stage := core.OwnershipStage(e, auth, op)
			defer e.DiscardCatalogStage(stage)
			changed := *original
			changed.ObjectName = "renamed"
			changed.ObjectRevision++
			if err := stage.Replace(&changed); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch fence {
			case "cancel":
				cancel()
			case "predecessor":
				e.Mu.Lock()
				next := catalog.CloneState(e.Catalog.State)
				current := *original
				current.ObjectRevision++
				current.ConnectionLimit = 2
				next.SetDescriptor(current.Address(), &current)
				projection := e.Catalog.ProjectSharedLocked(next, e.Cluster.Catalogs)
				e.Catalog.PublishLocked(next)
				projection.InstallLocked()
				e.Mu.Unlock()
			case "role":
				e.Mu.Lock()
				roles := *e.RoleStore.Published
				roles.Revision++
				e.RoleStore.PublishLocked(&roles)
				e.Mu.Unlock()
			case "destination":
				databaseCreateUnitInstall(t, e, seed, "renamed")
			}
			unchanged := databaseCreateUnitUnchanged(t, e)
			_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil {
				t.Fatal("publication ignored fence", fence)
			}
			if fence == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			unchanged()
		})
	}
}

func TestDatabaseAlterTemplateClone(t *testing.T) {
	e, seed := engine.New(), engine.New()
	source := databaseCreateUnitInstall(t, e, seed, "source")
	sourceOp, sourceDone := enginetest.RoleOperation(t, source, "postgres")
	for _, statement := range []parser.Statement{primaryKeyUnitCreate("t", "copied_unique"), primaryKeyUnitInsert(1), &parser.CreateSequence{Name: primaryKeyUnitName("copied_sequence"), Persistence: "logged", Options: []parser.SequenceOption{{Name: "start", Value: 41}}}} {
		if _, err := source.Execute(sourceOp, statement, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := source.Execute(sourceOp, &parser.CreateDatabaseStmt{Name: "current_clone", Options: []parser.DatabaseOption{{Name: "template", Value: "source"}}}, 0, nil); err != nil {
		t.Fatal("exact issuing source session was not excluded", err)
	}
	if e.Cluster.Targets["current_clone"] == nil {
		t.Fatal("current-source copy did not publish real target")
	}
	sourceDone()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.AlterDatabaseStmt{Name: "source", Action: "options", Options: []parser.DatabaseOption{{Name: "is_template", Value: "true"}, {Name: "allow_connections", Value: "false"}, {Name: "connection_limit", Value: "3", Numeric: true}}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(op, &parser.CreateDatabaseStmt{Name: "clone", Options: []parser.DatabaseOption{{Name: "template", Value: "source"}}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	clone := e.Cluster.Targets["clone"]
	if clone == nil || clone == source || clone.Catalog.Domain == source.Catalog.Domain || clone.RoleStore != source.RoleStore {
		t.Fatal("clone runtime authority")
	}
	table := primaryKeyUnitRelation(t, source, "t")
	indexAddress := table.Constraints.Unique[0].Index
	seqAddress := primaryKeyUnitRelation(t, source, "copied_sequence").Address()
	if source.Storage.Relations[table.Address()] == clone.Storage.Relations[table.Address()] || source.Storage.Sequences[seqAddress] == clone.Storage.Sequences[seqAddress] || source.Storage.IndexRoots[indexAddress] == clone.Storage.IndexRoots[indexAddress] {
		t.Fatal("mutable clone backing shared")
	}
	database := clone.CatalogRead().MustDatabase(clone.Catalog.DatabaseAddress())
	if database.IsTemplate || !database.AllowConnections || database.ConnectionLimit != -1 || database.ACL.Explicit || database.DefaultTablespace != catalog.DefaultTablespaceOID {
		t.Fatal("source database attributes copied")
	}
	cloneOp, cloneDone := enginetest.RoleOperation(t, clone, "postgres")
	defer cloneDone()
	if _, err := clone.Execute(cloneOp, primaryKeyUnitInsert(1), 0, nil); err == nil {
		t.Fatal("copied index lost uniqueness")
	}
	if _, err := clone.Execute(cloneOp, primaryKeyUnitInsert(2), 0, nil); err != nil {
		t.Fatal(err)
	}
	if len(source.Storage.Relations[table.Address()].Versions) != 1 || len(clone.Storage.Relations[table.Address()].Versions) != 2 || len(source.Storage.IndexRoots[indexAddress].Entries) != 1 {
		t.Fatal("clone mutation reached source")
	}
	var memory sequence.SessionMemory
	clone.Mu.Lock()
	value, err := sequence.CallLocked(functions.SequenceRequest{Name: "nextval"}, clone.Catalog.State.MustRelation(seqAddress), clone.Storage.Sequences[seqAddress], &memory)
	clone.Mu.Unlock()
	if err != nil || value != 41 || source.Storage.Sequences[seqAddress].Called {
		t.Fatal("sequence generator shared", value, err)
	}
	if clone.CatalogRead().MustRelation(table.Address()).Owner != table.Owner {
		t.Fatal("copied object owner changed")
	}
}

type databaseTemplateStartupGate struct{ entered, release chan struct{} }

func (g databaseTemplateStartupGate) SessionDefault(ctx context.Context, d settings.Definition) (string, bool, error) {
	if d.Name == "application_name" {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	return "", false, nil
}

func TestDatabaseAlterTemplateAdmissionRace(t *testing.T) {
	e, seed := engine.New(), engine.New()
	source := databaseCreateUnitInstall(t, e, seed, "source")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	source.ReplacementMu.Lock()
	locked := true
	defer func() {
		if locked {
			source.ReplacementMu.Unlock()
		}
	}()
	frontier, targets := e.Cluster.NextOID, maps.Clone(e.Cluster.Targets)
	copied := make(chan error, 1)
	copyJoined := false
	defer func() {
		cancel()
		if locked {
			source.ReplacementMu.Unlock()
			locked = false
		}
		if !copyJoined {
			<-copied
		}
	}()
	go func() {
		_, err := e.Execute(op, &parser.CreateDatabaseStmt{Name: "canceled_clone", Options: []parser.DatabaseOption{{Name: "template", Value: "source"}}}, 0, nil)
		copied <- err
	}()
	wait := func(waiting bool) {
		t.Helper()
		for {
			rows, err := e.Locks.PGLocks(e.Catalog.Domain, e.Catalog.State.Epoch())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.Kind == lock.ObjectRecord && row.Class == uint32(ids.DatabaseClassID) && row.Object == uint32(source.Catalog.DatabaseAddress().ObjectID) && row.Waiting == waiting {
					found = true
				}
			}
			if found {
				return
			}
			select {
			case err := <-copied:
				copyJoined = true
				t.Fatal("copy bypassed replacement gate", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			default:
				runtime.Gosched()
			}
		}
	}
	wait(false)
	admitted := make(chan *session.Session, 1)
	admissionError := make(chan error, 1)
	admissionCtx, stopAdmission := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopAdmission()
	go func() {
		s, err := enginetest.OpenSession(admissionCtx, source, session.SessionStart{ID: ids.SessionID(900001)})
		admitted <- s
		admissionError <- err
	}()
	admissionJoined := false
	defer func() {
		stopAdmission()
		if !admissionJoined {
			s := <-admitted
			<-admissionError
			if s != nil {
				s.RequestEnd(session.CleanClose)
				_ = s.WaitEnd(context.Background())
			}
		}
	}()
	wait(true)
	cancel()
	source.ReplacementMu.Unlock()
	locked = false
	copyErr := <-copied
	copyJoined = true
	if !errors.Is(copyErr, context.Canceled) {
		t.Fatal("canceled source copy", copyErr)
	}
	s := <-admitted
	admissionErr := <-admissionError
	admissionJoined = true
	if s != nil {
		defer func() { s.RequestEnd(session.CleanClose); _ = s.WaitEnd(context.Background()) }()
	}
	if admissionErr != nil || s == nil {
		t.Fatal("copy exclusion leaked", admissionErr)
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.Cluster.NextOID != frontier || !maps.Equal(e.Cluster.Targets, targets) {
		t.Fatal("canceled copy leaked target/frontier")
	}
	// Once startup has reserved its physical identity, even an initializer which
	// has not published Live is an actual source blocker.
	reserveCtx, stopReserve := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopReserve()
	gate := databaseTemplateStartupGate{make(chan struct{}), make(chan struct{})}
	type pendingStart struct {
		s   *session.Session
		err error
	}
	pending := make(chan pendingStart, 1)
	go func() {
		s, err := enginetest.OpenSession(reserveCtx, source, session.SessionStart{ID: ids.SessionID(900002), Defaults: gate})
		pending <- pendingStart{s, err}
	}()
	pendingJoined := false
	defer func() {
		stopReserve()
		if !pendingJoined {
			result := <-pending
			if result.s != nil {
				result.s.RequestEnd(session.CleanClose)
				_ = result.s.WaitEnd(context.Background())
			}
		}
	}()
	select {
	case <-gate.entered:
	case <-reserveCtx.Done():
		t.Fatal(reserveCtx.Err())
	}
	admin, closeAdmin := enginetest.RoleOperation(t, e, "postgres")
	defer closeAdmin()
	unchanged := databaseCreateUnitUnchanged(t, e)
	_, err = e.Execute(admin, &parser.CreateDatabaseStmt{Name: "reserved_clone", Options: []parser.DatabaseOption{{Name: "template", Value: "source"}}}, 0, nil)
	if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "55006" {
		t.Fatal("reserved source initializer was not busy", err)
	}
	unchanged()
	close(gate.release)
	initialized := <-pending
	pendingJoined = true
	if initialized.err != nil || initialized.s == nil {
		t.Fatal("source initializer did not finish", initialized.err)
	}
	initialized.s.RequestEnd(session.CleanClose)
	if err := initialized.s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
}
