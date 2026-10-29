package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Expectations in this family are independently derived from PostgreSQL 18
// ALTER EXTENSION and REL_18_6 extension.c, not learned from native output.
func ownershipScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, name := range []string{"eo_old", "eo_new", "eo_other"} {
		fixture.SchemaReferenceCleanup(t, name)
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			// Roll back stock membership mutations even when an assertion fails.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				c.ExecContext(ctx, "ROLLBACK")
				c.ExecContext(ctx, "RESET ROLE")
				c.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			}()
			body(t, p, backend, c)
		})
	}
}

func ownershipRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func(fixture.Step) {
	t.Helper()
	return ownershipRunWithRecorder(t, p, c, b.Recorder)
}

// The recorder belongs to the executor, including independent Restore destinations.
func ownershipRunWithRecorder(t *testing.T, p *fixture.Pair, c *sql.Conn, recorder *fixture.Recorder) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
}

func ownershipSetup(run func(fixture.Step)) {
	for _, q := range []string{"CREATE SCHEMA eo_old", "CREATE SCHEMA eo_new", "CREATE SCHEMA eo_other", "CREATE EXTENSION unaccent SCHEMA eo_old"} {
		run(fixture.Exec(q, 0))
	}
}

func ownershipError(q, code, message string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.ErrorStage = "execute"
	return s
}

func ownershipCount(q string, n int64, args ...any) fixture.Step {
	return fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{n}}, args...)
}

func ownershipID(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) int64 {
	t.Helper()
	var id int64
	if err := c.QueryRowContext(p.Ctx, q).Scan(&id); err != nil {
		t.Fatal(q, err)
	}
	return id
}

// A same-backend snapshot proves identity preservation only; callers separately
// assert the expected classes, cardinalities, namespace and callable results.
func ownershipSnapshot(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) [][]any {
	t.Helper()
	r, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cols, err := r.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for r.Next() {
		v := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range v {
			ptr[i] = &v[i]
		}
		if err := r.Scan(ptr...); err != nil {
			t.Fatal(err)
		}
		for i := range v {
			v[i] = fixture.Normalize(v[i])
		}
		out = append(out, v)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func ownershipUnchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, before [][]any) {
	t.Helper()
	if after := ownershipSnapshot(t, p, c, q); !reflect.DeepEqual(before, after) {
		t.Fatalf("identity/state changed: before=%v after=%v", before, after)
	}
}

const ownershipEdges = `SELECT d.classid::bigint,d.objid::bigint,d.refobjid::bigint,d.deptype FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='unaccent' AND d.deptype='e' ORDER BY d.classid,d.objid`
const ownershipProcedures = `SELECT p.oid::bigint,p.proowner::bigint,p.proname,p.proargtypes::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ('eo_old','eo_new') ORDER BY p.oid`

func ownershipCallable(run func(fixture.Step), schema string) {
	q := "SELECT " + schema + ".unaccent('Hôtel') AS translated," + schema + ".unaccent(NULL::text) AS absent," + schema + ".unaccent('') AS empty"
	s := fixture.Select(q, []string{"translated", "absent", "empty"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"Hotel", nil, ""}})
	run(s)
	run(fixture.Prepared(s))
}

func ownershipMemberCount(run func(fixture.Step), n int64) {
	run(ownershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='unaccent' AND d.deptype='e'`, n))
}

func ownershipNumber(n int64) string { return strconv.FormatInt(n, 10) }
