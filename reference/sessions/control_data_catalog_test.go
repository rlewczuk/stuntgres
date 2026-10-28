package reference_test

import ("database/sql";"testing";"stuntgres/reference/fixture")

func TestReferenceControlDataCatalog(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE pronamespace=11 AND proname IN ('pg_control_system','pg_control_checkpoint','pg_control_recovery','pg_control_init') ORDER BY oid`
		names := []string{"oid", "proname", "namespace", "owner", "language", "kind", "result", "proretset", "proisstrict", "volatility", "parallel", "procost", "prorows", "pronargdefaults", "variadic", "prosecdef", "proleakproof", "prosrc", "inputs", "alltypes", "modes", "names", "acl"}
		types := []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT2", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
		var rows [][]any
		rows=append(rows,[]any{int64(3441),"pg_control_system",int64(11),int64(10),int64(12),"f",int64(2249),false,true,"v","s",float64(1),float64(0),int64(0),int64(0),false,false,"pg_control_system","","{23,23,20,1184}","{o,o,o,o}","{pg_control_version,catalog_version_no,system_identifier,pg_control_last_modified}",nil})
		rows=append(rows,[]any{int64(3442),"pg_control_checkpoint",int64(11),int64(10),int64(12),"f",int64(2249),false,true,"v","s",float64(1),float64(0),int64(0),int64(0),false,false,"pg_control_checkpoint","","{3220,3220,25,23,23,16,25,26,28,28,28,26,28,28,26,28,28,1184}","{o,o,o,o,o,o,o,o,o,o,o,o,o,o,o,o,o,o}","{checkpoint_lsn,redo_lsn,redo_wal_file,timeline_id,prev_timeline_id,full_page_writes,next_xid,next_oid,next_multixact_id,next_multi_offset,oldest_xid,oldest_xid_dbid,oldest_active_xid,oldest_multi_xid,oldest_multi_dbid,oldest_commit_ts_xid,newest_commit_ts_xid,checkpoint_time}",nil})
		rows=append(rows,[]any{int64(3443),"pg_control_recovery",int64(11),int64(10),int64(12),"f",int64(2249),false,true,"v","s",float64(1),float64(0),int64(0),int64(0),false,false,"pg_control_recovery","","{3220,23,3220,3220,16}","{o,o,o,o,o}","{min_recovery_end_lsn,min_recovery_end_timeline,backup_start_lsn,backup_end_lsn,end_of_backup_record_required}",nil})
		rows=append(rows,[]any{int64(3444),"pg_control_init",int64(11),int64(10),int64(12),"f",int64(2249),false,true,"v","s",float64(1),float64(0),int64(0),int64(0),false,false,"pg_control_init","","{23,23,23,23,23,23,23,23,23,16,23,16}","{o,o,o,o,o,o,o,o,o,o,o,o}","{max_data_alignment,database_block_size,blocks_per_segment,wal_block_size,bytes_per_wal_segment,max_identifier_length,max_index_columns,max_toast_chunk_size,large_object_chunk_size,float8_pass_by_value,data_page_checksum_version,default_char_signedness}",nil})

		s := fixture.Select(q, names, types, rows)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
        for _,name:=range controlNames {
            q:="SELECT '"+name+"()'::regprocedure::text AS identity"
            fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(q,[]string{"identity"},[]string{"TEXT"},[][]any{{name+"()"}}))
        }
    })
}
