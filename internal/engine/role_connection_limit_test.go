package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"sync"
	"testing"
	"time"
)

func roleLimitStart(e *Engine, ref authorization.PrincipalRef, id ids.SessionID, p settings.SessionDefaultProvider) (*Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.StartSession(ctx, SessionStart{ID: id, Authenticated: ref, Defaults: p})
}
func roleLimitClose(t *testing.T, s *Session) {
	t.Helper()
	s.RequestEnd(CleanClose)
	s.RequestEnd(CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
}
func roleLimitDenied(t *testing.T, e *Engine, ref authorization.PrincipalRef, name string) {
	t.Helper()
	s, err := roleLimitStart(e, ref, ids.SessionID(testSessionID.Add(1)), nil)
	var d *sqlstate.Error
	want := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: "too many connections for role \"" + name + "\""}
	if s != nil || !errors.As(err, &d) || !reflect.DeepEqual(d, want) {
		if s != nil {
			roleLimitClose(t, s)
		}
		t.Fatalf("admission: %v %#v want %#v", s, err, want)
	}
}
func roleLimitOpen(t *testing.T, e *Engine, ref authorization.PrincipalRef) *Session {
	t.Helper()
	s, err := roleLimitStart(e, ref, ids.SessionID(testSessionID.Add(1)), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { roleLimitClose(t, s) })
	return s
}
func TestRoleAttributesConnectionLimitLifecycle(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	d := roleAttributeCreate(t, e, admin, "limited", true, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 0})
	roleLimitDenied(t, e, d.Ref, d.Name)
	alter := func(n int32) {
		roleAttributeApply(t, e, admin, roleAttributeAlter("limited", parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: n}))
	}
	alter(1)
	first := roleLimitOpen(t, e, d.Ref)
	roleLimitDenied(t, e, d.Ref, d.Name)
	// Acquire/reuse are borrows of one physical session, not new admissions.
	for range 3 {
		op, err := first.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		op.Done()
		if err := first.Reuse(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	roleLimitDenied(t, e, d.Ref, d.Name)
	alter(0)
	op, err := first.Acquire(context.Background())
	if err != nil {
		t.Fatal("limit decrease killed incumbent", err)
	}
	op.Done()
	roleLimitDenied(t, e, d.Ref, d.Name)
	alter(2)
	second := roleLimitOpen(t, e, d.Ref)
	roleLimitDenied(t, e, d.Ref, d.Name)
	roleLimitClose(t, first)
	replacement := roleLimitOpen(t, e, d.Ref)
	roleLimitClose(t, second)
	roleLimitClose(t, replacement)
	alter(-1)
	for range 3 {
		roleLimitOpen(t, e, d.Ref)
	}
	other := roleAttributeCreate(t, e, admin, "separate", true, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 1})
	roleLimitOpen(t, e, other.Ref)
	super := roleAttributeCreate(t, e, admin, "exempt", true, parser.RoleOptions{Present: parser.RoleSuperuser | parser.RoleConnectionLimit, Superuser: true, ConnectionLimit: 0})
	roleLimitOpen(t, e, super.Ref)
	roleLimitOpen(t, e, super.Ref)
	roleAttributeApply(t, e, admin, roleAttributeAlter("exempt", parser.RoleOptions{Present: parser.RoleLogin}))
	wantLogin := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: `role "exempt" is not permitted to log in`}
	for _, admission := range []func() (*Session, error){
		func() (*Session, error) {
			_, err := e.ResolveLogin("exempt")
			return nil, err
		},
		func() (*Session, error) {
			return roleLimitStart(e, super.Ref, ids.SessionID(testSessionID.Add(1)), nil)
		},
	} {
		s, err := admission()
		var diag *sqlstate.Error
		if s != nil || !errors.As(err, &diag) || !reflect.DeepEqual(diag, wantLogin) {
			if s != nil {
				roleLimitClose(t, s)
			}
			t.Fatalf("superuser NOLOGIN admission: %v %#v want %#v", s, err, wantLogin)
		}
	}
	// Trusted assertion of an unknown principal is a host API error, not a
	// PostgreSQL authentication handshake (which this connector does not perform).
	_, missingErr := e.ResolveLogin("absent")
	var missingDiag *sqlstate.Error
	if missingErr == nil || missingErr.Error() != `stuntgres: authenticated role "absent" does not exist` || errors.As(missingErr, &missingDiag) {
		t.Fatalf("trusted nonexistent principal boundary: %v", missingErr)
	}
	// Count attribution remains physical authenticated even across authorization.
	physical := roleLimitOpen(t, e, e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"))
	identity, err := physical.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	roleAttributeApply(t, e, identity, &parser.SessionAuthorization{Name: "separate"})
	roleAttributeApply(t, e, identity, &parser.RoleSelection{Reset: true})
	roleAttributeApply(t, e, identity, &parser.SessionAuthorization{Default: true})
	identity.Done()
	e.sessionMu.Lock()
	count := e.roleSessionCountLocked(other.Ref)
	e.sessionMu.Unlock()
	if count != 1 || physical.countedAuthenticated != e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres") {
		t.Fatal("identity switch changed accounting", count)
	}
}

