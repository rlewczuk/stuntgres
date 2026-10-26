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

const pgAmprocFamilyQuery = "SELECT a.oid,a.amprocfamily,a.amproclefttype,a.amprocrighttype,a.amprocnum,a.amproc FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE n.nspname=$1 AND f.opfname=$2 AND f.opfmethod=403 ORDER BY a.amprocnum"
const pgAmprocPopulate = "ALTER OPERATOR FAMILY amproc_life.members USING btree ADD FUNCTION 1 (integer,integer) btint4cmp(integer,integer)"

func pgAmprocOwnerLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	run("CREATE SCHEMA amproc_life")
	run("CREATE SCHEMA amproc_dest")
	run("CREATE OPERATOR FAMILY amproc_life.members USING btree")
	stmt, err := c.PrepareContext(p.Ctx, pgAmprocFamilyQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: pgAmprocFamilyQuery}
	check := func(schema, name string, want [][]any) {
		pgAmprocRows(t, p, b, held, pgAmprocFamilyQuery, want, false, schema, name)
	}
	check("amproc_life", "members", nil)
	run(pgAmprocPopulate)
	var family int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_opfamily WHERE opfname='members' AND opfnamespace='amproc_life'::regnamespace AND opfmethod=403").Scan(&family); err != nil || family <= 0 || family > 4294967295 {
		t.Fatal("family identity", family, err)
	}
	rows, err := stmt.QueryContext(p.Ctx, "amproc_life", "members")
	if err != nil {
		t.Fatal(err)
	}
	pgAmprocMetadata(t, rows, b.PG)
	observed := pgAmprocConsume(t, rows)
	rows.Close()
	if len(observed) != 1 {
		t.Fatal("member count", observed)
	}
	seen := map[int64]bool{}
	var want [][]any
	for i := range []int{1} {
		oid := observed[i][0].(int64)
		if oid <= 0 || oid > 4294967295 || seen[oid] {
			t.Fatal("member identity", oid)
		}
		seen[oid] = true
		want = append(want, []any{oid, family, int64(23), int64(23), int64(i + 1), "btint4cmp"})
		pgAmprocRows(t, p, b, c, pgAmprocProbe, want[i:i+1], false, oid)
		pgAmprocRows(t, p, b, c, pgAmprocProbe, want[i:i+1], true, oid)
	}
	if !reflect.DeepEqual(observed, want) {
		t.Fatal("owner semantics", observed, want)
	}
	check("amproc_life", "members", want)
	// A separate connection retains the already captured old result while the
	// owner connection mutates the family. No concurrent use of a pq portal.
	old, err := b.DB.QueryContext(p.Ctx, pgAmprocFamilyQuery, "amproc_life", "members")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgAmprocMetadata(t, old, b.PG)
	run("ALTER OPERATOR FAMILY amproc_life.members USING btree RENAME TO renamed")
	run("ALTER OPERATOR FAMILY amproc_life.renamed USING btree SET SCHEMA amproc_dest")
	check("amproc_life", "members", nil)
	check("amproc_dest", "renamed", want)
	pgAmprocMetadata(t, old, b.PG)
	if got := pgAmprocConsume(t, old); !reflect.DeepEqual(got, want) {
		t.Fatal("retained capture", got, want)
	}
	run("BEGIN")
	run("ALTER OPERATOR FAMILY amproc_dest.renamed USING btree DROP FUNCTION 1(integer,integer)")
	check("amproc_dest", "renamed", want[1:])
	run("ROLLBACK")
	check("amproc_dest", "renamed", want)
	run("ALTER OPERATOR FAMILY amproc_dest.renamed USING btree DROP FUNCTION 1(integer,integer)")
	check("amproc_dest", "renamed", want[1:])
	run("ALTER OPERATOR FAMILY amproc_dest.renamed USING btree ADD FUNCTION 1 (integer,integer) btint4cmp(integer,integer)")
	readded, err := stmt.QueryContext(p.Ctx, "amproc_dest", "renamed")
	if err != nil {
		t.Fatal(err)
	}
	again := pgAmprocConsume(t, readded)
	readded.Close()
	if len(again) != 1 {
		t.Fatal("readded member count", again)
	}
	id := again[0][0].(int64)
	if id <= 0 || id > 4294967295 || id == want[0][0] {
		t.Fatal("fresh member identity", again)
	}
	expected := append([]any{again[0][0]}, want[0][1:]...)
	if !reflect.DeepEqual(again, [][]any{expected}) {
		t.Fatal("readded semantics", again)
	}
	check("amproc_dest", "renamed", again)
	run("DROP OPERATOR FAMILY amproc_dest.renamed USING btree")
	check("amproc_dest", "renamed", nil)
	run("DROP SCHEMA amproc_life")
	run("DROP SCHEMA amproc_dest")
	pgAmprocRows(t, p, b, c, pgAmprocWitnessQuery, pgAmprocFacts(t, p, b), false)
}

func pgAmprocIndexLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	const bag = pgAmprocWitnessQuery
	want := pgAmprocFacts(t, p, b)
	check := func() {
		for _, prepared := range []bool{false, true} {
			pgAmprocRows(t, p, b, b.DB, bag, want, prepared)
		}
		run(fixture.Select("SELECT n FROM amproc_index ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
	}
	for _, prepared := range []bool{false, true} {
		pgAmprocRows(t, p, b, b.DB, bag, want, prepared)
	}
	run(fixture.Exec("CREATE TABLE amproc_index(n INT)", 0))
	run(fixture.Exec("INSERT INTO amproc_index VALUES(7),(8)", 2))
	check()
	run(fixture.Exec("CREATE INDEX amproc_index_b ON amproc_index(n)", 0))
	check()
	run(fixture.Exec("CREATE INDEX amproc_index_h ON amproc_index USING hash(n)", 0))
	check()
	run(fixture.Exec("CREATE INDEX amproc_index_r ON amproc_index USING brin(n)", 0))
	check()
	run(fixture.Exec("DROP INDEX amproc_index_r", 0))
	check()
	run(fixture.Exec("DROP INDEX amproc_index_b", 0))
	check()
	run(fixture.Exec("DROP INDEX amproc_index_h", 0))
	check()
	run(fixture.Exec("DROP TABLE amproc_index", 0))
}

func TestReferencePGAmprocHeldExecution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "amproc_life", "amproc_dest")
	pgOpclassScenario(t, pgAmprocOwnerLifecycle)
}

func pgAmprocLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgAmprocIndexLifecycle(t, p, b)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE amproc_restore(n INT)", 0))
	run(fixture.Exec("INSERT INTO amproc_restore VALUES(7),(8)", 2))
	run(fixture.Exec("CREATE INDEX amproc_restore_b ON amproc_restore(n)", 0))
	run(fixture.Exec("CREATE VIEW amproc_restore_view AS SELECT * FROM pg_catalog.pg_amproc WHERE amprocfamily=1976 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum=1", 0))
	stmt, err := b.DB.PrepareContext(p.Ctx, pgAmprocProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAmprocProbe}
	check := func() {
		pgAmprocRows(t, p, b, held, pgAmprocProbe, pgAmprocFacts(t, p, b)[1:2], false, pgAmprocFacts(t, p, b)[1][0])
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT * FROM amproc_restore_view", pgAmprocColumns, pgAmprocTypes, pgAmprocFacts(t, p, b)[1:2])
			s.Prepared = prepared
			// A stored view does not inherit its base catalog's NOT NULL flags.
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { pgAmprocResultMetadata(t, r, b.PG, true) }
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
		}
		run(fixture.Select("SELECT n FROM amproc_restore ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
		run(fixture.Select("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname='amproc_restore_b'", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX amproc_restore_b ON public.amproc_restore USING btree (n)"}}))
	}
	check()
	if b.PG {
		return
	}
	old, err := b.DB.QueryContext(p.Ctx, pgAmprocWitnessQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgAmprocMetadata(t, old, false)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE VIEW") || !strings.Contains(dump.String(), "amproc_restore_view") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_amproc") {
		t.Fatal("nonlogical dump", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	pgAmprocInventory(t, p, b)
	pgAmprocRows(t, p, b, held, pgAmprocProbe, pgAmprocFacts(t, p, b)[2:3], false, pgAmprocFacts(t, p, b)[2][0])
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	check()
	pgAmprocInventory(t, p, b)
	pgAmprocMetadata(t, old, false)
	if got := pgAmprocConsume(t, old); !reflect.DeepEqual(got, pgAmprocFacts(t, p, b)) {
		t.Fatal("retained pre-reset rows", got)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if again.String() != dump.String() {
		t.Fatalf("logical redump changed\nbefore:%s\nafter:%s", dump.String(), again.String())
	}
}

func TestReferencePGAmprocLifecycle(t *testing.T) { pgOpclassScenario(t, pgAmprocLifecycle) }

func TestReferencePGAmprocCancellation(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgAmprocRows(t, p, b, b.DB, pgAmprocProbe, pgAmprocFacts(t, p, b)[1:2], false, pgAmprocFacts(t, p, b)[1][0])
		if b.PG {
			return
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, pgAmprocProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
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
		rows, err := b.DB.QueryContext(ctx, pgAmprocProbe, pgAmprocFacts(t, p, b)[1][0])
		if rows != nil {
			rows.Close()
			t.Fatal("canceled direct rows")
		}
		check(err)
		rows, err = stmt.QueryContext(ctx, pgAmprocFacts(t, p, b)[1][0])
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held rows")
		}
		check(err)
		s, err := b.DB.PrepareContext(ctx, pgAmprocProbe)
		if s != nil {
			s.Close()
			t.Fatal("canceled prepare statement")
		}
		check(err)
		pgAmprocRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAmprocProbe}, pgAmprocProbe, pgAmprocFacts(t, p, b)[2:3], false, pgAmprocFacts(t, p, b)[2][0])
	})
}

func TestReferencePGAmprocFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "amproc_life", "amproc_dest")
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgAmprocInventory(t, p, b)
		pgAmprocShapes(t, p, b, b.DB)
		pgAmprocOwnerLifecycle(t, p, b)
		pgAmprocIndexLifecycle(t, p, b)
		pgAmprocDiagnosticFailures(t, p, b)
	})
}
