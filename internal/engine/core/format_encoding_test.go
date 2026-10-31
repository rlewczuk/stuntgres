package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestFormatEncodingCurrentRoleAndResetAll(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE ROLE fe_unit_reader LOGIN")
	messages, _ := e.Settings.Lookup("lc_messages")
	stats, _ := e.Settings.Lookup("log_statement_stats")
	if err := core.SettingAccess(messages, op); err != nil {
		t.Fatal("real bootstrap superuser denied", err)
	}
	if err := core.SettingAccess(stats, op); err != nil {
		t.Fatal("statistics superuser denied", err)
	}
	for _, definition := range []settings.Definition{{Name: "demo.privileged", Access: settings.Privileged}} {
		var d *sqlstate.Error
		if err := core.SettingAccess(definition, op); !errors.As(err, &d) || d.Code != "42501" {
			t.Fatal("unrelated privileged setting relaxed", definition, err)
		}
	}
	if _, err := core.EngineExecuteSetting(e, op, &parser.Set{Name: parser.Identifier{Text: "lc_messages"}, Values: []string{"POSIX"}}, 0, false); err != nil {
		t.Fatal(err)
	}
	reader, ok := e.Roles().Lookup("fe_unit_reader")
	if !ok {
		t.Fatal("real ordinary role missing")
	}
	op.Session.Mu.Lock()
	op.Session.Authorization.State.CurrentUser = reader.Ref
	op.Session.Mu.Unlock()
	if op.RoleState().SessionUser == reader.Ref {
		t.Fatal("fixture lost superuser session identity")
	}
	var diagnostic *sqlstate.Error
	if err := core.SettingAccess(messages, op); !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != `permission denied to set parameter "lc_messages"` {
		t.Fatal("current ordinary role admitted", err)
	}
	if op.Session.Settings.Value("lc_messages") != "POSIX" {
		t.Fatal("access check mutated setting")
	}
	if _, err := core.EngineExecuteSetting(e, op, &parser.Reset{All: true}, 0, false); err != nil {
		t.Fatal("all-reset incorrectly reused individual permission", err)
	}
	if op.Session.Settings.Value("lc_messages") != "C" {
		t.Fatal("all-reset retained privileged override")
	}
}

func TestFormatEncodingValidationNotices(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, threshold := range []session.NoticeLevel{session.LevelWarning, session.LevelError} {
		op.NoticeThreshold = threshold
		_, err := core.EngineExecuteSetting(e, op, &parser.Set{Name: parser.Identifier{Text: "lc_time"}, Local: true, Values: []string{"é🙂"}}, 0, true)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" {
			t.Fatal(err)
		}
		notices := op.DetachNotices()
		if threshold == session.LevelError {
			if len(notices) != 0 {
				t.Fatal("filtered validation warnings delivered", notices)
			}
		} else if len(notices) != 2 || notices[0].Code != "25P01" || notices[0].Message != "SET LOCAL can only be used in transaction blocks" || notices[1].Code != "22023" || notices[1].Severity != "WARNING" || notices[1].Message != `locale name "é🙂" contains non-ASCII characters` {
			t.Fatal("failed validation lost or reordered actual warnings", notices)
		}
		if op.Session.Settings.Value("lc_time") != "C" {
			t.Fatal("failed locale validation published candidate")
		}
		if len(op.DetachNotices()) != 0 {
			t.Fatal("notice transfer duplicated records")
		}
	}
}
