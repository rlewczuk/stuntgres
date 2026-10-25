package reference_test

import (
	"database/sql"
	"fmt"
	"path"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Release expectations, deliberately independent from engine definitions.
// All 46 literal selected names have an initialized read and mutation witness.
type clientSetting struct {
	name, kind, boot, initial, alternate, changed, description string
}

var clientSettings = []clientSetting{
	{"bytea_output", "enum", "hex", "hex", "escape", "escape", "Sets the output format for bytea."},
	{"check_function_bodies", "bool", "on", "on", "off", "off", "Check routine bodies during CREATE FUNCTION and CREATE PROCEDURE."},
	{"client_encoding", "string", "SQL_ASCII", "UTF8", "utf-8", "UTF8", "Sets the client's character set encoding."},
	{"client_min_messages", "enum", "notice", "notice", "warning", "warning", "Sets the message levels that are sent to the client."},
	{"createrole_self_grant", "string", "", "", "SET, INHERIT", "SET, INHERIT", "Sets whether a CREATEROLE user automatically grants the role to themselves, and with which options."},
	{"DateStyle", "string", "ISO, MDY", "ISO, MDY", "SQL, DMY", "SQL, DMY", "Sets the display format for date and time values."},
	{"default_table_access_method", "string", "heap", "heap", "heap", "heap", "Sets the default table access method for new tables."},
	{"default_tablespace", "string", "", "", "pg_default", "pg_default", "Sets the default tablespace to create tables and indexes in."},
	{"default_text_search_config", "string", "pg_catalog.simple", "pg_catalog.simple", "english", "pg_catalog.english", "Sets default text search configuration."},
	{"default_toast_compression", "enum", "pglz", "pglz", "lz4", "lz4", "Sets the default compression method for compressible values."},
	{"default_transaction_deferrable", "bool", "off", "off", "on", "on", "Sets the default deferrable status of new transactions."},
	{"default_transaction_isolation", "enum", "read committed", "read committed", "repeatable read", "repeatable read", "Sets the transaction isolation level of each new transaction."},
	{"default_transaction_read_only", "bool", "off", "off", "on", "on", "Sets the default read-only status of new transactions."},
	{"dynamic_library_path", "string", "$libdir", "$libdir", "relative::missing", "relative::missing", "Sets the path for dynamically loadable modules."},
	{"event_triggers", "bool", "on", "on", "off", "off", "Enables event triggers."},
	{"extension_control_path", "string", "$system", "$system", "", "", "Sets the path for extension control files."},
	{"extra_float_digits", "integer", "1", "1", "-1", "-1", "Sets the number of digits displayed for floating-point values."},
	{"gin_fuzzy_search_limit", "integer", "0", "0", "2147483647", "2147483647", "Sets the maximum allowed result for exact search by GIN."},
	{"gin_pending_list_limit", "integer", "4096", "4MB", "8MB", "8MB", "Sets the maximum size of the pending list for GIN index."},
	{"icu_validation_level", "enum", "warning", "warning", "notice", "notice", "Log level for reporting invalid ICU locale strings."},
	{"idle_in_transaction_session_timeout", "integer", "0", "0", "0", "0", "Sets the maximum allowed idle time between queries, when in a transaction."},
	{"idle_session_timeout", "integer", "0", "0", "0", "0", "Sets the maximum allowed idle time between queries, when not in a transaction."},
	{"IntervalStyle", "enum", "postgres", "postgres", "iso_8601", "iso_8601", "Sets the display format for interval values."},
	{"jit_provider", "string", "llvmjit", "llvmjit", "missing", "llvmjit", "JIT provider to use."},
	{"lc_messages", "string", "", "C", "POSIX", "POSIX", "Sets the language in which messages are displayed."},
	{"lc_monetary", "string", "C", "C", "POSIX", "POSIX", "Sets the locale for formatting monetary amounts."},
	{"lc_numeric", "string", "C", "C", "POSIX", "POSIX", "Sets the locale for formatting numbers."},
	{"lc_time", "string", "C", "C", "POSIX", "POSIX", "Sets the locale for formatting date and time values."},
	{"local_preload_libraries", "string", "", "", "missing_client_library", "missing_client_library", "Lists unprivileged shared libraries to preload into each backend."},
	{"lock_timeout", "integer", "0", "0", "0", "0", "Sets the maximum allowed duration of any wait for a lock."},
	{"restrict_nonsystem_relation_kind", "string", "", "", "view, foreign-table", "view, foreign-table", "Prohibits access to non-system relations of specified kinds."},
	{"row_security", "bool", "on", "on", "off", "off", "Enables row security."},
	{"search_path", "string", `"$user", public`, `"$user", public`, "public", "public", "Sets the schema search order for names that are not schema-qualified."},
	{"session_preload_libraries", "string", "", "", "missing_client_library", "missing_client_library", "Lists shared libraries to preload into each backend."},
	{"session_replication_role", "enum", "origin", "origin", "replica", "replica", "Sets the session's behavior for triggers and rewrite rules."},
	{"shared_preload_libraries", "string", "", "", "missing", "", "Lists shared libraries to preload into server."},
	{"statement_timeout", "integer", "0", "0", "0", "0", "Sets the maximum allowed duration of any statement."},
	{"temp_tablespaces", "string", "", "", "pg_default", "pg_default", "Sets the tablespace(s) to use for temporary tables and sort files."},
	{"TimeZone", "string", "GMT", "UTC", "UTC", "UTC", "Sets the time zone for displaying and interpreting time stamps."},
	{"timezone_abbreviations", "string", "", "Default", "Australia", "Australia", "Selects a file of time zone abbreviations."},
	{"transaction_deferrable", "bool", "off", "off", "on", "on", "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."},
	{"transaction_isolation", "enum", "read committed", "read committed", "repeatable read", "repeatable read", "Sets the current transaction's isolation level."},
	{"transaction_read_only", "bool", "off", "off", "on", "on", "Sets the current transaction's read-only status."},
	{"transaction_timeout", "integer", "0", "0", "0", "0", "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
	{"xmlbinary", "enum", "base64", "base64", "hex", "hex", "Sets how binary values are to be encoded in XML."},
	{"xmloption", "enum", "content", "content", "document", "document", "Sets whether XML data in implicit parsing and serialization operations is to be considered as documents or content fragments."},
}

func clientContext(name string) string {
	switch name {
	case "jit_provider", "shared_preload_libraries":
		return "postmaster"
	case "dynamic_library_path", "event_triggers", "extension_control_path", "lc_messages", "session_preload_libraries", "session_replication_role":
		return "superuser"
	}
	return "user"
}

func clientRestricted(name string) bool {
	switch name {
	case "dynamic_library_path", "extension_control_path", "jit_provider", "session_preload_libraries", "shared_preload_libraries":
		return true
	}
	return false
}

func clientInitial(tc clientSetting, pg bool) string {
	if pg {
		switch tc.name {
		case "TimeZone":
			return "Etc/UTC"
		case "default_text_search_config":
			return "pg_catalog.english"
		}
	}
	return tc.initial
}

func clientText(query, column, value string) fixture.Step {
	return fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{value}})
}

