package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

// Expectations in this family are independently derived from PostgreSQL 18
// ALTER EXTENSION and REL_18_6 extension.c, not learned from native output.
func OwnershipScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	for _, name := range []string{"eo_old", "eo_new", "eo_other"} {
		SchemaReferenceCleanup(t, name)
	}
	p := NewScenario(t)
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

func OwnershipRun(t *testing.T, p *Pair, b Backend, c *sql.Conn) func(Step) {
	t.Helper()
	return OwnershipRunWithRecorder(t, p, c, b.Recorder)
}

// The recorder belongs to the executor, including independent Restore destinations.
func OwnershipRunWithRecorder(t *testing.T, p *Pair, c *sql.Conn, recorder *Recorder) func(Step) {
	t.Helper()
	return func(s Step) { RunStep(t, p.Ctx, c, recorder, s) }
}

func OwnershipError(q, code, message string) Step {
	s := ImplicitError(q, Diagnostic{Code: code, Message: message})
	s.ErrorStage = "execute"
	return s
}

func OwnershipCount(q string, n int64, args ...any) Step {
	return Select(q, []string{"count"}, []string{"INT8"}, [][]any{{n}}, args...)
}

func OwnershipID(t *testing.T, p *Pair, c *sql.Conn, q string) int64 {
	t.Helper()
	var id int64
	if err := c.QueryRowContext(p.Ctx, q).Scan(&id); err != nil {
		t.Fatal(q, err)
	}
	return id
}

// A same-backend snapshot proves identity preservation only; callers separately
// assert the expected classes, cardinalities, namespace and callable results.
func OwnershipSnapshot(t *testing.T, p *Pair, c *sql.Conn, q string) [][]any {
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
			v[i] = Normalize(v[i])
		}
		out = append(out, v)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func OwnershipUnchanged(t *testing.T, p *Pair, c *sql.Conn, q string, before [][]any) {
	t.Helper()
	if after := OwnershipSnapshot(t, p, c, q); !reflect.DeepEqual(before, after) {
		t.Fatalf("identity/state changed: before=%v after=%v", before, after)
	}
}

func OwnershipNumber(n int64) string { return strconv.FormatInt(n, 10) }

// Numeric identities are checked within one catalog only. This logical snapshot
// includes the entire pg_proc declaration and current owner/ACL; regtype strings
// deliberately resolve destination identities instead of comparing source OIDs.
func OwnershipSurvivorDeclaration(signature string) string {
	return `SELECT p.proname,n.nspname,r.rolname,p.proacl::text,l.lanname,p.prokind,p.proargtypes::oid[]::regtype[]::text,p.proallargtypes::regtype[]::text,p.proargmodes::text,p.proargnames::text,p.prorettype::regtype::text,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin,p.proconfig::text,p.prosecdef,p.proleakproof,p.pronargs,p.pronargdefaults,p.proargdefaults::text,p.provariadic::regtype::text,p.prosupport::text,p.protrftypes::regtype[]::text,p.prosqlbody::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='` + signature + `'::regprocedure`
}

func OwnershipSurvivorRoles(run func(Step)) {
	for _, q := range []string{`CREATE ROLE eo_survivor_owner SUPERUSER`, `CREATE ROLE eo_survivor_acl`, `CREATE SCHEMA eo_old`, `CREATE SCHEMA eo_new`, `CREATE SCHEMA eo_other`, `GRANT USAGE ON SCHEMA eo_old,eo_new TO eo_survivor_acl`, `CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`} {
		run(Exec(q, 0))
	}
}

func OwnershipSurvivorACL(run func(Step), signature string) {
	for _, q := range []string{`ALTER FUNCTION ` + signature + ` OWNER TO eo_survivor_owner`, `REVOKE ALL ON FUNCTION ` + signature + ` FROM PUBLIC`, `GRANT EXECUTE ON FUNCTION ` + signature + ` TO eo_survivor_acl`} {
		run(Exec(q, 0))
	}
}

