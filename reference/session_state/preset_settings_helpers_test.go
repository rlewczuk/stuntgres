package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type presetDefinition struct {
	name, kind, unit, current, shown, boot, min, max, description, extra string
}

var presetDefinitions = []presetDefinition{
	{"block_size", "integer", "", "8192", "8192", "8192", "8192", "8192", "Shows the size of a disk block.", ""},
	{"data_checksums", "bool", "", "off", "off", "off", "", "", "Shows whether data checksums are turned on for this cluster.", ""},
	{"data_directory_mode", "integer", "", "0700", "0700", "448", "0", "511", "Shows the mode of the data directory.", "The parameter value is a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)"},
	{"debug_assertions", "bool", "", "off", "off", "off", "", "", "Shows whether the running server has assertion checks enabled.", ""},
	{"huge_pages_status", "enum", "", "off", "off", "unknown", "", "", "Indicates the status of huge pages.", ""},
	{"in_hot_standby", "bool", "", "off", "off", "off", "", "", "Shows whether hot standby is currently active.", ""},
	{"integer_datetimes", "bool", "", "on", "on", "on", "", "", "Shows whether datetimes are integer based.", ""},
	{"max_function_args", "integer", "", "100", "100", "100", "100", "100", "Shows the maximum number of function arguments.", ""},
	{"max_identifier_length", "integer", "", "63", "63", "63", "63", "63", "Shows the maximum identifier length.", ""},
	{"max_index_keys", "integer", "", "32", "32", "32", "32", "32", "Shows the maximum number of index keys.", ""},
	{"num_os_semaphores", "integer", "", "0", "0", "0", "0", "2147483647", "Shows the number of semaphores required for the server.", ""},
	{"segment_size", "integer", "8kB", "131072", "1GB", "131072", "131072", "131072", "Shows the number of pages per disk file.", ""},
	{"server_encoding", "string", "", "UTF8", "UTF8", "SQL_ASCII", "", "", "Shows the server (database) character set encoding.", ""},
	{"server_version", "string", "", "18.0", "18.0", "18.0", "", "", "Shows the server version.", ""},
	{"server_version_num", "integer", "", "180000", "180000", "180000", "180000", "180000", "Shows the server version as an integer.", ""},
	{"shared_memory_size", "integer", "MB", "0", "0", "0", "0", "2147483647", "Shows the size of the server's main shared memory area (rounded up to the nearest MB).", ""},
	{"shared_memory_size_in_huge_pages", "integer", "", "-1", "-1", "-1", "-1", "2147483647", "Shows the number of huge pages needed for the main shared memory area.", "-1 means huge pages are not supported."},
	{"ssl_library", "string", "", "", "", "", "", "", "Shows the name of the SSL library.", ""},
	{"wal_block_size", "integer", "", "8192", "8192", "8192", "8192", "8192", "Shows the block size in the write ahead log.", ""},
	{"wal_segment_size", "integer", "B", "16777216", "16MB", "16777216", "1048576", "1073741824", "Shows the size of write ahead log segments.", ""},
}

var presetPredicate = func() string {
	names := make([]string, len(presetDefinitions))
	for i, d := range presetDefinitions { names[i] = physicalLiteral(d.name) }
	return "name IN (" + strings.Join(names, ",") + ")"
}()

func presetDenied(name string) fixture.Diagnostic {
	return physicalDiagnostic("55P02", fmt.Sprintf("parameter %q cannot be changed", name))
}

func presetNullable(value string) any {
	if value == "" { return nil }; return value
}

