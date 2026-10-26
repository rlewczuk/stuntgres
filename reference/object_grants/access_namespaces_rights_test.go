package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessNamespacesRights(t *testing.T) {
	databases := []string{"an_defaults_direct", "an_defaults_prepared"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	objectGrantScenario(t, []string{"an_private"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		objects := accessNamespacesSetup(t, p, b, c, run)
		for _, o := range objects {
			q := "SELECT " + o.function + "($1::name,$2::oid,$3::text) AS v"
			held := accessExecutionHeld(t, p, c, q)
			check := func(actor string, ordinary, options bool) {
				for _, tc := range []struct {
					token string
					want  bool
				}{{o.privilege, ordinary}, {o.privilege + " WITH GRANT OPTION", options}} {
					s := objectGrantAccessValue(q, tc.want, actor, o.oid, tc.token)
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
			check("og_object_reader", false, false)
			check("og_object_other", false, false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader", 0))
			check("og_object_reader", true, false)
			check("og_object_other", false, false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader WITH GRANT OPTION", 0))
			check("og_object_reader", true, true)
			run(fixture.Exec("REVOKE GRANT OPTION FOR "+o.privilege+" ON "+o.target+" FROM og_object_reader CASCADE", 0))
			check("og_object_reader", true, false)
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM og_object_reader", 0))
			check("og_object_reader", false, false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_other", 0))
			run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT TRUE", 0))
			check("og_object_reader", true, false)
			run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT FALSE", 0))
			check("og_object_reader", false, false)
			run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM og_object_other", 0))
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO PUBLIC", 0))
			for _, actor := range []string{"'public'::name", "4294967294::oid"} {
				run(objectGrantAccessValue("SELECT "+o.function+"("+actor+",$1::oid,$2::text) AS v", true, o.oid, o.privilege))
			}
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM PUBLIC", 0))
			run(objectGrantAccessValue("SELECT "+o.function+"(4294967294::oid,$1::oid,$2::text) AS v", false, o.oid, o.privilege))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader", 0))
			check("og_object_reader", true, false)
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, objectGrantAccessValue(q, false, "og_object_reader", o.oid, o.privilege))
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("ROLLBACK", 0))
			check("og_object_reader", false, false)
			// The omitted-role recipe follows the executing session, not Prepare.
			omitted := accessExecutionHeld(t, p, c, "SELECT "+o.function+"($1::oid,$2::text) AS v")
			fixture.RunStep(t, p.Ctx, omitted, b.Recorder, objectGrantAccessValue(omitted.SQL, true, o.oid, o.privilege))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RunStep(t, p.Ctx, omitted, b.Recorder, objectGrantAccessValue(omitted.SQL, false, o.oid, o.privilege))
			})
			// Supported ownership DDL proves owner/inherited-owner options here.
			// Retained tablespace ownership is covered by persistence owner repair.
			if o.function != "has_tablespace_privilege" {
				target := o.target
				run(fixture.Exec("ALTER "+target+" OWNER TO og_object_other", 0))
				run(fixture.Exec("REVOKE ALL ON "+target+" FROM og_object_other", 0))
				check("og_object_other", false, true)
				run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT TRUE", 0))
				check("og_object_reader", false, true)
				run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
				run(fixture.Exec("ALTER "+target+" OWNER TO postgres", 0))
			}
		}
		accessNamespacesStoredAuthority(t, p, b, c, run, objects)
		for _, role := range []string{"pg_read_all_data", "pg_write_all_data"} {
			run(fixture.Exec("GRANT "+role+" TO og_object_reader", 0))
			for _, tc := range []struct {
				token string
				want  bool
			}{{"USAGE", true}, {"CREATE", false}, {"USAGE WITH GRANT OPTION", false}} {
				run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader','an_private',$1::text) AS v", tc.want, tc.token))
			}
			run(fixture.Exec("REVOKE "+role+" FROM og_object_reader", 0))
		}
		// Absent ACL defaults are distinct from the explicit ACLs above.
		run(fixture.Exec("CREATE DOMAIN an_private.defaults AS integer", 0))
		run(objectGrantAccessValue("SELECT has_type_privilege(4294967294::oid,'an_private.defaults','USAGE') AS v", true))
		run(fixture.Exec("REVOKE USAGE ON TYPE an_private.defaults FROM PUBLIC", 0))
		run(objectGrantAccessValue("SELECT has_type_privilege(4294967294::oid,'an_private.defaults','USAGE') AS v", false))
		// TEMP namespace special rights concern the queried actor's database ACL.
		run(fixture.Exec("CREATE TEMP TABLE an_temp(x integer)", 0))
		var temp int64
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_my_temp_schema()").Scan(&temp); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			token string
			want  bool
		}{{"USAGE", true}, {"CREATE", false}, {"USAGE WITH GRANT OPTION", false}, {"CREATE WITH GRANT OPTION", false}} {
			run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader',$1::oid,$2::text) AS v", tc.want, temp, tc.token))
		}
		run(fixture.Exec("GRANT TEMP ON "+objects[0].target+" TO og_object_reader", 0))
		run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader',$1::oid,'CREATE') AS v", true, temp))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("CREATE TEMP TABLE an_peer_temp(x integer)", 0))
		var otherTemp int64
		if err := peer.QueryRowContext(p.Ctx, "SELECT pg_my_temp_schema()").Scan(&otherTemp); err != nil {
			peer.Close()
			t.Fatal(err)
		}
		for _, token := range []string{"USAGE", "CREATE"} {
			run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader',$1::oid,$2::text) AS v", false, otherTemp, token))
		}
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		database := "an_defaults_direct"
		if prepared {
			database = "an_defaults_prepared"
		}
		run(fixture.Exec("CREATE DATABASE "+database, 0))
		run(fixture.Select("SELECT datacl IS NULL AS absent FROM pg_database WHERE datname=$1", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}, database))
		for _, tc := range []struct {
			token string
			want  bool
		}{{"CONNECT", true}, {"TEMP", true}, {"TEMPORARY", true}, {"CREATE", false}} {
			run(objectGrantAccessValue("SELECT has_database_privilege(4294967294::oid,$1::text,$2::text) AS v", tc.want, database, tc.token))
		}
		run(fixture.Exec("REVOKE ALL ON DATABASE "+database+" FROM PUBLIC", 0))
		for _, token := range []string{"CONNECT", "TEMP", "CREATE"} {
			run(objectGrantAccessValue("SELECT has_database_privilege(4294967294::oid,$1::text,$2::text) AS v", false, database, token))
		}
	})
}

