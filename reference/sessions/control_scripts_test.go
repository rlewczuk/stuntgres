package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func controlScriptsRoutes(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, r, s)
	fixture.RunStep(t, p.Ctx, x, r, fixture.Prepared(s))
}

func controlScriptsTarget(t *testing.T, p *fixture.Pair) (*sql.DB, *sql.DB, *fixture.Recorder) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector("control-scripts/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	t.Cleanup(func() { raw.Close() })
	db, recorder := fixture.RecordedNativePool(t, connector)
	if err := stuntgres.Reset(p.Ctx, raw); err != nil {
		t.Fatal(err)
	}
	return db, raw, recorder
}

func controlScriptsSeed(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE cs_z_base(id integer,note text)",
		"CREATE FUNCTION cs_y_calc(integer) RETURNS integer BEGIN ATOMIC SELECT $1 + 10; END",
		"CREATE VIEW cs_b_inner AS SELECT id,cs_y_calc(id) AS value FROM cs_z_base",
		"CREATE VIEW cs_a_outer AS SELECT id,value FROM cs_b_inner",
		"CREATE FUNCTION cs_dollar(integer) RETURNS integer LANGUAGE SQL AS $body$ SELECT ';'::text; SELECT $1 + 20; $body$",
	} {
		fixture.RunStep(t, p.Ctx, x, r, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, x, r, fixture.Exec("INSERT INTO cs_z_base VALUES(1,'a;one'),(2,NULL)", 2))
}

func controlScriptsRead(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, extra bool) {
	t.Helper()
	rows := [][]any{{int64(1), int64(11)}, {int64(2), int64(12)}}
	if extra {
		rows = append(rows, []any{int64(3), int64(13)})
	}
	controlScriptsRoutes(t, p, x, r, fixture.Select("SELECT id,value FROM cs_a_outer ORDER BY id", []string{"id", "value"}, []string{"INT4", "INT4"}, rows))
	controlScriptsRoutes(t, p, x, r, fixture.Select("SELECT cs_dollar(5) AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(25)}}))
}

func TestReferenceControlScriptsDependencyRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			controlScriptsSeed(t, p, b.DB, b.Recorder)
			controlScriptsRead(t, p, b.DB, b.Recorder, false)
		})
	}
	var first, second bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &first); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("nondeterministic dump")
	}
	db, raw, r := controlScriptsTarget(t, p)
	fixture.RunStep(t, p.Ctx, db, r, fixture.Exec("CREATE TABLE cs_sentinel(v integer)", 0))
	fixture.RunStep(t, p.Ctx, db, r, fixture.Exec("INSERT INTO cs_sentinel VALUES(77)", 1))
	old, err := db.QueryContext(p.Ctx, "SELECT v FROM cs_sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(first.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.CatalogControlNativeValues(t, old, 77)
	controlScriptsRead(t, p, db, r, false)
	missing := fixture.ControlsReferenceDiagnostic("42P01", "relation \"cs_sentinel\" does not exist")
	missing.Position = "15"
	fixture.RunStep(t, p.Ctx, db, r, fixture.ErrorDiagnostic("SELECT v FROM cs_sentinel", missing))
	var restored bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, raw, &restored); err != nil {
		t.Fatal(err)
	}
	if first.String() != restored.String() {
		t.Fatal("logical dump changed after independent restore")
	}
	fixture.RunStep(t, p.Ctx, db, r, fixture.Exec("INSERT INTO cs_z_base VALUES(3,'three')", 1))
	controlScriptsRead(t, p, db, r, true)
	controlScriptsRead(t, p, p.Memory, p.Backends()[0].Recorder, false)
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, first.String()); err != nil {
			t.Fatal(err)
		}
		for _, b := range p.Backends() {
			if b.PG {
				controlScriptsRead(t, p, b.DB, b.Recorder, false)
			}
		}
	}
}

// The first Read returns a valid statement prefix; the second fails. Restore
// must not execute the prefix or turn this adapter error into a SQL diagnostic.
type controlScriptsPrefixReader struct {
	sent bool
	err  error
}

func (r *controlScriptsPrefixReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, r.err
	}
	r.sent = true
	return copy(p, "CREATE TABLE must_not_publish(v integer);"), nil
}

type controlScriptsShortWriter struct{}

