package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGUserMappingsShape(t *testing.T) {
	fixture.RoleReferenceCleanup(t, pgUserMappingsRoles)
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		before := reader.snapshot()
		pgUserMappingsSetup(t, c, run)
		shape := func(want [][]any) {
			for _, source := range []string{"pg_user_mappings", "pg_catalog.pg_user_mappings"} {
				q := "SELECT * FROM " + source + " ORDER BY srvname,usename"
				for _, prepared := range []bool{false, true} {
					var e fixture.Executor = c
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					r, err := e.QueryContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					pgUserMappingsMetadata(t, r, b.PG)
					pgUserMappingsConsume(t, b, r, want)
					fixture.OidReferenceRows(t, p.Ctx, b, e, pgUserMappingsStep(q, want))
				}
			}
		}
		shape(nil)
		// Resolve the view identity, then assert its rowtype relationship rather
		// than transplanting either implementation's generated relation/type OID.
		view := pgUserMappingID(t, p, c, "SELECT c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pg_catalog' AND c.relname='pg_user_mappings'")
		pgUserMappingBoth(run, fixture.Select("SELECT c.relkind,t.typrelid=c.oid AS same_relation,c.reltype=t.oid AS same_type FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid=$1", []string{"relkind", "same_relation", "same_type"}, []string{"CHAR", "BOOL", "BOOL"}, [][]any{{"v", true, true}}, view))
		attributes := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid=$1 AND attnum>0 ORDER BY attnum",
			[]string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, [][]any{
				{int64(1), "umid", int64(26), false, int64(-1), int64(0), int64(0)},
				{int64(2), "srvid", int64(26), false, int64(-1), int64(0), int64(0)},
				{int64(3), "srvname", int64(19), false, int64(-1), int64(950), int64(0)},
				{int64(4), "umuser", int64(26), false, int64(-1), int64(0), int64(0)},
				{int64(5), "usename", int64(19), false, int64(-1), int64(950), int64(0)},
				{int64(6), "umoptions", int64(1009), false, int64(-1), int64(950), int64(0)},
			}, view)
		fixture.OidReferenceRows(t, p.Ctx, b, c, attributes)
		fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Prepared(attributes))
		pgUserMappingsSeed(run, false)
		run(fixture.Exec("CREATE SERVER fdw_second FOREIGN DATA WRAPPER fdw_meta", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR umv_alice SERVER fdw_second OPTIONS(password 'second-secret')", 0))
		run(fixture.Exec(`ALTER USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(ADD punctuation 'comma,{brace}',ADD quote 'apostrophe'' double" slash\')`, 0))
		// PostgreSQL array output quotes delimiter/braces/quote/backslash elements;
		// apostrophes need no array-level escape. Logical cells below are separate.
		const special = `{password=alice-secret,empty=,tag=é=🙂,"punctuation=comma,{brace}","quote=apostrophe' double\" slash\\"}`
		specs := pgUserMappingsSpecs(false)
		specs[0].options = special
		specs = append(specs, pgUserMappingExpected{"fdw_second", "umv_alice", "{password=second-secret}"})
		rows := pgUserMappingsRows(t, p, c, specs)
		shape(rows)
		pgUserMappingBoth(run, fixture.Select("SELECT umoptions[1] AS first,umoptions[2] AS second,umoptions[3] AS third,umoptions[4] AS fourth,umoptions[5] AS fifth,array_length(umoptions,1) AS length FROM pg_user_mappings WHERE srvname='fdw_server' AND usename='umv_alice'", []string{"first", "second", "third", "fourth", "fifth", "length"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4"}, [][]any{{"password=alice-secret", "empty=", "tag=é=🙂", "punctuation=comma,{brace}", `quote=apostrophe' double" slash\`, int64(5)}}))
		if !b.PG && reader.snapshot() != before {
			t.Fatal("view metadata invoked foreign provider")
		}
	})
}
