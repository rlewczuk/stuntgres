package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const pgOpfamilyKeyQuery = "SELECT f.oid,f.opfmethod,f.opfname,f.opfnamespace,f.opfowner FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE n.nspname=$1 AND f.opfname=$2 AND f.opfmethod=$3::oid"
const pgOpfamilyPopulate = "ALTER OPERATOR FAMILY opfamily_life.members USING btree ADD OPERATOR 3 =(integer,integer), FUNCTION 1 (integer,integer) btint4cmp(integer,integer)"

// Generated identities are discovered only after checking their relationships;
// they are never compared with another backend's allocator.
func pgOpfamilyCustom(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, schema, name string, method int64) []any {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, pgOpfamilyKeyQuery, schema, name, method)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pgOpfamilyMetadata(t, rows, b.PG, false)
	got := pgOpfamilyConsume(t, rows)
	if len(got) != 1 {
		t.Fatal("custom family count", got)
	}
	row := got[0]
	for _, i := range []int{0, 3, 4} {
		id := row[i].(int64)
		if id <= 0 || id > 4294967295 {
			t.Fatal("invalid custom identity", row)
		}
	}
	if row[1] != method || row[2] != name {
		t.Fatal("custom identity", row)
	}
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Select("SELECT n.nspname,r.rolname=current_user AS owner_match FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace JOIN pg_roles r ON r.oid=f.opfowner WHERE f.oid=$1::oid", []string{"nspname", "owner_match"}, []string{"NAME", "BOOL"}, [][]any{{schema, true}}, row[0]))
	return row
}

func pgOpfamilyCustomMembers(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, family any, populated bool) {
	t.Helper()
	var operators, support [][]any
	if populated {
		operators = [][]any{{family, int64(23), int64(23), int64(3), int64(96), "s", int64(403), int64(0)}}
		support = [][]any{{family, int64(23), int64(23), int64(1), int64(351)}}
	}
	for _, prepared := range []bool{false, true} {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT amopfamily,amoplefttype,amoprighttype,amopstrategy,amopopr,amoppurpose,amopmethod,amopsortfamily FROM pg_amop WHERE amopfamily=$1::oid ORDER BY amopstrategy", []string{"amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amopopr", "amoppurpose", "amopmethod", "amopsortfamily"}, []string{"OID", "OID", "OID", "INT2", "OID", "CHAR", "OID", "OID"}, operators, family),
			fixture.Select("SELECT amprocfamily,amproclefttype,amprocrighttype,amprocnum,amproc::oid FROM pg_amproc WHERE amprocfamily=$1::oid ORDER BY amprocnum", []string{"amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"OID", "OID", "OID", "INT2", "OID"}, support, family),
		} {
			s.Prepared = prepared
			fixture.PgDescriptionExecutorRows(t, p, b, e, s)
		}
	}
}

func pgOpfamilyOwnerLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	stmt, err := c.PrepareContext(p.Ctx, pgOpfamilyKeyQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: pgOpfamilyKeyQuery}
	check := func(schema, name string, method int64, want [][]any) {
		for _, prepared := range []bool{false, true} {
			pgOpfamilyRows(t, p, b, c, pgOpfamilyKeyQuery, want, prepared, schema, name, method)
		}
		pgOpfamilyRows(t, p, b, held, pgOpfamilyKeyQuery, want, false, schema, name, method)
	}
	check("opfamily_life", "members", 403, nil)
	run("CREATE SCHEMA opfamily_life")
	run("CREATE SCHEMA opfamily_dest")
	run("CREATE OPERATOR FAMILY opfamily_life.members USING btree")
	run("CREATE OPERATOR FAMILY opfamily_life.members USING hash")
	btree := pgOpfamilyCustom(t, p, b, c, "opfamily_life", "members", 403)
	hash := pgOpfamilyCustom(t, p, b, c, "opfamily_life", "members", 405)
	if btree[0] == hash[0] || btree[3] != hash[3] || btree[4] != hash[4] {
		t.Fatal("method-qualified identity", btree, hash)
	}
	check("opfamily_life", "members", 403, [][]any{btree})
	check("opfamily_life", "members", 405, [][]any{hash})
	pgOpfamilyCustomMembers(t, p, b, c, btree[0], false)
	run(pgOpfamilyPopulate)
	pgOpfamilyCustomMembers(t, p, b, c, btree[0], true)
	pgOpfamilyCustomMembers(t, p, b, c, hash[0], false)
	old, err := b.DB.QueryContext(p.Ctx, pgOpfamilyKeyQuery, "opfamily_life", "members", int64(403))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgOpfamilyMetadata(t, old, b.PG, false)
	oldTypes, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	run("ALTER OPERATOR FAMILY opfamily_life.members USING btree RENAME TO renamed")
	run("ALTER OPERATOR FAMILY opfamily_life.renamed USING btree SET SCHEMA opfamily_dest")
	moved := pgOpfamilyCustom(t, p, b, c, "opfamily_dest", "renamed", 403)
	if moved[0] != btree[0] || moved[1] != btree[1] || moved[4] != btree[4] || moved[3] == btree[3] {
		t.Fatal("stable family identity", btree, moved)
	}
	check("opfamily_life", "members", 403, nil)
	check("opfamily_dest", "renamed", 403, [][]any{moved})
	check("opfamily_life", "members", 405, [][]any{hash})
	if got := pgOpfamilyConsume(t, old); !reflect.DeepEqual(got, [][]any{btree}) {
		t.Fatal("retained family result", got, btree)
	}
	old.Close()
	for i, typ := range oldTypes {
		if typ.Name() != pgOpfamilyColumns[i] || typ.DatabaseTypeName() != pgOpfamilyTypes[i] {
			t.Fatal("retained family metadata", i, typ)
		}
	}
	run("BEGIN")
	run("ALTER OPERATOR FAMILY opfamily_dest.renamed USING btree RENAME TO temporary")
	check("opfamily_dest", "renamed", 403, nil)
	temporary := append([]any(nil), moved...)
	temporary[2] = "temporary"
	check("opfamily_dest", "temporary", 403, [][]any{temporary})
	run("ROLLBACK")
	check("opfamily_dest", "renamed", 403, [][]any{moved})
	check("opfamily_dest", "temporary", 403, nil)
	pgOpfamilyCustomMembers(t, p, b, c, moved[0], true)
	run("DROP OPERATOR FAMILY opfamily_dest.renamed USING btree")
	check("opfamily_dest", "renamed", 403, nil)
	pgOpfamilyCustomMembers(t, p, b, c, moved[0], false)
	check("opfamily_life", "members", 405, [][]any{hash})
	run("DROP OPERATOR FAMILY opfamily_life.members USING hash")
	run("DROP SCHEMA opfamily_life")
	run("DROP SCHEMA opfamily_dest")
}

func pgOpfamilyIndexLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE opfamily_index(n INT)", 0))
	run(fixture.Exec("INSERT INTO opfamily_index VALUES(7),(8),(NULL)", 3))
	check := func() {
		for _, prepared := range []bool{false, true} {
			pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyWitnessQuery, pgOpfamilyFacts(), prepared)
			for _, s := range []fixture.Step{
				fixture.Select("SELECT n FROM opfamily_index WHERE n=7", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}),
				fixture.Select("SELECT n FROM opfamily_index WHERE n<8", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}),
				fixture.Select("SELECT n FROM opfamily_index ORDER BY n ASC", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}, {nil}}),
			} {
				s.Prepared = prepared
				run(s)
			}
		}
		pgOpfamilyMembership(t, p, b)
	}
	check()
	for _, q := range []string{"CREATE INDEX opfamily_index_b ON opfamily_index(n)", "CREATE INDEX opfamily_index_h ON opfamily_index USING hash(n)", "DROP INDEX opfamily_index_b", "DROP INDEX opfamily_index_h"} {
		run(fixture.Exec(q, 0))
		check()
	}
	run(fixture.Exec("DROP TABLE opfamily_index", 0))
}

func TestReferencePGOpfamilyLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "opfamily_life", "opfamily_dest")
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOpfamilyOwnerLifecycle(t, p, b)
		pgOpfamilyIndexLifecycle(t, p, b)
	})
}

func TestReferencePGOpfamilyControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "opfamily_life")
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, q := range []string{"CREATE TABLE opfamily_restore(n INT)", "CREATE INDEX opfamily_restore_b ON opfamily_restore(n)", "CREATE VIEW opfamily_restore_view AS SELECT * FROM pg_catalog.pg_opfamily WHERE oid IN (1976,1977,1994)", "CREATE SCHEMA opfamily_life", "CREATE OPERATOR FAMILY opfamily_life.members USING btree", pgOpfamilyPopulate} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO opfamily_restore VALUES(7),(8)", 2))
		stmt, err := b.DB.PrepareContext(p.Ctx, pgOpfamilyProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOpfamilyProbe}
		check := func() {
			pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyWitnessQuery, pgOpfamilyFacts(), false)
			pgOpfamilyRows(t, p, b, held, pgOpfamilyProbe, pgOpfamilyFacts()[1:2], false, int64(1977))
			family := pgOpfamilyCustom(t, p, b, b.DB, "opfamily_life", "members", 403)
			pgOpfamilyCustomMembers(t, p, b, b.DB, family[0], true)
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT * FROM opfamily_restore_view ORDER BY oid", pgOpfamilyColumns, pgOpfamilyTypes, pgOpfamilyFacts())
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgOpfamilyMetadata(t, rows, b.PG, true) }
				fixture.PgDescriptionRows(t, p, b, s)
			}
			run(fixture.Select("SELECT n FROM opfamily_restore ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			run(fixture.Select("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname='opfamily_restore_b'", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX opfamily_restore_b ON public.opfamily_restore USING btree (n)"}}))
		}
		check()
		if b.PG {
			return
		}
		old, err := b.DB.QueryContext(p.Ctx, pgOpfamilyWitnessQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		pgOpfamilyMetadata(t, old, false, false)
		types, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, declaration := range []string{"CREATE TABLE", "CREATE INDEX", "CREATE VIEW", "CREATE OPERATOR FAMILY", "opfamily_life", "members"} {
			if !strings.Contains(dump.String(), declaration) {
				t.Fatal("missing logical declaration", declaration, dump.String())
			}
		}
		if strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_opfamily") {
			t.Fatal("synthetic catalog dump", dump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyWitnessQuery, pgOpfamilyFacts(), false)
		pgOpfamilyRows(t, p, b, held, pgOpfamilyProbe, pgOpfamilyFacts()[:1], false, int64(1976))
		pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyKeyQuery, nil, false, "opfamily_life", "members", int64(403))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('opfamily_restore','opfamily_restore_b','opfamily_restore_view')", []string{"relname"}, []string{"NAME"}, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		check()
		if got := pgOpfamilyConsume(t, old); !reflect.DeepEqual(got, pgOpfamilyFacts()) {
			t.Fatal("pre-reset capture", got)
		}
		old.Close()
		for i, typ := range types {
			if typ.Name() != pgOpfamilyColumns[i] || typ.DatabaseTypeName() != pgOpfamilyTypes[i] {
				t.Fatal("pre-reset metadata", i, typ)
			}
		}
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			t.Fatal(err)
		}
		if again.String() != dump.String() {
			t.Fatal("logical redump differs", dump.String(), again.String())
		}
		// Late failure must not publish the preceding valid replacement. The
		// extra live row distinguishes preservation from a partial valid replay.
		run(fixture.Exec("INSERT INTO opfamily_restore VALUES(9)", 1))
		before := pgOpfamilyCustom(t, p, b, b.DB, "opfamily_life", "members", 403)
		preserved := func() {
			run(fixture.Select("SELECT n FROM opfamily_restore ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}, {int64(9)}}))
			pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyKeyQuery, [][]any{before}, false, "opfamily_life", "members", int64(403))
			pgOpfamilyCustomMembers(t, p, b, b.DB, before[0], true)
			pgOpfamilyRows(t, p, b, held, pgOpfamilyProbe, pgOpfamilyFacts()[2:], false, int64(1994))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nINSERT INTO opfamily_missing VALUES(1);\n")); err == nil {
			t.Fatal("late invalid restore accepted")
		}
		preserved()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		preserved()
	})
}

func TestReferencePGOpfamilyCancellation(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyProbe, pgOpfamilyFacts()[:1], false, int64(1976))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE opfamily_cancel_sentinel(v INT)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO opfamily_cancel_sentinel VALUES(7)", 1))
		stmt, err := b.DB.PrepareContext(p.Ctx, pgOpfamilyProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOpfamilyProbe}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			check := func(err error) {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled operation", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("context error gained SQLSTATE", err)
				}
			}
			rows, err := b.DB.QueryContext(ctx, pgOpfamilyProbe, int64(1976))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled direct rows")
			}
			check(err)
			rows, err = stmt.QueryContext(ctx, int64(1976))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled held rows")
			}
			check(err)
			s, err := b.DB.PrepareContext(ctx, pgOpfamilyProbe)
			if s != nil {
				s.Close()
				t.Fatal("canceled prepare handle")
			}
			check(err)
		}
		pgOpfamilyRows(t, p, b, held, pgOpfamilyProbe, pgOpfamilyFacts()[1:2], false, int64(1977))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM opfamily_cancel_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferencePGOpfamilyFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "opfamily_life", "opfamily_dest")
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOpfamilyShapes(t, p, b, b.DB)
		pgOpfamilyIdentity(t, p, b)
		pgOpfamilyIndexLifecycle(t, p, b)
		pgOpfamilyOwnerLifecycle(t, p, b)
		pgOpfamilyDiagnosticFailures(t, p, b)
	})
}