func (s *physicalCase) clientReads(tc clientSetting, value string, prepared bool) {
	for _, query := range []string{"SHOW " + tc.name, "SELECT current_setting(" + physicalLiteral(tc.name) + ")"} {
		column := tc.name
		if strings.HasPrefix(query, "SELECT") {
			column = "current_setting"
		}
		step := clientText(query, column, value)
		step.Prepared = prepared
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, s.pg, column == "current_setting") }
		s.run(step)
	}
}

func TestReferenceClientSettingsInventory(t *testing.T) {
	for _, tc := range clientSettings {
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				initial := clientInitial(tc, s.pg)
				for _, prepared := range []bool{false, true} {
					s.clientReads(tc, initial, prepared)
					s.clientMetadata(tc, prepared)
					query := "SET " + tc.name + " TO " + physicalLiteral(tc.alternate)
					if clientContext(tc.name) == "postmaster" {
						s.run(physicalError(query, physicalRestart(tc.name), prepared, false))
						continue
					}
					step := fixture.Exec(query, 0)
					step.Prepared = prepared
					if strings.HasPrefix(tc.name, "transaction_") && tc.kind != "integer" {
						// Current characteristics only persist inside an explicit block.
						s.run(fixture.Exec("BEGIN", 0))
						s.run(step)
						s.clientReads(tc, tc.changed, prepared)
						s.run(fixture.Exec("ROLLBACK", 0))
						s.run(physicalError("RESET "+tc.name, physicalDiagnostic("0A000", fmt.Sprintf("parameter %q cannot be reset", tc.name)), prepared, false))
						continue
					}
					s.run(step)
					s.clientSessionMetadata(tc, prepared)
					s.clientReads(tc, tc.changed, prepared)
					step = clientText("SELECT set_config("+physicalLiteral(tc.name)+","+physicalLiteral(tc.alternate)+",false)", "set_config", tc.changed)
					step.Prepared = prepared
					s.run(step)
					for _, reset := range []string{"SET " + tc.name + " TO DEFAULT", "RESET " + tc.name} {
						step := fixture.Exec(reset, 0)
						step.Prepared = prepared
						s.run(step)
						s.clientReads(tc, initial, prepared)
						s.clientMetadata(tc, prepared)
					}
				}
			})
		})
	}
}