type roleLimitGate struct {
	entered, release chan struct{}
	once             sync.Once
	err              error
}

func (g *roleLimitGate) SessionDefault(ctx context.Context, d settings.Definition) (string, bool, error) {
	if d.Name != "application_name" {
		return "", false, nil
	}
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
		return "", false, g.err
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

type roleLimitStartResult struct {
	s   *Session
	err error
}

func roleLimitReceive(t *testing.T, ch <-chan roleLimitStartResult) roleLimitStartResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("session registration did not finish")
		return roleLimitStartResult{}
	}
}

type roleLimitEndGate struct {
	owner            SessionResource
	entered, release chan struct{}
}

func (g *roleLimitEndGate) EndSession(token ids.SessionToken, reason EndReason) error {
	err := g.owner.EndSession(token, reason)
	close(g.entered)
	<-g.release
	return err
}

type roleLimitFenceContext struct {
	context.Context
	e        *Engine
	cancel   context.CancelFunc
	observed bool
}

func (c *roleLimitFenceContext) Err() error {
	if c.e.sessionMu.TryLock() {
		c.e.sessionMu.Unlock()
	} else {
		c.observed = true
		c.cancel()
	}
	return c.Context.Err()
}
func TestRoleAttributesConnectionLimitRaces(t *testing.T) {
	// A canceled initializer must not claim a slot at the registry fence.
	t.Run("canceled final registration", func(t *testing.T) {
		e := New()
		admin, done := roleOperation(t, e, "postgres")
		defer done()
		d := roleAttributeCreate(t, e, admin, "final_cancel", true, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 1})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fence := &roleLimitFenceContext{Context: ctx, e: e, cancel: cancel}
		s, err := e.StartSession(fence, SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: d.Ref})
		if s != nil || !errors.Is(err, context.Canceled) || !fence.observed {
			t.Fatal("registry cancellation consumed slot", s, err)
		}
		roleLimitOpen(t, e, d.Ref)
	})
	t.Run("simultaneous admissions", func(t *testing.T) {
		e := New()
		admin, done := roleOperation(t, e, "postgres")
		defer done()
		d := roleAttributeCreate(t, e, admin, "race", true, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 1})
		ready := make(chan struct{})
		results := make(chan roleLimitStartResult, 2)
		for range 2 {
			go func() {
				<-ready
				s, err := roleLimitStart(e, d.Ref, ids.SessionID(testSessionID.Add(1)), nil)
				results <- roleLimitStartResult{s, err}
			}()
		}
		close(ready)
		a, b := roleLimitReceive(t, results), roleLimitReceive(t, results)
		successes := 0
		for _, r := range []roleLimitStartResult{a, b} {
			if r.err == nil {
				successes++
				roleLimitClose(t, r.s)
			} else {
				var diag *sqlstate.Error
				if !errors.As(r.err, &diag) || diag.Code != "53300" {
					t.Fatal(r.err)
				}
			}
		}
		if successes != 1 {
			t.Fatal("not one physical winner", successes)
		}
	})
	for _, change := range []string{"nologin", "limit", "drop recreate", "cancel", "failed initialization", "duplicate ID"} {
		t.Run(change, func(t *testing.T) {
			e := New()
			admin, done := roleOperation(t, e, "postgres")
			defer done()
			d := roleAttributeCreate(t, e, admin, "initializing", true, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 1})
			gate := &roleLimitGate{entered: make(chan struct{}), release: make(chan struct{})}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			id := ids.SessionID(testSessionID.Add(1))
			results := make(chan roleLimitStartResult, 1)
			go func() {
				s, err := e.StartSession(ctx, SessionStart{ID: id, Authenticated: d.Ref, Defaults: gate})
				results <- roleLimitStartResult{s, err}
			}()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("init gate", ctx.Err())
			}
			// No initialized callback consumes a physical slot.
			e.sessionMu.Lock()
			count := e.roleSessionCountLocked(d.Ref)
			e.sessionMu.Unlock()
			if count != 0 {
				t.Fatal("initialization consumed slot", count)
			}
			switch change {
			case "nologin":
				roleAttributeApply(t, e, admin, roleAttributeAlter(d.Name, parser.RoleOptions{Present: parser.RoleLogin}))
			case "limit":
				roleAttributeApply(t, e, admin, roleAttributeAlter(d.Name, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 0}))
			case "drop recreate":
				roleAttributeApply(t, e, admin, &parser.DropRole{Names: []string{d.Name}})
				roleAttributeCreate(t, e, admin, d.Name, true, parser.RoleOptions{})
			case "cancel":
				cancel()
			case "duplicate ID":
				s, err := roleLimitStart(e, d.Ref, id, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { roleLimitClose(t, s) })
			case "failed initialization":
				gate.err = errors.New("settings initialization failed")
			}
			close(gate.release)
			r := roleLimitReceive(t, results)
			if r.err == nil || r.s != nil {
				if r.s != nil {
					roleLimitClose(t, r.s)
				}
				t.Fatal("stale/canceled init registered", r.err)
			}
			if change == "nologin" {
				var diag *sqlstate.Error
				want := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: `role "initializing" is not permitted to log in`}
				if !errors.As(r.err, &diag) || !reflect.DeepEqual(diag, want) {
					t.Fatalf("final NOLOGIN fence: %#v want %#v", r.err, want)
				}
			}
			e.sessionMu.Lock()
			count = e.roleSessionCountLocked(d.Ref)
			e.sessionMu.Unlock()
			want := 0
			if change == "duplicate ID" {
				want = 1
			}
			if count != want {
				t.Fatal("failed init leaked count", count, want)
			}
		})
	}
	t.Run("peer mutex is not count authority", func(t *testing.T) {
		e := New()
		admin, done := roleOperation(t, e, "postgres")
		defer done()
		d := roleAttributeCreate(t, e, admin, "peer", true, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 2})
		first := roleLimitOpen(t, e, d.Ref)
		first.mu.Lock()
		results := make(chan roleLimitStartResult, 1)
		go func() {
			s, err := roleLimitStart(e, d.Ref, ids.SessionID(testSessionID.Add(1)), nil)
			results <- roleLimitStartResult{s, err}
		}()
		select {
		case r := <-results:
			first.mu.Unlock()
			if r.err != nil {
				t.Fatal(r.err)
			}
			roleLimitClose(t, r.s)
		case <-time.After(5 * time.Second):
			first.mu.Unlock()
			r := roleLimitReceive(t, results)
			if r.s != nil {
				roleLimitClose(t, r.s)
			}
			t.Fatal("counting locked peer session")
		}
	})
	t.Run("teardown keeps count after authorization release", func(t *testing.T) {
		e := New()
		admin, done := roleOperation(t, e, "postgres")
		defer done()
		d := roleAttributeCreate(t, e, admin, "ending", true, parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 1})
		s := roleLimitOpen(t, e, d.Ref)
		gate := &roleLimitEndGate{entered: make(chan struct{}), release: make(chan struct{})}
		var release sync.Once
		unblock := func() { release.Do(func() { close(gate.release) }) }
		t.Cleanup(unblock)
		s.mu.Lock()
		entry := s.resources[Authorization]
		gate.owner = entry.owner
		entry.owner = gate
		s.mu.Unlock()
		s.RequestEnd(CleanClose)
		select {
		case <-gate.entered:
		case <-time.After(5 * time.Second):
			unblock()
			t.Fatal("authorization teardown did not arrive")
		}
		roleLimitDenied(t, e, d.Ref, d.Name)
		unblock()
		roleLimitClose(t, s)
		roleLimitOpen(t, e, d.Ref)
	})
}

