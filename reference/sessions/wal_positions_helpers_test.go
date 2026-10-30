package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var walPositionNames = []string{"pg_current_wal_flush_lsn", "pg_current_wal_insert_lsn", "pg_current_wal_lsn", "pg_switch_wal", "pg_create_restore_point", "pg_log_standby_snapshot"}
var walPositionIdentities = []string{"pg_current_wal_flush_lsn()", "pg_current_wal_insert_lsn()", "pg_current_wal_lsn()", "pg_switch_wal()", "pg_create_restore_point(text)", "pg_log_standby_snapshot()"}
var walPositionCalls = []string{"pg_current_wal_flush_lsn()", "pg_current_wal_insert_lsn()", "pg_current_wal_lsn()", "pg_switch_wal()", "pg_create_restore_point('wal_positions_marker')", "pg_log_standby_snapshot()"}

func walPositionRestore(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, roles ...string) func() {
	t.Helper()
	return func() {
		ctx,cancel := context.WithTimeout(context.Background(),10*time.Second); defer cancel()
		if _,err := c.ExecContext(ctx,"RESET SESSION AUTHORIZATION"); err != nil { t.Error(err); return }
		if pg {
			// Ordinary REVOKE removes grant dependencies before exact raw ACL
			// replacement; catalog UPDATE alone does not maintain pg_shdepend.
			for _,identity := range walPositionIdentities {
				for _,role := range roles {
					if _,err := c.ExecContext(ctx,"REVOKE ALL ON FUNCTION pg_catalog."+identity+" FROM "+role); err != nil { t.Error(err) }
				}
			}
			if _,err := c.ExecContext(ctx,"UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid IN (3330,2852,2849)"); err != nil { t.Error(err) }
			if _,err := c.ExecContext(ctx,"UPDATE pg_catalog.pg_proc SET proacl=ARRAY['stuntgres=X/stuntgres']::aclitem[] WHERE oid IN (2848,3098,6305)"); err != nil { t.Error(err) }
			return
		}
		for i, identity := range walPositionIdentities {
			for _, role := range roles { if _,err := c.ExecContext(ctx,"REVOKE ALL ON FUNCTION pg_catalog."+identity+" FROM "+role); err != nil { t.Error(err) } }
			action := "GRANT EXECUTE ON FUNCTION pg_catalog."+identity+" TO PUBLIC"
			if i >= 3 { action = "REVOKE ALL ON FUNCTION pg_catalog."+identity+" FROM PUBLIC" }
			if _,err := c.ExecContext(ctx,action); err != nil { t.Error(err) }
		}
		// Exact native raw ACL repair belongs to isolated Restore, not SQL UPDATE.
		{
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx,p.Native,&dump); err != nil { t.Error(err); return }
			for i,identity := range walPositionIdentities {
				literal := "NULL::pg_catalog.aclitem[]"
				if i>=3 { literal = "'{postgres=X/postgres}'::pg_catalog.aclitem[]" }
				dump.WriteString("\nUPDATE pg_catalog.pg_proc SET proacl="+literal+" WHERE oid='pg_catalog."+identity+"'::pg_catalog.regprocedure;\n")
			}
			if err := stuntgres.Restore(ctx,p.Native,strings.NewReader(dump.String())); err != nil { t.Error(err) }
		}
	}
}

func walPositionBoundary(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, query, name string, args ...any) {
	t.Helper()
	if !b.PG {
		s := replicationMessageError(query,"0A000",name+" physical WAL access is unsupported in embedded mode","")
		s.Args = args
		fixture.RunStep(t,p.Ctx,executor,b.Recorder,s)
		return
	}
	rows,err := executor.QueryContext(p.Ctx,query,args...)
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	columns,err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 || columns[0].DatabaseTypeName() != "PG_LSN" { t.Fatal("genuine LSN metadata",columns,err) }
	if !rows.Next() { t.Fatal("missing real physical result",rows.Err()) }
	var value string
	if err := rows.Scan(&value); err != nil { t.Fatal(err) }
	parts := strings.Split(value,"/")
	if len(parts) != 2 { t.Fatal("invalid physical LSN",value) }
	var words [2]uint64
	for i,part := range parts { word,err := strconv.ParseUint(part,16,32); if err != nil || strings.ToUpper(strconv.FormatUint(word,16)) != part { t.Fatal("noncanonical LSN",value,err) }; words[i] = word }
	if words == [2]uint64{} { t.Fatal("zero physical LSN",value) }
	if rows.Next() || rows.Err() != nil { t.Fatal("non-scalar result",rows.Err()) }
}
func walPositionBoundaryRoutes(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,query,name string,args ...any) {
	t.Helper()
	walPositionBoundary(t,p,b,c,query,name,args...)
	stmt,err := c.PrepareContext(p.Ctx,query)
	if err != nil { t.Fatal("valid Prepare executed physical work",err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}
	walPositionBoundary(t,p,b,held,query,name,args...)
	walPositionBoundary(t,p,b,held,query,name,args...)
}
func walPositionMetadata(pg bool,names []string) func(*testing.T,*sql.Rows) {
	return func(t *testing.T,rows *sql.Rows) {
		columns,err := rows.ColumnTypes()
		if err != nil || len(columns) != len(names) { t.Fatal(columns,err) }
		for i,c := range columns {
			scan := reflect.TypeFor[string](); if pg { scan = reflect.TypeFor[any]() }
			if c.Name() != names[i] || c.DatabaseTypeName() != "PG_LSN" || c.ScanType() != scan { t.Fatal("PG_LSN presentation",i,c.Name(),c.DatabaseTypeName(),c.ScanType()) }
			if n,ok := c.Nullable(); n || ok { t.Fatal("computed nullability",n,ok) }
			if _,ok := c.Length(); ok { t.Fatal("unexpected length") }
			if _,_,ok := c.DecimalSize(); ok { t.Fatal("unexpected precision") }
		}
	}
}
func walPositionInitialPrivileges(pg bool) fixture.Step {
	acl := recoveryControlOwnerACL(pg)
	return fixture.Select("SELECT objoid::bigint AS oid,classoid::bigint AS class,objsubid,privtype::text AS kind,initprivs::text AS acl FROM pg_init_privs WHERE classoid=1255 AND objoid IN (2848,2849,2852,3098,3330,6305) ORDER BY objoid",[]string{"oid","class","objsubid","kind","acl"},[]string{"INT8","INT8","INT4","TEXT","TEXT"},[][]any{{int64(2848),int64(1255),int64(0),"i",acl},{int64(3098),int64(1255),int64(0),"i",acl},{int64(6305),int64(1255),int64(0),"i",acl}})
}
