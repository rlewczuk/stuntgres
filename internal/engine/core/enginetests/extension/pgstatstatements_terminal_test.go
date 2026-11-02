package extension_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsCompletionTerminalIdentity(t *testing.T) {
	for _, command := range []string{"SET ROLE terminal_target", "SET SESSION AUTHORIZATION terminal_target"} {
		for _, reset := range []string{"none", "admitted", "terminal", "unrelated"} {
			t.Run(command+"/"+reset, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE ROLE terminal_target", "CREATE ROLE terminal_unrelated", "SET pg_stat_statements.track_utility=on", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track='top'"} {
					pgssOwnerExec(t, e, s, q)
				}
				original := uint32(s.Authorization.State.CurrentUser.ID())
				// Resolve an unrelated principal through the genuine role command,
				// without manufacturing an allocator-dependent role identifier.
				pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
				pgssOwnerExec(t, e, s, "SET ROLE terminal_unrelated")
				unrelated := uint32(s.Authorization.State.CurrentUser.ID())
				pgssOwnerExec(t, e, s, "RESET ROLE")
				pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='top'")
				ast := enginetest.Statement(t, command)
				jumble, _ := analyze.JumbleUtility(ast)
				queryID := jumble.QueryID()
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				op.OriginalSQL, op.SQLAvailable = command, true
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(context.Canceled)
				if _, err := scope.Execute(ast, 0, nil); err != nil {
					t.Fatal(err)
				}
				terminal := uint32(s.Authorization.State.CurrentUser.ID())
				if terminal == original || terminal == unrelated {
					t.Fatal("utility did not install a distinct real terminal principal")
				}
				// The real utility has returned, but Finish still owns successful
				// publication. No collector events or completion callbacks are faked.
				for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
					if entry.Key.QueryID == queryID {
						t.Fatal("utility published before actual Finish", entry)
					}
				}
				if reset != "none" {
					user := unrelated
					if reset == "admitted" {
						user = original
					} else if reset == "terminal" {
						user = terminal
					}
					e.Cluster.StatementStatistics.Clear(statistics.StatementFilter{UserID: user, DatabaseID: uint32(e.Catalog.DatabaseAddress().ObjectID), QueryID: queryID}, false, time.Now())
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				var found []statistics.StatementEntry
				for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
					if entry.Key.QueryID == queryID {
						found = append(found, entry)
					}
				}
				if reset == "admitted" || reset == "terminal" {
					if len(found) != 0 {
						t.Fatal("matching clear resurrected in-flight utility history", found)
					}
					return
				}
				if len(found) != 1 || found[0].Key.UserID != terminal || !found[0].Key.TopLevel || found[0].Query != command || found[0].Execution.Count != 1 || found[0].Rows != 0 || found[0].Planning.Count != 0 {
					t.Fatal("successful terminal identity/raw representative", found)
				}
			})
		}
	}
}

func TestPgStatStatementsCompletionTerminalOrdinary(t *testing.T) {
	for _, planning := range []string{"off", "on"} {
		t.Run(planning, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE terminal_data(k integer)", "INSERT INTO terminal_data VALUES(7)", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=" + planning, "SET pg_stat_statements.track='top'"} {
				pgssOwnerExec(t, e, s, q)
			}
			query := "SELECT set_config('pg_stat_statements.track','none',false) AS phase_terminal_ordinary,(point '1,2')::text FROM terminal_data"
			ast, description := enginetest.PreparedDescription(t, e, s, query)
			pgssPhaseCounts(t, e, "phase_terminal_ordinary", 0, 0, 0)
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			op.OriginalSQL, op.SQLAvailable = query, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			scope.Expected = &description
			result, err := scope.Execute(ast, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			s.Settings.Mu.Lock()
			track := s.Settings.Value("pg_stat_statements.track")
			s.Settings.Mu.Unlock()
			if track != "none" {
				t.Fatal("real ordinary execution did not retain its setting effect", track)
			}
			accepted := description.Folds.Current()
			if accepted.Generation == 0 || len(accepted.Entries) == 0 || result.Rows == nil {
				t.Fatal("ordinary execution lacks real accepted immutable folds", accepted)
			}
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			plans := int64(0)
			if planning == "on" {
				plans = 1
			}
			pgssPhaseCounts(t, e, "phase_terminal_ordinary", plans, 0, 0)
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			pgssPhaseCounts(t, e, "phase_terminal_ordinary", plans, 0, 0)
			current := description.Folds.Current()
			if current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] {
				t.Fatal("terminal policy changed accepted fold ownership")
			}
		})
	}
}

