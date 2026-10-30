package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

const publicationRelRaw = "SELECT * FROM pg_catalog.pg_publication_rel ORDER BY prpubid,prrelid"
const publicationRelEdges = "SELECT p.pubname,c.relname,r.prattrs::text,pg_get_expr(r.prqual,r.prrelid) FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid JOIN pg_catalog.pg_class c ON c.oid=r.prrelid ORDER BY p.pubname,c.relname"

func publicationRelMetadata(t *testing.T, pg bool, columns []*sql.ColumnType) {
	t.Helper()
	fixture.TableDDLColumnTypes(t, pg, columns, []string{"oid", "prpubid", "prrelid", "prqual", "prattrs"}, []string{"OID", "OID", "OID", "PG_NODE_TREE", "INT2VECTOR"}, []bool{false, false, false, true, true})
	for _, c := range columns {
		if n, ok := c.Length(); n != 0 || ok { t.Fatalf("%s Length=(%d,%t)", c.Name(), n, ok) }
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok { t.Fatalf("%s DecimalSize=(%d,%d,%t)", c.Name(), p, s, ok) }
	}
}

// Raw carrier bytes are backend-local evidence, never cross-backend expectations.
func publicationRelConsume(t *testing.T, b fixture.Backend, rows *sql.Rows, count int) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil { t.Fatal(err) }
	publicationRelMetadata(t, b.PG, columns)
	var result [][]any
	seen := map[int64]bool{}
	for rows.Next() {
		raw := make([]any, 5)
		if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4]); err != nil { t.Fatal(err) }
		for i := range 3 {
			var id int64
			if b.PG {
				bytes, ok := raw[i].([]byte)
				if !ok { t.Fatalf("raw OID %d: %T", i, raw[i]) }
				id, err = strconv.ParseInt(string(bytes), 10, 64)
				if err != nil { t.Fatal(err) }
			} else {
				var ok bool
				id, ok = raw[i].(int64)
				if !ok { t.Fatalf("raw OID %d: %T", i, raw[i]) }
			}
			if id <= 0 || id > 4294967295 { t.Fatalf("invalid OID %d", id) }
			raw[i] = id
		}
		id := raw[0].(int64)
		if seen[id] { t.Fatalf("duplicate mapping identity %d", id) }
		seen[id] = true
		for i := 3; i < 5; i++ {
			if raw[i] == nil { continue }
			if b.PG {
				v, ok := raw[i].([]byte)
				if !ok { t.Fatalf("raw payload %d: %T", i, raw[i]) }
				raw[i] = string(v)
			} else if _, ok := raw[i].(string); !ok { t.Fatalf("raw payload %d: %T", i, raw[i]) }
			if raw[i] == "" { t.Fatalf("empty non-NULL payload %d", i) }
		}
		result = append(result, raw)
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if len(result) != count { t.Fatalf("raw rows=%v, expected %d", result, count) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	publicationRelMetadata(t, b.PG, columns)
	return result
}

func publicationRelRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, count int, prepared bool) [][]any {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		s, e2 := e.PrepareContext(p.Ctx, query)
		if e2 != nil { t.Fatal(e2) }
		defer s.Close()
		rows, err = s.QueryContext(p.Ctx)
	} else { rows, err = e.QueryContext(p.Ctx, query) }
	if err != nil { t.Fatal(err) }
	return publicationRelConsume(t, b, rows, count)
}

func publicationRelState(rows ...[]any) fixture.Step {
	return fixture.Select(publicationRelEdges, []string{"pubname", "relname", "prattrs", "pg_get_expr"}, []string{"NAME", "NAME", "TEXT", "TEXT"}, rows)
}

func publicationRelSame(t *testing.T, got, want [][]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) { t.Fatalf("raw capture changed: got %v, want %v", got, want) }
}

