package logical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// recorder is the private output plugin: it renders every callback of the
// real native stream as one text row. Options: "binary", "stream",
// "rewrites", "local" (skip transactions with an origin) and "fail" (the
// callback name that returns an error).
type recorder struct {
	opts  map[string]string
	probe *pluginProbe
}

// pluginProbe observes plugin lifetimes across decoding contexts.
type pluginProbe struct {
	mu        sync.Mutex
	startups  []logical.Startup
	shutdowns int
	// gate, when armed, blocks the next Begin until released: that decoder
	// then holds its slot.
	gate, entered chan struct{}
}

// arm blocks the next Begin callback. entered is closed once a decoder waits
// there; release lets it continue.
func (p *pluginProbe) arm() (entered <-chan struct{}, release func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gate, p.entered = make(chan struct{}), make(chan struct{})
	gate := p.gate
	return p.entered, func() { close(gate) }
}

func (p *pluginProbe) wait() {
	p.mu.Lock()
	gate, entered := p.gate, p.entered
	p.gate, p.entered = nil, nil
	p.mu.Unlock()
	if gate != nil {
		close(entered)
		<-gate
	}
}

func (p *pluginProbe) record(s logical.Startup) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.startups = append(p.startups, s)
}

func (p *pluginProbe) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.startups), p.shutdowns
}

func (r *recorder) Startup(s logical.Startup) (logical.Options, error) {
	r.probe.record(s)
	r.opts = map[string]string{}
	for _, o := range s.Options {
		switch o.Name {
		case "binary", "stream", "rewrites", "local", "fail":
			r.opts[o.Name] = o.Value
		default:
			return logical.Options{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`option "%s" = "%s" is unknown`, o.Name, o.Value)}
		}
	}
	if r.opts["fail"] == "startup" {
		return logical.Options{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "recorder: startup failure"}
	}
	return logical.Options{Binary: r.opts["binary"] == "1", Streaming: r.opts["stream"] == "1", ReceiveRewrites: r.opts["rewrites"] == "1"}, nil
}

func (r *recorder) fail(o *logical.Output, callback, line string) error {
	if r.opts["fail"] == callback {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "recorder: " + callback + " failure"}
	}
	o.Write([]byte(line))
	return nil
}

func (r *recorder) Begin(o *logical.Output, t *logical.Txn) error {
	r.probe.wait()
	return r.fail(o, "begin", "BEGIN")
}

func (r *recorder) Change(o *logical.Output, t *logical.Txn, c *logical.Change) error {
	rel := c.Relation.Schema + "." + c.Relation.Name
	var line string
	switch c.Kind {
	case logical.Insert:
		line = "INSERT " + rel + " " + renderTuple(c.Relation, c.New)
	case logical.Update:
		line = "UPDATE " + rel + oldPart(c) + " " + renderTuple(c.Relation, c.New)
	case logical.Delete:
		line = "DELETE " + rel + oldPart(c)
	}
	return r.fail(o, "change", line)
}

func (r *recorder) Truncate(o *logical.Output, t *logical.Txn, c *logical.Change) error {
	names := make([]string, len(c.Truncated))
	for i, rel := range c.Truncated {
		names[i] = rel.Schema + "." + rel.Name
	}
	line := "TRUNCATE " + strings.Join(names, ",")
	if c.Cascade {
		line += " cascade"
	}
	if c.RestartIdentity {
		line += " restart"
	}
	return r.fail(o, "truncate", line)
}

func (r *recorder) Commit(o *logical.Output, t *logical.Txn) error {
	return r.fail(o, "commit", "COMMIT")
}

func (r *recorder) BeginPrepare(o *logical.Output, t *logical.Txn) error {
	return r.fail(o, "begin", "BEGIN PREPARE "+t.GID)
}

func (r *recorder) Prepare(o *logical.Output, t *logical.Txn) error {
	return r.fail(o, "prepare", "PREPARE "+t.GID)
}