// Runtime counts are independently range checked, not learned native goldens.
func (s *physicalCase) presetInventory(prepared bool) map[string]string {
	s.t.Helper()
	query := "SELECT " + physicalColumnsSQL + " FROM pg_settings WHERE " + presetPredicate + " ORDER BY name"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else { rows, err = s.conn.QueryContext(s.p.Ctx, query) }
	if err != nil { s.t.Fatal(err) }; defer rows.Close()
	statisticsSettingsCheckMetadata(s.t, rows, s.pg, physicalColumns, physicalTypes)
	shown := map[string]string{}
	for i := 0; rows.Next(); i++ {
		if i >= len(presetDefinitions) { s.t.Fatal("extra preset row") }
		d := presetDefinitions[i]
		cells, dest := make([]any, 17), make([]any, 17)
		for j := range cells { dest[j] = &cells[j] }
		if err := rows.Scan(dest...); err != nil { s.t.Fatal(err) }
		for j := range cells { cells[j] = fixture.Normalize(cells[j]) }
		current, display, boot, min, max := d.current, d.shown, d.boot, d.min, d.max
		if s.pg {
			switch d.name {
			case "data_checksums": current, display = "on", "on"
			case "ssl_library": current, display, boot = "OpenSSL", "OpenSSL", "OpenSSL"
			case "server_version":
				v, ok := cells[1].(string)
				if !ok || !(v == "18.6" || strings.HasPrefix(v, "18.6 (")) { s.t.Fatalf("pinned version: %v", cells) }
				current, display, boot = v, v, v
			case "server_version_num": current, display, boot, min, max = "180006", "180006", "180006", "180006", "180006"
			case "num_os_semaphores", "shared_memory_size", "shared_memory_size_in_huge_pages":
				v, ok := cells[1].(string); n, err := strconv.ParseInt(v, 10, 32)
				lower := int64(0); if d.name == "shared_memory_size" { lower = 1 }; if d.name == "shared_memory_size_in_huge_pages" { lower = -1 }
				if !ok || err != nil || n < lower { s.t.Fatalf("runtime count: %v", cells) }
				current, display = v, v
				if d.name == "shared_memory_size" {
					display = v + "MB"
					if n%1048576 == 0 { display = strconv.FormatInt(n/1048576,10)+"TB" } else if n%1024 == 0 { display = strconv.FormatInt(n/1024,10)+"GB" }
				}
			}
		}
		reset := current; if d.name == "data_directory_mode" { reset = "448" }
		var enums any; if d.name == "huge_pages_status" { enums = "{off,on,unknown}" }
		want := []any{d.name,current,presetNullable(d.unit),"Preset Options",d.description,presetNullable(d.extra),"internal",d.kind,"default",presetNullable(min),presetNullable(max),enums,boot,reset,nil,nil,false}
		if !reflect.DeepEqual(cells,want) { s.t.Fatalf("%s metadata got %#v want %#v",d.name,cells,want) }
		shown[d.name] = display
	}
	if err := rows.Err(); err != nil || len(shown) != 20 { s.t.Fatalf("preset count=%d: %v",len(shown),err) }
	return shown
}

func (s *physicalCase) presetReads(values map[string]string, prepared bool) {
	s.t.Helper()
	for _, d := range presetDefinitions {
		for _, q := range []string{"SHOW " + d.name, "SELECT current_setting(" + physicalLiteral(strings.ToUpper(d.name)) + ")"} {
			column := "current_setting"; if strings.HasPrefix(q,"SHOW ") { column = d.name }
			step := fixture.Select(q,[]string{column},[]string{"TEXT"},[][]any{{values[d.name]}})
			step.Prepared = prepared
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t,rows,s.pg,column == "current_setting") }
			s.run(step)
			count := int64(1); if column != "current_setting" { count = 0 }
			exec := fixture.Exec(q,count); exec.Prepared = prepared; s.run(exec)
		}
	}
	var rows *sql.Rows
	var err error
	if prepared {
		stmt,e := s.conn.PrepareContext(s.p.Ctx,"SHOW ALL"); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
		rows,err = stmt.QueryContext(s.p.Ctx)
	} else { rows,err = s.conn.QueryContext(s.p.Ctx,"SHOW ALL") }
	if err != nil { s.t.Fatal(err) }; defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name,value,description string
		if err := rows.Scan(&name,&value,&description); err != nil { s.t.Fatal(err) }
		for _, d := range presetDefinitions {
			if name == d.name {
				if seen[name] || value != values[name] || description != d.description { s.t.Fatal(name,value,description) }
				seen[name] = true
			}
		}
	}
	if err := rows.Err(); err != nil || len(seen) != 20 { s.t.Fatal(seen,err) }
}

func (s *physicalCase) presetUnchanged(values map[string]string, application string) {
	s.t.Helper()
	if got := s.presetInventory(false); !reflect.DeepEqual(got,values) { s.t.Fatal("preset changed",got,values) }
	s.run(fixture.Select("SELECT id,value FROM preset_sentinel ORDER BY id",[]string{"id","value"},[]string{"INT4","TEXT"},[][]any{{int64(1),"kept"},{int64(2),nil}}))
	s.run(fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{application}}))
}