func TestReferenceAccessNamespacesTokens(t *testing.T) {
	objectGrantScenario(t, []string{"an_private"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		objects := accessNamespacesSetup(t, p, b, c, run)
		for i, o := range objects {
			allowed := [][]string{{"CREATE", "CONNECT", "TEMPORARY", "TEMP"}, {"CREATE", "USAGE"}, {"USAGE"}, {"CREATE"}}[i]
			q := "SELECT " + o.function + "('og_object_reader',$1::oid,$2::text) AS v"
			for _, token := range allowed {
				run(objectGrantAccessValue(q, false, o.oid, token))
				run(objectGrantAccessValue(q, false, o.oid, token+" WITH GRANT OPTION"))
				run(fixture.Exec("GRANT "+token+" ON "+o.target+" TO og_object_reader WITH GRANT OPTION", 0))
				for _, spelling := range []string{token, strings.ToLower(token), " \t" + strings.ToLower(token) + "\r\n", token + " WITH GRANT OPTION", strings.ToLower(token + " WITH GRANT OPTION")} {
					run(objectGrantAccessValue(q, true, o.oid, spelling))
				}
				run(fixture.Exec("REVOKE "+token+" ON "+o.target+" FROM og_object_reader CASCADE", 0))
			}
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader", 0))
			for _, tc := range []struct {
				token string
				want  bool
			}{{o.privilege, true}, {" \t" + o.privilege + "\r\n", true}, {o.privilege + " WITH GRANT OPTION", false}, {o.privilege + " WITH GRANT OPTION," + o.privilege, true}} {
				run(objectGrantAccessValue(q, tc.want, o.oid, tc.token))
			}
			for _, tc := range []struct{ input, bad string }{{"", ""}, {"ALL", "ALL"}, {",", ""}, {o.privilege + ",", ""}, {"," + o.privilege, ""}, {o.privilege + ",,bad", ""}, {o.privilege + ",bad", "bad"}, {o.privilege + "  WITH GRANT OPTION", o.privilege + "  WITH GRANT OPTION"}, {o.privilege + "\tWITH GRANT OPTION", o.privilege + "\tWITH GRANT OPTION"}, {o.privilege + " WITH GRANT OPTION bad", o.privilege + " WITH GRANT OPTION bad"}, {"EXECUTE", "EXECUTE"}} {
				s := accessExecutionError(q, "22023", "unrecognized privilege type: \""+tc.bad+"\"", o.oid, tc.input)
				run(s)
				s.Query = "SELECT " + o.function + "('postgres',$1::oid,$2::text) AS v"
				run(s)
			}
			if i < 2 {
				other := "CREATE"
				for _, token := range []string{other + "," + o.privilege, other + " WITH GRANT OPTION," + o.privilege} {
					run(objectGrantAccessValue(q, true, o.oid, token))
				}
				run(objectGrantAccessValue(q, false, o.oid, other+" WITH GRANT OPTION,"+o.privilege+" WITH GRANT OPTION"))
			}
		}
		q := "SELECT has_database_privilege('og_object_reader',$1::oid,$2::text) AS v"
		run(fixture.Exec("GRANT TEMP ON "+objects[0].target+" TO og_object_reader WITH GRANT OPTION", 0))
		for _, token := range []string{"TEMP", "temporary", "TEMP WITH GRANT OPTION", "TEMPORARY WITH GRANT OPTION"} {
			run(objectGrantAccessValue(q, true, objects[0].oid, token))
		}
	})
}
