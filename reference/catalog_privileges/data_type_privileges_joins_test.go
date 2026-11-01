package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Every branch constrains the represented kind and joins the complete identity.
func dtpCheckRoutineJoins(t *testing.T, p *fixture.Pair, c *sql.Conn, run func(fixture.Step), schema, id string, formals []string, result any) {
	t.Helper()
	where := dtpRoutineWhere(schema,id)
	var want [][]any
	for i, formal := range formals { want = append(want,[]any{formal,int64(i+1)}) }
	s := fixture.Select("SELECT d.dtd_identifier,p.ordinal_position FROM information_schema.data_type_privileges d JOIN information_schema.parameters p ON p.specific_catalog=d.object_catalog AND p.specific_schema=d.object_schema AND p.specific_name=d.object_name AND p.dtd_identifier=d.dtd_identifier WHERE "+where, []string{"dtd_identifier","ordinal_position"}, []string{"NAME","INT4"}, want)
	s.Bag = true
	run(s)
	run(fixture.Select("SELECT d.dtd_identifier,r.dtd_identifier FROM information_schema.data_type_privileges d JOIN information_schema.routines r ON r.specific_catalog=d.object_catalog AND r.specific_schema=d.object_schema AND r.specific_name=d.object_name AND (d.dtd_identifier=r.dtd_identifier OR (d.dtd_identifier IS NULL AND r.dtd_identifier IS NULL)) WHERE "+where, []string{"dtd_identifier","dtd_identifier"}, []string{"NAME","NAME"}, [][]any{{result,result}}))
}

func TestReferenceDataTypePrivilegesJoins(t *testing.T) {
	dtpScenario(t,func(t *testing.T,p *fixture.Pair,_ fixture.Backend,c *sql.Conn,run func(fixture.Step)){
		for _,q := range []string{
			"CREATE TABLE dtp_join_table(a integer,b text,c integer[])","ALTER TABLE dtp_join_table DROP COLUMN b","ALTER TABLE dtp_join_table ADD COLUMN d text",
			"CREATE DOMAIN dtp_join_domain AS integer[]",
			"CREATE FUNCTION dtp_join_function(a integer,b text) RETURNS integer LANGUAGE SQL AS 'SELECT a'",
			"CREATE FUNCTION dtp_join_function(a text) RETURNS text LANGUAGE SQL AS 'SELECT a'",
			"CREATE FUNCTION dtp_join_zero() RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
			"CREATE PROCEDURE dtp_join_proc(a integer,b text) LANGUAGE SQL AS 'SELECT 1'",
			"CREATE PROCEDURE dtp_join_pzero() LANGUAGE SQL AS 'SELECT 1'",
			"REVOKE EXECUTE ON FUNCTION dtp_join_function(integer,text),dtp_join_function(text),dtp_join_zero() FROM PUBLIC",
			"REVOKE EXECUTE ON PROCEDURE dtp_join_proc(integer,text),dtp_join_pzero() FROM PUBLIC",
		}{run(fixture.Exec(q,0))}
		want := append(dtpRows("public","dtp_join_table","TABLE","1","3","4"),dtpRows("public","dtp_join_domain","DOMAIN","1")...)
		type routine struct{name,inputs string; formals []string; result any}
		routines := []routine{{"dtp_join_function","integer,text",[]string{"1","2"},"0"},{"dtp_join_function","text",[]string{"1"},"0"},{"dtp_join_zero","",nil,"0"},{"dtp_join_proc","integer,text",[]string{"1","2"},nil},{"dtp_join_pzero","",nil,nil}}
		var identities []string
		for _,r := range routines{
			id:=dtpRoutineIdentity(t,p,c,"public",r.name,r.inputs);identities=append(identities,id)
			want=append(want,dtpRoutineRows("public",id,r.result)...)
			for _,f:=range r.formals{want=append(want,dtpRoutineRows("public",id,f)...)}
		}
		// Literal nonempty inventory comes first, so missing producers cannot pass joins vacuously.
		run(dtpProjection(t,p,c,"object_schema='public'",want))
		s:=fixture.Select("SELECT d.dtd_identifier,c.column_name FROM information_schema.data_type_privileges d JOIN information_schema.columns c ON c.table_catalog=d.object_catalog AND c.table_schema=d.object_schema AND c.table_name=d.object_name AND c.dtd_identifier=d.dtd_identifier WHERE d.object_type='TABLE' AND d.object_schema='public' AND d.object_name='dtp_join_table'",[]string{"dtd_identifier","column_name"},[]string{"NAME","NAME"},[][]any{{"1","a"},{"3","c"},{"4","d"}});s.Bag=true;run(s)
		run(fixture.Select("SELECT d.dtd_identifier,x.domain_name FROM information_schema.data_type_privileges d JOIN information_schema.domains x ON x.domain_catalog=d.object_catalog AND x.domain_schema=d.object_schema AND x.domain_name=d.object_name AND x.dtd_identifier=d.dtd_identifier WHERE d.object_type='DOMAIN' AND d.object_schema='public' AND d.object_name='dtp_join_domain'",[]string{"dtd_identifier","domain_name"},[]string{"NAME","NAME"},[][]any{{"1","dtp_join_domain"}}))
		for i,r:=range routines{dtpCheckRoutineJoins(t,p,c,run,"public",identities[i],r.formals,r.result)}
		run(fixture.Exec("GRANT SELECT(c) ON dtp_join_table TO dtp_reader",0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION dtp_join_function(text) TO dtp_reader",0))
		dtpActor(run,"dtp_reader",func(){
			visible:=append(dtpRows("public","dtp_join_table","TABLE","3"),dtpRows("public","dtp_join_domain","DOMAIN","1")...)
			visible=append(visible,dtpRoutineRows("public",identities[1],"0","1")...)
			run(dtpProjection(t,p,c,"object_schema='public'",visible))
			dtpCheckRoutineJoins(t,p,c,run,"public",identities[1],[]string{"1"},"0")
		})
		// NULL extension is a join row, not an invented datatype-usage descriptor.
		run(fixture.Exec("CREATE TABLE dtp_join_one(a integer)",0));run(fixture.Exec("INSERT INTO dtp_join_one VALUES(1)",1))
		nulls:=fixture.Select("SELECT d.* FROM dtp_join_one x LEFT JOIN information_schema.data_type_privileges d ON d.object_name='dtp_join_absent'",dtpColumns,dtpTypes,[][]any{{nil,nil,nil,nil,nil}})
		nulls.CheckMetadata=func(t *testing.T,rows *sql.Rows){types,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};if len(types)!=5{t.Fatal(types)};for i,typ:=range types{if typ.DatabaseTypeName()!=dtpTypes[i]{t.Fatal("NULL extension carrier",i,typ.DatabaseTypeName())}}}
		run(nulls)
	})
}
