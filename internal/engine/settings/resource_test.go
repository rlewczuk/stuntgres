package settings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

// name/type/context/unit/category/boot/current/min/max are independent literals.
const resourceDescriptorExpectations = `
autovacuum_work_mem integer sighup kB M -1 -1 -1 2147483647
backend_flush_after integer user 8kB I 0 0 0 256
bgwriter_delay integer sighup ms B 200 200 10 10000
bgwriter_flush_after integer sighup 8kB B 64 64 0 256
bgwriter_lru_maxpages integer sighup - B 100 100 0 1073741823
bgwriter_lru_multiplier real sighup - B 2 2 0 10
commit_timestamp_buffers integer postmaster 8kB M 0 32 0 131072
dynamic_shared_memory_type enum postmaster - M posix posix - -
effective_io_concurrency integer user - I 16 16 0 1000
file_copy_method enum user - D copy copy - -
file_extend_method enum sighup - D posix_fallocate posix_fallocate - -
hash_mem_multiplier real user - M 2 2 1 1000
huge_page_size integer postmaster kB M 0 0 0 2147483647
huge_pages enum postmaster - M try try - -
io_combine_limit integer user 8kB I 16 16 1 128
io_max_combine_limit integer postmaster 8kB I 16 16 1 128
io_max_concurrency integer postmaster - I -1 64 -1 1024
io_method enum postmaster - I worker worker - -
io_workers integer sighup - I 3 3 1 32
logical_decoding_work_mem integer user kB M 65536 65536 64 2147483647
maintenance_io_concurrency integer user - I 16 16 0 1000
maintenance_work_mem integer user kB M 65536 65536 64 2147483647
max_files_per_process integer postmaster - K 1000 1000 64 2147483647
max_notify_queue_pages integer postmaster - D 1048576 1048576 64 2147483647
max_parallel_maintenance_workers integer user - W 2 2 0 1024
max_parallel_workers integer user - W 8 8 0 1024
max_parallel_workers_per_gather integer user - W 2 2 0 1024
max_prepared_transactions integer postmaster - M 0 0 0 262143
max_stack_depth integer superuser kB M 100 2048 100 2147483647
max_worker_processes integer postmaster - W 8 8 0 262143
min_dynamic_shared_memory integer postmaster MB M 0 0 0 2147483647
multixact_member_buffers integer postmaster 8kB M 32 32 16 131072
multixact_offset_buffers integer postmaster 8kB M 16 16 16 131072
notify_buffers integer postmaster 8kB M 16 16 16 131072
parallel_leader_participation bool user - W on on - -
serializable_buffers integer postmaster 8kB M 32 32 16 131072
shared_buffers integer postmaster 8kB M 16384 16384 16 1073741823
shared_memory_type enum postmaster - M mmap mmap - -
subtransaction_buffers integer postmaster 8kB M 0 32 0 131072
temp_buffers integer user 8kB M 1024 1024 100 1073741823
temp_file_limit integer superuser kB D -1 -1 -1 2147483647
transaction_buffers integer postmaster 8kB M 0 32 0 131072
vacuum_buffer_usage_limit integer user kB M 2048 2048 0 16777216
work_mem integer user kB M 4096 4096 64 2147483647
`

func TestResourceSettingsDescriptors(t *testing.T) {
	r:=NewRegistry(nil,false)
	types:=map[string]Type{"integer":Integer,"real":Floating,"enum":Enum,"bool":Boolean}
	contexts:=map[string]Access{"user":Session,"superuser":Privileged,"postmaster":StartupOnly,"sighup":Sighup}
	units:=map[string]Unit{"-":UnitNone,"kB":Kilobytes,"MB":Megabytes,"8kB":Blocks8kB,"ms":Milliseconds}
	categories:=map[string]string{"M":"Memory","D":"Disk","K":"Kernel Resources","B":"Background Writer","I":"I/O","W":"Worker Processes"}
	lines:=strings.Split(strings.TrimSpace(resourceDescriptorExpectations),"\n")
	if len(lines)!=44 { t.Fatal("independent inventory must contain44 rows") }
	for _,line:=range lines {
		f:=strings.Fields(line); if len(f)!=9 { t.Fatal(line) }
		d,err:=r.Lookup(f[0]); if err!=nil { t.Fatal(err) }
		count:=0; for _,name:=range r.Order() { if name==f[0] { count++ } }
		if count!=1 || d.Type!=types[f[1]] || d.Access!=contexts[f[2]] || d.Unit!=units[f[3]] || d.Category!="Resource Usage / "+categories[f[4]] || d.Raw(d.BootValue())!=f[5] || d.Raw(d.DefaultValue())!=f[6] || d.ReadRestricted || d.BootDefaultNull || d.FixedDefault || d.Description=="" { t.Fatalf("descriptor %s: %#v",line,d) }
		if f[1]=="integer" { min,_:=strconv.ParseInt(f[7],10,32); max,_:=strconv.ParseInt(f[8],10,32); if d.IntegerMin!=int32(min) || d.IntegerMax!=int32(max) { t.Fatal(line,d) } }
		if f[1]=="real" { min,_:=strconv.ParseFloat(f[7],64); max,_:=strconv.ParseFloat(f[8],64); if d.FloatMin!=min || d.FloatMax!=max { t.Fatal(line,d) } }
		if f[5]!=f[6] && (d.NativeStartup==nil || d.NativeStartup.Source!="default" || d.NativeStartup.ResetNull) { t.Fatal("startup provenance",line,d.NativeStartup) }
	}
	for name,want:=range map[string]string{"dynamic_shared_memory_type":"posix,sysv,mmap","shared_memory_type":"sysv,mmap","huge_pages":"off,on,try","file_copy_method":"copy,clone","file_extend_method":"posix_fallocate,write_zeros","io_method":"sync,worker"} {
		d,_:=r.Lookup(name); if strings.Join(d.EnumValues,",")!=want { t.Fatal(name,d.EnumValues) }
	}
}

