package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// DROP runs before ADD, so IF NOT EXISTS must inspect the evolving relation,
// not the descriptor at statement entry. A genuinely admitted ordinary column
// still reaches the execution-time foreign-table kind check.
func TestReferenceExternalSQLProviderColumnOptionsDropAddOrdinary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			for _, mode := range []string{"direct", "prepared"} {
				t.Run(mode, func(t *testing.T) {
					prepared := mode == "prepared"
					table := "external_sql_options_plain_" + mode
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					run(fixture.Exec("CREATE TABLE "+table+"(sentinel integer NOT NULL,v integer NOT NULL)", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1,7),(2,9)", 2))
					unchanged := func() {
						run(externalSQLColumnOptionRegressionSchema(table, 23, true, nil))
						run(fixture.Select("SELECT sentinel,v FROM "+table+" ORDER BY sentinel", []string{"sentinel", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(9)}}))
					}
					unchanged()
					want := fixture.ControlsReferenceDiagnostic("42809", `"`+table+`" is not a foreign table`)
					failure := fixture.ErrorDiagnostic("ALTER TABLE "+table+" DROP COLUMN v, ADD COLUMN IF NOT EXISTS v bytea OPTIONS(column_name 'remote_v')", want)
					failure.ErrorStage = "execute"
					run(failure)
					unchanged()
				})
			}
		})
	}
}

// Accepted and rejected replacements are separate leaves: losing options and
// bypassing the real public SQL validator must each have an independent witness.
func TestReferenceExternalSQLProviderColumnOptionsDropAddForeign(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			externalSQLColumnOptionRegressionValidator(func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
			for _, spelling := range []struct{ name, sql string }{
				{"table", "ALTER TABLE"},
				{"foreign", "ALTER FOREIGN TABLE"},
			} {
				t.Run(spelling.name, func(t *testing.T) {
					for _, mode := range []string{"direct", "prepared"} {
						t.Run(mode, func(t *testing.T) {
							for _, outcome := range []string{"accepted", "rejected"} {
								t.Run(outcome, func(t *testing.T) {
									prepared := mode == "prepared"
									table := "external_sql_options_" + spelling.name + "_" + mode + "_" + outcome
									run := func(s fixture.Step) {
										s.Prepared = prepared
										fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
									}
									run(fixture.Exec("CREATE FOREIGN TABLE "+table+"(sentinel integer NOT NULL,v integer OPTIONS(column_name 'original_v') NOT NULL) SERVER external_sql_metadata_server", 0))
									run(externalSQLColumnOptionRegressionSchema(table, 23, true, "{column_name=original_v}"))
									option := "remote_v"
									if outcome == "rejected" {
										option = "reject_v"
									}
									query := spelling.sql + " " + table + " DROP COLUMN v, ADD COLUMN IF NOT EXISTS v bytea OPTIONS(column_name '" + option + "')"
									if outcome == "accepted" {
										run(fixture.Exec(query, 0))
										run(externalSQLColumnOptionRegressionSchema(table, 17, false, "{column_name=remote_v}"))
									} else {
										want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
										want.Context = `SQL function "external_sql_options_guard" statement 1`
										failure := fixture.ErrorDiagnostic(query, want)
										failure.ErrorStage = "execute"
										run(failure)
										run(externalSQLColumnOptionRegressionSchema(table, 23, true, "{column_name=original_v}"))
									}
								})
							}
						})
					}
				})
			}
		})
	}
}

// Unlike DROP/re-add, an unchanged existing v genuinely skips ADD. Even the
// validator-rejected option must neither run validation nor update metadata.
func TestReferenceExternalSQLProviderColumnOptionsSkippedAdd(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			externalSQLColumnOptionRegressionValidator(func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
			for _, target := range []struct{ name, create, alter string }{
				{"plain", "CREATE TABLE", "ALTER TABLE"},
				{"table", "CREATE FOREIGN TABLE", "ALTER TABLE"},
				{"foreign", "CREATE FOREIGN TABLE", "ALTER FOREIGN TABLE"},
			} {
				t.Run(target.name, func(t *testing.T) {
					for _, mode := range []string{"direct", "prepared"} {
						t.Run(mode, func(t *testing.T) {
							prepared := mode == "prepared"
							table := "external_sql_options_skip_" + target.name + "_" + mode
							run := func(s fixture.Step) {
								s.Prepared = prepared
								fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							}
							definition := "(sentinel integer NOT NULL,v integer NOT NULL)"
							var options any
							if target.name != "plain" {
								definition = "(sentinel integer NOT NULL,v integer OPTIONS(column_name 'original_v') NOT NULL) SERVER external_sql_metadata_server"
								options = "{column_name=original_v}"
							}
							run(fixture.Exec(target.create+" "+table+definition, 0))
							if target.name == "plain" {
								run(fixture.Exec("INSERT INTO "+table+" VALUES(1,7),(2,9)", 2))
							}
							run(fixture.Exec(target.alter+" "+table+" ADD COLUMN IF NOT EXISTS v bytea OPTIONS(column_name 'reject_v')", 0))
							run(externalSQLColumnOptionRegressionSchema(table, 23, true, options))
							if target.name == "plain" {
								run(fixture.Select("SELECT sentinel,v FROM "+table+" ORDER BY sentinel", []string{"sentinel", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(9)}}))
							}
						})
					}
				})
			}
		})
	}
}

func externalSQLColumnOptionRegressionValidator(run func(fixture.Step)) {
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE FUNCTION external_sql_options_guard(text[],oid) RETURNS integer LANGUAGE SQL AS $$SELECT 1 / CASE WHEN 'column_name=reject_v'=ANY($1) THEN 0 ELSE 1 END$$", 0),
		fixture.Exec("CREATE FOREIGN DATA WRAPPER external_sql_metadata NO HANDLER VALIDATOR external_sql_options_guard", 0),
		fixture.Exec("CREATE SERVER external_sql_metadata_server FOREIGN DATA WRAPPER external_sql_metadata", 0),
	} {
		run(s)
	}
}

func externalSQLColumnOptionRegressionSchema(table string, valueType int64, notNull bool, options any) fixture.Step {
	return fixture.Select("SELECT attname::text AS name,atttypid::bigint AS oid,atttypmod,attnotnull,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"name", "oid", "atttypmod", "attnotnull", "options"}, []string{"TEXT", "INT8", "INT4", "BOOL", "TEXT"}, [][]any{
		{"sentinel", int64(23), int64(-1), true, nil},
		{"v", valueType, int64(-1), notNull, options},
	})
}
