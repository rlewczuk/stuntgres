package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

var pgIndexColumns = []string{"indexrelid", "indrelid", "indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct", "indisprimary", "indisexclusion", "indimmediate", "indisclustered", "indisvalid", "indcheckxmin", "indisready", "indislive", "indisreplident", "indkey", "indcollation", "indclass", "indoption", "indexprs", "indpred"}
var pgIndexTypes = []string{"OID", "OID", "INT2", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT2VECTOR", "OIDVECTOR", "OIDVECTOR", "INT2VECTOR", "PG_NODE_TREE", "PG_NODE_TREE"}

func pgIndexBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}

func pgIndexSchema(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	s := fixture.Select("SELECT * FROM pg_catalog.pg_index WHERE FALSE", pgIndexColumns, pgIndexTypes, nil)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		for i, typ := range types {
			nullable, known := typ.Nullable()
			if b.PG {
				if known { t.Fatalf("pq column %s unexpectedly knows nullability", typ.Name()) }
			} else if !known || nullable != (i >= 19) {
				t.Fatalf("column %s nullable=%v known=%v", typ.Name(), nullable, known)
			}
		}
	}
	pgIndexBoth(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }, s)
}

// Every scalar fact is asserted, while generated OIDs are checked against their
// actual pg_class endpoints rather than another allocator's numeric sequence.
func pgIndexLogical(name string, rows [][]any) fixture.Step {
	return fixture.Select(`SELECT i.indexrelid=c.oid AS same_index,i.indrelid=p.oid AS same_parent,i.indnatts,i.indnkeyatts,i.indisunique,i.indnullsnotdistinct,i.indisprimary,i.indisexclusion,i.indimmediate,i.indisclustered,i.indisvalid,i.indcheckxmin,i.indisready,i.indislive,i.indisreplident,i.indkey::text,i.indcollation::text,i.indclass::text,i.indoption::text,pg_get_expr(i.indexprs,i.indrelid) AS expressions,pg_get_expr(i.indpred,i.indrelid) AS predicate FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid JOIN pg_catalog.pg_class p ON p.oid=i.indrelid JOIN pg_catalog.pg_namespace ns ON ns.oid=c.relnamespace WHERE ns.nspname='public' AND c.relname=$1`,
		[]string{"same_index", "same_parent", "indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct", "indisprimary", "indisexclusion", "indimmediate", "indisclustered", "indisvalid", "indcheckxmin", "indisready", "indislive", "indisreplident", "indkey", "indcollation", "indclass", "indoption", "expressions", "predicate"},
		[]string{"BOOL", "BOOL", "INT2", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, rows, name)
}

func pgIndexOrdinary(natts, keys int64, key, coll, class, option string, expressions, predicate any) []any {
	return []any{true, true, natts, keys, false, false, false, false, true, false, true, false, true, true, false, key, coll, class, option, expressions, predicate}
}

func TestReferencePGIndexSchema(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgIndexSchema(t, p, b)
		typeIDs := []int64{26,26,21,21,16,16,16,16,16,16,16,16,16,16,16,22,30,30,22,194,194}
		var expected [][]any
		for i, name := range pgIndexColumns {
			dims, coll := int64(0), int64(0)
			if i >= 15 && i <= 18 { dims = 1 }
			if i >= 19 { coll = 950 }
			expected = append(expected, []any{int64(i+1), name, typeIDs[i], int64(-1), i < 19, dims, coll, false})
		}
		s := fixture.Select(`SELECT attnum,attname,atttypid::bigint,atttypmod,attnotnull,attndims,attcollation::bigint,attisdropped FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_index'::regclass AND attnum>0 ORDER BY attnum`, []string{"attnum","attname","atttypid","atttypmod","attnotnull","attndims","attcollation","attisdropped"}, []string{"INT2","NAME","INT8","INT4","BOOL","INT2","INT8","BOOL"}, expected)
		pgIndexBoth(func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }, s)
	})
}

func pgIndexInclude(run func(fixture.Step), dropped bool) {
	key := "1 2 3"
	if dropped { key = "2 3 4" }
	pgIndexBoth(run, pgIndexLogical("pgi_ix", [][]any{pgIndexOrdinary(3,2,key,"0 0","1978 1978","3 0",nil,nil)}))
	pgIndexBoth(run, fixture.Select(`SELECT indkey,indcollation,indclass,indoption,indexprs,indpred FROM pg_index WHERE indexrelid='pgi_ix'::regclass`, []string{"indkey","indcollation","indclass","indoption","indexprs","indpred"}, []string{"INT2VECTOR","OIDVECTOR","OIDVECTOR","INT2VECTOR","PG_NODE_TREE","PG_NODE_TREE"}, [][]any{{key,"0 0","1978 1978","3 0",nil,nil}}))
	pgIndexBoth(run, fixture.Select(`SELECT array_lower(indcollation,1) AS coll_lower,array_lower(indclass,1) AS class_lower,array_lower(indoption,1) AS option_lower,indcollation[0]::bigint AS coll,indclass[1]::bigint AS second_class,indoption[1] AS second_option,indclass[2] AS no_include_class FROM pg_index WHERE indexrelid='pgi_ix'::regclass`, []string{"coll_lower","class_lower","option_lower","coll","second_class","second_option","no_include_class"}, []string{"INT4","INT4","INT4","INT8","INT8","INT2","OID"}, [][]any{{int64(0),int64(0),int64(0),int64(0),int64(1978),int64(0),nil}}))
	pgIndexBoth(run, fixture.Select(`SELECT c.relnatts,i.indrelid='pgi_t'::regclass AS parent,array_lower(i.indkey,1) AS lower,array_length(i.indkey,1) AS all_keys,array_length(i.indclass,1) AS classes,array_length(i.indcollation,1) AS collations,array_length(i.indoption,1) AS options,i.indkey[0] AS first,i.indkey[2] AS included,i.indclass[0]::bigint AS class,i.indoption[0] AS option FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.oid='pgi_ix'::regclass`, []string{"relnatts","parent","lower","all_keys","classes","collations","options","first","included","class","option"}, []string{"INT2","BOOL","INT4","INT4","INT4","INT4","INT4","INT2","INT2","INT8","INT2"}, [][]any{{int64(3),true,int64(0),int64(3),int64(2),int64(2),int64(2),int64(1+boolInt(dropped)),int64(3+boolInt(dropped)),int64(1978),int64(3)}}))
}

