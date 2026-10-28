package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const ltreeComparisonSchema = "lt_cmp_provider"

func ltreeComparisonControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE SCHEMA "+ltreeComparisonSchema,
		"CREATE EXTENSION ltree SCHEMA "+ltreeComparisonSchema,
		"SET search_path=public,"+ltreeComparisonSchema,
	} { ltreeComparisonRun(t,p,b,c,fixture.Exec(q,0)) }
	ltreeComparisonTable(t,p,b,c)
	for _, q := range []string{
		"CREATE INDEX lt_cmp_btree ON lt_cmp(v "+ltreeComparisonSchema+".ltree_ops)",
		"CREATE INDEX lt_cmp_hash ON lt_cmp USING hash(v "+ltreeComparisonSchema+".hash_ltree_ops)",
		"CREATE INDEX lt_cmp_expression ON lt_cmp((v<'a-b'::ltree))",
		"CREATE TABLE lt_cmp_checked(v ltree CHECK (v<>'forbidden'::ltree))",
		"CREATE TABLE lt_cmp_vacant(v ltree)",
		"CREATE TABLE lt_cmp_nulls(id integer,v ltree)",
		"CREATE VIEW lt_cmp_view AS SELECT id,"+ltreeComparisonProjection("v","'a.b'::ltree")+",v IN ('a.b'::ltree,NULL::ltree) AS member,CASE v WHEN 'a.b'::ltree THEN true ELSE false END AS chosen,NULLIF(v,'a.b'::ltree) IS NULL AS nullified,v IS NOT DISTINCT FROM NULL::ltree AS null_value FROM lt_cmp",
		"CREATE VIEW lt_cmp_empty_view AS SELECT v='a.b'::ltree AS eq FROM lt_cmp_vacant ORDER BY v",
		"CREATE VIEW lt_cmp_null_view AS SELECT id,v='a.b'::ltree AS eq FROM lt_cmp_nulls ORDER BY v,id",
		"CREATE VIEW lt_cmp_ordered_view AS SELECT id,v FROM lt_cmp ORDER BY v,id LIMIT 4",
	} { ltreeComparisonRun(t,p,b,c,fixture.Exec(q,0)) }
	ltreeComparisonRun(t,p,b,c,fixture.Exec("INSERT INTO lt_cmp_checked VALUES ('a.b'),(NULL)",2))
	ltreeComparisonRun(t,p,b,c,fixture.Exec("INSERT INTO lt_cmp_nulls VALUES (1,NULL),(2,NULL)",2))
}

func ltreeComparisonViewStep() fixture.Step {
	columns := append([]string{"id"},ltreeComparisonColumns...)
	columns = append(columns,"member","chosen","nullified","null_value")
	types := append([]string{"INT4"},ltreeComparisonTypes...)
	types = append(types,"BOOL","BOOL","BOOL","BOOL")
	var rows [][]any
	for i, sign := range []int{1,0,-1,-1,0,0} {
		row := []any{int64(i+1)}
		if i == 4 {
			row = append(row,nil,nil,nil,nil,nil,nil)
		} else { row = append(row,ltreeComparisonRow(sign)... ) }
		member,chosen,nullified,nullValue := any(nil),false,false,false
		if i==1 || i==5 { member,chosen,nullified = true,true,true }
		if i==4 { nullified,nullValue = true,true }
		row = append(row,member,chosen,nullified,nullValue)
		rows = append(rows,row)
	}
	return fixture.Select("SELECT id,eq,ne,lt,le,gt,ge,member,chosen,nullified,null_value FROM lt_cmp_view ORDER BY id",columns,types,rows)
}

func ltreeComparisonControlChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
	t.Helper()
	ltreeComparisonRun(t,p,b,c,fixture.Exec("SET search_path=public,"+ltreeComparisonSchema,0))
	steps := append(ltreeComparisonConsumerChecks(b),ltreeComparisonCatalogChecks(ltreeComparisonSchema)...)
	steps = append(steps,
		ltreeComparisonViewStep(),
		fixture.Select("SELECT eq FROM lt_cmp_empty_view",[]string{"eq"},[]string{"BOOL"},[][]any{}),
		fixture.Select("SELECT id,eq FROM lt_cmp_null_view ORDER BY id",[]string{"id","eq"},[]string{"INT4","BOOL"},[][]any{{int64(1),nil},{int64(2),nil}}),
		// LIMIT makes the stored comparator determine membership; outer id
		// ordering specifies portable output without concealing a wrong inner sort.
		ltreeSelect(b,"SELECT id,v FROM lt_cmp_ordered_view ORDER BY id",[]string{"id","v"},[][]any{{int64(2),"a.b"},{int64(3),"a"},{int64(4),""},{int64(6),"a.b"}}),
		fixture.Select("SELECT count(*) AS n FROM lt_cmp_checked",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}}),
		fixture.Select("SELECT 'a.b'::ltree OPERATOR("+ltreeComparisonSchema+".<) 'a-b'::ltree AS lt",[]string{"lt"},[]string{"BOOL"},[][]any{{true}}),
	)
	for _, s := range steps {
		ltreeComparisonRun(t,p,b,c,s)
		ltreeComparisonRun(t,p,b,c,fixture.Prepared(s))
	}
	for _, s := range []fixture.Step{
		fixture.Exec("INSERT INTO lt_cmp VALUES (7,'a.b')",1),
		ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='a.b'::ltree ORDER BY id",2,6,7),
		fixture.Exec("UPDATE lt_cmp SET v='ab' WHERE id=7",1),
		ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='a.b'::ltree ORDER BY id",2,6),
		ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='ab'::ltree ORDER BY id",7),
		fixture.Exec("DELETE FROM lt_cmp WHERE id=7",1),
		ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='ab'::ltree ORDER BY id"),
	} { ltreeComparisonRun(t,p,b,c,s) }
}