func TestReferencePGPublicationRelShape(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		run := func(s ...fixture.Step) { publicationRun(t, p, b, c, s...) }
		s, err := c.PrepareContext(p.Ctx, publicationRelRaw)
		if err != nil { t.Fatal(err) }
		defer s.Close()
		held := fixture.HeldStatement{Executor:c, Statement:s, SQL:publicationRelRaw}
		probe := func(count int) {
			for _, prepared := range []bool{false,true} {
				publicationRelRead(t,p,b,c,publicationRelRaw,count,prepared)
				publicationRelRead(t,p,b,c,"SELECT * FROM pg_catalog.pg_publication_rel WHERE FALSE",0,prepared)
				publicationRelRead(t,p,b,c,"SELECT * FROM pg_catalog.pg_publication_rel LIMIT 0",0,prepared)
			}
			publicationRelRead(t,p,b,held,publicationRelRaw,count,false)
		}
		probe(0)
		run(fixture.Select("SELECT attname,atttypid::bigint AS atttypid,attnotnull,attcollation::bigint AS attcollation FROM pg_catalog.pg_attribute WHERE attrelid=6106 AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname","atttypid","attnotnull","attcollation"}, []string{"NAME","INT8","BOOL","INT8"}, [][]any{{"oid",int64(26),true,int64(0)},{"prpubid",int64(26),true,int64(0)},{"prrelid",int64(26),true,int64(0)},{"prqual",int64(194),false,int64(950)},{"prattrs",int64(22),false,int64(0)}}),
			fixture.Select("SELECT c.oid::bigint AS oid,n.nspname,c.relkind,c.relisshared,t.typrelid=c.oid AS row_owner,a.typelem=t.oid AS array_owner,a.typarray=0 AS array_terminal FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.relname='pg_publication_rel'", []string{"oid","nspname","relkind","relisshared","row_owner","array_owner","array_terminal"}, []string{"INT8","NAME","CHAR","BOOL","BOOL","BOOL","BOOL"}, [][]any{{int64(6106),"pg_catalog","r",false,true,true,true}}),
			fixture.Exec("CREATE TABLE publication_rt_t(id integer)",0),fixture.Exec("CREATE TABLE publication_rt_u(id integer)",0))
		probe(0)
		run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t,publication_rt_u",0),fixture.Exec("CREATE PUBLICATION publication_rt_q FOR TABLE publication_rt_t",0),publicationRelState([]any{"publication_rt_p","publication_rt_t",nil,nil},[]any{"publication_rt_p","publication_rt_u",nil,nil},[]any{"publication_rt_q","publication_rt_t",nil,nil}))
		probe(3)
		raw := publicationRelRead(t,p,b,c,publicationRelRaw,3,false)
		for _, row := range raw {
			if row[3] != nil || row[4] != nil { t.Fatalf("omitted payload not NULL: %v",row) }
			publicationRelResolved(t,p,c,row)
		}
	})
}

// Resolve numeric identities against independently enumerated named edges, not
// allocator-specific values. Shape's named state assertion is the release oracle.
func publicationRelResolved(t *testing.T, p *fixture.Pair, e fixture.Executor, row []any) {
	t.Helper()
	pubs := map[string]bool{"publication_rt_p":true,"publication_rt_q":true}
	rels := map[string]bool{"publication_rt_t":true,"publication_rt_u":true}
	var pub, rel string
	rows, err := e.QueryContext(p.Ctx,"SELECT p.pubname,c.relname FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid JOIN pg_catalog.pg_class c ON c.oid=r.prrelid WHERE r.oid=$1 AND r.prpubid=$2 AND r.prrelid=$3",row[0],row[1],row[2])
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	if !rows.Next() { t.Fatal("unresolved mapping identity") }
	if err := rows.Scan(&pub,&rel); err != nil { t.Fatal(err) }
	if !pubs[pub] || !rels[rel] || pub=="publication_rt_q" && rel!="publication_rt_t" { t.Fatalf("unexpected edge %s/%s",pub,rel) }
	if rows.Next() || rows.Err()!=nil { t.Fatal("nonunique relationship",rows.Err()) }
}

func publicationRelPayloadSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	publicationRun(t,p,b,e,fixture.Exec("CREATE TABLE publication_rt_t(id integer,removed integer,v integer)",0),fixture.Exec("ALTER TABLE publication_rt_t DROP COLUMN removed",0),fixture.Exec("INSERT INTO publication_rt_t(id,v) VALUES(1,5),(2,-1),(3,NULL)",3),fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t(v,id) WHERE(v>0) WITH(publish='insert')",0))
}

