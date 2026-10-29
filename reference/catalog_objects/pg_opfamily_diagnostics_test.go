package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgOpfamilyColumns = []string{"oid", "opfmethod", "opfname", "opfnamespace", "opfowner"}
var pgOpfamilyTypes = []string{"OID", "OID", "NAME", "OID", "OID"}

const pgOpfamilyWitnessQuery = "SELECT * FROM pg_catalog.pg_opfamily WHERE oid IN (1976,1977,1994) ORDER BY oid"
const pgOpfamilyProbe = "SELECT * FROM pg_catalog.pg_opfamily WHERE oid=$1::oid"

// Fixed identities from the released catalog, not native-output goldens.
func pgOpfamilyFacts() [][]any {
	return [][]any{
		{int64(1976), int64(403), "integer_ops", int64(11), int64(10)},
		{int64(1977), int64(405), "integer_ops", int64(11), int64(10)},
		{int64(1994), int64(403), "text_ops", int64(11), int64(10)},
	}
}

func pgOpfamilyRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want [][]any, prepared bool, args ...any) {
	t.Helper()
	s := fixture.Select(q, pgOpfamilyColumns, pgOpfamilyTypes, want, args...)
	s.Prepared = prepared
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgOpfamilyMetadata(t, rows, b.PG, false) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		want := reflect.TypeFor[int64]()
		if column == 2 {
			want = reflect.TypeFor[string]()
		}
		if b.PG {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("raw column %d row %d: %T; want %v", column, row, value, want)
		}
		return true
	}
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
}

// Check on the same pinned session that denied the mutation, including public
// reads under SET ROLE. Membership expectations use semantic keys, not generated
// pg_amproc row identities or a backend's entire catalog inventory.
func pgOpfamilyPermissionState(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, held fixture.HeldStatement) {
	t.Helper()
	pgOpfamilyRows(t, p, b, e, pgOpfamilyWitnessQuery, pgOpfamilyFacts(), false)
	pgOpfamilyRows(t, p, b, held, pgOpfamilyProbe, pgOpfamilyFacts()[1:2], false, int64(1977))
	pgAmopRows(t, p, b, e, pgAmopWitnessQuery, pgAmopFacts(), false)
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Select(
		"SELECT amprocfamily,amproclefttype,amprocrighttype,amprocnum,amproc::oid FROM pg_catalog.pg_amproc WHERE (amprocfamily=1976 AND ((amproclefttype=21 AND amprocrighttype=20) OR (amproclefttype=23 AND amprocrighttype=23)) AND amprocnum=1) OR (amprocfamily=1977 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum IN (1,2)) ORDER BY amprocfamily,amproclefttype,amprocrighttype,amprocnum",
		[]string{"amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"},
		[]string{"OID", "OID", "OID", "INT2", "OID"},
		[][]any{
			{int64(1976), int64(21), int64(20), int64(1), int64(2192)},
			{int64(1976), int64(23), int64(23), int64(1), int64(351)},
			{int64(1977), int64(23), int64(23), int64(1), int64(450)},
			{int64(1977), int64(23), int64(23), int64(2), int64(425)},
		}))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT v FROM public.opfamily_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferencePGOpfamilyPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"opfamily_reader"})
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		pool, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.DB, b.Recorder = pool, recorder
		defer func() {
			if len(notices) != 0 {
				t.Fatal("unexpected permission notices", notices)
			}
		}()
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE opfamily_reader", 0))
		run(fixture.Exec("CREATE TABLE public.opfamily_sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO public.opfamily_sentinel VALUES(7)", 1))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO opfamily_reader", 0))
		run(fixture.Exec("GRANT SELECT ON public.opfamily_sentinel TO opfamily_reader", 0))
		mutations := []string{
			"INSERT INTO pg_catalog.pg_opfamily SELECT * FROM pg_catalog.pg_opfamily WHERE oid=1976",
			"INSERT INTO pg_catalog.pg_opfamily SELECT * FROM pg_catalog.pg_opfamily WHERE FALSE",
			"UPDATE pg_catalog.pg_opfamily SET opfowner=10 WHERE oid=1976",
			"UPDATE pg_catalog.pg_opfamily SET opfowner=10 WHERE FALSE",
			"DELETE FROM pg_catalog.pg_opfamily WHERE oid=1976",
			"DELETE FROM pg_catalog.pg_opfamily WHERE FALSE",
		}
		// Semantic planning succeeds before execution-time authority checks.
		// Never execute PostgreSQL owner mutations, even empty predicates.
		commands := make([]fixture.HeldStatement, len(mutations))
		for i, q := range mutations {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("owner prepare", q, err)
			}
			defer stmt.Close()
			commands[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		}
		reader, err := c.PrepareContext(p.Ctx, pgOpfamilyProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		held := fixture.HeldStatement{Executor: c, Statement: reader, SQL: pgOpfamilyProbe}
		pgOpfamilyPermissionState(t, p, b, c, held)
		run(fixture.Exec("SET ROLE opfamily_reader", 0))
		defer c.ExecContext(context.Background(), "RESET ROLE")
		type permissionRoute struct {
			e        fixture.Executor
			prepared bool
		}
		for i, q := range mutations {
			// Reader-held execution is separate from fresh preparation and
			// from reusing a handle prepared before SET ROLE.
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("reader prepare", q, err)
			}
			defer stmt.Close()
			routes := []permissionRoute{
				{c, false},
				{c, true},
				{commands[i], false},
				{fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, false},
			}
			for _, route := range routes {
				// ImplicitError supplies both ERROR severity fields; every
				// omitted optional diagnostic field must be absent.
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_opfamily"})
				s.Prepared, s.ErrorStage = route.prepared, "execute"
				fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
				pgOpfamilyPermissionState(t, p, b, c, held)
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		if !b.PG {
			for i, q := range mutations {
				routes := []permissionRoute{{c, false}, {c, true}, {commands[i], false}}
				for _, route := range routes {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = route.prepared, "execute"
					fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
					pgOpfamilyPermissionState(t, p, b, c, held)
				}
			}
		}
	})
}

