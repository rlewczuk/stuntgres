package core_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func templateUnitLease(t *testing.T,e *core.Engine,s *session.Session) *core.DatabaseTemplateLease {
	t.Helper();e.Mu.Lock();l,err:=e.AcquireDatabaseTemplateLocked(s);e.Mu.Unlock()
	if err!=nil {t.Fatal(err)};t.Cleanup(l.Release);return l
}

func templateUnitBusy(t *testing.T,e *core.Engine,s *session.Session) {
	t.Helper();e.Mu.Lock();l,err:=e.AcquireDatabaseTemplateLocked(s);e.Mu.Unlock()
	if l!=nil {l.Release();t.Fatal("busy source admitted")}
	var d *sqlstate.Error;if !errors.As(err,&d)||d.Code!="55006" {t.Fatal("busy source diagnostic",err)}
}

// Done is evaluated by AdmitDatabase only after the owning exclusion channel
// was captured and both metadata locks released. This observes actual waiting,
// not a worker-start event or a scheduler delay.
type templateWaitContext struct {context.Context; once sync.Once; waiting chan struct{}}
func (c *templateWaitContext) Done() <-chan struct{} {c.once.Do(func(){close(c.waiting)});return c.Context.Done()}

func TestDatabaseTemplateSourceLease(t *testing.T) {
	e,login:=databaseAdmissionFixture()
	e.ReplacementMu.RLock();defer e.ReplacementMu.RUnlock()
	reserved:=databaseAdmissionReserve(t,e,login)
	templateUnitBusy(t,e,nil) // Includes reservations before any startup callback.
	reserved.Release()
	first:=templateUnitLease(t,e,nil);second:=templateUnitLease(t,e,nil)
	base,cancel:=context.WithTimeout(t.Context(),enginetest.Liveness);defer cancel()
	ctx:=&templateWaitContext{Context:base,waiting:make(chan struct{})}
	type result struct{a *core.DatabaseAdmission;err error};done:=make(chan result,1)
	go func(){a,err:=e.AdmitDatabase(ctx,login,core.DatabaseSelector{});done<-result{a,err}}()
	joined:=false
	defer func(){cancel();if !joined {r:=<-done;if r.a!=nil {r.a.Release()}}}()
	select {case <-ctx.waiting:case <-base.Done():t.Fatal(base.Err())}
	databaseAdmissionCount(t,e,0)
	if !e.Mu.TryLock(){t.Fatal("waiting admission retained metadata mutex")};e.Mu.Unlock()
	if !e.Cluster.AdmissionMu.TryLock(){t.Fatal("waiting admission retained reservation mutex")};e.Cluster.AdmissionMu.Unlock()
	first.Release()
	select {case r:=<-done:joined=true;if r.a!=nil {r.a.Release()};t.Fatal("admission crossed remaining reader",r.err);default:}
	cancel();r:=<-done;joined=true;if r.a!=nil||!errors.Is(r.err,context.Canceled){t.Fatal("canceled waiter reserved",r)}
	databaseAdmissionCount(t,e,0)
	second.Release()
	a:=databaseAdmissionReserve(t,e,login)
	s,err:=e.StartSession(t.Context(),a,session.SessionStart{ID:enginetest.NextSessionID()});if err!=nil {t.Fatal(err)}
	self:=templateUnitLease(t,e,s);self.Release()
	// Equal token and payload values on a different Session pointer confer no
	// physical self exemption; private maintenance and foreign owners do not either.
	copied:=&session.Session{Payload:s.Payload,SessionToken:s.SessionToken}
	templateUnitBusy(t,e,copied)
	private,err:=e.StartRestoreSession(t.Context(),e.Roles().LookupRef("postgres"));if err!=nil {t.Fatal(err)}
	templateUnitBusy(t,e,private);roleLimitClose(t,private)
	ending,err:=s.Acquire(t.Context());if err!=nil {t.Fatal(err)}
	s.RequestEnd(session.CleanClose)
	templateUnitBusy(t,e,nil) // Ending owners still reserve their physical slot.
	ending.Done()
	if err:=s.WaitEnd(t.Context());err!=nil {t.Fatal(err)}
	a=databaseAdmissionReserve(t,e,login);a.Release()
}