func (controlScriptsShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestReferenceControlScriptsAtomicFailures(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct{ sql, code, message, detail string }{
				{"CREATE TABLE cs_parse(", "42601", "syntax error at end of input", ""},
				{"CREATE VIEW cs_bad AS SELECT v FROM cs_missing", "42P01", "relation \"cs_missing\" does not exist", ""},
				{"CREATE FUNCTION cs_bad() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$", "42P13", "return type mismatch in function declared to return integer", "Actual return type is text."},
			} {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				if tc.code == "42601" {
					d.Position = fixture.Position(len(tc.sql) + 1)
				}
				if tc.code == "42P01" {
					d.Position = fixture.Position(strings.Index(tc.sql, "cs_missing") + 1)
				}
				if tc.code == "42P13" {
					d.Context = "SQL function \"cs_bad\""
				}
				controlScriptsRoutes(t, p, b.DB, b.Recorder, fixture.ErrorDiagnostic(tc.sql, d))
			}
			// Invocation is a separate phase from the enabled CREATE witness
			// above. Start valid and make the quoted result stale lawfully.
			for _, q := range []string{
				"CREATE TABLE cs_return(v integer)",
				"CREATE FUNCTION cs_bad() RETURNS integer LANGUAGE SQL AS $$SELECT v FROM cs_return$$",
				"DROP TABLE cs_return", "CREATE TABLE cs_return(v text)",
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			d := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
			d.Detail = "Actual return type is text."
			d.Context = "SQL function \"cs_bad\" statement 1"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT cs_bad()", d)
				s.SelectRows, s.ErrorStage, s.Prepared = true, "execute", prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			s := fixture.ErrorDiagnostic("SELECT cs_bad()", d)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		})
	}
	db, raw, r := controlScriptsTarget(t, p)
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE TABLE cs_sentinel(v integer)", "CREATE VIEW cs_kept AS SELECT v FROM cs_sentinel", "SET application_name='control-target'"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, db, r, fixture.Exec("INSERT INTO cs_sentinel VALUES(77)", 1))
	ioFailure := errors.New("control script reader failure")
	for _, name := range []string{"corrupt suffix", "missing dependency", "deferred return", "reader prefix", "pre-cancel", "cancel read"} {
		t.Run(name, func(t *testing.T) {
			old, err := db.QueryContext(p.Ctx, "SELECT v FROM cs_kept")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			var src io.Reader = strings.NewReader("CREATE TABLE must_not_publish(v integer); CREATE TABLE broken(")
			var ordinary error
			var want fixture.Diagnostic
			switch name {
			case "corrupt suffix":
				want = fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
				want.Position = fixture.Position(len("CREATE TABLE must_not_publish(v integer); CREATE TABLE broken(") + 1)
			case "missing dependency":
				q := "CREATE TABLE must_not_publish(v integer); CREATE VIEW cs_bad AS SELECT v FROM cs_missing"
				src = strings.NewReader(q)
				want = fixture.ControlsReferenceDiagnostic("42P01", "relation \"cs_missing\" does not exist")
				want.Position = fixture.Position(strings.Index(q, "cs_missing") + 1)
			case "deferred return":
				src = strings.NewReader("SET check_function_bodies = false; CREATE TABLE must_not_publish(v integer); CREATE FUNCTION cs_bad() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$; CREATE TABLE cs_reached(v integer DEFAULT cs_bad()); INSERT INTO cs_reached DEFAULT VALUES;")
				want = fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
				want.Detail = "Actual return type is text."
				want.Context = "SQL function \"cs_bad\" statement 1"
			case "reader prefix":
				src = &controlScriptsPrefixReader{err: ioFailure}
				ordinary = ioFailure
			case "pre-cancel":
				cancel()
				ordinary = context.Canceled
			case "cancel read":
				src = &fixture.CatalogControlCancelReader{Cancel: cancel}
				ordinary = context.Canceled
			}
			err = stuntgres.Restore(ctx, raw, src)
			if ordinary != nil {
				if !errors.Is(err, ordinary) {
					t.Fatalf("got %v want %v", err, ordinary)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary error acquired SQLSTATE", err)
				}
			} else {
				fixture.AssertControlDiagnostic(t, err, want)
			}
			fixture.CatalogControlNativeValues(t, old, 77)
			controlScriptsRoutes(t, p, db, r, fixture.Select("SELECT v FROM cs_kept", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
			fixture.RunStep(t, p.Ctx, db, r, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='must_not_publish'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"control-target"}}))
		})
	}
	for _, tc := range []struct {
		writer io.Writer
		want   error
	}{{fixture.CatalogControlFailingWriter{Err: ioFailure}, ioFailure}, {controlScriptsShortWriter{}, io.ErrShortWrite}} {
		err := stuntgres.Dump(p.Ctx, raw, tc.writer)
		if !errors.Is(err, tc.want) {
			t.Fatalf("writer error %v want %v", err, tc.want)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("writer SQLSTATE", err)
		}
	}
	for _, restoring := range []bool{false, true} {
		t.Run(map[bool]string{false: "writer releases catalog lock", true: "reader releases catalog lock"}[restoring], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			if restoring {
				reader := &fixture.CatalogControlGatedReader{Started: started, Release: release}
				go func() { done <- stuntgres.Restore(p.Ctx, raw, reader) }()
			} else {
				writer := &fixture.CatalogControlGatedWriter{Started: started, Release: release}
				go func() { done <- stuntgres.Dump(p.Ctx, raw, writer) }()
			}
			// Release and join on every exit, including a failed progress query.
			defer func() {
				close(release)
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			select {
			case <-started:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			controlScriptsRoutes(t, p, c, r, fixture.Select("SELECT v FROM cs_kept", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
		})
	}
}

func TestReferenceControlScriptsSessionBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET application_name='control-target'", 0))
			controlScriptsRoutes(t, p, c, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"control-target"}}))
			if b.PG {
				return
			}
			for _, script := range []string{"SET application_name='must-not-leak';", "SELECT set_config('application_name','must-not-leak',false);"} {
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
				fixture.State(t, err, "0A000")
				controlScriptsRoutes(t, p, c, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"control-target"}}))
			}
			forward := "SET check_function_bodies = false; CREATE FUNCTION cs_forward() RETURNS integer LANGUAGE SQL AS $$SELECT cs_later()$$; CREATE FUNCTION cs_later() RETURNS integer LANGUAGE SQL AS $$SELECT 9$$;"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(forward)); err != nil {
				t.Fatal(err)
			}
			controlScriptsRoutes(t, p, c, b.Recorder, fixture.Select("SELECT cs_forward() AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(9)}}))
			controlScriptsRoutes(t, p, c, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"control-target"}}))
		})
	}
}
