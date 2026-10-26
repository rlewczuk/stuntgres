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

const pgAmopFamilyQuery = "SELECT a.oid,a.amopfamily,a.amoplefttype,a.amoprighttype,a.amopstrategy,a.amoppurpose,a.amopopr,a.amopmethod,a.amopsortfamily FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE n.nspname=$1 AND f.opfname=$2 AND f.opfmethod=403 ORDER BY a.amopstrategy"
const pgAmopPopulate = "ALTER OPERATOR FAMILY amop_life.members USING btree ADD OPERATOR 1 < (integer,integer), OPERATOR 2 <= (integer,integer), OPERATOR 3 = (integer,integer), OPERATOR 4 >= (integer,integer), OPERATOR 5 > (integer,integer)"

func pgAmopOwnerLifecycle(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	c := fixture.SequenceCoreConn(t,p.Ctx,b.DB)
	run := func(q string) { fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0)) }
	run("CREATE SCHEMA amop_life")
	run("CREATE SCHEMA amop_dest")
	run("CREATE OPERATOR FAMILY amop_life.members USING btree")
	stmt, err := c.PrepareContext(p.Ctx,pgAmopFamilyQuery); if err != nil { t.Fatal(err) }; defer stmt.Close()
	held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:pgAmopFamilyQuery}
	check := func(schema,name string,want [][]any) { pgAmopRows(t,p,b,held,pgAmopFamilyQuery,want,false,schema,name) }
	check("amop_life","members",nil)
	run(pgAmopPopulate)
	var family int64
	if err := c.QueryRowContext(p.Ctx,"SELECT oid FROM pg_opfamily WHERE opfname='members' AND opfnamespace='amop_life'::regnamespace AND opfmethod=403").Scan(&family); err != nil || family <= 0 || family > 4294967295 { t.Fatal("family identity",family,err) }
	rows, err := stmt.QueryContext(p.Ctx,"amop_life","members"); if err != nil { t.Fatal(err) }
	pgAmopMetadata(t,rows,b.PG)
	observed := pgAmopConsume(t,rows); rows.Close()
	if len(observed) != 5 { t.Fatal("member count",observed) }
	seen := map[int64]bool{}
	var want [][]any
	for i, operator := range []int64{97,523,96,525,521} {
		oid := observed[i][0].(int64)
		if oid <= 0 || oid > 4294967295 || seen[oid] { t.Fatal("member identity",oid) }; seen[oid] = true
		want = append(want,[]any{oid,family,int64(23),int64(23),int64(i+1),"s",operator,int64(403),int64(0)})
		pgAmopRows(t,p,b,c,pgAmopProbe,want[i:i+1],false,oid)
		pgAmopRows(t,p,b,c,pgAmopProbe,want[i:i+1],true,oid)
	}
	if !reflect.DeepEqual(observed,want) { t.Fatal("owner semantics",observed,want) }
	check("amop_life","members",want)
	// A separate connection retains the already captured old result while the
	// owner connection mutates the family. No concurrent use of a pq portal.
	old, err := b.DB.QueryContext(p.Ctx,pgAmopFamilyQuery,"amop_life","members"); if err != nil { t.Fatal(err) }; defer old.Close()
	pgAmopMetadata(t,old,b.PG)
	run("ALTER OPERATOR FAMILY amop_life.members USING btree RENAME TO renamed")
	run("ALTER OPERATOR FAMILY amop_life.renamed USING btree SET SCHEMA amop_dest")
	check("amop_life","members",nil)
	check("amop_dest","renamed",want)
	pgAmopMetadata(t,old,b.PG)
	if got := pgAmopConsume(t,old); !reflect.DeepEqual(got,want) { t.Fatal("retained capture",got,want) }
	run("BEGIN")
	run("ALTER OPERATOR FAMILY amop_dest.renamed USING btree DROP OPERATOR 1(integer)")
	check("amop_dest","renamed",want[1:])
	run("ROLLBACK")
	check("amop_dest","renamed",want)
	run("ALTER OPERATOR FAMILY amop_dest.renamed USING btree DROP OPERATOR 1(integer)")
	check("amop_dest","renamed",want[1:])
	run("DROP OPERATOR FAMILY amop_dest.renamed USING btree")
	check("amop_dest","renamed",nil)
	run("DROP SCHEMA amop_life")
	run("DROP SCHEMA amop_dest")
	pgAmopRows(t,p,b,c,pgAmopWitnessQuery,pgAmopFacts(),false)
}

