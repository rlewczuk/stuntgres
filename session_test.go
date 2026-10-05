package stuntgres

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"testing"
	"time"
)

func sessionPool(t *testing.T, d *Driver, dsn string) *sql.DB {
	t.Helper()
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func nativeSession(t *testing.T, c *sql.Conn) *engine.Session {
	t.Helper()
	var s *engine.Session
	if err := c.Raw(func(raw any) error { s = raw.(*conn).session; return nil }); err != nil {
		t.Fatal(err)
	}
	return s
}
func sessionShow(t *testing.T, c *sql.Conn, want string, prepared bool) {
	t.Helper()
	ctx := context.Background()
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, "SHOW application_name")
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		rows, err = s.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, "SHOW application_name")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"application_name"}) || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
		t.Fatal(columns, types)
	}
	if !rows.Next() {
		t.Fatal("no SHOW row", rows.Err())
	}
	var got any
	if err = rows.Scan(&got); err != nil || got != want {
		t.Fatalf("SHOW %#v want %q err %v", got, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("SHOW EOF", rows.Err())
	}
}
func sessionSet(t *testing.T, c *sql.Conn, q string, prepared bool) {
	t.Helper()
	ctx := context.Background()
	var r sql.Result
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		r, err = s.ExecContext(ctx)
	} else {
		r, err = c.ExecContext(ctx, q)
	}
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestSessionPhysicalIsolationReuseAndReplacement(t *testing.T) {
	ctx := context.Background()
	d := NewDriver()
	db := sessionPool(t, d, "sessions")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sa, sb := nativeSession(t, a), nativeSession(t, b)
	if sa.Token().ID() == 0 || sa.Token() == sb.Token() || sa.Engine() != sb.Engine() {
		t.Fatal("physical identity")
	}
	sessionSet(t, a, "SET SESSION application_name TO 'session-a'", false)
	sessionSet(t, b, "SET application_name = 'session-b'", true)
	sessionShow(t, a, "session-a", true)
	sessionShow(t, b, "session-b", false)
	for _, q := range []string{"CREATE TABLE sentinel(v INT)", "INSERT INTO sentinel VALUES(7)"} {
		if _, err = a.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	pool := sessionPool(t, d, "sessions")
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	c, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := nativeSession(t, c)
	sessionSet(t, c, "SET application_name TO pooled", false)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nativeSession(t, c) != old {
		t.Fatal("reuse changed session")
	}
	sessionShow(t, c, "pooled", true)
	if err = c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	_ = c.Close()
	c, err = pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fresh := nativeSession(t, c)
	if fresh.Token().ID() <= old.Token().ID() {
		t.Fatal("identity reused")
	}
	sessionShow(t, c, "", false)
	sessionShow(t, b, "session-b", true)
	var v int64
	if err = c.QueryRowContext(ctx, "SELECT v FROM sentinel").Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
	var dump bytes.Buffer
	if err = Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	sessionShow(t, a, "session-a", false)
	if err = Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	sessionShow(t, b, "session-b", true)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if err = pool.Close(); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := sessionPool(t, d, "sessions")
	c, err = reopened.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sessionShow(t, c, "", true)
	if err = c.QueryRowContext(ctx, "SELECT v FROM sentinel").Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
}

// Cancel at Reuse's Err check, after ResetSession's initial check succeeds.
// The error comes from a real cancellable context, not a fabricated return.
type sessionResetCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *sessionResetCancelContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestSessionResetCancellationBetweenChecks(t *testing.T) {
	connector, err := NewDriver().OpenConnector("reset-cancellation")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	defer c.Close()
	if _, err = c.ExecContext(context.Background(), "SET application_name TO retained", nil); err != nil {
		t.Fatal(err)
	}
	peerRaw, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	peer := peerRaw.(*conn)
	defer peer.Close()
	peerToken := peer.session.Token()
	if peerToken == c.session.Token() || peer.session.Engine() != c.session.Engine() {
		t.Fatal("expected independent sessions on shared engine")
	}
	if _, err = peer.ExecContext(context.Background(), "SET application_name TO peer", nil); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"CREATE TABLE sentinel(v INT)", "INSERT INTO sentinel VALUES(7)"} {
		if _, err = c.ExecContext(context.Background(), query, nil); err != nil {
			t.Fatal(err)
		}
	}
	token := c.session.Token()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &sessionResetCancelContext{Context: base, cancel: cancel}
	if err = c.ResetSession(ctx); err != context.Canceled {
		t.Fatalf("reset cancellation: %v", err)
	}
	if ctx.checks != 2 || base.Err() != context.Canceled {
		t.Fatalf("cancellation boundary: checks=%d err=%v", ctx.checks, base.Err())
	}
	if c.session.Token() != token || !c.IsValid() || !c.session.IsLive() {
		t.Fatal("reset cancellation changed physical identity or health")
	}
	if err = c.ResetSession(context.Background()); err != nil {
		t.Fatal("subsequent reuse", err)
	}
	if err = c.Ping(context.Background()); err != nil {
		t.Fatal("subsequent ping", err)
	}
	for _, tc := range []struct {
		conn  *conn
		query string
		want  driver.Value
	}{
		{c, "SHOW application_name", "retained"},
		{peer, "SHOW application_name", "peer"},
		{c, "SELECT v FROM sentinel", int64(7)},
		{peer, "SELECT v FROM sentinel", int64(7)},
	} {
		rows, err := tc.conn.QueryContext(context.Background(), tc.query, nil)
		if err != nil {
			t.Fatal(err)
		}
		values := make([]driver.Value, 1)
		err = rows.Next(values)
		closeErr := rows.Close()
		if err != nil || closeErr != nil || values[0] != tc.want {
			t.Fatalf("%q retained value: %v next=%v close=%v", tc.query, values, err, closeErr)
		}
	}
	if peer.session.Token() != peerToken || !peer.IsValid() {
		t.Fatal("reset cancellation changed peer identity or health")
	}
}

