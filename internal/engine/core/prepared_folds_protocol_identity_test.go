package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"context"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/engine/core"
)

func TestPreparedFoldProtocolBoundIdentity(t *testing.T) {
	for _, phase := range []string{"path_change", "discard_shadow"} {
		t.Run(phase, func(t *testing.T) {
			e := rootengine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE SCHEMA fold_bound_early", "CREATE SCHEMA fold_bound_late", "CREATE TABLE fold_bound_late.target(v text)", "SET extra_float_digits=3", "BEGIN"} {
				enginetest.SessionSQL(t, s, q)
			}
			if phase == "path_change" {
				enginetest.SessionSQL(t, s, "CREATE TABLE fold_bound_early.target(v text)")
				enginetest.SessionSQL(t, s, "SET search_path TO fold_bound_late,fold_bound_early")
			} else {
				enginetest.SessionSQL(t, s, "SET search_path TO fold_bound_early,fold_bound_late")
			}
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("identity", "INSERT INTO target VALUES((point '0.10000000000000002,1')::text)", nil)
				return err
			})
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "identity", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("old")
			registry := core.PayloadOf(s).PreparedSQL
			original := registry.Binding("old").Entry.Description.Analysis.Target
			enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
			if phase == "path_change" {
				enginetest.SessionSQL(t, s, "SET search_path TO fold_bound_early,fold_bound_late")
			} else {
				enginetest.SessionSQL(t, s, "CREATE TABLE fold_bound_early.target(v text)")
				enginetest.SessionSQL(t, s, "DISCARD PLANS")
			}
			// Fresh planning belongs to the definition, never to the old portal.
			bind("new")
			if registry.Binding("new").Entry.Description.Analysis.Target == original {
				t.Fatal("new Bind failed to select current target")
			}
			for _, name := range []string{"old", "new"} {
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
					return err
				})
			}
			if registry.Binding("old").Entry.Description.Analysis.Target != original {
				t.Fatal("old portal adopted a replacement target")
			}
			for table, want := range map[string]string{"fold_bound_late.target": "(0.10000000000000002,1)", "fold_bound_early.target": "(0.1,1)"} {
				result := enginetest.SessionSQL(t, s, "SELECT v FROM "+table)
				var row [1]sqltype.Value
				more, err := result.Rows.Next(context.Background(), row[:])
				if err != nil || !more {
					t.Fatal("missing accepted target row", table, err)
				}
				got, err := row[0].Type().Output(context.Background(), row[0])
				if err != nil || got != want {
					t.Fatal("wrong target generation", table, got, want, err)
				}
				if more, err = result.Rows.Next(context.Background(), row[:]); more || err != nil {
					t.Fatal("duplicate target effect", table, err)
				}
				result.Rows.Close()
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