func (s *physicalCase) clientMetadata(tc clientSetting, prepared bool) {
	query := "SELECT " + physicalColumnsSQL + " FROM pg_settings WHERE name=" + physicalLiteral(tc.name)
	// Check rows directly to independently constrain installation file provenance.
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query)
		if e != nil {
			s.t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, query)
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	statisticsSettingsCheckMetadata(s.t, rows, s.pg, physicalColumns, physicalTypes)
	if !rows.Next() {
		s.t.Fatal("missing selected parameter", tc.name)
	}
	got, dest := make([]any, 17), make([]any, 17)
	for i := range got {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		s.t.Fatal(err)
	}
	for i := range got {
		got[i] = fixture.Normalize(got[i])
	}
	want := clientMetadataRow(tc, s.pg)
	if s.pg && want[8] == "configuration file" {
		file, ok := got[14].(string)
		line, lineOK := got[15].(int64)
		if !ok || !path.IsAbs(file) || path.Base(file) != "postgresql.conf" || !lineOK || line <= 0 {
			s.t.Fatalf("configuration provenance: %#v", got)
		}
		want[14], want[15] = file, line
	}
	if !reflect.DeepEqual(got, want) {
		s.t.Fatalf("%s metadata got %#v want %#v", tc.name, got, want)
	}
	if rows.Next() || rows.Err() != nil {
		s.t.Fatal("selected cardinality", rows.Err())
	}
}