func TestSessionResetExpiredDeadline(t *testing.T) {
	connector, err := NewDriver().OpenConnector("reset-deadline")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	defer c.Close()
	if _, err = c.ExecContext(context.Background(), "SET application_name TO retained", nil); err != nil {
		t.Fatal(err)
	}
	token := c.session.Token()
	// A deadline in the past expires synchronously without sleeping or faking Err.
	expired, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	if expired.Err() != context.DeadlineExceeded {
		t.Fatal("deadline context did not expire", expired.Err())
	}
	err = c.ResetSession(expired)
	if err != context.DeadlineExceeded || errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("reset deadline classification: %v", err)
	}
	var diagnostic interface{ SQLState() string }
	if errors.As(err, &diagnostic) {
		t.Fatalf("deadline fabricated SQLSTATE: %v", err)
	}
	if c.session.Token() != token || !c.session.IsLive() || !c.IsValid() {
		t.Fatal("reset deadline changed physical identity or health")
	}
	if err = c.ResetSession(context.Background()); err != nil {
		t.Fatal("subsequent reset", err)
	}
	if err = c.Ping(context.Background()); err != nil {
		t.Fatal("subsequent ping", err)
	}
	rows, err := c.QueryContext(context.Background(), "SHOW application_name", nil)
	if err != nil {
		t.Fatal("subsequent show", err)
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err = rows.Next(values); err != nil || values[0] != "retained" {
		t.Fatalf("deadline retained setting: %v %v", values, err)
	}
}

