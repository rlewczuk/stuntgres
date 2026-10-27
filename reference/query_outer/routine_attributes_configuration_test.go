package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAttributesConfigurationSaveRestore(t *testing.T) {
	for _, lang := range []string{"sql", "plpgsql"} {
		for _, kind := range []string{"FUNCTION", "PROCEDURE"} {
			t.Run(lang+"_"+kind, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "SET application_name='outside'")
					for _, action := range []string{"observe", "local", "escape", "config"} {
						prefix := ""
						if action == "local" {
							prefix = "SET LOCAL application_name='local';"
						}
						if action == "escape" {
							prefix = "SET application_name='escape';"
						}
						if action == "config" {
							prefix = "SELECT set_config('application_name','config',false);"
						}
						body := prefix + "SELECT current_setting('application_name')"
						signature, returns := "()", " RETURNS text"
						if kind == "PROCEDURE" {
							signature, returns = "(OUT answer text)", ""
						}
						if lang == "plpgsql" {
							if action == "config" {
								prefix = "PERFORM set_config('application_name','config',false);"
							}
							body = "BEGIN " + prefix + "RETURN current_setting('application_name');END"
							if kind == "PROCEDURE" {
								body = "BEGIN " + prefix + "answer:=current_setting('application_name');END"
							}
						}
						resultsExec(t, p, b, c, "CREATE "+kind+" ra_config_"+action+signature+returns+" LANGUAGE "+lang+" SET application_name='inside' AS $$"+body+"$$")
					}
					for _, action := range []string{"observe", "local", "escape", "config"} {
						want, outside := "inside", "outside"
						if action == "local" {
							want = "local"
						}
						if action == "escape" {
							want, outside = "escape", "escape"
						}
						if action == "config" {
							want, outside = "config", "config"
						}
						query, names := "SELECT ra_config_"+action+"() AS v", []string{"v"}
						if kind == "PROCEDURE" {
							query, names = "CALL ra_config_"+action+"(NULL)", []string{"answer"}
						}
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						for _, route := range []string{"direct", "fresh", "held"} {
							resultsExec(t, p, b, c, "SET application_name='outside'")
							s := attributesStep(b, query, names, []string{"TEXT"}, [][]any{{want}})
							target := fixture.Executor(c)
							if route == "fresh" {
								s = fixture.Prepared(s)
							}
							if route == "held" {
								target = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
							}
							fixture.RunStep(t, p.Ctx, target, b.Recorder, s)
							attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", outside)
						}
						stmt.Close()
					}
					// Successful ordinary SET escapes SAVE, but not its owning transaction.
					resultsExec(t, p, b, c, "SET application_name='outside'")
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					q, names := "SELECT ra_config_escape() AS v", []string{"v"}
					if kind == "PROCEDURE" {
						q, names = "CALL ra_config_escape(NULL)", []string{"answer"}
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, attributesStep(b, q, names, []string{"TEXT"}, [][]any{{"escape"}}))
					attributesSettingBoth(t, p, b, tx, "SELECT current_setting('application_name') AS v", "escape")
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
				})
			})
		}
	}
	t.Run("nested_save_and_unsaved_actions", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "SET application_name='outside'", "SET client_min_messages='notice'",
				"CREATE FUNCTION ra_inner() RETURNS text LANGUAGE SQL SET application_name='inner' AS $$SELECT current_setting('application_name')$$",
				"CREATE FUNCTION ra_other_setting() RETURNS text LANGUAGE SQL SET client_min_messages='warning' AS $$SELECT current_setting('application_name')||':'||current_setting('client_min_messages')$$",
				"CREATE FUNCTION ra_escape_inner() RETURNS text LANGUAGE SQL SET application_name='inner' AS $$SET application_name='escaped';SET LOCAL application_name='masked';SELECT current_setting('application_name')$$",
				"CREATE FUNCTION ra_outer(OUT before_name text,OUT inner_name text,OUT after_name text,OUT other_name text) LANGUAGE plpgsql SET application_name='outer' AS $$BEGIN before_name:=current_setting('application_name');inner_name:=ra_inner();after_name:=current_setting('application_name');other_name:=ra_other_setting();END$$",
				"CREATE FUNCTION ra_outer_escape(OUT inner_name text,OUT after_name text) LANGUAGE plpgsql SET application_name='outer' AS $$BEGIN inner_name:=ra_escape_inner();after_name:=current_setting('application_name');END$$",
				"CREATE FUNCTION ra_unsaved() RETURNS text LANGUAGE SQL SET client_min_messages='warning' AS $$SET application_name='unsaved';SELECT current_setting('application_name')$$",
				"CREATE FUNCTION ra_unsaved_local() RETURNS text LANGUAGE SQL SET client_min_messages='warning' AS $$SET LOCAL application_name='unsaved_local';SELECT current_setting('application_name')$$")
			attributesBoth(t, p, b, c, "SELECT * FROM ra_outer()", []string{"before_name", "inner_name", "after_name", "other_name"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"outer", "inner", "outer", "outer:warning"}})
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('client_min_messages') AS v", "notice")
			// LOCAL remains visible through enclosing SAVE exits in this command;
			// the ordinary SET candidate survives at the transaction boundary.
			for range 2 {
				resultsExec(t, p, b, c, "SET application_name='outside'")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, attributesStep(b, "SELECT * FROM ra_outer_escape()", []string{"inner_name", "after_name"}, []string{"TEXT", "TEXT"}, [][]any{{"masked", "masked"}}))
				attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "escaped")
			}
			attributesTextBoth(t, p, b, c, "SELECT ra_unsaved() AS v", "unsaved")
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "unsaved")
			attributesTextBoth(t, p, b, c, "SELECT ra_unsaved_local() AS v", "unsaved_local")
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "unsaved")
		})
	})
	t.Run("capture_reset_and_validation", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "SET application_name='A'")
			ddl := "CREATE FUNCTION ra_capture() RETURNS text LANGUAGE SQL SET application_name FROM CURRENT AS $$SELECT current_setting('application_name')$$"
			stmt, err := c.PrepareContext(p.Ctx, ddl)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='ra_capture'", 0)
			resultsExec(t, p, b, c, "SET application_name='B'")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "B")
			resultsExec(t, p, b, c, "SET application_name='C'")
			attributesTextBoth(t, p, b, c, "SELECT ra_capture() AS v", "B")
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_capture() SET application_name='first' SET client_min_messages='warning' SET application_name='last'")
			attributesTextBoth(t, p, b, c, "SELECT proconfig::text AS v FROM pg_proc WHERE proname='ra_capture'", "{application_name=last,client_min_messages=warning}")
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_capture() SET application_name TO DEFAULT")
			attributesTextBoth(t, p, b, c, "SELECT ra_capture() AS v", "C")
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_capture() SET application_name='again' RESET client_min_messages")
			attributesTextBoth(t, p, b, c, "SELECT proconfig::text AS v FROM pg_proc WHERE proname='ra_capture'", "{application_name=again}")
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_capture() RESET ALL SET application_name FROM CURRENT")
			attributesTextBoth(t, p, b, c, "SELECT ra_capture() AS v", "C")
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_capture() SET application_name='discard' RESET ALL")
			attributesBoth(t, p, b, c, "SELECT proconfig IS NULL AS v FROM pg_proc WHERE proname='ra_capture'", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
			for _, tc := range []struct{ clause, code, message string }{{"SET ra_unknown_setting='x'", "42704", `unrecognized configuration parameter "ra_unknown_setting"`}, {"SET work_mem='wrong'", "22023", `invalid value for parameter "work_mem": "wrong"`}} {
				want := proceduresDiagnostic(tc.code, tc.message, 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("ALTER FUNCTION ra_capture() "+tc.clause, want))
				attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "C")
			}
		})
	})
	for _, lang := range []string{"sql", "plpgsql"} {
		t.Run("error_cancel_"+lang, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "SET application_name='outside'", "CREATE TABLE ra_config_log(v integer)", "CREATE TABLE ra_config_unique(v integer CONSTRAINT ra_config_key UNIQUE)")
				body := "SET application_name='escape';INSERT INTO ra_config_log VALUES(x);SELECT 10/x"
				cancelBody := "INSERT INTO ra_config_log VALUES(30);SELECT current_setting('application_name') FROM pg_sleep(seconds)"
				contextText := `SQL function "ra_config_fail" statement 3`
				cancelContext := `SQL function "ra_config_pause" statement 2`
				if lang == "plpgsql" {
					body = "BEGIN SET application_name='escape';INSERT INTO ra_config_log VALUES(x);RETURN 10/x;END"
					cancelBody = "BEGIN INSERT INTO ra_config_log VALUES(30);RETURN (SELECT current_setting('application_name') FROM pg_sleep(seconds));END"
					contextText = "PL/pgSQL function ra_config_fail(integer) line 1 at RETURN"
					cancelContext = "PL/pgSQL expression \"(SELECT current_setting('application_name') FROM pg_sleep(seconds))\"\nPL/pgSQL function ra_config_pause(double precision) line 1 at RETURN"
				}
				resultsExec(t, p, b, c, "CREATE FUNCTION ra_config_fail(x integer) RETURNS integer LANGUAGE "+lang+" SET application_name='inside' AS $$"+body+"$$", "CREATE FUNCTION ra_config_pause(seconds double precision) RETURNS text LANGUAGE "+lang+" SET application_name='inside' AS $$"+cancelBody+"$$")
				want := proceduresDiagnostic("22012", "division by zero", 0)
				want.Context = contextText
				attributesQueryError(t, p, b, c, "SELECT ra_config_fail(0)", want)
				proceduresCount(t, p, b, c, "ra_config_log", 0)
				attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
				constraintBody := "SET application_name='constraint_escape';INSERT INTO ra_config_log VALUES(x);INSERT INTO ra_config_unique VALUES(x),(x);SELECT x"
				constraintContext := `SQL function "ra_config_constraint" statement 3`
				if lang == "plpgsql" {
					constraintBody = "BEGIN SET application_name='constraint_escape';INSERT INTO ra_config_log VALUES(x);INSERT INTO ra_config_unique VALUES(x),(x);RETURN x;END"
					constraintContext = "SQL statement \"INSERT INTO ra_config_unique VALUES(x),(x)\"\nPL/pgSQL function ra_config_constraint(integer) line 1 at SQL statement"
				}
				resultsExec(t, p, b, c, "CREATE FUNCTION ra_config_constraint(x integer) RETURNS integer LANGUAGE "+lang+" SET application_name='inside' AS $$"+constraintBody+"$$")
				constraintWant := proceduresDiagnostic("23505", `duplicate key value violates unique constraint "ra_config_key"`, 0)
				constraintWant.Detail = "Key (v)=(4) already exists."
				constraintWant.Schema, constraintWant.Table, constraintWant.Constraint = "public", "ra_config_unique", "ra_config_key"
				constraintWant.Context = constraintContext
				attributesQueryError(t, p, b, c, "SELECT ra_config_constraint(4)", constraintWant)
				proceduresCount(t, p, b, c, "ra_config_log", 0)
				proceduresCount(t, p, b, c, "ra_config_unique", 0)
				attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
				q := "SELECT ra_config_pause($1::double precision) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, cancelContext, nil, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := stmt.QueryContext(ctx, 600)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				proceduresCount(t, p, b, c, "ra_config_log", 0)
				attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, attributesStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{"inside"}}, 0))
				attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
				proceduresCount(t, p, b, c, "ra_config_log", 1)
				resultsExec(t, p, b, c, "CREATE FUNCTION ra_catch() RETURNS text LANGUAGE plpgsql SET application_name='outer' AS $$BEGIN BEGIN PERFORM ra_config_fail(0);EXCEPTION WHEN division_by_zero THEN PERFORM 1;END;RETURN current_setting('application_name');END$$")
				attributesTextBoth(t, p, b, c, "SELECT ra_catch() AS v", "outer")
				proceduresCount(t, p, b, c, "ra_config_log", 1)
				attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
			})
		})
	}
	t.Run("no_reset_current_transaction_settings", func(t *testing.T) {
		for _, lang := range []string{"sql", "plpgsql"} {
			for _, kind := range []string{"FUNCTION", "PROCEDURE"} {
				t.Run(lang+"_"+kind, func(t *testing.T) {
					proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						resultsExec(t, p, b, c, "SET application_name='outside'", "CREATE TABLE ra_no_reset_log(v integer)")
						checkModes := func() {
							profile := resultsTextMetadata
							profile.Nullable, profile.NullableKnown = true, true
							attributesBothMetadata(t, p, b, c,
								"SELECT current_setting('application_name') AS application,current_setting('transaction_read_only') AS readonly,current_setting('transaction_deferrable') AS deferrable,current_setting('transaction_isolation') AS isolation",
								[]string{"application", "readonly", "deferrable", "isolation"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"},
								[][]any{{"outside", "off", "off", "read committed"}}, []resultsNativeColumn{profile, profile, profile, profile})
						}
						checkUnchanged := func() {
							checkModes()
							proceduresCount(t, p, b, c, "ra_no_reset_log", 0)
						}
						checkUnchanged()
						// GUCArrayAdd checks values with PGC_S_TEST/ACTION_SET before
						// ProcedureCreate can apply SAVE for either language validator.
						// The released transaction hooks distinguish idempotent isolation
						// from deferrability, which checks the snapshot even for "off".
						for _, setting := range []struct {
							name, value, suffix, declarationMessage string
						}{
							{"transaction_read_only", "on", "readonly_on", ""},
							{"transaction_read_only", "off", "readonly_off", ""},
							{"transaction_deferrable", "off", "deferrable_off", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"},
							{"transaction_deferrable", "on", "deferrable_on", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"},
							{"transaction_isolation", "read committed", "isolation_same", ""},
							{"transaction_isolation", "repeatable read", "isolation_changed", "SET TRANSACTION ISOLATION LEVEL must be called before any query"},
						} {
							name := "ra_no_reset_" + setting.suffix
							signature, returns := "()", " RETURNS integer"
							body := "INSERT INTO ra_no_reset_log VALUES(1);SELECT 7"
							q := "SELECT " + name + "() AS v"
							outputName := "v"
							if kind == "PROCEDURE" {
								signature, returns = "(OUT answer integer)", ""
								q = "CALL " + name + "(NULL)"
								outputName = "answer"
							}
							if lang == "plpgsql" {
								body = "BEGIN INSERT INTO ra_no_reset_log VALUES(1);RETURN 7;END"
								if kind == "PROCEDURE" {
									body = "BEGIN INSERT INTO ra_no_reset_log VALUES(1);answer:=7;END"
								}
							}
							declaration := kind + " " + name + signature + returns + " LANGUAGE " + lang
							config := " SET application_name='inside' SET " + setting.name + "='" + setting.value + "'"
							source := " AS $$" + body + "$$"
							entryWant := proceduresDiagnostic("0A000", "parameter \""+setting.name+"\" cannot be set locally in functions", 0)
							createWant := entryWant
							if setting.declarationMessage != "" {
								createWant = proceduresDiagnostic("25001", setting.declarationMessage, 0)
							}
							// Rejected declaration values never reach the validator's SAVE.
							// Admitted values reach SAVE before body validation and fail there.
							create := fixture.ErrorDiagnostic("CREATE "+declaration+config+source, createWant)
							create.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, create)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(create))
							proceduresCount(t, p, b, c, "pg_proc WHERE proname='"+name+"'", 0)
							checkUnchanged()
							resultsExec(t, p, b, c, "CREATE "+declaration+" SET application_name='baseline'"+source)
							catalog := func(entries [][]any) {
								attributesBoth(t, p, b, c,
									"SELECT proconfig[1]::text AS first,proconfig[2]::text AS second FROM pg_proc WHERE proname='"+name+"'",
									[]string{"first", "second"}, []string{"TEXT", "TEXT"}, entries)
							}
							baseline := [][]any{{"application_name=baseline", nil}}
							replace := fixture.ErrorDiagnostic("CREATE OR REPLACE "+declaration+config+source, createWant)
							replace.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, replace)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(replace))
							catalog(baseline)
							checkUnchanged()
							resetWant := proceduresDiagnostic("0A000", "parameter \""+setting.name+"\" cannot be reset", 0)
							reset := fixture.ErrorDiagnostic("ALTER "+kind+" "+name+"() RESET "+setting.name, resetWant)
							reset.ErrorStage = "execute"
							// RESET validates even a setting absent from proconfig.
							fixture.RunStep(t, p.Ctx, c, b.Recorder, reset)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(reset))
							catalog(baseline)
							checkUnchanged()
							alter := "ALTER " + kind + " " + name + "()" + config
							if setting.declarationMessage != "" {
								// ALTER has no validator, but still runs the same value hook.
								// Its failure must retain both the old config and executable body.
								rejected := fixture.ErrorDiagnostic(alter, createWant)
								rejected.ErrorStage = "execute"
								fixture.RunStep(t, p.Ctx, c, b.Recorder, rejected)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(rejected))
								catalog(baseline)
								checkUnchanged()
								attributesBoth(t, p, b, c, q, []string{outputName}, []string{"INT4"}, [][]any{{int64(7)}})
								stmt, err := c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
									attributesStep(b, q, []string{outputName}, []string{"INT4"}, [][]any{{int64(7)}}))
								stmt.Close()
								proceduresCount(t, p, b, c, "ra_no_reset_log", 3)
								attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ra_no_reset_log", 3))
								fixture.RunStep(t, p.Ctx, c, b.Recorder, reset)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(reset))
								catalog(baseline)
								checkUnchanged()
								continue
							}
							// Admitted ALTER values are stored without SAVE or a validator.
							// Only the subsequent invocation attempts SAVE and fails.
							resultsExec(t, p, b, c, alter)
							configured := [][]any{{"application_name=inside", setting.name + "=" + setting.value}}
							catalog(configured)
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							checkUnchanged()
							attributesQueryError(t, p, b, c, q, entryWant)
							s := fixture.ErrorDiagnostic(q, entryWant)
							s.SelectRows, s.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
							stmt.Close()
							// application_name is saved first, then unwound without any
							// body execution or current-transaction mode mutation.
							checkUnchanged()
							fixture.RunStep(t, p.Ctx, c, b.Recorder, reset)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(reset))
							catalog(configured)
							checkUnchanged()
						}
					})
				})
			}
		}
	})
	t.Run("partial_entry_and_setting_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_setting_owner", "ra_setting_caller"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE ROLE ra_setting_owner", "CREATE ROLE ra_setting_caller", "GRANT USAGE ON SCHEMA public TO PUBLIC", "GRANT CREATE ON SCHEMA public TO ra_setting_caller", "SET application_name='outside'",
				"CREATE TABLE ra_partial_log(v integer)", "GRANT INSERT ON ra_partial_log TO ra_setting_owner",
				"CREATE FUNCTION ra_partial() RETURNS text LANGUAGE SQL SECURITY DEFINER SET application_name='partial' SET log_min_messages='warning' AS $$INSERT INTO public.ra_partial_log VALUES(1);SELECT current_setting('application_name')$$",
				"ALTER FUNCTION ra_partial() OWNER TO ra_setting_owner", "SET SESSION AUTHORIZATION ra_setting_caller")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
					return
				}
				for _, q := range []string{"REVOKE SET ON PARAMETER log_min_messages FROM ra_setting_owner", "REVOKE SET ON PARAMETER \"ra.custom\" FROM ra_setting_caller"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			want := proceduresDiagnostic("42501", `permission denied to set parameter "log_min_messages"`, 0)
			attributesQueryError(t, p, b, c, "SELECT ra_partial()", want)
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			proceduresCount(t, p, b, c, "ra_partial_log", 0)
			resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_setting_caller")
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
			attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", "ra_setting_caller")
			custom := proceduresDiagnostic("42501", `permission denied to set parameter "ra.custom"`, 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("CREATE FUNCTION ra_custom() RETURNS integer LANGUAGE SQL SET ra.custom='x' AS 'SELECT 1'", custom))
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SET ON PARAMETER log_min_messages TO ra_setting_owner", "GRANT SET ON PARAMETER \"ra.custom\" TO ra_setting_caller", "SET SESSION AUTHORIZATION ra_setting_caller")
			attributesTextBoth(t, p, b, c, "SELECT ra_partial() AS v", "partial")
			resultsExec(t, p, b, c, "CREATE FUNCTION ra_custom() RETURNS text LANGUAGE SQL SET ra.custom='x' AS $$SELECT current_setting('ra.custom')$$")
			attributesTextBoth(t, p, b, c, "SELECT ra_custom() AS v", "x")
		})
	})
	t.Run("presentation_and_row_security", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_policy_reader"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "SET DateStyle='ISO, MDY'", "SET TimeZone='UTC'",
				"CREATE FUNCTION ra_codecs() RETURNS text LANGUAGE SQL SET DateStyle='SQL, DMY' SET TimeZone='Europe/Paris' AS $$SELECT '2000-01-02'::date::text||'|'||'2000-01-01 00:00:00+00'::timestamptz::text$$")
			attributesTextBoth(t, p, b, c, "SELECT ra_codecs() AS v", "02/01/2000|01/01/2000 01:00:00 CET")
			attributesTextBoth(t, p, b, c, "SELECT '2000-01-02'::date::text||'|'||'2000-01-01 00:00:00+00'::timestamptz::text AS v", "2000-01-02|2000-01-01 00:00:00+00")
			resultsExec(t, p, b, c, "CREATE ROLE ra_policy_reader", "GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE TABLE ra_policy(v integer)", "GRANT SELECT ON ra_policy TO ra_policy_reader", "ALTER TABLE ra_policy ENABLE ROW LEVEL SECURITY", "CREATE POLICY ra_policy_visible ON ra_policy USING (v=42)",
				"CREATE FUNCTION ra_rls() RETURNS integer LANGUAGE SQL SET row_security=on AS 'SELECT v FROM public.ra_policy'",
				"CREATE FUNCTION ra_rls_off() RETURNS integer LANGUAGE SQL SET row_security=off AS 'SELECT v FROM public.ra_policy'")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_policy VALUES(42),(9)", 2))
			resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_policy_reader")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			attributesIntBoth(t, p, b, c, "SELECT ra_rls() AS v", 42)
			want := proceduresDiagnostic("42501", `query would be affected by row-level security policy for table "ra_policy"`, 0)
			want.Context = `SQL function "ra_rls_off" statement 1`
			attributesQueryError(t, p, b, c, "SELECT ra_rls_off()", want)
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('row_security') AS v", "on")
		})
	})
}
