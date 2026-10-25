package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCatalogVectorsCatalogReuse(t *testing.T) {
	catalogVectorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		run:=func(s fixture.Step){catalogVectorRows(t,p,b,b.DB,s)}
		// pg_type.dat fixes these identities; function references are explicitly
		// cast to OID, not regproc output names or backend-allocated identities.
		run(fixture.Select("SELECT oid,typname,typelem,typarray,typlen,typbyval,typtype,typcategory,typispreferred,typinput::oid AS i,typoutput::oid AS o,typreceive::oid AS r,typsend::oid AS s,typsubscript::oid AS sub,typalign,typstorage,typcollation FROM pg_type WHERE oid IN (22,30) ORDER BY oid",
			[]string{"oid","typname","typelem","typarray","typlen","typbyval","typtype","typcategory","typispreferred","i","o","r","s","sub","typalign","typstorage","typcollation"},
			[]string{"OID","NAME","OID","OID","INT2","BOOL","CHAR","CHAR","BOOL","OID","OID","OID","OID","OID","CHAR","CHAR","OID"},
			[][]any{{int64(22),"int2vector",int64(21),int64(1006),int64(-1),false,"b","A",false,int64(40),int64(41),int64(2410),int64(2411),int64(6179),"i","p",int64(0)},
				{int64(30),"oidvector",int64(26),int64(1013),int64(-1),false,"b","A",false,int64(54),int64(55),int64(2420),int64(2421),int64(6179),"i","p",int64(0)}}))
		run(fixture.Exec("CREATE TABLE vectors(id INT,s INT2VECTOR,o OIDVECTOR NOT NULL)",0))
		run(fixture.Select("SELECT attname,attnum,atttypid,atttypmod,attcollation,attnotnull FROM pg_attribute WHERE attrelid='vectors'::regclass AND attnum>0 ORDER BY attnum",[]string{"attname","attnum","atttypid","atttypmod","attcollation","attnotnull"},[]string{"NAME","INT2","OID","INT4","OID","BOOL"},[][]any{{"id",int64(1),int64(23),int64(-1),int64(0),false},{"s",int64(2),int64(22),int64(-1),int64(0),false},{"o",int64(3),int64(30),int64(-1),int64(0),true}}))
		run(fixture.Select("SELECT attname,attnum,atttypid,attnotnull FROM pg_attribute WHERE attrelid='pg_proc'::regclass AND attname='proargtypes'",[]string{"attname","attnum","atttypid","attnotnull"},[]string{"NAME","INT2","OID","BOOL"},[][]any{{"proargtypes",int64(20),int64(30),true}}))
		run(fixture.Select("SELECT attname,attnum,atttypid,attnotnull FROM pg_attribute WHERE attrelid='pg_index'::regclass AND attname IN ('indkey','indcollation','indclass','indoption') ORDER BY attnum",[]string{"attname","attnum","atttypid","attnotnull"},[]string{"NAME","INT2","OID","BOOL"},[][]any{{"indkey",int64(16),int64(22),true},{"indcollation",int64(17),int64(30),true},{"indclass",int64(18),int64(30),true},{"indoption",int64(19),int64(22),true}}))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT oid,proargtypes FROM pg_proc WHERE oid IN (41,55,1598) ORDER BY oid",[]string{"oid","proargtypes"},[]string{"OID","OIDVECTOR"},[][]any{{int64(41),"22"},{int64(55),"30"},{int64(1598),""}}))
		run(fixture.Exec("CREATE FUNCTION vector_args(a smallint,b oid) RETURNS integer LANGUAGE SQL AS 'SELECT 1'",0))
		run(fixture.Exec("CREATE FUNCTION vector_zero() RETURNS integer LANGUAGE SQL AS 'SELECT 1'",0))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT proname,proargtypes,array_lower(proargtypes,1) AS l,proargtypes[0] AS first FROM pg_proc WHERE oid IN ('vector_args(smallint,oid)'::regprocedure,'vector_zero()'::regprocedure) ORDER BY proname",[]string{"proname","proargtypes","l","first"},[]string{"NAME","OIDVECTOR","INT4","OID"},[][]any{{"vector_args","21 26",int64(0),int64(21)},{"vector_zero","",int64(0),nil}}))
		run(fixture.Exec("CREATE TABLE vector_src(a INT,b INT,c INT)",0))
		run(fixture.Exec("CREATE INDEX vector_idx ON vector_src(a DESC NULLS FIRST,b) INCLUDE(c)",0))
		indexWant:=fixture.Select("SELECT indkey,indcollation,indclass,indoption FROM pg_index WHERE indexrelid='vector_idx'::regclass",[]string{"indkey","indcollation","indclass","indoption"},[]string{"INT2VECTOR","OIDVECTOR","OIDVECTOR","INT2VECTOR"},[][]any{{"1 2 3","0 0","1978 1978","3 0"}})
		catalogVectorRows(t,p,b,b.DB,indexWant)
		run(fixture.Exec("CREATE INDEX vector_expr ON vector_src((a+1))",0))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT indkey,indclass,indoption FROM pg_index WHERE indexrelid='vector_expr'::regclass",[]string{"indkey","indclass","indoption"},[]string{"INT2VECTOR","OIDVECTOR","INT2VECTOR"},[][]any{{"0","1978","0"}}))
		run(fixture.Exec("ALTER TABLE vector_src RENAME COLUMN a TO renamed",0))
		catalogVectorRows(t,p,b,b.DB,indexWant)
		run(fixture.Exec("ALTER FUNCTION vector_args(smallint,oid) RENAME TO vector_renamed",0))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT proargtypes FROM pg_proc WHERE oid='vector_renamed(smallint,oid)'::regprocedure",[]string{"proargtypes"},[]string{"OIDVECTOR"},[][]any{{"21 26"}}))
		// A retained catalog result must not be rebuilt from current row text.
		ctx:=p.Ctx
		rows,err:=b.DB.QueryContext(ctx,"SELECT proargtypes FROM pg_proc WHERE oid='vector_renamed(smallint,oid)'::regprocedure");if err!=nil{t.Fatal(err)}
		defer rows.Close()
		columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
		run(fixture.Exec("DROP FUNCTION vector_renamed(smallint,oid)",0))
		if !rows.Next(){t.Fatal("retained signature missing",rows.Err())};var value string;if err:=rows.Scan(&value);err!=nil{t.Fatal(err)}
		if value!="21 26"||columns[0].DatabaseTypeName()!="OIDVECTOR"{t.Fatal("retained catalog vector",value,columns[0])}
		if rows.Next()||rows.Err()!=nil{t.Fatal("extra retained signature",rows.Err())}
	})
}
