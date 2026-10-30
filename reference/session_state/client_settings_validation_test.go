package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func clientInvalid(name, input, detail, hint string) fixture.Diagnostic {
	d := physicalDiagnostic("22023", fmt.Sprintf(`invalid value for parameter "%s": "%s"`, name, input))
	d.Detail, d.Hint = detail, hint
	return d
}

func clientEnums(name string) []string {
	switch name {
	case "bytea_output":
		return []string{"escape", "hex"}
	case "client_min_messages":
		return []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "notice", "warning", "error"}
	case "default_toast_compression":
		return []string{"pglz", "lz4"}
	case "default_transaction_isolation", "transaction_isolation":
		return []string{"serializable", "repeatable read", "read committed", "read uncommitted"}
	case "icu_validation_level":
		return []string{"disabled", "debug5", "debug4", "debug3", "debug2", "debug1", "log", "notice", "warning", "error"}
	case "IntervalStyle":
		return []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"}
	case "session_replication_role":
		return []string{"origin", "replica", "local"}
	case "xmlbinary":
		return []string{"base64", "hex"}
	case "xmloption":
		return []string{"content", "document"}
	}
	return nil
}

func TestReferenceClientSettingsValidation(t *testing.T) {
	for _, tc := range clientSettings {
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				s.clientConfiguration(tc)
				if clientContext(tc.name) == "postmaster" {
					s.run(physicalError("SET "+tc.name+"='broken'", physicalRestart(tc.name), false, false))
					return
				}
				current := strings.HasPrefix(tc.name, "transaction_") && tc.kind != "integer"
				for _, prepared := range []bool{false, true} {
					prior := clientInitial(tc, s.pg)
					set := func(input, want string) {
						if current {
							s.run(fixture.Exec("BEGIN", 0))
						}
						step := fixture.Exec("SET "+tc.name+"="+physicalLiteral(input), 0)
						if tc.name == "temp_tablespaces" {
							step = clientText("SELECT set_config("+physicalLiteral(tc.name)+","+physicalLiteral(input)+",false)", "set_config", want)
						}
						step.Prepared = prepared
						s.run(step)
						s.clientReads(tc, want, prepared)
						if current {
							s.run(fixture.Exec("ROLLBACK", 0))
						}
						if !current {
							prior = want
						}
					}
					bad := func(input string, d fixture.Diagnostic) {
						// LIST_QUOTE SQL literals denote identifiers, not raw list
						// syntax. Malformed raw lists are exercised by set_config.
						if tc.name != "search_path" && (tc.name != "temp_tablespaces" || input == "missing") {
							s.run(physicalError("SET "+tc.name+"="+physicalLiteral(input), d, prepared, false))
						}
						s.run(physicalError("SELECT set_config("+physicalLiteral(tc.name)+","+physicalLiteral(input)+",false)", d, prepared, true))
						s.clientReads(tc, prior, prepared)
					}
					switch tc.kind {
					case "bool":
						for _, v := range []struct{ input, want string }{{"TRUE", "on"}, {"t", "on"}, {"yes", "on"}, {"1", "on"}, {"FALSE", "off"}, {"f", "off"}, {"no", "off"}, {"0", "off"}} {
							set(v.input, v.want)
						}
						for _, input := range []string{"o", " true ", "broken"} {
							bad(input, physicalDiagnostic("22023", fmt.Sprintf(`parameter "%s" requires a Boolean value`, tc.name)))
						}
					case "enum":
						values := clientEnums(tc.name)
						for _, value := range values {
							set(strings.ToUpper(value), value)
						}
						if tc.name == "client_min_messages" || tc.name == "icu_validation_level" {
							set("info", "info")
							set("debug", "debug2")
						}
						for _, input := range []string{"broken", " " + values[0], values[0] + " ", "brok"} {
							bad(input, clientInvalid(tc.name, input, "", "Available values: "+strings.Join(values, ", ")+"."))
						}
					case "integer":
						// Keep accepted timeout probes far from expiry; actual
						// cancellation/termination belongs to the owning scenarios.
						if tc.name == "gin_fuzzy_search_limit" {
							set("0", "0")
							set("2147483647", "2147483647")
							set("2.5", "2")
							set("3.5", "4")
							bad("-1", physicalDiagnostic("22023", `-1 is outside the valid range for parameter "gin_fuzzy_search_limit" (0 .. 2147483647)`))
							bad("1kB", clientInvalid(tc.name, "1kB", "", ""))
						} else if tc.name == "gin_pending_list_limit" {
							for _, v := range []struct{ input, want string }{{"64", "64kB"}, {"2147483647", "2147483647kB"}, {"8MB", "8MB"}, {"64.5", "64kB"}, {"65.5", "66kB"}, {"65536B", "64kB"}} {
								set(v.input, v.want)
							}
							for _, input := range []string{"63", "-1"} {
								bad(input, physicalDiagnostic("22023", input+` kB is outside the valid range for parameter "gin_pending_list_limit" (64 kB .. 2147483647 kB)`))
							}
							bad("1mb", clientInvalid(tc.name, "1mb", "", `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`))
						} else if tc.name == "extra_float_digits" {
							set("-15", "-15")
							set("3", "3")
							bad("-16", physicalDiagnostic("22023", `-16 is outside the valid range for parameter "extra_float_digits" (-15 .. 3)`))
							bad("4", physicalDiagnostic("22023", `4 is outside the valid range for parameter "extra_float_digits" (-15 .. 3)`))
						} else {
							set("0", "0")
							set("2147483647", "2147483647ms")
							set("1h", "1h")
							set("0.5ms", "0")
							bad("-1", physicalDiagnostic("22023", fmt.Sprintf(`-1 ms is outside the valid range for parameter "%s" (0 ms .. 2147483647 ms)`, tc.name)))
							bad("1MS", clientInvalid(tc.name, "1MS", "", `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`))
						}
						bad("2147483648", clientInvalid(tc.name, "2147483648", "", "Value exceeds integer range."))
					case "string":
						s.clientStringValidation(tc, prepared, set, bad)
					}
					if !current {
						s.run(fixture.Exec("RESET "+tc.name, 0))
					}
				}
			})
		})
	}
	t.Run("released-fts-inventory", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			for _, name := range []string{"simple", "arabic", "armenian", "basque", "catalan", "danish", "dutch", "english", "estonian", "finnish", "french", "german", "greek", "hindi", "hungarian", "indonesian", "irish", "italian", "lithuanian", "nepali", "norwegian", "portuguese", "romanian", "russian", "serbian", "spanish", "swedish", "tamil", "turkish", "yiddish"} {
				if !s.pg && name != "simple" && name != "english" && name != "arabic" && name != "armenian" && name != "basque" && name != "danish" && name != "dutch" {
					s.run(physicalError("SET default_text_search_config="+physicalLiteral(name), clientInvalid("default_text_search_config", name, "", ""), false, false))
					continue
				}
				for _, input := range []string{strings.ToUpper(name), `pg_catalog."` + name + `"`} {
					s.run(clientText("SELECT set_config('default_text_search_config',"+physicalLiteral(input)+",false)", "set_config", "pg_catalog."+name))
				}
			}
			for _, input := range []string{"", "missing", "a..b", `"unclosed`, `pg_catalog."English"`, "missing.english"} {
				s.run(physicalError("SET default_text_search_config="+physicalLiteral(input), clientInvalid("default_text_search_config", input, "", ""), false, false))
			}
			for _, tc := range []struct{ input, code, message string }{{"a.b.c.d", "42601", "improper qualified name (too many dotted names): a.b.c.d"}, {"foreign_db.pg_catalog.english", "0A000", "cross-database references are not implemented: foreign_db.pg_catalog.english"}} {
				s.run(physicalError("SET default_text_search_config="+physicalLiteral(tc.input), physicalDiagnostic(tc.code, tc.message), false, false))
			}
		})
	})
	t.Run("catalog-name-truncation", clientSettingsNameTruncation)
}

