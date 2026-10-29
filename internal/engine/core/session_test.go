package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

type sessionOwner struct {
	fn func(ids.SessionToken, session.EndReason) error
}

func (o *sessionOwner) EndSession(t ids.SessionToken, r session.EndReason) error { return o.fn(t, r) }

type defaultProvider struct {
	value string
	err   error
}

func (p *defaultProvider) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	if d.Name != "application_name" {
		return "", false, nil
	}
	return p.value, true, p.err
}
func sessionStart(t *testing.T, e *core.Engine, p settings.SessionDefaultProvider) *session.Session {
	t.Helper()
	s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}
func sessionSQL(t *testing.T, s *session.Session, q string) core.Result {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		t.Cleanup(func() {
			if err := r.Rows.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}
func sessionValue(t *testing.T, s *session.Session, want string) {
	t.Helper()
	r := sessionSQL(t, s, "SHOW application_name")
	if r.Count != 0 || !reflect.DeepEqual(r.Rows.Schema(), []catalog.Column{{Name: "application_name", Usage: enginetest.Usage(core.EngineOf(s), sqltype.Text), NullabilityKnown: true}}) || !reflect.DeepEqual(enginetest.Tuples(r), [][]driver.Value{{want}}) {
		t.Fatalf("SHOW count=%d %+v %v want count=0 value=%q", r.Count, r.Rows.Schema(), enginetest.Tuples(r), want)
	}
}
func TestSessionEffectiveDefaultsAndControls(t *testing.T) {
	e := engine.New()
	p := &defaultProvider{value: "provider-a"}
	a := sessionStart(t, e, p)
	b := sessionStart(t, e, &defaultProvider{value: "provider-b"})
	sessionValue(t, a, "provider-a")
	sessionValue(t, b, "provider-b")
	sessionSQL(t, a, "SET application_name TO custom")
	p.value = "replacement"
	sessionSQL(t, a, "SET application_name TO DEFAULT")
	sessionValue(t, a, "provider-a")
	c := sessionStart(t, e, p)
	sessionValue(t, c, "replacement")
	for _, tc := range []struct{ sql, want string }{
		{"'DEFAULT'", "DEFAULT"}, {"+00042", "42"}, {"-00042", "-42"}, {"-00000", "0"},
		{"+0002147483647", "2147483647"}, {"-0002147483647", "-2147483647"},
		{"0002147483648", "0002147483648"}, {"+0002147483648", "0002147483648"},
		{"-0002147483648", "-0002147483648"}, {"+0002147483649", "0002147483649"},
		{"MixedCase", "mixedcase"}, {"true", "true"}, {"'é'", `\xc3\xa9`},
		{"'" + strings.Repeat("a", 64) + "'", strings.Repeat("a", 63)},
		{"'" + strings.Repeat("a", 62) + "é'", strings.Repeat("a", 62)},
		{"'a\n\tb'", `a\x0a\x09b`},
	} {
		sessionSQL(t, a, "SET application_name TO "+tc.sql)
		sessionValue(t, a, tc.want)
	}
	sessionSQL(t, a, "SET application_name TO retained")
	snapshot := sessionSQL(t, a, "SHOW application_name")
	sessionSQL(t, a, "CREATE TABLE sentinel(v INT)")
	sessionSQL(t, a, "INSERT INTO sentinel VALUES(7)")
	var dump strings.Builder
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	restored := engine.New()
	if err := engine.Restore(context.Background(), restored, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	fresh := sessionStart(t, restored, &defaultProvider{value: "fresh-default"})
	sessionValue(t, fresh, "fresh-default")
	if got := enginetest.Tuples(sessionSQL(t, fresh, "SELECT * FROM sentinel")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("dump lost sentinel data", got)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	sessionValue(t, a, "retained")
	sessionValue(t, b, "provider-b")
	if err := engine.Restore(context.Background(), e, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE lost(v INT); SET application_name TO x", "CREATE TABLE lost(v INT); SHOW application_name"} {
		if err := engine.Restore(context.Background(), e, strings.NewReader(q)); err == nil {
			t.Fatal("restore accepted setting")
		}
		if got := enginetest.Tuples(sessionSQL(t, b, "SELECT * FROM sentinel")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
			t.Fatal(got)
		}
	}
	a.RequestEnd(session.CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(enginetest.Tuples(snapshot), [][]driver.Value{{"retained"}}) {
		t.Fatal("snapshot lost")
	}
	failure := errors.New("provider failed")
	if s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: &defaultProvider{err: failure}}); s != nil || !errors.Is(err, failure) {
		t.Fatalf("init %v %v", s, err)
	}
	core.EngineSessionMuRef(e).Lock()
	n := len(core.EngineSessions(e))
	core.EngineSessionMuRef(e).Unlock()
	if n != 2 {
		t.Fatalf("live registry %d", n)
	}
}
func TestSessionDetachEndInterleaving(t *testing.T) {
	e := engine.New()
	s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("detach failure")
	calls := 0
	op, _ := s.Acquire(context.Background())
	h, err := op.Attach(session.Temporary, &sessionOwner{fn: func(token ids.SessionToken, r session.EndReason) error {
		calls++
		if token != s.Token() {
			t.Error("token")
		}
		close(entered)
		<-release
		return failure
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Detach(h, session.CleanClose); err == nil {
		t.Fatal("active detach")
	}
	op.Done()
	op.Done()
	result := make(chan error, 1)
	go func() { result <- s.Detach(h, session.CleanClose) }()
	<-entered
	if _, err = s.Acquire(context.Background()); err == nil {
		t.Fatal("acquire during detach")
	}
	s.RequestEnd(session.Terminated)
	s.RequestEnd(session.Abandoned)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.WaitEnd(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err = <-result; !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err = s.WaitEnd(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if calls != 1 || s.Reason != session.Terminated {
		t.Fatalf("calls/reason %d %d", calls, s.Reason)
	}
	if err = s.Detach(h, session.CleanClose); err != nil {
		t.Fatal(err)
	}
}
func TestSessionTransferDrainAndOrder(t *testing.T) {
	e := engine.New()
	s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Replace the production SQL-name owner only in this callback-order unit.
	if err = s.Detach(s.Resources[session.PreparedSQL].Handle, session.CleanClose); err != nil {
		t.Fatal(err)
	}
	op, _ := s.Acquire(context.Background())
	if _, err = e.Begin(op, session.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(op, enginetest.Statement(t, "DECLARE terminal NO SCROLL CURSOR FOR SELECT 1"), 0, nil); err != nil {
		t.Fatal(err)
	}
	cursor := core.PayloadOf(s).Cursors.Entries["terminal"]
	var order []session.ResourceKind
	var mu sync.Mutex
	for _, kind := range []session.ResourceKind{session.Temporary, session.PreparedSQL} {
		k := kind
		owner := &sessionOwner{fn: func(_ ids.SessionToken, _ session.EndReason) error {
			if s.TransactionState() != session.TxIdle {
				t.Error("transaction cleanup did not precede resources")
			}
			if len(core.PayloadOf(s).Cursors.Entries) != 0 || cursor.Source != nil || core.CursorAuthority(cursor).Host != nil {
				t.Error("named execution cleanup did not precede resources")
			}
			mu.Lock()
			defer mu.Unlock()
			order = append(order, k)
			return nil
		}}
		if _, err = op.Attach(kind, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = op.Attach(kind, owner); err == nil {
			t.Fatal("duplicate")
		}
	}
	var typedNil *sessionOwner
	for _, owner := range []session.SessionResource{nil, typedNil} {
		if _, err = op.Attach(session.Settings, owner); err == nil {
			t.Fatal("invalid owner")
		}
	}
	if _, err = op.Attach(session.AdvisorySession+1, &sessionOwner{fn: func(ids.SessionToken, session.EndReason) error { return nil }}); err == nil {
		t.Fatal("unknown kind")
	}
	copyOp := *op
	copyOp.Done()
	if _, err = e.Describe(&copyOp, enginetest.Statement(t, "SHOW application_name")); err == nil {
		t.Fatal("copied lease")
	}
	s.RequestEnd(session.CleanClose)
	<-op.Context().Done()
	mu.Lock()
	n := len(order)
	mu.Unlock()
	if n != 0 {
		t.Fatal("cleanup before drain")
	}
	op.Done()
	if _, err = op.Attach(session.Settings, &sessionOwner{fn: func(ids.SessionToken, session.EndReason) error {
		t.Error("rejected attachment transferred")
		return nil
	}}); err == nil {
		t.Fatal("ending attach")
	}
	if err = s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []session.ResourceKind{session.PreparedSQL, session.Temporary}) {
		t.Fatal(order)
	}
	lockState := e.Locks.Inspect()
	advisoryLive := lockState.Valid(s.Advisory.Ticket)
	if advisoryLive {
		t.Fatal("built-in advisory delegate survived session cleanup")
	}
	if _, err = e.Execute(op, enginetest.Statement(t, "SHOW application_name"), 0, nil); err == nil {
		t.Fatal("done lease")
	}
	if _, err = engine.New().Describe(op, enginetest.Statement(t, "SHOW application_name")); err == nil {
		t.Fatal("foreign lease")
	}
	if _, err = e.Describe(nil, nil); err == nil {
		t.Fatal("nil lease")
	}
}
func TestSessionCancellationAndStaleHandles(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	op, _ := s.Acquire(context.Background())
	count := 0
	owner := &sessionOwner{fn: func(ids.SessionToken, session.EndReason) error { count++; return nil }}
	h, err := op.Attach(session.Temporary, owner)
	if err != nil {
		t.Fatal(err)
	}
	op.Done()
	if err = s.Detach(h, session.CleanClose); err != nil {
		t.Fatal(err)
	}
	op, _ = s.Acquire(context.Background())
	newHandle, err := op.Attach(session.Temporary, owner)
	if err != nil || newHandle == h {
		t.Fatal(newHandle, err)
	}
	op.Done()
	if err = s.Detach(h, session.CleanClose); err != nil || count != 1 {
		t.Fatal(err, count)
	}
	foreign := h
	foreign.Session = withSessionDomain(foreign.Session, engine.New().Catalog.Domain)
	if err = s.Detach(foreign, session.CleanClose); err == nil {
		t.Fatal("foreign detach")
	}
	sessionSQL(t, s, "SET application_name TO safe")
	ctx, cancel := context.WithCancel(context.Background())
	op, err = s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = e.Execute(op, enginetest.Statement(t, "SET application_name TO lost"), 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	op.Done()
	sessionValue(t, s, "safe")
	if _, err = s.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = s.Detach(s.Resources[session.PreparedSQL].Handle, session.CleanClose); err != nil {
		t.Fatal(err)
	}
	s.Mu.Lock()
	s.Serial = ^uint64(0)
	s.Mu.Unlock()
	op, _ = s.Acquire(context.Background())
	if _, err = op.Attach(session.PreparedSQL, owner); err == nil {
		t.Fatal("serial wrap")
	}
	op.Done()
}

func TestSessionTerminalElectionAndJoinedErrors(t *testing.T) {
	e := engine.New()
	s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	// This unit injects cleanup errors rather than exercising SQL definitions.
	if err = s.Detach(s.Resources[session.PreparedSQL].Handle, session.CleanClose); err != nil {
		t.Fatal(err)
	}
	op, _ := s.Acquire(context.Background())
	first, second := errors.New("first cleanup"), errors.New("second cleanup")
	calls := 0
	for _, kind := range []session.ResourceKind{session.Temporary, session.PreparedSQL} {
		failure := first
		if kind == session.PreparedSQL {
			failure = second
		}
		if _, err = op.Attach(kind, &sessionOwner{fn: func(token ids.SessionToken, reason session.EndReason) error {
			calls++
			if token != s.Token() || reason != s.Reason {
				t.Error("terminal ownership/reason")
			}
			return failure
		}}); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, reason := range []session.EndReason{session.CleanClose, session.Terminated, session.Abandoned} {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; s.RequestEnd(reason) }()
	}
	close(start)
	wg.Wait()
	if op.Context().Err() != context.Canceled {
		t.Fatal("terminal cancellation not delivered")
	}
	op.Done()
	if err = s.WaitEnd(context.Background()); !errors.Is(err, first) || !errors.Is(err, second) || calls != 2 {
		t.Fatalf("cleanup %v calls%d", err, calls)
	}
	if err = s.WaitEnd(context.Background()); !errors.Is(err, first) || calls != 2 {
		t.Fatal(err, calls)
	}
}

func TestSessionPrivateInitializationUnwind(t *testing.T) {
	e := engine.New()
	s := &session.Session{Host: (*core.SessionHost)(e), SessionToken: e.Catalog.Domain.Session(1), State: session.Initializing, Completed: make(chan struct{}), Leases: 1, Removed: make(map[session.ResourceHandle]bool)}
	s.Cond = sync.NewCond(&s.Mu)
	op := s.Operation(context.Background(), true)
	calls := 0
	for _, kind := range []session.ResourceKind{session.Settings, session.Authorization} {
		if _, err := op.Attach(kind, &sessionOwner{fn: func(_ ids.SessionToken, reason session.EndReason) error {
			calls++
			if reason != session.InitFailure {
				t.Error(reason)
			}
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Acquire(context.Background()); err == nil {
		t.Fatal("initializing public lease")
	}
	s.RequestEnd(session.InitFailure)
	if calls != 0 {
		t.Fatal("private lease not drained")
	}
	op.Done()
	if err := s.WaitEnd(context.Background()); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	core.EngineSessionMuRef(e).Lock()
	defer core.EngineSessionMuRef(e).Unlock()
	if len(core.EngineSessions(e)) != 0 {
		t.Fatal("failed initialization published")
	}
}

func TestSessionProviderNormalizationAndInitializationCancellation(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, &defaultProvider{value: "é"})
	sessionValue(t, s, `\xc3\xa9`)
	sessionSQL(t, s, "SET application_name TO override")
	sessionSQL(t, s, "SET application_name TO DEFAULT")
	sessionValue(t, s, `\xc3\xa9`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if session, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: 0}); session != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(session, err)
	}
	if session, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 0}); session != nil || err == nil {
		t.Fatal(session, err)
	}
	if session, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: s.Token().ID()}); session != nil || err == nil {
		t.Fatal("duplicate initialization", session, err)
	}
	sessionValue(t, s, `\xc3\xa9`)
}

func withSessionDomain(token ids.SessionToken, domain *ids.Domain) ids.SessionToken {
	if private := token.Private(); private != nil {
		return domain.PrivateSession(private)
	}
	return domain.Session(token.ID())
}