func ltreeComparisonDump(t *testing.T,p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&out); err != nil { t.Fatal(err) }
	return out.Bytes()
}

func ltreeComparisonRetain(t *testing.T,p *fixture.Pair,b fixture.Backend) (*sql.Rows,[]*sql.ColumnType) {
	t.Helper()
	rows,err := b.DB.QueryContext(p.Ctx,"SELECT id,v FROM lt_cmp ORDER BY v,id")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ rows.Close() })
	return rows,ltreeMetadata(t,b.PG,rows,[]string{"id","v"},[]bool{false,true})
}

func ltreeComparisonConsume(t *testing.T,rows *sql.Rows,columns []*sql.ColumnType) {
	t.Helper()
	ltreeConsumeRetained(t,rows,[][]any{{int64(4),""},{int64(3),"a"},{int64(2),"a.b"},{int64(6),"a.b"},{int64(1),"a-b"},{int64(5),nil}})
	ltreeColumnMetadata(t,false,columns,[]string{"id","v"},[]bool{false,true})
}

func ltreeComparisonHeldChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,held fixture.HeldStatement) {
	t.Helper()
	for _, tc := range []struct{ left,right any; row []any }{
		{"a.b","a-b",[]any{true,false}}, {"a.b",[]byte("a.b"),[]any{false,true}}, {nil,"a.b",[]any{nil,nil}},
	} {
		ltreeComparisonRun(t,p,b,held,fixture.Select(held.SQL,[]string{"lt","eq"},[]string{"BOOL","BOOL"},[][]any{tc.row},tc.left,tc.right))
	}
}

func ltreeComparisonDrop(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"DROP VIEW lt_cmp_view", "DROP VIEW lt_cmp_empty_view", "DROP VIEW lt_cmp_null_view", "DROP VIEW lt_cmp_ordered_view",
		"DROP TABLE lt_cmp", "DROP TABLE lt_cmp_checked", "DROP TABLE lt_cmp_vacant", "DROP TABLE lt_cmp_nulls",
		"DROP EXTENSION ltree", "DROP SCHEMA "+ltreeComparisonSchema,
	} { ltreeComparisonRun(t,p,b,c,fixture.Exec(q,0)) }
}

func ltreeComparisonSQLRecreate(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,held fixture.HeldStatement) {
	t.Helper()
	var before,after int64
	q := "SELECT ('"+ltreeComparisonSchema+".ltree'::regtype)::oid"
	if err := c.QueryRowContext(p.Ctx,q).Scan(&before); err != nil { t.Fatal(err) }
	ltreeComparisonDrop(t,p,b,c)
	ltreeComparisonControlSetup(t,p,b,c)
	if err := c.QueryRowContext(p.Ctx,q).Scan(&after); err != nil { t.Fatal(err) }
	if before<=0 || after<=0 || before==after { t.Fatalf("recreated identity %d/%d",before,after) }
	ltreeComparisonControlChecks(t,p,b,c)
	ltreeComparisonHeldChecks(t,p,b,held)
}

func ltreeComparisonReplay(t *testing.T,p *fixture.Pair,dump []byte) {
	t.Helper()
	if p.Postgres==nil { return }
	c,err := p.Postgres.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	clean := "DROP SCHEMA IF EXISTS "+ltreeComparisonSchema+" CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Fatal(err) }
	defer func(){ if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Error(err) } }()
	if _,err := c.ExecContext(p.Ctx,string(dump)); err != nil { t.Fatalf("actual emitted comparison SQL: %v",err) }
	b := fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
	ltreeComparisonControlChecks(t,p,b,c)
}

