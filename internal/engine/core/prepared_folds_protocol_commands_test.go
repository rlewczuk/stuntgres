package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/engine/core"
)

func preparedFoldProtocolTry(t *testing.T, e *core.Engine, s *session.Session, cycle *session.ProtocolCycle, canceled bool, fn func(*core.StatementScope) error) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		t.Fatal(err)
	}
	if canceled {
		cancel()
	}
	err = fn(scope)
	return scope.Finish(err)
}

func preparedFoldProtocolRows(t *testing.T, s *session.Session, want string) {
	t.Helper()
	result := enginetest.SessionSQL(t, s, "SELECT v FROM fold_commands ORDER BY id")
	defer result.Rows.Close()
	var row [1]sqltype.Value
	more, err := result.Rows.Next(context.Background(), row[:])
	if err != nil || !more {
		t.Fatal("missing fixture row", err)
	}
	got, err := row[0].Type().Output(context.Background(), row[0])
	if err != nil || got != want {
		t.Fatalf("row=%q want=%q err=%v", got, want, err)
	}
	if more, err = result.Rows.Next(context.Background(), row[:]); more || err != nil {
		t.Fatal("unexpected effect prefix", more, err)
	}
}

func TestPreparedFoldProtocolCommandOwnership(t *testing.T) {
	expression := "length((point '0.10000000000000002,1')::text)"
	commands := map[string]string{
		"insert":        "INSERT INTO fold_commands VALUES(2," + expression + ")",
		"update":        "UPDATE fold_commands SET v=" + expression + " WHERE id=1",
		"rule_update":   "UPDATE fold_commands SET v=" + expression + " WHERE id=1",
		"delete":        "DELETE FROM fold_commands WHERE v=" + expression,
		"merge":         "MERGE INTO fold_commands AS dst USING (VALUES(1)) AS src(id) ON dst.id=src.id WHEN MATCHED THEN UPDATE SET v=" + expression,
		"modifying_cte": "WITH changed AS (UPDATE fold_commands SET v=" + expression + " WHERE id=1 RETURNING v) SELECT v FROM changed",
	}
	for kind, query := range commands {
		t.Run(kind, func(t *testing.T) {
			e := rootengine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE fold_commands(id int,v int)")
			enginetest.SessionSQL(t, s, "INSERT INTO fold_commands VALUES(1,0)")
			if kind == "rule_update" {
				enginetest.SessionSQL(t, s, "CREATE TABLE fold_rule(v int)")
				enginetest.SessionSQL(t, s, "CREATE RULE fold_log AS ON UPDATE TO fold_commands DO ALSO INSERT INTO fold_rule VALUES("+expression+")")
			}
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
			parse := func() {
				command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol("command", query, nil); return err })
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "command", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
			parse()
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("command")
			owner := definition.Description.Folds
			bind("old")
			portal := registry.Binding("old")
			if portal.Resource != nil && portal.Resource.Root != nil || portal.Folds == nil || len(portal.Folds.Entries) == 0 || !portal.Folds.Candidate().Fixed {
				t.Fatal("command Bind omitted private accepted typed generation")
			}
			preparedFoldEntry(t, definition.Description, sqltype.Int4, "23")
			entries, generation := owner.Current().Entries, owner.Current().Generation
			if &portal.Folds.Entries[0] != &entries[0] {
				t.Fatal("Bind copied immutable backing")
			}
			preparedFoldProtocolRows(t, s, "0")
			enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
			command(func(scope *core.StatementScope) error { _, err := scope.DescribeProtocolStatement("command"); return err })
			bind("again")
			if &owner.Current().Entries[0] != &entries[0] || owner.Current().Generation != generation {
				t.Fatal("Describe/repeated Bind replaced accepted generation")
			}
			enginetest.SessionSQL(t, s, "DISCARD PLANS")
			bind("discarded")
			preparedFoldEntry(t, definition.Description, sqltype.Int4, "7")
			if &portal.Folds.Entries[0] != &entries[0] {
				t.Fatal("DISCARD mutated old portal generation")
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("command") })
			if owner.Live() {
				t.Fatal("closed owner kept publication rights")
			}
			parse()
			bind("new")
			replacement := registry.Lookup("command").Description.Folds
			replacementEntries, replacementGeneration := replacement.Current().Entries, replacement.Current().Generation
			command(func(scope *core.StatementScope) error {
				_, err := scope.ExecuteProtocolPortal("old", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
				return err
			})
			if replacement == owner || replacement.Current().Generation != replacementGeneration || &replacement.Current().Entries[0] != &replacementEntries[0] || owner.Live() {
				t.Fatal("old portal published into a definition")
			}
			if kind == "insert" {
				enginetest.SessionSQL(t, s, "DELETE FROM fold_commands WHERE id=1")
			}
			want := "23"
			if kind == "delete" {
				want = "0"
			}
			preparedFoldProtocolRows(t, s, want)
			if kind == "rule_update" {
				result := enginetest.SessionSQL(t, s, "SELECT v FROM fold_rule")
				var row [1]sqltype.Value
				more, err := result.Rows.Next(context.Background(), row[:])
				value, valueErr := row[0].Integer()
				if err != nil || valueErr != nil || !more || value != 23 {
					t.Fatal("rule action did not retain accepted Bind value", value, err, valueErr)
				}
				if more, err = result.Rows.Next(context.Background(), row[:]); more || err != nil {
					t.Fatal("rule effects occurred before Execute", more, err)
				}
				result.Rows.Close()
			}
			for _, name := range []string{"old", "again", "discarded", "new"} {
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}

func TestPreparedFoldProtocolCommandFailure(t *testing.T) {
	for _, phase := range []string{"late_cte", "late_rule", "cancel", "authority"} {
		t.Run(phase, func(t *testing.T) {
			e := rootengine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE fold_commands(id int,v int)")
			enginetest.SessionSQL(t, s, "INSERT INTO fold_commands VALUES(1,0)")
			query := "UPDATE fold_commands SET v=length((point '0.10000000000000002,1')::text)"
			if phase == "late_cte" {
				query = "WITH first AS (UPDATE fold_commands SET v=length((point '0.10000000000000002,1')::text) RETURNING v), second AS (UPDATE fold_commands SET v=(1/0)::bigint RETURNING v) SELECT v FROM first"
			}
			if phase == "late_rule" {
				enginetest.SessionSQL(t, s, "CREATE TABLE fold_rule(v int)")
				enginetest.SessionSQL(t, s, "CREATE RULE fold_fail AS ON UPDATE TO fold_commands DO ALSO INSERT INTO fold_rule VALUES((1/0)::bigint)")
			}
			if phase == "authority" {
				enginetest.SessionSQL(t, s, "CREATE USER fold_command_reader")
				enginetest.SessionSQL(t, s, "GRANT UPDATE ON fold_commands TO fold_command_reader")
				enginetest.SessionSQL(t, s, "SET SESSION AUTHORIZATION fold_command_reader")
			}
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			preparedFoldProtocolCommand(t, e, s, cycle, func(scope *core.StatementScope) error { _, err := scope.ParseProtocol("failed", query, nil); return err })
			owner := core.PayloadOf(s).PreparedSQL.Lookup("failed").Description.Folds
			if phase == "authority" {
				enginetest.SessionSQL(t, s, "RESET SESSION AUTHORIZATION")
				enginetest.SessionSQL(t, s, "REVOKE UPDATE ON fold_commands FROM fold_command_reader")
				enginetest.SessionSQL(t, s, "SET SESSION AUTHORIZATION fold_command_reader")
			}
			err := preparedFoldProtocolTry(t, e, s, cycle, phase == "cancel", func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal("failed", "failed", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			if err == nil || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 || core.PayloadOf(s).PreparedSQL.Binding("failed") != nil {
				t.Fatal("failed Bind published a prefix", err)
			}
			assertFoldReuseRetirement(t, owner, false)
			if phase == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation", err)
			}
			if phase == "authority" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
					t.Fatal("lost current authority", err)
				}
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
			if phase == "authority" {
				enginetest.SessionSQL(t, s, "RESET SESSION AUTHORIZATION")
			}
			preparedFoldProtocolRows(t, s, "0")
		})
	}
}