func (s *physicalCase) clientStringValidation(tc clientSetting, prepared bool, set func(string, string), bad func(string, fixture.Diagnostic)) {
	switch tc.name {
	case "createrole_self_grant", "restrict_nonsystem_relation_kind":
		values := []string{"SET", "INHERIT"}
		if tc.name == "restrict_nonsystem_relation_kind" {
			values = []string{"VIEW", "FOREIGN-TABLE"}
		}
		for _, input := range []string{"", values[0], values[0] + ", " + values[1], "  " + values[0] + " ," + strings.ToLower(values[1]) + " ", values[0] + "," + values[0], `"` + values[0] + `"`} {
			set(input, input)
			step := clientText("SELECT set_config("+physicalLiteral(tc.name)+","+physicalLiteral(input)+",false)", "set_config", input)
			step.Prepared = prepared
			s.run(step)
		}
		bad("bogus", clientInvalid(tc.name, "bogus", `Unrecognized key word: "bogus".`, ""))
		bad(`"se""t"`, clientInvalid(tc.name, `"se""t"`, `Unrecognized key word: "se"t".`, ""))
		for _, input := range []string{",", values[0] + ",", `"unclosed`, values[0] + ",," + values[1]} {
			bad(input, clientInvalid(tc.name, input, "List syntax is invalid.", ""))
		}
		step := fixture.Exec("SET "+tc.name+" TO "+physicalLiteral(values[0])+","+physicalLiteral(values[1]), 0)
		step.Prepared = prepared
		s.run(step)
		s.clientReads(tc, strings.Join(values, ", "), prepared)
	case "default_table_access_method":
		bad("", clientInvalid(tc.name, "", `"default_table_access_method" cannot be empty.`, ""))
		bad("missing", clientInvalid(tc.name, "missing", `Table access method "missing" does not exist.`, ""))
		bad("btree", physicalDiagnostic("55000", `access method "btree" is not of type TABLE`))
	case "default_tablespace":
		set("", "")
		set("pg_global", "pg_global")
		bad("missing", clientInvalid(tc.name, "missing", `Tablespace "missing" does not exist.`, ""))
	case "temp_tablespaces":
		for _, input := range []string{"", `""`, "pg_default, pg_default", `"pg_default", ""`, "pg_global"} {
			set(input, input)
		}
		bad("missing", physicalDiagnostic("42704", `tablespace "missing" does not exist`))
		bad(`"unclosed`, clientInvalid(tc.name, `"unclosed`, "List syntax is invalid.", ""))
		bad(`"a""b"`, physicalDiagnostic("42704", `tablespace "a"b" does not exist`))
		step := fixture.Exec("SET temp_tablespaces TO 'pg_default',''", 0)
		step.Prepared = prepared
		s.run(step)
		s.clientReads(tc, `pg_default, ""`, prepared)
	case "local_preload_libraries", "session_preload_libraries", "dynamic_library_path", "extension_control_path":
		for _, input := range []string{"", "relative::missing", `missing,"unclosed`, "../../no_such_library"} {
			step := clientText("SELECT set_config("+physicalLiteral(tc.name)+","+physicalLiteral(input)+",false)", "set_config", input)
			step.Prepared = prepared
			s.run(step)
		}
		if tc.name == "local_preload_libraries" || tc.name == "session_preload_libraries" {
			step := fixture.Exec("SET "+tc.name+` TO 'missing,a','b"c'`, 0)
			step.Prepared = prepared
			s.run(step)
			s.clientReads(tc, `"missing,a", "b""c"`, prepared)
		}
	case "client_encoding":
		bad("missing", clientInvalid(tc.name, "missing", "", ""))
	case "lc_messages", "lc_monetary", "lc_numeric", "lc_time":
		detail := "Only C and POSIX locales are supported by stuntgres."
		if s.pg {
			detail = ""
		}
		bad("missing_locale", clientInvalid(tc.name, "missing_locale", detail, ""))
	case "TimeZone":
		bad("No/Such_Zone", clientInvalid(tc.name, "No/Such_Zone", "", ""))
	case "timezone_abbreviations":
		bad("../Default", clientInvalid(tc.name, "../Default", "", ""))
	case "DateStyle":
		bad("broken", clientInvalid(tc.name, "broken", "Unrecognized key word: \"broken\".", ""))
	case "search_path":
		bad(`"unclosed`, clientInvalid(tc.name, `"unclosed`, "List syntax is invalid.", ""))
	case "default_text_search_config":
		bad("missing", clientInvalid(tc.name, "missing", "", ""))
	}
}
