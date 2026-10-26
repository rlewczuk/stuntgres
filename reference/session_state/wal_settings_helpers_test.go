package reference_test

import (
	"database/sql"
	"path"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type walExpectation struct {
	name, kind, context, unit, boot, raw, show, min, max, category, description, extra, enums string
}

// Independent PostgreSQL 18 release expectations, not engine-derived data.
var walSettings = []walExpectation{
	{"archive_cleanup_command","string","sighup","","","","","","","Archive Recovery","Sets the shell command that will be executed at every restart point.","",""},
	{"archive_command","string","sighup","","","(disabled)","(disabled)","","","Archiving","Sets the shell command that will be called to archive a WAL file.","An empty string means use \"archive_library\".",""},
	{"archive_library","string","sighup","","","","","","","Archiving","Sets the library that will be called to archive a WAL file.","An empty string means use \"archive_command\".",""},
	{"archive_mode","enum","postmaster","","off","off","off","","","Archiving","Allows archiving of WAL files using \"archive_command\".","","{always,on,off}"},
	{"archive_timeout","integer","sighup","s","0","0","0","0","1073741823","Archiving","Sets the amount of time to wait before forcing a switch to the next WAL file.","0 disables the timeout.",""},
	{"checkpoint_completion_target","real","sighup","","0.9","0.9","0.9","0","1","Checkpoints","Time spent flushing dirty buffers during checkpoint, as fraction of checkpoint interval.","",""},
	{"checkpoint_flush_after","integer","sighup","8kB","32","32","256kB","0","256","Checkpoints","Number of pages after which previously performed writes are flushed to disk.","0 disables forced writeback.",""},
	{"checkpoint_timeout","integer","sighup","s","300","300","5min","30","86400","Checkpoints","Sets the maximum time between automatic WAL checkpoints.","",""},
	{"checkpoint_warning","integer","sighup","s","30","30","30s","0","2147483647","Checkpoints","Sets the maximum time before warning if checkpoints triggered by WAL volume happen too frequently.","Write a message to the server log if checkpoints caused by the filling of WAL segment files happen more frequently than this amount of time. 0 disables the warning.",""},
	{"commit_delay","integer","superuser","","0","0","0","0","100000","Settings","Sets the delay in microseconds between transaction commit and flushing WAL to disk.","",""},
	{"commit_siblings","integer","user","","5","5","5","0","1000","Settings","Sets the minimum number of concurrent open transactions required before performing \"commit_delay\".","",""},
	{"fsync","bool","sighup","","on","on","on","","","Settings","Forces synchronization of updates to disk.","The server will use the fsync() system call in several places to make sure that updates are physically written to disk. This ensures that a database cluster will recover to a consistent state after an operating system or hardware crash.",""},
	{"full_page_writes","bool","sighup","","on","on","on","","","Settings","Writes full pages to WAL when first modified after a checkpoint.","A page write in process during an operating system crash might be only partially written to disk.  During recovery, the row changes stored in WAL are not enough to recover.  This option writes pages when first modified after a checkpoint to WAL so full recovery is possible.",""},
	{"max_wal_size","integer","sighup","MB","1024","1024","1GB","2","2147483647","Checkpoints","Sets the WAL size that triggers a checkpoint.","",""},
	{"min_wal_size","integer","sighup","MB","80","80","80MB","2","2147483647","Checkpoints","Sets the minimum size to shrink the WAL to.","",""},
	{"recovery_end_command","string","sighup","","","","","","","Archive Recovery","Sets the shell command that will be executed once at the end of recovery.","",""},
	{"recovery_prefetch","enum","sighup","","try","try","try","","","Recovery","Prefetch referenced blocks during recovery.","Look ahead in the WAL to find references to uncached data.","{off,on,try}"},
	{"recovery_target","string","postmaster","","","","","","","Recovery Target","Set to \"immediate\" to end recovery as soon as a consistent state is reached.","",""},
	{"recovery_target_action","enum","postmaster","","pause","pause","pause","","","Recovery Target","Sets the action to perform upon reaching the recovery target.","","{pause,promote,shutdown}"},
	{"recovery_target_inclusive","bool","postmaster","","on","on","on","","","Recovery Target","Sets whether to include or exclude transaction with recovery target.","",""},
	{"recovery_target_lsn","string","postmaster","","","","","","","Recovery Target","Sets the LSN of the write-ahead log location up to which recovery will proceed.","",""},
	{"recovery_target_name","string","postmaster","","","","","","","Recovery Target","Sets the named restore point up to which recovery will proceed.","",""},
	{"recovery_target_time","string","postmaster","","","","","","","Recovery Target","Sets the time stamp up to which recovery will proceed.","",""},
	{"recovery_target_timeline","string","postmaster","","latest","latest","latest","","","Recovery Target","Specifies the timeline to recover into.","",""},
	{"recovery_target_xid","string","postmaster","","","","","","","Recovery Target","Sets the transaction ID up to which recovery will proceed.","",""},
	{"restore_command","string","sighup","","","","","","","Archive Recovery","Sets the shell command that will be called to retrieve an archived WAL file.","",""},
	{"summarize_wal","bool","sighup","","off","off","off","","","Summarization","Starts the WAL summarizer process to enable incremental backup.","",""},
	{"synchronous_commit","enum","user","","on","on","on","","","Settings","Sets the current transaction's synchronization level.","","{local,remote_write,remote_apply,on,off}"},
	{"wal_buffers","integer","postmaster","8kB","-1","512","4MB","-1","262143","Settings","Sets the number of disk-page buffers in shared memory for WAL.","-1 means use a fraction of \"shared_buffers\".",""},
	{"wal_compression","enum","superuser","","off","off","off","","","Settings","Compresses full-page writes written in WAL file with specified method.","","{pglz,lz4,zstd,on,off}"},
	{"wal_decode_buffer_size","integer","postmaster","B","524288","524288","512kB","65536","1073741823","Recovery","Buffer size for reading ahead in the WAL during recovery.","Maximum distance to read ahead in the WAL to prefetch referenced data blocks.",""},
	{"wal_init_zero","bool","superuser","","on","on","on","","","Settings","Writes zeroes to new WAL files before first use.","",""},
	{"wal_level","enum","postmaster","","replica","replica","replica","","","Settings","Sets the level of information written to the WAL.","","{minimal,replica,logical}"},
	{"wal_log_hints","bool","postmaster","","off","off","off","","","Settings","Writes full pages to WAL when first modified after a checkpoint, even for a non-critical modification.","",""},
	{"wal_recycle","bool","superuser","","on","on","on","","","Settings","Recycles WAL files by renaming them.","",""},
	{"wal_skip_threshold","integer","user","kB","2048","2048","2MB","0","2147483647","Settings","Minimum size of new file to fsync instead of writing WAL.","",""},
	{"wal_summary_keep_time","integer","sighup","min","14400","14400","10d","0","35791394","Summarization","Time for which WAL summary files should be kept.","0 disables automatic summary file deletion.",""},
	{"wal_sync_method","enum","sighup","","fdatasync","fdatasync","fdatasync","","","Settings","Selects the method used for forcing WAL updates to disk.","","{fsync,fdatasync,open_sync,open_datasync}"},
	{"wal_writer_delay","integer","sighup","ms","200","200","200ms","1","10000","Settings","Time between WAL flushes performed in the WAL writer.","",""},
	{"wal_writer_flush_after","integer","sighup","8kB","128","128","1MB","0","2147483647","Settings","Amount of WAL written out by WAL writer that triggers a flush.","",""},
}

var walMutable = []struct{ name, input, show, raw string }{
	{"commit_delay","100000","100000","100000"}, {"commit_siblings","0","0","0"},
	{"synchronous_commit","off","off","off"}, {"wal_compression","on","pglz","pglz"},
	{"wal_init_zero","off","off","off"}, {"wal_recycle","off","off","off"},
	{"wal_skip_threshold","1MB","1MB","1024"},
}

func walDefinition(name string) walExpectation {
	for _, d := range walSettings { if d.name == name { return d } }
	panic("unknown WAL test setting: "+name)
}
func walNames() string {
	names := make([]string,0,len(walSettings))
	for _, d := range walSettings { names = append(names,physicalLiteral(d.name)) }
	return strings.Join(names,",")
}
// The pinned fixture starts PostgreSQL with fsync=off. Its released boot value
// remains on; native initialization is deliberately unchanged.
func walProfile(d walExpectation, pg bool) walExpectation {
	if pg && d.name == "fsync" { d.raw,d.show = "off","off" }
	return d
}
func walReset(d walExpectation) string { if d.name == "archive_command" { return "" }; return d.raw }
func walSource(d walExpectation, pg bool) string {
	if pg && d.name == "fsync" { return "command line" }
	if pg && (d.name == "max_wal_size" || d.name == "min_wal_size") { return "configuration file" }
	return "default"
}
func (s *physicalCase) walValue(name, show, raw, source string, prepared bool) {
	d := walProfile(walDefinition(name),s.pg)
	for _, query := range []string{"SHOW "+name,"SELECT current_setting('"+name+"')","SELECT current_setting('"+name+"',true)"} {
		isShow := strings.HasPrefix(query,"SHOW ")
		column := "current_setting"; if isShow { column = name }
		step := fixture.Select(query,[]string{column},[]string{"TEXT"},[][]any{{show}})
		step.Prepared = prepared
		step.CheckMetadata = func(t *testing.T,rows *sql.Rows) { settingsTextMetadata(t,rows,s.pg,!isShow) }
		s.run(step)
	}
	step := fixture.Select("SELECT setting,boot_val,reset_val,source FROM pg_settings WHERE name='"+name+"'",[]string{"setting","boot_val","reset_val","source"},[]string{"TEXT","TEXT","TEXT","TEXT"},[][]any{{raw,d.boot,walReset(d),source}})
	step.Prepared = prepared; s.run(step)
}
func (s *physicalCase) walInventory(prepared, fileAccess bool) {
	query := "SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name IN ("+walNames()+") ORDER BY name"
	var rows *sql.Rows; var err error
	if prepared { stmt,e := s.conn.PrepareContext(s.p.Ctx,query); if e != nil { s.t.Fatal(e) }; defer stmt.Close(); rows,err = stmt.QueryContext(s.p.Ctx) } else { rows,err = s.conn.QueryContext(s.p.Ctx,query) }
	if err != nil { s.t.Fatal(err) }; defer rows.Close()
	statisticsSettingsCheckMetadata(s.t,rows,s.pg,physicalColumns,physicalTypes)
	count := 0
	for rows.Next() {
		if count >= len(walSettings) { s.t.Fatal("extra WAL row") }; d := walProfile(walSettings[count],s.pg)
		cells,dest := make([]any,17),make([]any,17); for j := range cells { dest[j] = &cells[j] }
		if err := rows.Scan(dest...); err != nil { s.t.Fatal(err) }; for j := range cells { cells[j] = fixture.Normalize(cells[j]) }
		source := walSource(d,s.pg)
		var file,line any
		if s.pg && fileAccess && source == "configuration file" {
			text,ok := cells[14].(string); number,nok := cells[15].(int64)
			if !ok || !path.IsAbs(text) || text == "/" || !nok || number <= 0 { s.t.Fatal("invalid oracle file provenance",cells) }
			file,line = text,number
		}
		want := []any{d.name,d.raw,replicationNullable(d.unit),"Write-Ahead Log / "+d.category,d.description,replicationNullable(d.extra),d.context,d.kind,source,replicationNullable(d.min),replicationNullable(d.max),replicationNullable(d.enums),d.boot,walReset(d),file,line,false}
		if !reflect.DeepEqual(cells,want) { s.t.Fatalf("%s inventory got %#v want %#v",d.name,cells,want) }; count++
	}
	if rows.Err() != nil || count != 40 { s.t.Fatal("WAL inventory",count,rows.Err()) }
}
func (s *physicalCase) walDefaults(prepared bool) {
	for _, d := range walSettings { d = walProfile(d,s.pg); s.walValue(d.name,d.show,d.raw,walSource(d,s.pg),prepared) }
}
func (s *physicalCase) walSet(name, input string, prepared bool) {
	step := fixture.Exec("SET "+name+"="+physicalLiteral(input),0); step.Prepared = prepared; s.run(step)
}
func walContext(d walExpectation) fixture.Diagnostic {
	if d.context == "postmaster" { return physicalRestart(d.name) }
	return physicalDiagnostic("55P02","parameter \""+d.name+"\" cannot be changed now")
}
func walDenied(name string) fixture.Diagnostic { return physicalDiagnostic("42501","permission denied to set parameter \""+name+"\"") }
