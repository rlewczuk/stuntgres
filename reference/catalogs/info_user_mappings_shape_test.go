package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUserMappingsShape(t *testing.T) {
	iumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		where := "foreign_server_name='IUM Shape Server'"
		check := func(want [][]any) {
			s := iumProjection(b.PG, where, want)
			run(s)
			run(fixture.Prepared(s))
		}
		check(nil)
		run(fixture.Exec(`CREATE SERVER "IUM Shape Server" FOREIGN DATA WRAPPER ium_fdw`, 0))
		check(nil) // A server is not itself a mapping or a NULL placeholder.
		run(fixture.Exec(`CREATE USER MAPPING FOR "IUM Mixed Role" SERVER "IUM Shape Server"`, 0))
		run(fixture.Exec(`CREATE USER MAPPING FOR ium_other SERVER "IUM Shape Server" OPTIONS(password 'ium_secret',tag 'identity')`, 0))
		run(fixture.Exec(`CREATE USER MAPPING FOR PUBLIC SERVER "IUM Shape Server" OPTIONS(password 'ium_public_secret')`, 0))
		want := iumRows(database, "IUM Shape Server", "IUM Mixed Role", "ium_other", "PUBLIC")
		check(want)
		s := iumProjection(b.PG, where, want)
		held := iumHeld(t, p, c, r, s.Query)
		held(s)
		run(fixture.Exec(`ALTER USER MAPPING FOR ium_other SERVER "IUM Shape Server" OPTIONS(SET password 'changed_secret',DROP tag)`, 0))
		held(s)
		check(want)
		empty := iumProjection(b.PG, "FALSE", nil)
		run(empty)
		run(fixture.Prepared(empty))
		for _, step := range []fixture.Step{s, empty} {
			for _, prepared := range []bool{false, true} {
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, step.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer func() {
						if err := closeQuery(); err != nil {
							t.Error(err)
						}
					}()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					iumMetadata(t, columns, b.PG)
					count := 0
					for rows.Next() {
						var a, d, server any
						if err := rows.Scan(&a, &d, &server); err != nil {
							t.Fatal(err)
						}
						for i, value := range []any{a, d, server} {
							step.CheckRaw(t, i, count, value)
						}
						count++
					}
					if err := rows.Err(); err != nil || count != len(step.Rows) {
						t.Fatalf("drain count=%d error=%v", count, err)
					}
					iumMetadata(t, columns, b.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					iumMetadata(t, columns, b.PG)
				}()
			}
		}
		var attributes [][]any
		for i, name := range fixture.IUMColumns {
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.user_mappings'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,t.typname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype WHERE c.oid='information_schema.user_mappings'::regclass`, []string{"same_type", "same_relation", "typname"}, []string{"BOOL", "BOOL", "NAME"}, [][]any{{true, true, "user_mappings"}}))
	})
}

func TestReferenceInfoUserMappingsDiagnostics(t *testing.T) {
	iumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.user_mappings", d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		run(fixture.Exec("CREATE SERVER ium_server FOREIGN DATA WRAPPER ium_fdw", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER ium_server", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR ium_mapped SERVER ium_server", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR ium_other SERVER ium_server", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ium_reader", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT * FROM pg_catalog.pg_user_mapping", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_user_mapping"))
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			run(s)
		}
		run(iumProjection(b.PG, "foreign_server_name='ium_server'", nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ium_server TO ium_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ium_reader", 0))
		run(iumProjection(b.PG, "foreign_server_name='ium_server'", iumRows(database, "ium_server", "ium_mapped", "ium_other", "PUBLIC")))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