func TestSessionAdapterCancellationCleanupAndExhaustion(t *testing.T) {
	ctx, cancelDial := context.WithCancel(context.Background())
	d := NewDriver()
	connector, err := d.OpenConnector("adapter")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	cancelDial()
	if err = c.Ping(context.Background()); err != nil {
		t.Fatal("dial context retained", err)
	}
	if _, err = c.ExecContext(context.Background(), "SET application_name TO safe", nil); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, call := range []func() error{func() error { return c.Ping(canceled) }, func() error { _, err := c.PrepareContext(canceled, "SHOW application_name"); return err }, func() error { _, err := c.ExecContext(canceled, "SET application_name TO lost", nil); return err }, func() error { return c.ResetSession(canceled) }} {
		if err = call(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if !c.IsValid() {
		t.Fatal("cancellation invalidated")
	}
	if err = c.ResetSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := c.QueryContext(context.Background(), "SHOW application_name", nil)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]driver.Value, 1)
	if err = rows.Next(values); err != nil || values[0] != "safe" {
		t.Fatal(values, err)
	}
	rows.Close()
	cleanupSession(cleanupCapsule{c.session})
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.session.Token().ID() != 1 || c.IsValid() {
		t.Fatal("ended health")
	}
	if err = c.ResetSession(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	if _, err = connector.Connect(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	d.mu.Lock()
	id := d.lastSession
	d.lastSession = ^engine.SessionID(0)
	d.mu.Unlock()
	if id != 1 {
		t.Fatal("pre-canceled allocation")
	}
	if _, err = connector.Connect(context.Background()); err == nil {
		t.Fatal("session identity wrapped")
	}
}
func TestSessionDiagnosticsAndPreparedLiveValue(t *testing.T) {
	db := sessionPool(t, NewDriver(), "diagnostics")
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sessionSet(t, c, "SET application_name TO retained", false)
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q, code, message, position string
			prepareFails               bool
		}{
			{"SET absent_setting TO x", "42704", `unrecognized configuration parameter "absent_setting"`, "", false},
			{"SHOW absent_setting", "42704", `unrecognized configuration parameter "absent_setting"`, "", true},
			{`SET "a""b" TO x`, "42704", `unrecognized configuration parameter "a"b"`, "", false},
			{`SHOW "a""b"`, "42704", `unrecognized configuration parameter "a"b"`, "", true},
			{`SET "a\b" TO x`, "42704", `unrecognized configuration parameter "a\b"`, "", false},
			{`SHOW "a\b"`, "42704", `unrecognized configuration parameter "a\b"`, "", true},
			{"SET application_name TO 'a','b'", "22023", "SET application_name takes only one argument", "", false},
			{"SET absent_setting TO 'a','b'", "22023", "SET absent_setting takes only one argument", "", false},
			{`SET "APPLICATION_NAME" TO 'a','b'`, "22023", "SET APPLICATION_NAME takes only one argument", "", false},
			{"SET application_name TO NULL", "42601", `syntax error at or near "NULL"`, "25", true},
			{"SET application_name TO $1", "42601", `syntax error at or near "$1"`, "25", true},
			{"SET application_name TO TO", "42601", `syntax error at or near "TO"`, "25", true},
			{"CREATE TABLE invalid_to(to BOOL)", "42601", `syntax error at or near "to"`, "25", true},
		} {
			var failure error
			if prepared {
				s, e := c.PrepareContext(context.Background(), tc.q)
				if tc.prepareFails {
					failure = e
					if s != nil {
						s.Close()
						t.Fatal("unexpected prepare success")
					}
				} else {
					if e != nil {
						t.Fatal(e)
					}
					_, failure = s.ExecContext(context.Background())
					s.Close()
				}
			} else {
				_, failure = c.ExecContext(context.Background(), tc.q)
			}
			var got *Error
			want := &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
			if !errors.As(failure, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("%q diagnostic %#v want %#v (%v)", tc.q, got, want, failure)
			}
			sessionShow(t, c, "retained", prepared)
		}
	}
	s, err := c.PrepareContext(context.Background(), "SHOW application_name")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, value := range []string{"first", "second"} {
		sessionSet(t, c, "SET application_name TO '"+value+"'", true)
		var got string
		if err = s.QueryRowContext(context.Background()).Scan(&got); err != nil || got != value {
			t.Fatal(got, err)
		}
	}
}

func TestSessionShowCommandCount(t *testing.T) {
	db := sessionPool(t, NewDriver(), "show-command-count")
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		sessionSet(t, c, "SHOW application_name", prepared)
		sessionShow(t, c, "", prepared)
		sessionSet(t, c, "SET application_name TO retained", prepared)
		sessionSet(t, c, "SHOW application_name", prepared)
		sessionShow(t, c, "retained", prepared)
		sessionSet(t, c, "SET application_name TO DEFAULT", prepared)
	}
}
