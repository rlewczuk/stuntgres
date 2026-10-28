package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// DDL routes have independent initial catalogs: Query is not a second mutation
// of the Exec case, and a fresh Prepare is not a held-statement witness.
type eventDDLRoute struct {
	prepared, query bool
}

func (r eventDDLRoute) name() string {
	return fmt.Sprintf("prepared=%t/query=%t", r.prepared, r.query)
}

func eventDDLRoutes(t *testing.T, body func(*testing.T, eventDDLRoute)) {
	t.Helper()
	for _, r := range []eventDDLRoute{{}, {prepared: true}, {query: true}, {prepared: true, query: true}} {
		t.Run(r.name(), func(t *testing.T) { body(t, r) })
	}
}

type eventDDLCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	c *sql.Conn
	r eventDDLRoute
}

func (d eventDDLCase) run(s fixture.Step) {
	d.t.Helper()
	fixture.RunStep(d.t, d.p.Ctx, d.c, d.b.Recorder, s)
}

func (d eventDDLCase) exec(queries ...string) {
	d.t.Helper()
	for _, q := range queries {
		d.run(fixture.Exec(q, 0))
	}
}

func (d eventDDLCase) utility(q string) {
	d.t.Helper()
	s := fixture.Exec(q, 0)
	if d.r.query {
		s = fixture.Select(q, []string{}, []string{}, nil)
	}
	s.Prepared = d.r.prepared
	d.run(s)
}

func (d eventDDLCase) failure(q string, want fixture.Diagnostic, syntax bool) {
	d.t.Helper()
	s := fixture.ErrorDiagnostic(q, want)
	s.Prepared, s.SelectRows = d.r.prepared, d.r.query
	s.ErrorStage = "execute"
	if syntax && d.r.prepared {
		s.ErrorStage = "prepare"
	}
	d.run(s)
}

func (d eventDDLCase) seed(name, event, tags string) {
	d.t.Helper()
	eventTriggerAuditSetup(d.t, d.p, d.b, d.c)
	d.exec("CREATE TABLE et_ddl_data(id integer)")
	d.run(fixture.Exec("INSERT INTO et_ddl_data VALUES(1)", 1))
	eventTriggerFunction(d.t, d.p, d.b, d.c, "et_ddl_fn", 1, "RETURN;")
	eventTriggerCreate(d.t, d.p, d.b, d.c, name, event, tags, "et_ddl_fn")
}

type eventDDLIdentity struct {
	oid, owner, function int64
	name, event, mode    string
	tags                 any
}

// Only allocator-dependent identities are sampled. Names, event, mode and tags
// always come from the caller's independent definition, not catalog goldens.
func (d eventDDLCase) identity(name, event, mode string, tags any) eventDDLIdentity {
	d.t.Helper()
	i := eventDDLIdentity{name: name, event: event, mode: mode, tags: tags}
	if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid,evtowner,evtfoid FROM pg_event_trigger WHERE evtname=$1", name).Scan(&i.oid, &i.owner, &i.function); err != nil {
		d.t.Fatal(err)
	}
	if i.oid <= 0 || i.owner <= 0 || i.function <= 0 {
		d.t.Fatalf("invalid generated identities: %+v", i)
	}
	d.catalog(d.c, i)
	return i
}

func (d eventDDLCase) catalog(e fixture.Executor, identities ...eventDDLIdentity) {
	d.t.Helper()
	var rows [][]any
	for _, i := range identities {
		rows = append(rows, []any{i.oid, i.name, i.event, i.owner, i.function, i.mode, i.tags})
	}
	s := pgEventTriggerCatalogRows("SELECT oid,evtname,evtevent,evtowner,evtfoid,evtenabled,evttags FROM pg_event_trigger ORDER BY evtname", rows)
	pgEventTriggerCatalogBoth(d.t, d.p, d.b, e, s)
}

func (d eventDDLCase) role(name string) int64 {
	d.t.Helper()
	var oid int64
	if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", name).Scan(&oid); err != nil || oid <= 0 {
		d.t.Fatalf("role %s: oid=%d error=%v", name, oid, err)
	}
	return oid
}

func (d eventDDLCase) held(q string) fixture.HeldStatement {
	d.t.Helper()
	s, err := d.c.PrepareContext(d.p.Ctx, q)
	if err != nil {
		d.t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: d.c, Statement: s, SQL: q}
}

func (d eventDDLCase) heldExec(h fixture.HeldStatement) {
	d.t.Helper()
	fixture.RunStep(d.t, d.p.Ctx, h, d.b.Recorder, fixture.Exec(h.SQL, 0))
}

func (d eventDDLCase) audit(rows ...[]any) { d.run(eventTriggerAudit(rows...)) }

func (d eventDDLCase) clearAudit(n int64) {
	d.t.Helper()
	d.run(fixture.Exec("DELETE FROM et_audit", n))
}

func (d eventDDLCase) dependencies(i eventDDLIdentity, present bool) {
	d.t.Helper()
	n := int64(0)
	if present {
		n = 1
	}
	d.run(eventTriggerCount(fmt.Sprintf("SELECT count(*) AS n FROM pg_depend WHERE classid=3466 AND objid=%d AND refclassid=1255 AND refobjid=%d AND deptype='n'", i.oid, i.function), n))
}

func eventDDLFunctionDependency(function, trigger string) fixture.Diagnostic {
	d := eventTriggerDiagnostic("2BP01", "cannot drop function "+function+"() because other objects depend on it")
	d.Detail = "event trigger " + trigger + " depends on function " + function + "()"
	d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	return d
}

func eventDDLRoleDependency(role, trigger string) fixture.Diagnostic {
	d := eventTriggerDiagnostic("2BP01", "role \""+role+"\" cannot be dropped because some objects depend on it")
	d.Detail = "owner of event trigger " + trigger
	return d
}