func TestPgStatStatementsCompletionTerminalResource(t *testing.T) {
	for _, restoration := range []string{"session", "invocation", "invocation_session"} {
		for _, planning := range []string{"off", "on"} {
			t.Run(restoration+"/"+planning, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				config, track, local := "", "all", "false"
				if restoration != "session" {
					config, track = " SET pg_stat_statements.track='all'", "top"
				}
				if restoration == "invocation" {
					local = "true"
				}
				for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE terminal_data(k integer)", "INSERT INTO terminal_data VALUES(7)", "CREATE FUNCTION terminal_fn() RETURNS text LANGUAGE plpgsql" + config + " AS $$DECLARE answer text; BEGIN SELECT /*phase_terminal_child*/ set_config('pg_stat_statements.track','none'," + local + ") INTO answer FROM terminal_data; RETURN answer; END$$", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=" + planning, "SET pg_stat_statements.track='" + track + "'", "BEGIN"} {
					pgssOwnerExec(t, e, s, q)
				}
				cycle := s.NewProtocolCycle()
				command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
				query := "SELECT terminal_fn() AS phase_terminal_outer,(point '1,2')::text FROM terminal_data"
				command(func(scope *core.StatementScope) error {
					_, err := scope.ParseProtocol("terminal_definition", query, nil)
					return err
				})
				pgssPhaseCounts(t, e, "phase_terminal_outer", 0, 0, 0)
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal("terminal_portal", "terminal_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
				registry := core.PayloadOf(s).PreparedSQL
				definition := registry.Lookup("terminal_definition")
				binding := registry.Binding("terminal_portal")
				if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Folds == nil {
					t.Fatal("missing actual initialized terminal resource")
				}
				root, folds := binding.Resource.Root, binding.Folds
				resource := binding.Resource
				accepted := definition.Description.Folds.Current()
				if accepted.Generation == 0 || len(accepted.Entries) == 0 {
					t.Fatal("missing accepted terminal fold backing", accepted)
				}
				plans := int64(0)
				if planning == "on" {
					plans = 1
				}
				pgssPhaseCounts(t, e, "phase_terminal_outer", plans, 0, 0)
				delivered := 0
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal("terminal_portal", 0, func([]session.ProtocolField) error { delivered++; return nil }, nil, nil, core.CopyProtocolHooks{})
					return err
				})
				if delivered != 1 || binding.Resource.Root != root || binding.Folds != folds {
					t.Fatal("execution replaced the accepted live terminal resource", delivered)
				}
				s.Settings.Mu.Lock()
				effective := s.Settings.Value("pg_stat_statements.track")
				s.Settings.Mu.Unlock()
				wantTrack := "none"
				if restoration == "invocation" {
					wantTrack = "top"
				}
				if effective != wantTrack {
					t.Fatal("invocation restoration boundary", effective, wantTrack)
				}
				if planning == "on" {
					child := pgssPhaseEntry(t, e, "phase_terminal_child")
					outer := pgssPhaseEntry(t, e, "phase_terminal_outer")
					if child.Key.TopLevel || !outer.Key.TopLevel || child.Key.UserID != outer.Key.UserID || child.Key.QueryID == outer.Key.QueryID {
						t.Fatal("nested actual resource lost phase provenance", child, outer)
					}
				}
				pgssPhaseCounts(t, e, "phase_terminal_child", plans, 0, 0)
				pgssPhaseCounts(t, e, "phase_terminal_outer", plans, 0, 0)
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("terminal_portal") })
				calls := int64(0)
				if restoration == "invocation" {
					calls = 1
				}
				pgssPhaseCounts(t, e, "phase_terminal_outer", plans, calls, calls)
				pgssPhaseCounts(t, e, "phase_terminal_child", plans, 0, 0)
				if registry.Binding("terminal_portal") != nil {
					t.Fatal("normal terminal Close retained portal ownership")
				}
				if resource.Root != nil || binding.Folds != nil {
					t.Fatal("normal terminal retained its live graph/fold group")
				}
				if err := resource.Close(context.Background()); err != nil {
					t.Fatal("repeat disposal of the actual held resource", err)
				}
				pgssPhaseCounts(t, e, "phase_terminal_outer", plans, calls, calls)
				pgssOwnerExec(t, e, s, "COMMIT")
				pgssPhaseCounts(t, e, "phase_terminal_outer", plans, calls, calls)
				current := definition.Description.Folds.Current()
				if current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] {
					t.Fatal("resource disposal mutated held immutable fold backing")
				}
			})
		}
	}
}