func OwnershipSurvivorControls(t *testing.T, p *Pair, b Backend, c *sql.Conn, name string, signatures []string, check func(*sql.Conn, *Recorder)) {
	t.Helper()
	check(c, b.Recorder)
	queries := make([]string, len(signatures))
	before := make([][][]any, len(signatures))
	for i, signature := range signatures {
		queries[i] = OwnershipSurvivorDeclaration(signature)
		before[i] = OwnershipSnapshot(t, p, c, queries[i])
		if len(before[i]) != 1 {
			t.Fatal("missing genuine survivor declaration", signature)
		}
	}
	if b.PG {
		return
	}
	saved := ExtensionDump(t, p.Ctx, p.Native)
	// Controls use raw pools; assertions use that destination's recording adapter.
	restore := func(control, recorded *sql.DB, recorder *Recorder) {
		if err := stuntgres.Reset(p.Ctx, control); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		conn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		check(conn, recorder)
		for i, q := range queries {
			OwnershipUnchanged(t, p, conn, q, before[i])
		}
	}
	restore(p.Native, b.DB, b.Recorder)
	connector, err := stuntgres.NewDriver().OpenConnector("eo_survivor_" + name)
	if err != nil {
		t.Fatal(err)
	}
	recorded, recorder := RecordedNativePool(t, connector)
	other := sql.OpenDB(connector)
	defer other.Close()
	for _, q := range []string{`CREATE ROLE eo_survivor_owner SUPERUSER`, `CREATE ROLE eo_survivor_acl`} {
		if _, err := other.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	restore(other, recorded, recorder)
}

// Exercise mutation failures against the actual released object, then prove
// rollback restores its exact reciprocal membership and logical declaration.
func OwnershipSurvivorMutation(t *testing.T, p *Pair, c *sql.Conn, run func(Step), origin, signature string) {
	declaration := OwnershipSurvivorDeclaration(signature)
	before := OwnershipSnapshot(t, p, c, declaration)
	for _, receiver := range []string{origin, `"uuid-ossp"`} {
		run(OwnershipError("ALTER EXTENSION "+receiver+" ADD FUNCTION "+signature, "55000", "function "+signature+` is already a member of extension "`+origin+`"`))
	}
	OwnershipSurvivorNonmember(run, `"uuid-ossp"`, signature)
	run(Exec(`SET ROLE eo_survivor_acl`, 0))
	routine := strings.Split(strings.Split(signature, "(")[0], ".")
	run(OwnershipError("ALTER FUNCTION "+signature+" RENAME TO eo_forbidden", "42501", "must be owner of function "+routine[len(routine)-1]))
	run(Exec(`RESET ROLE`, 0))
	run(Exec(`BEGIN`, 0))
	run(Exec(`SAVEPOINT survivor_membership`, 0))
	run(Exec("ALTER EXTENSION "+origin+" DROP FUNCTION "+signature, 0))
	run(OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_extension'::regclass AND deptype='e'`, 0))
	run(Exec(`ROLLBACK TO survivor_membership`, 0))
	run(Exec(`COMMIT`, 0))
	run(OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='`+origin+`') AND deptype='e'`, 1))
	OwnershipUnchanged(t, p, c, declaration, before)
}

func OwnershipSurvivorNonmember(run func(Step), extension, signature string) {
	run(OwnershipError("ALTER EXTENSION "+extension+" DROP FUNCTION "+signature, "55000", "function "+signature+` is not a member of extension "`+strings.Trim(extension, `"`)+`"`))
}

func OwnershipSurvivorMemberRestriction(run func(Step), signature, extension string) {
	for _, suffix := range []string{"", " CASCADE"} {
		s := OwnershipError("DROP FUNCTION "+signature+suffix, "2BP01", "cannot drop function "+signature+" because extension "+extension+" requires it")
		s.Diagnostic.Hint = "You can drop extension " + extension + " instead."
		run(s)
	}
}