func TestResourceSettingsValidation(t *testing.T) {
	r:=NewRegistry(nil,false)
	for _,line:=range strings.Split(strings.TrimSpace(resourceDescriptorExpectations),"\n") {
		f:=strings.Fields(line); d,_:=r.Lookup(f[0])
		if _,err:=d.Parse("bogus"); err==nil { t.Fatal("malformed accepted",f[0]) }
		if f[1]!="integer" && f[1]!="real" { continue }
		for _,input:=range []string{f[7],f[8]} {
			_,err:=d.Parse(input)
			if f[0]=="max_stack_depth" && input==f[8] { if err==nil { t.Fatal("stack ceiling bypass") }; continue }
			if err!=nil { t.Fatal(f[0],input,err) }
		}
		min,_:=strconv.ParseFloat(f[7],64); max,_:=strconv.ParseFloat(f[8],64)
		for _,input:=range []string{strconv.FormatFloat(min-1,'f',0,64),strconv.FormatFloat(max+1,'f',0,64)} {
			_,err:=d.Parse(input); var diagnostic *sqlstate.Error
			if !errors.As(err,&diagnostic) || diagnostic.Code!="22023" { t.Fatal(f[0],input,err) }
		}
	}
	for _,tc:=range []struct{name,input,raw string}{{"autovacuum_work_mem","-1","-1"},{"autovacuum_work_mem","0","64"},{"autovacuum_work_mem","63","64"},{"autovacuum_work_mem","64","64"},{"vacuum_buffer_usage_limit","0","0"},{"vacuum_buffer_usage_limit","128","128"},{"max_stack_depth","7169","7169"},{"max_stack_depth","7680","7680"},{"work_mem","30.1GB","31561728"},{"io_combine_limit","128","128"}} {
		d,_:=r.Lookup(tc.name); v,err:=d.Parse(tc.input); if err!=nil || d.Raw(v)!=tc.raw { t.Fatal(tc, v, err) }
	}
	for _,tc:=range []struct{name,input,number,detail,hint string}{
		{"vacuum_buffer_usage_limit","127","127",`"vacuum_buffer_usage_limit" must be 0 or between 128 kB and 16777216 kB.`,""},
		{"vacuum_buffer_usage_limit","127kB","127",`"vacuum_buffer_usage_limit" must be 0 or between 128 kB and 16777216 kB.`,""},
		{"vacuum_buffer_usage_limit","0x7f","127",`"vacuum_buffer_usage_limit" must be 0 or between 128 kB and 16777216 kB.`,""},
		{"max_stack_depth","7681","7681",`"max_stack_depth" must not exceed 7680kB.`,`Increase the platform's stack depth limit via "ulimit -s" or local equivalent.`},
		{"max_stack_depth","2147483647","2147483647",`"max_stack_depth" must not exceed 7680kB.`,`Increase the platform's stack depth limit via "ulimit -s" or local equivalent.`},
		{"io_max_concurrency","0","0","Only -1 or values bigger than 0 are valid.",""},
	} { resourceCheckDiagnostic(t,r,tc.name,tc.input,tc.number,tc.detail,tc.hint) }
	for _,name:=range []string{"commit_timestamp_buffers","multixact_member_buffers","multixact_offset_buffers","notify_buffers","serializable_buffers","subtransaction_buffers","transaction_buffers"} { resourceCheckDiagnostic(t,r,name,"17","17",fmt.Sprintf(`"%s" must be a multiple of 16.`,name),"") }
}

func resourceCheckDiagnostic(t *testing.T,r Registry,name,input,number,detail,hint string) {
	t.Helper(); d,_:=r.Lookup(name); _,err:=d.Parse(input); var got *sqlstate.Error
	want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:fmt.Sprintf(`invalid value for parameter "%s": %s`,name,number),Detail:detail,Hint:hint}
	if !errors.As(err,&got) || *got!=want { t.Fatalf("%s %q got %#v want %#v",name,input,got,want) }
}