func TestPgStatStatementsCompletionTerminalRestoration(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		setup         []string
	}{
		{"discard", "DISCARD ALL", []string{"SET ROLE terminal_target"}},
		{"commit_local", "COMMIT", []string{"BEGIN", "SET LOCAL ROLE terminal_target"}},
		{"rollback", "ROLLBACK", []string{"BEGIN", "SET ROLE terminal_target"}},
		{"rollback_to", "ROLLBACK TO SAVEPOINT terminal_saved", []string{"BEGIN", "SAVEPOINT terminal_saved", "SET ROLE terminal_target"}},
	} {
		for _, reset := range []string{"none", "admitted", "terminal", "restored", "unrelated"} {
			t.Run(tc.name+"/"+reset, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE ROLE terminal_target", "CREATE ROLE terminal_unrelated", "SET pg_stat_statements.track_utility=on", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track='top'"} {
					pgssOwnerExec(t, e, s, q)
				}
				original := uint32(s.Authorization.State.CurrentUser.ID())
				unrelated, ok := e.Roles().Lookup("terminal_unrelated")
				if !ok {
					t.Fatal("missing actual unrelated principal")
				}
				for _, q := range tc.setup {
					pgssOwnerExec(t, e, s, q)
				}
				admitted := uint32(s.Authorization.State.CurrentUser.ID())
				if admitted == original {
					t.Fatal("restoration witness did not change the real role")
				}
				ast := enginetest.Statement(t, tc.command)
				jumble, _ := analyze.JumbleUtility(ast)
				queryID := jumble.QueryID()
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				op.OriginalSQL, op.SQLAvailable = tc.command, true
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(context.Canceled)
				if _, err := scope.Execute(ast, 0, nil); err != nil {
					t.Fatal(err)
				}
				if uint32(s.Authorization.State.CurrentUser.ID()) != original {
					t.Fatal("genuine utility owner did not restore its principal")
				}
				// Transaction command-control acceptance is the utility-hook
				// terminal, before command-end identity restoration. DISCARD
				// instead restores inside the utility implementation itself.
				terminal := admitted
				if tc.name == "discard" {
					terminal = original
				}
				for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
					if entry.Key.QueryID == queryID {
						t.Fatal("restoration published before actual terminal", entry)
					}
				}
				if reset != "none" {
					user := uint32(unrelated.Ref.ID())
					if reset == "admitted" {
						user = admitted
					} else if reset == "terminal" {
						user = terminal
					} else if reset == "restored" {
						user = original
					}
					e.Cluster.StatementStatistics.Clear(statistics.StatementFilter{UserID: user, DatabaseID: uint32(e.Catalog.DatabaseAddress().ObjectID), QueryID: queryID}, false, time.Now())
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				var found []statistics.StatementEntry
				for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
					if entry.Key.QueryID == queryID {
						found = append(found, entry)
					}
				}
				if reset == "admitted" || reset == "terminal" || reset == "restored" && terminal == original {
					if len(found) != 0 {
						t.Fatal("matching clear resurrected restored utility", found)
					}
				} else if len(found) != 1 || found[0].Key.UserID != terminal || !found[0].Key.TopLevel || found[0].Execution.Count != 1 || found[0].Rows != 0 || found[0].Planning.Count != 0 {
					t.Fatal("restoration terminal charged wrong authority", found)
				}
			})
		}
	}
}