func pgOpfamilyDiagnosticFailures(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var notices []fixture.Diagnostic
	pool, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	b.DB, b.Recorder = pool, recorder
	defer func() {
		if len(notices) != 0 {
			t.Fatal("unexpected diagnostic notices", notices)
		}
	}()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE public.opfamily_diag_sentinel(v INT)", 0))
	run(fixture.Exec("INSERT INTO public.opfamily_diag_sentinel VALUES(7)", 1))
	check := func() {
		pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyWitnessQuery, pgOpfamilyFacts(), false)
		run(fixture.Select("SELECT v FROM public.opfamily_diag_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
	for _, tc := range []struct{ query, code, message, position string }{
		{"SELECT missing FROM pg_catalog.pg_opfamily", "42703", `column "missing" does not exist`, "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_opfamily", "42703", `column "missing" does not exist`, "14"},
		{"SELECT 'x'::oid FROM pg_catalog.pg_opfamily WHERE FALSE", "22P02", `invalid input syntax for type oid: "x"`, "8"},
		{"SELECT '4294967296'::oid FROM pg_catalog.pg_opfamily WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`, "8"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
			check()
		}
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgOpfamilyProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOpfamilyProbe}
	for _, tc := range []struct{ value, code, message string }{
		{"x", "22P02", `invalid input syntax for type oid: "x"`},
		{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
	} {
		for _, route := range []struct {
			e        fixture.Executor
			prepared bool
		}{{b.DB, false}, {b.DB, true}, {held, false}} {
			s := fixture.ImplicitError(pgOpfamilyProbe, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, tc.value)
			s.SelectRows, s.Prepared, s.ErrorStage = true, route.prepared, "execute"
			fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
			check()
			pgOpfamilyRows(t, p, b, held, pgOpfamilyProbe, pgOpfamilyFacts()[1:2], false, int64(1977))
		}
	}
	run(fixture.Exec("DROP TABLE public.opfamily_diag_sentinel", 0))
}

func TestReferencePGOpfamilyDiagnostics(t *testing.T) {
	pgOpclassScenario(t, pgOpfamilyDiagnosticFailures)
}
