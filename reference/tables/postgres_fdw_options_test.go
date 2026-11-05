package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type postgresFDWOption struct {
	name, value string
	classes     []int
}

func postgresFDWOptions() []postgresFDWOption {
	var options []postgresFDWOption
	add := func(names, value string, classes ...int) {
		for _, name := range strings.Fields(names) {
			options = append(options, postgresFDWOption{name, value, classes})
		}
	}
	add("schema_name table_name", "", 3118)
	add("column_name", "", 1249)
	add("use_remote_estimate updatable truncatable async_capable", "true", 1417, 3118)
	add("keep_connections parallel_commit parallel_abort", "false", 1417)
	add("use_scram_passthrough", "false", 1417, 1418)
	add("password_required", "true", 1418)
	add("fetch_size batch_size", "1", 1417, 3118)
	add("fdw_startup_cost fdw_tuple_cost", "0", 1417)
	add("extensions", "", 1417)
	add("analyze_sampling", "auto", 1417, 3118)
	add("user password sslpassword", "value", 1418)
	add("sslcert sslkey gssdelegation", "value", 1417, 1418)
	add("service passfile channel_binding connect_timeout dbname host hostaddr port options application_name keepalives keepalives_idle keepalives_interval keepalives_count tcp_user_timeout sslmode sslnegotiation sslcompression sslcertmode sslrootcert sslcrl sslcrldir sslsni requirepeer require_auth min_protocol_version max_protocol_version ssl_min_protocol_version ssl_max_protocol_version gssencmode krbsrvname gsslib target_session_attrs load_balance_hosts", "value", 1417)
	return options
}

func postgresFDWOptionDDL(class int, name, value string) (string, string) {
	option := `"` + strings.ReplaceAll(name, `"`, `""`) + `" ` + fileFDWLiteral(value)
	switch class {
	case 1417:
		return "ALTER SERVER pf_server OPTIONS(ADD " + option + ")", "ALTER SERVER pf_server OPTIONS(DROP \"" + name + "\")"
	case 1418:
		return "CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(" + option + ")", "DROP USER MAPPING FOR CURRENT_USER SERVER pf_server"
	case 3118:
		return "CREATE FOREIGN TABLE pf_table(id integer) SERVER pf_server OPTIONS(" + option + ")", "DROP FOREIGN TABLE pf_table"
	case 1249:
		return "CREATE FOREIGN TABLE pf_table(id integer OPTIONS(" + option + ")) SERVER pf_server", "DROP FOREIGN TABLE pf_table"
	default:
		panic("unknown context")
	}
}