func TestRoleAttributesPasswordCancellationLifecycle(t *testing.T) {
	for _, creating := range []bool{false, true} {
		for _, teardown := range []bool{false, true} {
			t.Run(fmt.Sprintf("create=%v/teardown=%v", creating, teardown), func(t *testing.T) {
				e := New()
				admin, done := roleOperation(t, e, "postgres")
				defer done()
				owner := roleAttributeCreate(t, e, admin, "cancel_owner", true, parser.RoleOptions{
					Present:    parser.RoleCreateRole | parser.RoleCreateDB | parser.RoleReplication | parser.RoleBypassRLS | parser.RoleConnectionLimit | parser.RolePassword | parser.RoleValidUntil,
					CreateRole: true, CreateDB: true, Replication: true, BypassRLS: true, ConnectionLimit: 1, Password: "retained", ValidUntil: "2000-01-01",
				})
				s := roleLimitOpen(t, e, owner.Ref)
				setup, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				// Retain a real automatic ADMIN edge alongside every owner field.
				roleAttributeCreate(t, e, setup, "retained_child", false, parser.RoleOptions{})
				setup.Done()
				beforePublication := e.roles.published
				before := e.Roles()
				next, revision := e.roles.nextPrincipal, beforePublication.Revision
				salt := []byte("0123456789abcdef")
				secret := strings.Replace(passwordExpectedSecret("notempty", salt, 1), "$4096:", "$2147483647:", 1)
				var stmt parser.Statement = roleAttributeAlter("cancel_owner", parser.RoleOptions{Present: parser.RolePassword, Password: secret})
				if creating {
					stmt = &parser.CreateRole{Name: parser.Identifier{Text: "canceled_child"}, Options: parser.RoleOptions{Present: parser.RolePassword, Password: secret}}
				}
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				// Enter the real statement first, then count only Execute checks.
				// A one-iteration calibration below proves 128 cannot be an
				// admission/publication checkpoint: it is inside long Hi work.
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				ctx := &cancelAtErrCheck{Context: op.Context(), at: 128}
				ctx.cancel = func() {
					if teardown {
						s.RequestEnd(CleanClose)
					} else {
						op.cancel(nil)
					}
				}
				op.ctx = ctx
				_, err = scope.Execute(stmt, 0, nil)
				err = scope.Finish(err)
				if !errors.Is(err, context.Canceled) || ctx.checks.Load() < 128 || ctx.checks.Load() > 130 {
					t.Fatal("operation did not stop inside derivation", err, ctx.checks.Load())
				}
				if e.roles.published != beforePublication || e.roles.nextPrincipal != next || e.roles.published.Revision != revision || !reflect.DeepEqual(e.Roles().Memberships(), before.Memberships()) {
					t.Fatal("canceled derivation published attributes, graph, revision or allocator")
				}
				if _, ok := e.Roles().Lookup("canceled_child"); ok || len(op.DetachNotices()) != 0 {
					t.Fatal("canceled derivation published name or notice")
				}
				op.Done()
				if !teardown {
					recovery, err := s.Acquire(context.Background())
					if err != nil {
						t.Fatal("same physical session did not recover", err)
					}
					probe := &cancelAtErrCheck{Context: recovery.Context(), at: 128, cancel: func() { recovery.cancel(nil) }}
					recovery.ctx = probe
					short := passwordExpectedSecret("notempty", salt, 1)
					short = strings.Replace(short, "$4096:", "$1:", 1)
					_, err = e.Execute(recovery, roleAttributeAlter("cancel_owner", parser.RoleOptions{Present: parser.RolePassword, Password: short}), 0, nil)
					recovery.Done()
					if err != nil || probe.checks.Load() >= 128 {
						t.Fatal("one-iteration calibration or physical recovery failed", err, probe.checks.Load())
					}
				}
				roleLimitClose(t, s)
				// Limit one replacement proves the operation lease drained and
				// physical teardown released the original registry slot.
				roleLimitOpen(t, e, owner.Ref)
			})
		}
	}
}