func pgAmopIndexLifecycle(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
	const bag = "SELECT * FROM pg_amop WHERE amopfamily IN (1976,1977) AND amoplefttype=23 AND amoprighttype=23 ORDER BY amopmethod,amopstrategy"
	var want [][]any
	for i, operator := range []int64{97,523,96,525,521} { want = append(want,[]any{int64(10015+i),int64(1976),int64(23),int64(23),int64(i+1),"s",operator,int64(403),int64(0)}) }
	want = append(want,pgAmopFacts()[3])
	check := func() {
		for _, prepared := range []bool{false,true} { pgAmopRows(t,p,b,b.DB,bag,want,prepared) }
		run(fixture.Select("SELECT n FROM amop_index ORDER BY n",[]string{"n"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
	}
	for _, prepared := range []bool{false,true} { pgAmopRows(t,p,b,b.DB,bag,want,prepared) }
	run(fixture.Exec("CREATE TABLE amop_index(n INT)",0))
	run(fixture.Exec("INSERT INTO amop_index VALUES(7),(8)",2))
	check()
	run(fixture.Exec("CREATE INDEX amop_index_b ON amop_index(n)",0)); check()
	run(fixture.Exec("CREATE INDEX amop_index_h ON amop_index USING hash(n)",0)); check()
	run(fixture.Exec("DROP INDEX amop_index_b",0)); check()
	run(fixture.Exec("DROP INDEX amop_index_h",0)); check()
	run(fixture.Exec("DROP TABLE amop_index",0))
}

func TestReferencePGAmopHeldExecution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"amop_life","amop_dest")
	pgOpclassScenario(t,pgAmopOwnerLifecycle)
}

func pgAmopLifecycle(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	pgAmopIndexLifecycle(t,p,b)
	run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
	run(fixture.Exec("CREATE TABLE amop_restore(n INT)",0))
	run(fixture.Exec("INSERT INTO amop_restore VALUES(7),(8)",2))
	run(fixture.Exec("CREATE INDEX amop_restore_b ON amop_restore(n)",0))
	run(fixture.Exec("CREATE VIEW amop_restore_view AS SELECT * FROM pg_catalog.pg_amop WHERE oid=10015",0))
	stmt, err := b.DB.PrepareContext(p.Ctx,pgAmopProbe); if err != nil { t.Fatal(err) }; defer stmt.Close()
	held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:pgAmopProbe}
	check := func() {
		pgAmopRows(t,p,b,held,pgAmopProbe,pgAmopFacts()[1:2],false,int64(10015))
		for _, prepared := range []bool{false,true} {
			s := fixture.Select("SELECT * FROM amop_restore_view",pgAmopColumns,pgAmopTypes,pgAmopFacts()[1:2])
			s.Prepared = prepared
			// A stored view does not inherit its base catalog's NOT NULL flags.
			s.CheckMetadata = func(t *testing.T,r *sql.Rows) { pgAmopResultMetadata(t,r,b.PG,true) }
			fixture.PgDescriptionExecutorRows(t,p,b,b.DB,s)
		}
		run(fixture.Select("SELECT n FROM amop_restore ORDER BY n",[]string{"n"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
		run(fixture.Select("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname='amop_restore_b'",[]string{"pg_get_indexdef"},[]string{"TEXT"},[][]any{{"CREATE INDEX amop_restore_b ON public.amop_restore USING btree (n)"}}))
	}
	check()
	if b.PG { return }
	old, err := b.DB.QueryContext(p.Ctx,pgAmopWitnessQuery); if err != nil { t.Fatal(err) }; defer old.Close()
	pgAmopMetadata(t,old,false)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) }
	if !strings.Contains(dump.String(),"CREATE VIEW") || !strings.Contains(dump.String(),"amop_restore_view") || strings.Contains(dump.String(),"INSERT INTO pg_catalog.pg_amop") { t.Fatal("nonlogical dump",dump.String()) }
	if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
	pgAmopInventory(t,p,b)
	pgAmopRows(t,p,b,held,pgAmopProbe,pgAmopFacts()[2:3],false,int64(10017))
	if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
	check(); pgAmopInventory(t,p,b)
	pgAmopMetadata(t,old,false)
	if got := pgAmopConsume(t,old); !reflect.DeepEqual(got,pgAmopFacts()) { t.Fatal("retained pre-reset rows",got) }
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&again); err != nil { t.Fatal(err) }
	if again.String() != dump.String() { t.Fatalf("logical redump changed\nbefore:%s\nafter:%s",dump.String(),again.String()) }
}

func TestReferencePGAmopLifecycle(t *testing.T) { pgOpclassScenario(t,pgAmopLifecycle) }

func TestReferencePGAmopCancellation(t *testing.T) {
	pgOpclassScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		pgAmopRows(t,p,b,b.DB,pgAmopProbe,pgAmopFacts()[1:2],false,int64(10015))
		if b.PG { return }
		stmt, err := b.DB.PrepareContext(p.Ctx,pgAmopProbe); if err != nil { t.Fatal(err) }; defer stmt.Close()
		ctx,cancel := context.WithCancel(p.Ctx); cancel()
		check := func(err error) {
			if !errors.Is(err,context.Canceled) { t.Fatal("canceled operation",err) }
			var state interface{ SQLState() string }; if errors.As(err,&state) { t.Fatal("context error gained SQLSTATE",err) }
		}
		rows, err := b.DB.QueryContext(ctx,pgAmopProbe,int64(10015)); if rows != nil { rows.Close(); t.Fatal("canceled direct rows") }; check(err)
		rows, err = stmt.QueryContext(ctx,int64(10015)); if rows != nil { rows.Close(); t.Fatal("canceled held rows") }; check(err)
		s, err := b.DB.PrepareContext(ctx,pgAmopProbe); if s != nil { s.Close(); t.Fatal("canceled prepare statement") }; check(err)
		pgAmopRows(t,p,b,fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:pgAmopProbe},pgAmopProbe,pgAmopFacts()[2:3],false,int64(10017))
	})
}

func TestReferencePGAmopFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"amop_life","amop_dest")
	pgOpclassScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		pgAmopInventory(t,p,b)
		pgAmopShapes(t,p,b,b.DB)
		pgAmopOwnerLifecycle(t,p,b)
		pgAmopIndexLifecycle(t,p,b)
		pgAmopDiagnosticFailures(t,p,b)
	})
}