func TestReferencePostgresFDWOptionPlacement(t *testing.T) {
	for _, option := range postgresFDWOptions() {
		t.Run(option.name, func(t *testing.T) {
			postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				postgresFDWInstall(run)
				for _, class := range option.classes {
					t.Run(postgresFDWClassName(class), func(t *testing.T) {
						run(fileFDWVoid(postgresFDWCall([]string{option.name + "=" + option.value}, class)))
						create, drop := postgresFDWOptionDDL(class, option.name, option.value)
						run(fixture.Exec(create, 0))
						postgresFDWStored(run, class, option.name+"="+option.value)
						run(fixture.Exec(drop, 0))
					})
				}
				// Wrapper has no valid options: deterministic full no-options hint
				// independently covers a wrong placement for EVERY admitted name.
				run(postgresFDWInvalid(postgresFDWCall([]string{option.name + "=" + option.value}, 2328), option.name, "There are no valid options in this context."))
				wrongClass := 1249
				if option.name == "column_name" {
					wrongClass = 1417
				}
				run(postgresFDWInvalid(postgresFDWCall([]string{option.name + "=" + option.value}, wrongClass), option.name, ""))
			})
		})
	}
	for _, name := range strings.Fields("client_encoding fallback_application_name replication scram_client_key scram_server_key sslkeylogfile oauth_issuer oauth_client_id oauth_client_secret oauth_scope oauth_future import_collate import_default import_generated import_not_null") {
		t.Run("excluded_"+name, func(t *testing.T) {
			postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				postgresFDWInstall(run)
				run(postgresFDWInvalid(postgresFDWCall([]string{name + "=value"}, 2328), name, "There are no valid options in this context."))
				// None of the released accepted names is within the stock
				// spelling-distance threshold for these excluded keywords.
				for _, class := range []int{1417, 1418, 3118, 1249} {
					run(postgresFDWInvalid(postgresFDWCall([]string{name + "=value"}, class), name, ""))
				}
			})
		})
	}
	t.Run("ddl_atomicity", func(t *testing.T) {
		postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
			postgresFDWInstall(run)
			run(fixture.Exec("CREATE TABLE pf_local(v integer)", 0))
			run(fixture.Exec("INSERT INTO pf_local VALUES(73)", 1))
			run(fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD fetch_size '010',ADD batch_size '2')", 0))
			run(fixture.Exec("ALTER SERVER pf_server OPTIONS(SET fetch_size '1.5',DROP batch_size,ADD keep_connections 'ON')", 0))
			for _, tc := range []struct{ clause, code, message string }{
				{"ADD fetch_size '3'", "42710", `option "fetch_size" provided more than once`},
				{"SET batch_size '3'", "42704", `option "batch_size" not found`},
				{"DROP batch_size", "42704", `option "batch_size" not found`},
				{`ADD "a=b" '3'`, "22023", `invalid option name "a=b": must not contain "="`},
				{"SET fetch_size '0'", "22023", `"fetch_size" must be an integer value greater than zero`},
			} {
				run(postgresFDWError("ALTER SERVER pf_server OPTIONS(ADD application_name 'not-published',"+tc.clause+")", tc.code, tc.message))
				postgresFDWStored(run, 1417, "fetch_size=1.5", "keep_connections=ON")
				run(fixture.Select("SELECT v FROM pf_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
			}
			for _, tc := range []struct {
				name  string
				class int
				hint  string
			}{
				{"column_nam", 1249, `Perhaps you meant the option "column_name".`},
				{"fetch_siz", 1417, `Perhaps you meant the option "fetch_size".`},
				{"unrelated_option_zzzzzz", 1417, ""},
				{"FETCH_SIZE", 1249, ""},
				{"x", 0, "There are no valid options in this context."},
			} {
				run(postgresFDWInvalid(postgresFDWCall([]string{tc.name + "=x"}, tc.class), tc.name, tc.hint))
			}
			run(postgresFDWInvalid("CREATE SERVER pf_rejected FOREIGN DATA WRAPPER postgres_fdw OPTIONS(unrelated_option_zzzzzz 'x')", "unrelated_option_zzzzzz", ""))
			run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='pf_rejected'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("CREATE FOREIGN TABLE pf_table(id integer OPTIONS(column_name 'Remote ID')) SERVER pf_server OPTIONS(table_name 'source')", 0))
			run(postgresFDWInvalid("ALTER FOREIGN TABLE pf_table ADD COLUMN earlier integer,ALTER COLUMN id OPTIONS(ADD unrelated_option_zzzzzz 'x')", "unrelated_option_zzzzzz", ""))
			run(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='pf_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, [][]any{{"id", int64(1), `{"column_name=Remote ID"}`}}))
			postgresFDWStored(run, 3118, "table_name=source")
			run(postgresFDWError("CREATE FOREIGN TABLE pf_rejected(id integer OPTIONS(column_name 'x'),label text OPTIONS(column_nam 'x')) SERVER pf_server OPTIONS(fetch_size '0')", "22023", `"fetch_size" must be an integer value greater than zero`))
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='pf_rejected'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}

func TestReferencePostgresFDWOptionValues(t *testing.T) {
	for _, option := range postgresFDWOptions() {
		if !strings.Contains(" use_remote_estimate updatable truncatable async_capable keep_connections parallel_commit parallel_abort use_scram_passthrough password_required ", " "+option.name+" ") {
			continue
		}
		t.Run(option.name, func(t *testing.T) {
			postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				postgresFDWInstall(run)
				for _, class := range option.classes {
					for _, value := range []string{"true", "FALSE", "On", "oFf"} {
						run(fileFDWVoid(postgresFDWCall([]string{option.name + "=" + value}, class)))
					}
					for _, value := range []string{"yes", "no", "t", "f", "1", "0", "tr", " true", "true ", ""} {
						run(postgresFDWError(postgresFDWCall([]string{option.name + "=" + value}, class), "42601", option.name+" requires a Boolean value"))
					}
				}
			})
		})
	}
	for _, name := range []string{"fetch_size", "batch_size", "fdw_startup_cost", "fdw_tuple_cost", "analyze_sampling"} {
		t.Run(name, func(t *testing.T) {
			postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				postgresFDWInstall(run)
				good := []string{"1", "0x10", "010", "1.5", "2.5", "1e2", " 2 ", "2147483647"}
				bad := []struct{ value, message string }{}
				if strings.HasPrefix(name, "fdw_") {
					good = []string{"0", "-0", "1.5", "+Infinity"}
					bad = append(bad, struct{ value, message string }{"-1", `"` + name + `" must be a floating point value greater than or equal to zero`})
					for _, value := range []string{"NaN", "1e999", "x", "1ms", ""} {
						bad = append(bad, struct{ value, message string }{value, `invalid value for floating point option "` + name + `": ` + value})
					}
				} else if name == "analyze_sampling" {
					good = []string{"off", "auto", "random", "system", "bernoulli"}
					for _, value := range []string{"AUTO", "on", "", "random "} {
						bad = append(bad, struct{ value, message string }{value, `invalid value for string option "analyze_sampling": ` + value})
					}
				} else {
					for _, value := range []string{"0", "-1", "0.5"} {
						bad = append(bad, struct{ value, message string }{value, `"` + name + `" must be an integer value greater than zero`})
					}
					for _, value := range []string{"08", "1ms", "2147483648", "x", ""} {
						bad = append(bad, struct{ value, message string }{value, `invalid value for integer option "` + name + `": ` + value})
					}
				}
				for _, value := range good {
					run(fileFDWVoid(postgresFDWCall([]string{name + "=" + value}, 1417)))
					run(fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD "+name+" "+fileFDWLiteral(value)+")", 0))
					postgresFDWStored(run, 1417, name+"="+value)
					run(fixture.Exec("ALTER SERVER pf_server OPTIONS(DROP "+name+")", 0))
				}
				for _, tc := range bad {
					run(postgresFDWError(postgresFDWCall([]string{name + "=" + tc.value}, 1417), "22023", tc.message))
					run(postgresFDWError("ALTER SERVER pf_server OPTIONS(ADD "+name+" "+fileFDWLiteral(tc.value)+")", "22023", tc.message))
					postgresFDWStored(run, 1417)
				}
			})
		})
	}
}