func clientMetadataRow(tc clientSetting, pg bool) []any {
	category := "Client Connection Defaults / Statement Behavior"
	switch tc.name {
	case "client_encoding", "DateStyle", "IntervalStyle", "TimeZone", "timezone_abbreviations", "lc_messages", "lc_monetary", "lc_numeric", "lc_time", "extra_float_digits", "icu_validation_level", "default_text_search_config":
		category = "Client Connection Defaults / Locale and Formatting"
	case "local_preload_libraries", "session_preload_libraries", "shared_preload_libraries", "jit_provider":
		category = "Client Connection Defaults / Shared Library Preloading"
	case "dynamic_library_path", "extension_control_path", "gin_fuzzy_search_limit":
		category = "Client Connection Defaults / Other Defaults"
	}
	var extra, unit, min, max, enums, boot any
	boot = tc.boot
	if tc.name == "timezone_abbreviations" {
		boot = nil
	}
	switch tc.name {
	case "event_triggers":
		extra = "When enabled, event triggers will fire for all applicable statements."
	case "gin_fuzzy_search_limit":
		extra = "0 means no limit."
	case "default_tablespace", "temp_tablespaces":
		extra = "An empty string means use the database's default tablespace."
	case "createrole_self_grant":
		extra = "An empty string disables automatic self grants."
	case "dynamic_library_path":
		extra = "If a dynamically loadable module needs to be opened and the specified name does not have a directory component (i.e., the name does not contain a slash), the system will search this path for the specified file."
	case "extension_control_path":
		extra = "The remaining extension script and secondary control files are then loaded from the same directory where the primary control file was found."
	case "row_security":
		extra = "When enabled, row security will be applied to all users."
	case "DateStyle":
		extra = "Also controls interpretation of ambiguous date inputs."
	case "lc_messages", "lc_monetary", "lc_numeric", "lc_time":
		extra = "An empty string means use the operating system setting."
	case "client_min_messages":
		extra = "Each level includes all the levels that follow it. The later the level, the fewer messages are sent."
	case "extra_float_digits":
		extra = "This affects real, double precision, and geometric data types. A zero or negative parameter value is added to the standard number of digits (FLT_DIG or DBL_DIG as appropriate). Any value greater than zero selects precise output mode."
	case "statement_timeout", "lock_timeout", "transaction_timeout", "idle_session_timeout", "idle_in_transaction_session_timeout":
		extra = "0 disables the timeout."
	}
	if tc.kind == "integer" {
		min, max = "0", "2147483647"
		switch tc.name {
		case "extra_float_digits":
			min, max = "-15", "3"
		case "gin_pending_list_limit":
			min, unit = "64", "kB"
		case "gin_fuzzy_search_limit":
		default:
			unit = "ms"
		}
	}
	switch tc.name {
	case "bytea_output":
		enums = "{escape,hex}"
	case "default_toast_compression":
		enums = "{pglz,lz4}"
	case "default_transaction_isolation", "transaction_isolation":
		enums = `{serializable,"repeatable read","read committed","read uncommitted"}`
	case "IntervalStyle":
		enums = "{postgres,postgres_verbose,sql_standard,iso_8601}"
	case "client_min_messages":
		enums = "{debug5,debug4,debug3,debug2,debug1,log,notice,warning,error}"
	case "icu_validation_level":
		enums = "{disabled,debug5,debug4,debug3,debug2,debug1,log,notice,warning,error}"
	case "session_replication_role":
		enums = "{origin,replica,local}"
	case "xmlbinary":
		enums = "{base64,hex}"
	case "xmloption":
		enums = "{content,document}"
	}
	initial := clientInitial(tc, pg)
	raw := initial
	if tc.name == "gin_pending_list_limit" {
		raw = "4096"
	}
	source := "default"
	switch tc.name {
	case "client_encoding", "lc_messages", "TimeZone", "timezone_abbreviations":
		source = "override"
	}
	if pg {
		switch tc.name {
		case "client_encoding", "DateStyle":
			source = "client"
		case "TimeZone", "lc_messages", "lc_monetary", "lc_numeric", "lc_time", "default_text_search_config":
			source = "configuration file"
		case "timezone_abbreviations":
			source = "default"
		case "transaction_isolation", "transaction_read_only", "transaction_deferrable":
			source = "override"
		}
	}
	return []any{tc.name, raw, unit, category, tc.description, extra, clientContext(tc.name), tc.kind, source, min, max, enums, boot, raw, nil, nil, false}
}

func (s *physicalCase) clientSessionMetadata(tc clientSetting, prepared bool) {
	want := clientMetadataRow(tc, s.pg)
	want[1], want[8], want[14], want[15] = tc.changed, "session", nil, nil
	if tc.name == "gin_pending_list_limit" {
		want[1] = "8192"
	}
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name="+physicalLiteral(tc.name), physicalColumns, physicalTypes, [][]any{want})
	step.Prepared = prepared
	s.run(step)
}
