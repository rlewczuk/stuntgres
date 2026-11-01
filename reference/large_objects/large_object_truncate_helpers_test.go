package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const (
	truncateRead = 262144
	truncateWrite = 131072
	truncateMaximum int64 = 4398046509056
)

type truncateSession struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	c *sql.Conn
	prepared bool
}

// Each route owns its scenario; peers keep the same backend's recorder.
func truncateScenario(t *testing.T, roles []string, body func(*truncateSession)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared { name = "fresh-prepared" }
		t.Run(name, func(t *testing.T) {
			if len(roles) != 0 { fixture.RoleReferenceCleanup(t, roles) }
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					s := &truncateSession{t:t, p:p, b:b, c:c, prepared:prepared}
					defer s.close()
					for _, role := range roles { s.run(fixture.Exec("CREATE ROLE "+role, 0)) }
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
							if _, err := c.ExecContext(ctx, q); err != nil { t.Error(err) }
						}
						for _, role := range roles {
							for _, q := range []string{"DROP OWNED BY "+role+" CASCADE", "DROP ROLE "+role} {
								if _, err := c.ExecContext(ctx, q); err != nil { t.Error(err) }
							}
						}
					}()
					body(s)
				})
			}
		})
	}
}

func (s *truncateSession) close() {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.c.ExecContext(ctx, "ROLLBACK"); err != nil { s.t.Error(err) }
	if err := s.c.Close(); err != nil { s.t.Error(err) }
}

func (s *truncateSession) run(step fixture.Step) fixture.Observed {
	s.t.Helper()
	step.Prepared = s.prepared
	return fixture.RunStep(s.t, s.p.Ctx, s.c, s.b.Recorder, step)
}

func (s *truncateSession) peer() *truncateSession {
	s.t.Helper()
	c, err := s.b.DB.Conn(s.p.Ctx)
	if err != nil { s.t.Fatal(err) }
	peer := &truncateSession{t:s.t, p:s.p, b:s.b, c:c, prepared:s.prepared}
	return peer
}

func truncateValue(expression, typ string, value any, args ...any) fixture.Step {
	return fixture.Select("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{value}}, args...)
}

func truncateError(query, code, message string, args ...any) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code:code, Message:message}, args...)
}

func (s *truncateSession) recover(step fixture.Step) {
	s.t.Helper()
	s.run(fixture.Exec("SAVEPOINT truncate_error", 0))
	s.run(step)
	s.run(fixture.Exec("ROLLBACK TO SAVEPOINT truncate_error", 0))
	s.run(fixture.Exec("RELEASE SAVEPOINT truncate_error", 0))
}

func (s *truncateSession) cleanup(oids ...int64) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
		if _, err := s.c.ExecContext(ctx, q); err != nil { s.t.Error(err) }
	}
	for _, oid := range oids {
		// Only this scenario's identities; no warning-producing unlink of absent objects.
		step := truncateValue(fmt.Sprintf("COALESCE((SELECT lo_unlink(oid) FROM pg_largeobject_metadata WHERE oid=%d::oid),1)", oid), "INT4", int64(1))
		fixture.RunStep(s.t, ctx, s.c, s.b.Recorder, step)
	}
}

func (s *truncateSession) create(oid int64, hex string) {
	s.run(truncateValue(fmt.Sprintf("lo_from_bytea(%d::oid,decode('%s','hex'))::bigint", oid, hex), "INT8", oid))
}

func (s *truncateSession) open(oid int64, mode int, slot int64) int64 {
	s.t.Helper()
	observed := s.run(truncateValue("lo_open($1::oid,$2::integer)", "INT4", slot, oid, mode))
	return observed.Rows[0][0].(int64)
}

func truncateCall(name string, fd, length any) fixture.Step {
	typ := "integer"
	if name == "lo_truncate64" { typ = "bigint" }
	return truncateValue(name+"($1::integer,$2::"+typ+")", "INT4", int64(0), fd, length)
}

func (s *truncateSession) read(fd int64, count int, hex string) {
	s.run(truncateValue("encode(loread($1::integer,$2::integer),'hex')", "TEXT", hex, fd, count))
}

func (s *truncateSession) bytes(oid, offset int64, count int, hex string) {
	s.run(truncateValue("encode(lo_get($1::oid,$2::bigint,$3::integer),'hex')", "TEXT", hex, oid, offset, count))
}

func (s *truncateSession) pages(oid int64, want ...[]any) {
	s.run(fixture.Select("SELECT pageno,octet_length(data) AS size,encode(data,'hex') AS hex FROM pg_largeobject WHERE loid=$1::oid ORDER BY pageno", []string{"pageno", "size", "hex"}, []string{"INT4", "INT4", "TEXT"}, want, oid))
}

func (s *truncateSession) held(query string) (fixture.HeldStatement, *sql.Stmt) {
	s.t.Helper()
	stmt, err := s.c.PrepareContext(s.p.Ctx, query)
	if err != nil { s.t.Fatal(err) }
	return fixture.HeldStatement{Executor:s.c, Statement:stmt, SQL:query}, stmt
}

func (s *truncateSession) execute(held fixture.HeldStatement, step fixture.Step) fixture.Observed {
	s.t.Helper()
	step.Prepared = false
	return fixture.RunStep(s.t, s.p.Ctx, held, s.b.Recorder, step)
}