func (r *recorder) CommitPrepared(o *logical.Output, t *logical.Txn) error {
	return r.fail(o, "commit", "COMMIT PREPARED "+t.GID)
}

func (r *recorder) RollbackPrepared(o *logical.Output, t *logical.Txn) error {
	return r.fail(o, "rollback", "ROLLBACK PREPARED "+t.GID)
}

func (r *recorder) FilterByOrigin(origin uint32) bool { return r.opts["local"] == "1" }

func (r *recorder) Shutdown() {
	r.probe.mu.Lock()
	r.probe.shutdowns++
	r.probe.mu.Unlock()
}

// plainPlugin has only the required callbacks: no TRUNCATE, no two-phase and
// no origin filter.
type plainPlugin struct{ r recorder }

func (p *plainPlugin) Startup(s logical.Startup) (logical.Options, error) { return p.r.Startup(s) }
func (p *plainPlugin) Begin(o *logical.Output, t *logical.Txn) error      { return p.r.Begin(o, t) }
func (p *plainPlugin) Change(o *logical.Output, t *logical.Txn, c *logical.Change) error {
	return p.r.Change(o, t, c)
}
func (p *plainPlugin) Commit(o *logical.Output, t *logical.Txn) error { return p.r.Commit(o, t) }
func (p *plainPlugin) Shutdown()                                      { p.r.Shutdown() }

func renderTuple(r *logical.Relation, values []sqltype.Value) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = r.Columns[i].Name + "[" + r.Columns[i].TypeName + "]:" + render(v)
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func oldPart(c *logical.Change) string {
	switch c.OldTuple {
	case logical.OldKey:
		return " old-key:" + renderTuple(c.Relation, c.Old)
	case logical.OldFull:
		return " old:" + renderTuple(c.Relation, c.Old)
	}
	return ""
}

func render(v sqltype.Value) string {
	if v.IsNull() {
		return "null"
	}
	text, err := v.Type().Output(context.Background(), v)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return "'" + text + "'"
}

// fixture is one cluster started with wal_level=logical and the recorder
// registered as "recorder" (and the reduced plugin as "plain").
type fixture struct {
	t     *testing.T
	e     *core.Engine
	probe *pluginProbe
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	probe := &pluginProbe{}
	plugins := map[string]logical.Factory{
		"recorder": func() logical.Plugin { return &recorder{probe: probe} },
		"plain":    func() logical.Plugin { return &plainPlugin{recorder{probe: probe}} },
	}
	e := engine.NewWithStartup(nil, "postgres", core.Startup{LogicalWAL: true, OutputPlugins: plugins})
	return &fixture{t: t, e: e, probe: probe}
}

// session starts a superuser session that may name the test plugins.
func (f *fixture) session() *session.Session {
	f.t.Helper()
	s := enginetest.StartSession(f.t, f.e, nil)
	f.must(s, "SET output_plugin_libraries = recorder, plain, missing")
	return s
}

// run executes one statement as the driver does: admitted by StartStatement
// and completed by Finish, so a failure is a top-level error. Rows render as
// output text, nil for NULL.
func run(s *session.Session, sql string) ([][]any, error) {
	return runContext(context.Background(), s, sql)
}

func runContext(ctx context.Context, s *session.Session, sql string) (rows [][]any, err error) {
	op, err := s.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer op.Done()
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		return nil, err
	}
	defer func() { err = scope.Finish(err) }()
	statements, err := parser.Parse(sql)
	if err != nil {
		return nil, err
	}
	if len(statements) != 1 {
		return nil, fmt.Errorf("expected one statement in %q", sql)
	}
	r, err := scope.Execute(statements[0], 0, nil)
	if err != nil || r.Rows == nil {
		return nil, err
	}
	defer r.Rows.Close()
	return readRows(ctx, r.Rows)
}