func ltreeComparisonLifecycle(t *testing.T,phase string) {
	fixture.SchemaReferenceCleanup(t,ltreeComparisonSchema)
	ltreeComparisonScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeComparisonControlSetup(t,p,b,c)
		ltreeComparisonControlChecks(t,p,b,c)
		q := "SELECT $1::text::ltree < $2::text::ltree AS lt,$1::text::ltree = $2::text::ltree AS eq"
		stmt,err := c.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		ltreeComparisonHeldChecks(t,p,b,held)
		if phase=="recreate" || b.PG {
			ltreeComparisonSQLRecreate(t,p,b,c,held)
			return
		}
		ltreeComparisonRun(t,p,b,c,fixture.Exec("BEGIN",0))
		ltreeComparisonRun(t,p,b,c,fixture.Exec("INSERT INTO lt_cmp VALUES (99,'PrivateSentinel')",1))
		dump := ltreeComparisonDump(t,p)
		ltreeComparisonRun(t,p,b,c,fixture.Exec("ROLLBACK",0))
		if strings.Contains(string(dump),"PrivateSentinel") { t.Fatal("Dump included uncommitted row") }
		if first,consumer := bytes.Index(dump,[]byte("CREATE EXTENSION")),bytes.Index(dump,[]byte("CREATE TABLE")); first<0 || consumer<first { t.Fatal("extension must precede consumers") }
		for _, forbidden := range []string{"CREATE TYPE", "CREATE FUNCTION", "CREATE OPERATOR CLASS", "CREATE OPERATOR FAMILY"} {
			if bytes.Contains(dump,[]byte(forbidden)) { t.Fatalf("standalone extension member %s",forbidden) }
		}
		old,columns := ltreeComparisonRetain(t,p,b)
		if phase=="same" {
			if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
			ltreeComparisonRun(t,p,b,c,fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'",[]string{"extname"},[]string{"NAME"},[][]any{}))
			if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
			ltreeComparisonControlChecks(t,p,b,c)
			ltreeComparisonHeldChecks(t,p,b,held)
			if got := ltreeComparisonDump(t,p); !bytes.Equal(got,dump) { t.Fatal("stable replacement changed deterministic dump") }
			ltreeComparisonConsume(t,old,columns)
			ltreeComparisonReplay(t,p,dump)
			return
		}
		target := fixture.NewFixture(t,false,true)
		tc,err := target.Memory.Conn(target.Ctx)
		if err != nil { t.Fatal(err) }
		defer tc.Close()
		tb := fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
		ltreeComparisonRun(t,target,tb,tc,fixture.Exec("CREATE TABLE incumbent(id integer)",0))
		ltreeComparisonRun(t,target,tb,tc,fixture.Exec("INSERT INTO incumbent VALUES (73)",1))
		incumbent,err := target.Memory.QueryContext(target.Ctx,"SELECT id FROM incumbent")
		if err != nil { t.Fatal(err) }
		defer incumbent.Close()
		if err := stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
		if !incumbent.Next() { t.Fatal("retained incumbent reader lost") }
		var sentinel int
		if err := incumbent.Scan(&sentinel); err != nil || sentinel!=73 { t.Fatalf("incumbent %d: %v",sentinel,err) }
		if incumbent.Next() || incumbent.Err()!=nil { t.Fatalf("incumbent tail: %v",incumbent.Err()) }
		incumbent.Close()
		ltreeComparisonControlChecks(t,target,tb,tc)
		ltreeComparisonControlChecks(t,p,b,c)
		if got := ltreeComparisonDump(t,p); !bytes.Equal(got,dump) { t.Fatal("destination replacement mutated source") }
		ltreeComparisonConsume(t,old,columns)
		before := ltreeComparisonDump(t,target)
		retained,metadata := ltreeComparisonRetain(t,target,tb)
		var beforeOID,afterOID int64
		identity := "SELECT ('"+ltreeComparisonSchema+".ltree'::regtype)::oid"
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&beforeOID); err != nil { t.Fatal(err) }
		bad := "CREATE EXTENSION ltree; CREATE TABLE staged(v ltree); CREATE VIEW staged_view AS SELECT v<'a-b'::ltree AS lt FROM staged; INSERT INTO staged VALUES ('a'); INSERT INTO staged VALUES ('a..b');"
		want := fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3")
		want.Position = ltreeSQLPosition(bad,"'a..b'")
		fixture.AssertDiagnostic(t,stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad)),&want)
		canceled,cancel := context.WithCancel(target.Ctx); cancel()
		if err := stuntgres.Restore(canceled,target.Native,bytes.NewReader(dump)); !errors.Is(err,context.Canceled) { t.Fatalf("canceled Restore: %v",err) }
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&afterOID); err != nil { t.Fatal(err) }
		if beforeOID!=afterOID { t.Fatal("failed Restore replaced installed identity") }
		ltreeComparisonControlChecks(t,target,tb,tc)
		if got := ltreeComparisonDump(t,target); !bytes.Equal(got,before) { t.Fatal("failed Restore changed deterministic dump") }
		ltreeComparisonConsume(t,retained,metadata)
	})
}

func TestReferenceLtreeComparisonControls(t *testing.T) {
	for _, phase := range []string{"same","fresh_and_failure"} {
		t.Run(phase,func(t *testing.T){ ltreeComparisonLifecycle(t,phase) })
	}
}

func TestReferenceLtreeComparisonRecreation(t *testing.T) { ltreeComparisonLifecycle(t,"recreate") }