func TestDatabaseTemplateLeaseProvenance(t *testing.T) {
	e,login:=databaseAdmissionFixture();e.ReplacementMu.RLock();defer e.ReplacementMu.RUnlock()
	lease:=templateUnitLease(t,e,nil)
	copy:=*lease;copy.Release()
	base,cancel:=context.WithTimeout(t.Context(),enginetest.Liveness);defer cancel()
	ctx:=&templateWaitContext{Context:base,waiting:make(chan struct{})}
	done:=make(chan error,1)
	go func(){a,err:=e.AdmitDatabase(ctx,login,core.DatabaseSelector{});if a!=nil {a.Release()};done<-err}()
	joined:=false
	defer func(){cancel();if !joined {<-done}}()
	select {case <-ctx.waiting:case <-base.Done():t.Fatal(base.Err())}
	select {case err:=<-done:joined=true;t.Fatal("copied lease released real reader",err);default:}
	lease.Release();lease.Release()
	err:=<-done;joined=true;if err!=nil {t.Fatal(err)}
	databaseAdmissionCount(t,e,0)
	// An old owning handle cannot release the next exclusion generation.
	next:=templateUnitLease(t,e,nil);lease.Release()
	base2,cancel2:=context.WithTimeout(t.Context(),enginetest.Liveness);defer cancel2()
	ctx2:=&templateWaitContext{Context:base2,waiting:make(chan struct{})}
	done2:=make(chan error,1)
	go func(){a,err:=e.AdmitDatabase(ctx2,login,core.DatabaseSelector{});if a!=nil {a.Release()};done2<-err}()
	joined2:=false
	defer func(){cancel2();if !joined2 {<-done2}}()
	select {case <-ctx2.waiting:case <-base2.Done():t.Fatal(base2.Err())}
	cancel2();err=<-done2;joined2=true;if !errors.Is(err,context.Canceled){t.Fatal(err)}
	next.Release()
}

func TestDatabaseTemplateReplacementFence(t *testing.T) {
	e,_:=databaseAdmissionFixture()
	ctx,cancel:=context.WithTimeout(t.Context(),enginetest.Liveness);defer cancel()
	e.ReplacementMu.RLock()
	held:=true
	defer func(){if held {e.ReplacementMu.RUnlock()}}()
	lease:=templateUnitLease(t,e,nil)
	done:=make(chan error,1)
	go func(){done<-engine.Reset(ctx,e)}()
	joined:=false
	defer func(){lease.Release();if held {e.ReplacementMu.RUnlock();held=false};if !joined {<-done}}()
	// Observe the actual replacement writer queue under its owning mutex.
	mu:=core.ReplacementAdmissionMuRef(&e.ReplacementMu)
	for {
		mu.Lock();writers:=core.ReplacementAdmissionWriters(&e.ReplacementMu);mu.Unlock()
		if writers!=0 {break}
		select {case err:=<-done:joined=true;t.Fatal("replacement crossed source read protection",err);case <-ctx.Done():t.Fatal(ctx.Err());default:runtime.Gosched()}
	}
	lease.Release();e.ReplacementMu.RUnlock();held=false
	if err:=<-done;err!=nil {t.Fatal(err)};joined=true
	if !e.ReplacementMu.TryLock(){t.Fatal("replacement fence not released")};e.ReplacementMu.Unlock()
}

func TestDatabaseTemplateStartupReservation(t *testing.T) {
	e,login:=databaseAdmissionFixture()
	e.ReplacementMu.RLock();defer e.ReplacementMu.RUnlock()
	a:=databaseAdmissionReserve(t,e,login)
	gate:=&roleLimitGate{entered:make(chan struct{}),release:make(chan struct{})}
	var once sync.Once
	unblock:=func(){once.Do(func(){close(gate.release)})}
	done:=make(chan roleLimitStartResult,1)
	go func(){s,err:=e.StartSession(t.Context(),a,session.SessionStart{ID:enginetest.NextSessionID(),Defaults:gate});done<-roleLimitStartResult{s,err}}()
	joined:=false
	defer func(){unblock();if !joined {r:=roleLimitReceive(t,done);if r.s!=nil {roleLimitClose(t,r.s)}}}()
	ctx,cancel:=context.WithTimeout(t.Context(),enginetest.Liveness);defer cancel()
	select {case <-gate.entered:case <-ctx.Done():t.Fatal(ctx.Err())}
	// Initialization has consumed the real reservation but has not published a
	// live session. Catalog/session-registry-only counting would miss it.
	templateUnitBusy(t,e,nil)
	if len(core.EngineSessions(e))!=0 {t.Fatal("startup owner published before callback")}
	unblock();r:=roleLimitReceive(t,done);joined=true
	if r.err!=nil {t.Fatal(r.err)};roleLimitClose(t,r.s)
	lease:=templateUnitLease(t,e,nil);lease.Release()
}
