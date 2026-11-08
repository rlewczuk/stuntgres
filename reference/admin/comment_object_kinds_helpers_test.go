package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// COMMENT ON TYPE/DOMAIN/INDEX/TRIGGER/FUNCTION is a utility statement whose
// whole public contract is the catalog row it leaves behind. One harness runs
// the same body on both execution routes and both backends.
type cokRoute struct {
	name     string
	prepared bool
}

var cokRoutes = []cokRoute{
	{name: "direct"},
	{name: "prepared", prepared: true},
}

// cokSession groups one route's connection: the executed step, the oracle
// recorder and the catalog probes all observe the same session. Utility steps
// are prepared on the prepared route, exactly like an application using
// database/sql; catalog probes stay direct observations of the result.
type cokSession struct {
	t        *testing.T
	p        *fixture.Pair
	backend  fixture.Backend
	conn     *sql.Conn
	recorder *fixture.Recorder
	prepared bool
	suffix   string
}

func newCokSession(t *testing.T, p *fixture.Pair, b fixture.Backend, route cokRoute) *cokSession {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		if err := conn.Close(); err != nil {
			t.Errorf("comment kinds connection close: %v", err)
		}
	})
	return &cokSession{t: t, p: p, backend: b, conn: conn, recorder: b.Recorder, prepared: route.prepared, suffix: route.name}
}

// cokScenario runs one body per route and backend. A route gets its own
// scenario so public objects keep their literal names; roles and schemas
// outside public are route-suffixed or dropped by each body.
func cokScenario(t *testing.T, body func(t *testing.T, p *fixture.Pair, b fixture.Backend, s *cokSession)) {
	t.Helper()
	for _, route := range cokRoutes {
		t.Run(route.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					body(t, p, backend, newCokSession(t, p, backend, route))
				})
			}
		})
	}
}

func (s *cokSession) run(step fixture.Step) {
	s.t.Helper()
	if s.prepared {
		step = fixture.Prepared(step)
	}
	fixture.RunStep(s.t, s.p.Ctx, s.conn, s.recorder, step)
}

func (s *cokSession) exec(query string) {
	s.t.Helper()
	s.run(fixture.Exec(query, 0))
}

// execDirect runs session/transaction control that must not be prepared.
func (s *cokSession) execDirect(query string) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.conn, s.recorder, fixture.Exec(query, 0))
}

// description runs a pg_description probe through the fixture's OID
// normalization: real PostgreSQL exports OID cells as decimal bytes while the
// native adapter exports int64, so the numeric expectation must be rewritten
// per backend. The probe stays on this session's connection so transaction-
// scoped comments are observable.
func (s *cokSession) description(step fixture.Step) {
	s.t.Helper()
	if s.prepared {
		step = fixture.Prepared(step)
	}
	fixture.PgDescriptionExecutorRows(s.t, s.p, s.backend, s.conn, step)
}

// denied asserts the exact runtime diagnostic (ERROR/ERROR, message only, no
// Position) shared by missing, wrong-kind and ownership failures.
func (s *cokSession) denied(query, code, message string) {
	s.t.Helper()
	s.run(fixture.RoleReferenceError(query, code, message, "", s.prepared, "execute"))
}

// failure asserts a complete diagnostic including optional hint/position. The
// stage is only observed on the prepared route; a direct execution parses at
// execute time. It intentionally mirrors RoleReferenceError's routing.
func (s *cokSession) failure(query string, want fixture.Diagnostic, stage string) {
	s.t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	step := fixture.ErrorDiagnostic(query, want)
	step.Prepared = s.prepared
	step.ErrorStage = "execute"
	if s.prepared {
		step.ErrorStage = stage
	}
	s.run(step)
}

// oid resolves a catalog identity expression such as
// `'name'::regtype::oid`; metadata probes are direct observations.
func (s *cokSession) oid(query string) int64 {
	s.t.Helper()
	var oid int64
	if err := s.conn.QueryRowContext(s.p.Ctx, query).Scan(&oid); err != nil {
		s.t.Fatalf("%s: %v", query, err)
	}
	return oid
}

// zeroRows executes a utility statement through the query surface and proves it
// publishes no result columns and no rows, as PostgreSQL's utility protocol does.
func (s *cokSession) zeroRows(query string) {
	s.t.Helper()
	var (
		rows *sql.Rows
		err  error
	)
	if s.prepared {
		var stmt *sql.Stmt
		stmt, err = s.conn.PrepareContext(s.p.Ctx, query)
		if err != nil {
			s.t.Fatalf("%s: %v", query, err)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, query)
	}
	if err != nil {
		s.t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) != 0 || rows.Next() || rows.Err() != nil {
		s.t.Fatalf("%s result metadata: columns=%v err=%v rowErr=%v", query, columns, err, rows.Err())
	}
}

func (s *cokSession) role(base string) string { return base + "_" + s.suffix }

const (
	cokTypeClass      = int64(1247)
	cokProcedureClass = int64(1255)
	cokRelationClass  = int64(1259)
	cokTriggerClass   = int64(2620)
)

var (
	cokDescriptionColumns = []string{"objoid", "classoid", "objsubid", "description"}
	cokDescriptionTypes   = []string{"OID", "OID", "INT4", "TEXT"}
)

// cokDescription asserts exactly one object-comment row for an objsubid=0
// comment; text is nil to prove removal.
func cokDescription(oid, classoid int64, text any) fixture.Step {
	return fixture.Select(
		"SELECT objoid,classoid,objsubid,description FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=$2 AND objsubid=0",
		cokDescriptionColumns, cokDescriptionTypes,
		[][]any{{oid, classoid, int64(0), text}}, oid, classoid)
}

// cokNoDescription asserts the addressed object has no comment row at all.
func cokNoDescription(oid, classoid int64) fixture.Step {
	return fixture.Select(
		"SELECT objoid,classoid,objsubid,description FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=$2 AND objsubid=0",
		cokDescriptionColumns, cokDescriptionTypes, [][]any{}, oid, classoid)
}

// cokText reads one TEXT inquiry result; want is nil for SQL NULL.
func cokText(query string, want any) fixture.Step {
	return fixture.Select(query, []string{"text"}, []string{"TEXT"}, [][]any{{want}})
}