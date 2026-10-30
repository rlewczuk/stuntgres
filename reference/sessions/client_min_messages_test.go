package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

const cmmProducer = "CREATE TABLE IF NOT EXISTS cmm_rows(replaced BOOL)"

var cmmNotice = fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "cmm_rows" already exists, skipping`}

type cmmSession struct {
	t      *testing.T
	ctx    context.Context
	conn   *sql.Conn
	record *fixture.Recorder
	pg     bool
	notices []fixture.Diagnostic
}

func cmmOpen(t *testing.T, p *fixture.Pair, pg bool) *cmmSession {
	t.Helper()
	s := &cmmSession{t: t, ctx: p.Ctx, pg: pg}
	_, s.conn, s.record = fixture.NoticeReferencePool(t, p, pg, &s.notices)
	return s
}

// Every non-producer operation is independently quiet. Only PostgreSQL's
// incidental internal DEBUG traces are outside the feature diagnostic contract.
func (s *cmmSession) run(step fixture.Step) {
	s.t.Helper()
	before := len(s.notices)
	fixture.RunStep(s.t, s.ctx, s.conn, s.record, step)
	noticesWitness(s.t, s.pg, s.notices[before:], cmmNotice, false)
}

func (s *cmmSession) setup() {
	s.run(fixture.Exec("CREATE TABLE cmm_rows(id INT NOT NULL,note TEXT)", 0))
	s.run(fixture.Exec("INSERT INTO cmm_rows VALUES(7,'kept')", 1))
}

func (s *cmmSession) rows() {
	s.t.Helper()
	s.run(fixture.Select("SELECT * FROM cmm_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}}))
}

func (s *cmmSession) set(value string, prepared bool) {
	s.t.Helper()
	step := fixture.Exec("SET client_min_messages TO "+value, 0)
	step.Prepared = prepared
	s.run(step)
}

func (s *cmmSession) prepare(query string) fixture.HeldStatement {
	s.t.Helper()
	before := len(s.notices)
	stmt, err := s.conn.PrepareContext(s.ctx, query)
	if err != nil {
		s.t.Fatal("prepare", query, err)
	}
	s.t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			s.t.Error(err)
		}
	})
	noticesWitness(s.t, s.pg, s.notices[before:], cmmNotice, false)
	return fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: query}
}

func (s *cmmSession) effect(value string, held *fixture.HeldStatement) {
	s.t.Helper()
	before := len(s.notices)
	var executor fixture.Executor = s.conn
	if held != nil {
		executor = *held
	}
	fixture.RunStep(s.t, s.ctx, executor, s.record, fixture.Exec(cmmProducer, 0))
	noticesWitness(s.t, s.pg, s.notices[before:], cmmNotice, value == "notice")
	s.rows()
}

func (s *cmmSession) read(value string, prepared, full bool) {
	s.t.Helper()
	for _, q := range []struct{ query, column string }{
		{"SHOW client_min_messages", "client_min_messages"},
		{"SELECT current_setting('client_min_messages')", "current_setting"},
	} {
		step := fixture.Select(q.query, []string{q.column}, []string{"TEXT"}, [][]any{{value}})
		step.Prepared = prepared
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].ScanType() != reflect.TypeOf("") {
				t.Fatal("text metadata", types, err)
			}
		}
		s.run(step)
	}
	if !full {
		step := fixture.Select("SELECT setting,context FROM pg_settings WHERE name='client_min_messages'", []string{"setting", "context"}, []string{"TEXT", "TEXT"}, [][]any{{value, "user"}})
		step.Prepared = prepared
		s.run(step)
		return
	}
	columns := []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}
	types := []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}
	want := []any{"client_min_messages", value, nil, "Client Connection Defaults / Statement Behavior", "Sets the message levels that are sent to the client.", "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", "user", "enum", "session", nil, nil, "{debug5,debug4,debug3,debug2,debug1,log,notice,warning,error}", "notice", "notice", nil, nil, false}
	step := fixture.Select("SELECT name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,enumvals,boot_val,reset_val,sourcefile,sourceline,pending_restart FROM pg_settings WHERE name='client_min_messages'", columns, types, [][]any{want})
	step.Prepared = prepared
	s.run(step)
}

func (s *cmmSession) invalid() {
	s.t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ query, message, hint string }{
			{"SET client_min_messages TO bogus", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."},
			{"SET client_min_messages TO notice,warning", "SET client_min_messages takes only one argument", ""},
		} {
			step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22023", Message: tc.message, Hint: tc.hint})
			step.ErrorStage = "execute"
			if prepared {
				held := s.prepare(tc.query)
				before := len(s.notices)
				fixture.RunStep(s.t, s.ctx, held, s.record, step)
				noticesWitness(s.t, s.pg, s.notices[before:], cmmNotice, false)
			} else {
				s.run(step)
			}
			s.read("warning", prepared, true)
			s.effect("warning", nil)
		}
	}
}

func TestReferenceClientMinMessagesEffectAndMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			s := cmmOpen(t, p, backend.PG)
			s.setup()
			s.read("notice", false, false)
			held := s.prepare(cmmProducer)
			for _, value := range []string{"notice", "warning", "notice"} {
				s.set(value, false)
				for _, prepared := range []bool{false, true} {
					s.read(value, prepared, true)
				}
				s.effect(value, nil)
				s.effect(value, &held)
			}
			s.set("warning", true)
			s.invalid()
		})
	}
}

func TestReferenceClientMinMessagesSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			s := cmmOpen(t, p, backend.PG)
			s.setup()
			s.set("notice", false)
			s.read("notice", false, true)
			held := s.prepare(cmmProducer)
			s.effect("notice", &held)
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Exec("SAVEPOINT cmm_smoke", 0))
			s.set("warning", true)
			s.read("warning", true, true)
			s.effect("warning", &held)
			s.run(fixture.Exec("ROLLBACK TO cmm_smoke", 0))
			s.read("notice", true, false)
			s.effect("notice", &held)
			s.run(fixture.Exec("ROLLBACK", 0))
			s.set("warning", false)
			s.invalid()
		})
	}
}