func boolInt(v bool) int { if v { return 1 }; return 0 }

func TestReferencePGIndexDefinition(t *testing.T) {
	t.Run("keys", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
			for _, q := range []string{"CREATE TABLE pgi_t(unused INT,a INT,b INT,c INT,s TEXT)","CREATE INDEX pgi_ix ON pgi_t(a DESC NULLS FIRST,b) INCLUDE(c)","ALTER TABLE pgi_t DROP COLUMN unused"} { run(fixture.Exec(q,0)) }
			pgIndexInclude(run,true)
			run(fixture.Exec("CREATE INDEX pgi_simple ON pgi_t(a)",0))
			pgIndexBoth(run,pgIndexLogical("pgi_simple",[][]any{pgIndexOrdinary(1,1,"2","0","1978","0",nil,nil)}))
			var indexOID, parentOID int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pgi_simple'::regclass::oid::bigint,'pgi_t'::regclass::oid::bigint").Scan(&indexOID, &parentOID); err != nil || indexOID <= 0 || parentOID <= 0 {
				t.Fatal("actual catalog identities", indexOID, parentOID, err)
			}
			raw := []any{indexOID,parentOID,int64(1),int64(1),false,false,false,false,true,false,true,false,true,true,false,"2","0","1978","0",nil,nil}
			if b.PG {
				raw[0],raw[1] = strconv.FormatInt(indexOID,10),strconv.FormatInt(parentOID,10)
			}
			pgIndexBoth(run, fixture.Select("SELECT * FROM pg_catalog.pg_index WHERE indexrelid=$1::oid", pgIndexColumns, pgIndexTypes, [][]any{raw}, indexOID))
			run(fixture.Exec("CREATE INDEX pgi_order ON pgi_t(a ASC NULLS LAST,a ASC NULLS FIRST,a DESC NULLS LAST,a DESC NULLS FIRST)",0))
			pgIndexBoth(run,pgIndexLogical("pgi_order",[][]any{pgIndexOrdinary(4,4,"2 2 2 2","0 0 0 0","1978 1978 1978 1978","0 2 1 3",nil,nil)}))
			run(fixture.Exec("CREATE INDEX pgi_expr ON pgi_t(lower(s),(a+1),a DESC NULLS LAST)",0))
			pgIndexBoth(run,pgIndexLogical("pgi_expr",[][]any{pgIndexOrdinary(3,3,"0 0 2","100 0 0","3126 1978 1978","0 0 1","lower(s), (a + 1)",nil)}))
			run(fixture.Exec(`CREATE INDEX pgi_coll ON pgi_t((s COLLATE "C"),(s COLLATE "POSIX"),s)`,0))
			pgIndexBoth(run,pgIndexLogical("pgi_coll",[][]any{pgIndexOrdinary(3,3,"5 5 5","950 951 100","3126 3126 3126","0 0 0",nil,nil)}))
			pgIndexBoth(run,fixture.Select(`SELECT indexprs,indpred FROM pg_index WHERE indexrelid='pgi_simple'::regclass`,[]string{"indexprs","indpred"},[]string{"PG_NODE_TREE","PG_NODE_TREE"},[][]any{{nil,nil}}))
		})
	})
	for _, tc := range []struct { method, typ, class string }{{"hash","INT","10020"},{"gist","POINT","10061"},{"spgist","TEXT","10086"},{"brin","INT","10104"}} {
		t.Run(tc.method,func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
				run(fixture.Exec("CREATE TABLE pgi_method(v "+tc.typ+")",0))
				run(fixture.Exec(fmt.Sprintf("CREATE INDEX pgi_method_ix ON pgi_method USING %s(v)",tc.method),0))
				coll := "0"; if tc.method == "spgist" { coll = "100" }
				pgIndexBoth(run,pgIndexLogical("pgi_method_ix",[][]any{pgIndexOrdinary(1,1,"1",coll,tc.class,"0",nil,nil)}))
			})
		})
	}
}