// runBatch executes a multi-statement message as the driver's simple query
// does: one implicit transaction committed by FinishMessage.
func runBatch(s *session.Session, sql string) (err error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer op.Done()
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		return err
	}
	statements, err := parser.Parse(sql)
	if err != nil {
		return scope.Finish(err)
	}
	if err = scope.RetainMessage(); err != nil {
		return scope.Finish(err)
	}
	defer func() { err = scope.FinishMessage(err) }()
	for i, statement := range statements {
		if i != 0 {
			if scope, err = scope.NextStatement(); err != nil {
				return err
			}
		}
		r, err := scope.Execute(statement, 0, nil)
		if err == nil && r.Rows != nil {
			_, err = readRows(context.Background(), r.Rows)
			r.Rows.Close()
		}
		if err = scope.Finish(err); err != nil {
			return err
		}
	}
	return nil
}

// copyIn runs COPY ... FROM STDIN with data as one top-level statement.
func copyIn(s *session.Session, sql, data string) (err error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer op.Done()
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		return err
	}
	defer func() { err = scope.Finish(err) }()
	statements, err := parser.Parse(sql)
	if err != nil {
		return err
	}
	_, err = scope.Copy(statements[0].(*parser.Copy), strings.NewReader(data), nil)
	return err
}

func readRows(ctx context.Context, result exec.RowResult) ([][]any, error) {
	cursor := enginetest.IndependentFinalRows(result)
	defer cursor.Close()
	var rows [][]any
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(ctx, values)
		if err != nil || !present {
			return rows, err
		}
		row := make([]any, len(values))
		for i, v := range values {
			if !v.IsNull() {
				text, err := v.Type().Output(ctx, v)
				if err != nil {
					return nil, err
				}
				row[i] = text
			}
		}
		rows = append(rows, row)
	}
}

func (f *fixture) must(s *session.Session, sql string) [][]any {
	f.t.Helper()
	rows, err := run(s, sql)
	if err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return rows
}

// fails requires sql to fail with code and message.
func (f *fixture) fails(s *session.Session, sql, code, message string) *sqlstate.Error {
	f.t.Helper()
	_, err := run(s, sql)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message {
		f.t.Fatalf("%s: got %v, want %s %s", sql, err, code, message)
	}
	return d
}

// value returns the single value of sql.
func (f *fixture) value(s *session.Session, sql string) any {
	f.t.Helper()
	rows := f.must(s, sql)
	if len(rows) != 1 || len(rows[0]) != 1 {
		f.t.Fatalf("%s: %v", sql, rows)
	}
	return rows[0][0]
}

// data returns the data column of a decoding call.
func (f *fixture) data(s *session.Session, call string) []string {
	f.t.Helper()
	rows := f.must(s, "SELECT data FROM "+call)
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row[0].(string)
	}
	return out
}

// changes returns pg_logical_slot_get_changes(slot, NULL, NULL, options...).
func (f *fixture) changes(s *session.Session, slot string, options ...string) []string {
	f.t.Helper()
	return f.data(s, "pg_logical_slot_get_changes('"+slot+"', NULL, NULL"+optionList(options)+")")
}

func (f *fixture) peek(s *session.Session, slot string, options ...string) []string {
	f.t.Helper()
	return f.data(s, "pg_logical_slot_peek_changes('"+slot+"', NULL, NULL"+optionList(options)+")")
}

func optionList(options []string) string {
	var b strings.Builder
	for _, o := range options {
		b.WriteString(", '" + o + "'")
	}
	return b.String()
}

func (f *fixture) slot(s *session.Session, name string) {
	f.t.Helper()
	f.must(s, "SELECT pg_create_logical_replication_slot('"+name+"', 'recorder')")
}

func (f *fixture) expect(got []string, want ...string) {
	f.t.Helper()
	if !slices.Equal(got, want) {
		f.t.Fatalf("decoded\n  got  %q\n  want %q", got, want)
	}
}

func tx(lines ...string) []string {
	return append(append([]string{"BEGIN"}, lines...), "COMMIT")
}

func concat(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// newReplicaEngine is a cluster with the default wal_level=replica.
func newReplicaEngine() *core.Engine { return engine.NewWithStartup(nil, "postgres", core.Startup{}) }