func publicationRelSentinels() fixture.Step {
	return fixture.Select("SELECT id,v FROM publication_rt_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(5)},{int64(2),int64(-1)},{int64(3),nil}})
}

func TestReferencePGPublicationRelPayloads(t *testing.T) {
	publicationScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		c:=publicationConn(t,p,b)
		run:=func(s ...fixture.Step){publicationRun(t,p,b,c,s...)}
		publicationRelPayloadSetup(t,p,b,c)
		run(fixture.Exec("CREATE PUBLICATION publication_rt_q FOR TABLE publication_rt_t WITH(publish='insert')",0),fixture.Exec("CREATE PUBLICATION publication_rt_explicit FOR TABLE publication_rt_t(id,v) WITH(publish='insert')",0))
		before:=publicationRelRead(t,p,b,c,publicationRelRaw,3,false)
		for _, prepared:=range []bool{false,true} {
			s:=fixture.Select("SELECT p.pubname,r.prattrs::text,array_ndims(r.prattrs),array_lower(r.prattrs,1),array_upper(r.prattrs,1),array_length(r.prattrs,1),cardinality(r.prattrs),r.prattrs[0],r.prattrs[1],r.prattrs[2],pg_get_expr(r.prqual,r.prrelid) FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid ORDER BY p.pubname",[]string{"pubname","prattrs","array_ndims","array_lower","array_upper","array_length","cardinality","prattrs","prattrs","prattrs","pg_get_expr"},[]string{"NAME","TEXT","INT4","INT4","INT4","INT4","INT4","INT2","INT2","INT2","TEXT"},[][]any{{"publication_rt_explicit","1 3",int64(1),int64(0),int64(1),int64(2),int64(2),int64(1),int64(3),nil,nil},{"publication_rt_p","1 3",int64(1),int64(0),int64(1),int64(2),int64(2),int64(1),int64(3),nil,"(v > 0)"},{"publication_rt_q",nil,nil,nil,nil,nil,nil,nil,nil,nil,nil}})
			if prepared { s=fixture.Prepared(s) };run(s)
		}
		run(fixture.Exec("ALTER TABLE publication_rt_t RENAME COLUMN v TO value",0),publicationRelState([]any{"publication_rt_explicit","publication_rt_t","1 3",nil},[]any{"publication_rt_p","publication_rt_t","1 3","(value > 0)"},[]any{"publication_rt_q","publication_rt_t",nil,nil}))
		after:=publicationRelRead(t,p,b,c,publicationRelRaw,3,true)
		publicationRelSame(t,after,before)
		run(fixture.Exec("CREATE SCHEMA publication_rt_s",0),fixture.Exec("CREATE TABLE publication_rt_s.u(id integer)",0),fixture.Exec("CREATE PUBLICATION publication_rt_schema FOR TABLES IN SCHEMA publication_rt_s",0),fixture.Exec("CREATE PUBLICATION publication_rt_all FOR ALL TABLES",0),fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid WHERE p.pubname IN ('publication_rt_schema','publication_rt_all')",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}),fixture.Exec("CREATE TABLE publication_rt_root(id integer) PARTITION BY RANGE(id)",0),fixture.Exec("CREATE TABLE publication_rt_child PARTITION OF publication_rt_root FOR VALUES FROM(0) TO(10)",0),fixture.Exec("CREATE PUBLICATION publication_rt_partition FOR TABLE publication_rt_root",0),fixture.Select("SELECT c.relname FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_class c ON c.oid=r.prrelid JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid WHERE p.pubname='publication_rt_partition'",[]string{"relname"},[]string{"NAME"},[][]any{{"publication_rt_root"}}),fixture.Exec("ALTER PUBLICATION publication_rt_partition DROP TABLE publication_rt_root",0),fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid WHERE p.pubname='publication_rt_partition'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}),fixture.Select("SELECT id,value FROM publication_rt_t ORDER BY id",[]string{"id","value"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(5)},{int64(2),int64(-1)},{int64(3),nil}}))
		run(publicationMembers("publication_rt_schema", []any{"publication_rt_s", "u"}),
			fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_tables WHERE pubname='publication_rt_all' AND schemaname='public' AND tablename='publication_rt_t'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}
