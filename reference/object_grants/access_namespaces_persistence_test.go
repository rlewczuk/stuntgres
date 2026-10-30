package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func accessNamespacesPersistenceSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE SCHEMA an_persist",
		"CREATE SCHEMA an_persist_null",
		"CREATE SCHEMA an_persist_empty",
		"CREATE SCHEMA an_persist_equal",
		"REVOKE ALL ON SCHEMA an_persist_empty FROM postgres",
		"GRANT ALL ON SCHEMA an_persist_equal TO postgres",
		"CREATE DOMAIN an_persist.d AS integer",
		"CREATE TYPE an_persist.e AS ENUM ('one','two')",
		"CREATE TYPE an_persist.c AS (v integer)",
		"CREATE TYPE an_persist.r AS RANGE (subtype=integer)",
		"CREATE DOMAIN an_persist.absent AS integer",
		"CREATE DOMAIN an_persist.empty AS integer",
		"CREATE DOMAIN an_persist.equivalent AS integer",
		"REVOKE ALL ON TYPE an_persist.empty FROM PUBLIC,postgres",
		"GRANT USAGE ON TYPE an_persist.equivalent TO PUBLIC",
		"REVOKE ALL ON TYPE an_persist.d,an_persist.e,an_persist.c,an_persist.r FROM PUBLIC",
		"GRANT USAGE ON SCHEMA an_persist TO og_object_other WITH GRANT OPTION",
		"GRANT USAGE ON TYPE an_persist.d,an_persist.e,an_persist.c,an_persist.r TO og_object_other WITH GRANT OPTION",
		"GRANT CREATE ON TABLESPACE pg_default TO og_object_other WITH GRANT OPTION",
		"SET ROLE og_object_other",
		"GRANT USAGE ON SCHEMA an_persist TO og_object_reader",
		"GRANT USAGE ON TYPE an_persist.d,an_persist.e,an_persist.c,an_persist.r TO og_object_reader",
		"GRANT CREATE ON TABLESPACE pg_default TO og_object_reader",
		"RESET ROLE",
		"REVOKE ALL ON SCHEMA an_persist FROM postgres",
		"REVOKE ALL ON TYPE an_persist.d FROM postgres",
	} {
		run(fixture.Exec(q, 0))
	}
}

func accessNamespacesPersistenceCheck(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
	for _, o := range []struct{ function, name, privilege, table, key string }{
		{"has_schema_privilege", "an_persist", "USAGE", "pg_namespace", "nspname='an_persist'"},
		{"has_type_privilege", "an_persist.d", "USAGE", "pg_type", "oid='an_persist.d'::regtype"},
		{"has_type_privilege", "an_persist.e", "USAGE", "pg_type", "oid='an_persist.e'::regtype"},
		{"has_type_privilege", "an_persist.c", "USAGE", "pg_type", "oid='an_persist.c'::regtype"},
		{"has_type_privilege", "an_persist.r", "USAGE", "pg_type", "oid='an_persist.r'::regtype"},
		{"has_tablespace_privilege", "pg_default", "CREATE", "pg_tablespace", "spcname='pg_default'"},
	} {
		for _, prepared := range []bool{false, true} {
			s := objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',$1::text,$2::text) AS v", true, o.name, o.privilege)
			s.Prepared = prepared
			run(s)
			s = objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',oid,$1::text) AS v FROM "+o.table+" WHERE "+o.key, true, o.privilege)
			s.Prepared = prepared
			run(s)
		}
		run(objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',$1::text,$2::text) AS v", false, o.name, o.privilege+" WITH GRANT OPTION"))
		run(objectGrantAccessValue("SELECT "+o.function+"('og_object_other',$1::text,$2::text) AS v", true, o.name, o.privilege+" WITH GRANT OPTION"))
	}
	for _, q := range []string{
		"SELECT nspowner='postgres'::regrole AS v FROM pg_namespace WHERE nspname='an_persist'",
		"SELECT typowner='postgres'::regrole AS v FROM pg_type WHERE oid='an_persist.d'::regtype",
		"SELECT spcowner='og_object_other'::regrole AS v FROM pg_tablespace WHERE spcname='pg_global'",
		"SELECT nspowner='og_object_other'::regrole AS v FROM pg_namespace WHERE nspname='information_schema'",
		"SELECT NOT rolsuper AS v FROM pg_roles WHERE rolname='og_object_other'",
	} {
		run(objectGrantAccessValue(q, true))
	}
	run(fixture.Select("SELECT spcacl IS NULL AS absent,spcacl::text AS acl FROM pg_tablespace WHERE spcname='pg_global'", []string{"absent", "acl"}, []string{"BOOL", "TEXT"}, [][]any{{false, "{postgres=C/og_object_other}"}}))
	for _, o := range []struct {
		query   string
		entries [][]any
	}{
		{"SELECT unnest(nspacl)::text AS acl FROM pg_namespace WHERE nspname='an_persist'", [][]any{{"og_object_other=U*/postgres"}, {"og_object_reader=U/og_object_other"}}},
		{"SELECT unnest(typacl)::text AS acl FROM pg_type WHERE oid='an_persist.d'::regtype", [][]any{{"og_object_other=U*/postgres"}, {"og_object_reader=U/og_object_other"}}},
		{"SELECT unnest(spcacl)::text AS acl FROM pg_tablespace WHERE spcname='pg_default'", [][]any{{"postgres=C/postgres"}, {"og_object_other=C*/postgres"}, {"og_object_reader=C/og_object_other"}}},
		{"SELECT unnest(spcacl)::text AS acl FROM pg_tablespace WHERE spcname='pg_global'", [][]any{{"postgres=C/og_object_other"}}},
		{"SELECT unnest(typacl)::text AS acl FROM pg_type WHERE oid='an_persist.equivalent'::regtype", [][]any{{"postgres=U/postgres"}, {"=U/postgres"}}},
		{"SELECT unnest(nspacl)::text AS acl FROM pg_namespace WHERE nspname='an_persist_equal'", [][]any{{"postgres=UC/postgres"}}},
		{"SELECT unnest(nspacl)::text AS acl FROM pg_namespace WHERE nspname='information_schema'", [][]any{{"postgres=UC/postgres"}, {"=U/postgres"}}},
	} {
		s := fixture.Select(o.query, []string{"acl"}, []string{"TEXT"}, o.entries)
		s.Bag = true
		run(s)
	}
	run(fixture.Select("SELECT nspname,nspacl IS NULL AS absent,nspacl::text AS acl FROM pg_namespace WHERE nspname IN('an_persist_null','an_persist_empty') ORDER BY nspname", []string{"nspname", "absent", "acl"}, []string{"NAME", "BOOL", "TEXT"}, [][]any{{"an_persist_empty", false, "{}"}, {"an_persist_null", true, nil}}))
	run(fixture.Select("SELECT typname,typacl IS NULL AS absent,typacl::text AS acl FROM pg_type WHERE oid IN('an_persist.absent'::regtype,'an_persist.empty'::regtype) ORDER BY typname", []string{"typname", "absent", "acl"}, []string{"NAME", "BOOL", "TEXT"}, [][]any{{"absent", true, nil}, {"empty", false, "{}"}}))
	for _, name := range []string{"an_persist.d[]", "an_persist.r[]", "an_persist.r_multirange", "an_persist.r_multirange[]"} {
		run(objectGrantAccessValue("SELECT has_type_privilege('og_object_reader',$1::text,'USAGE') AS v", true, name))
	}
	run(objectGrantAccessValue("SELECT has_type_privilege('og_object_reader','integer','USAGE') AS v", true))
	run(objectGrantAccessValue("SELECT has_type_privilege('public','an_persist.d','USAGE') AS v", false))
	run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_other','an_persist','CREATE') AS v", false))
	// pg_global has an explicit ACL granting only postgres ordinary CREATE.
	// Its nonsuperuser owner retains implicit options, not ordinary rights;
	// inheriting that owner must not manufacture an ordinary CREATE grant.
	checkRetainedOwner := func(inherits bool) {
		for _, actor := range []struct {
			name    string
			options bool
		}{
			{"og_object_other", true},
			{"og_object_reader", inherits},
			{"pg_monitor", false},
			{"public", false},
		} {
			for _, privilege := range []struct {
				token string
				want  bool
			}{
				{"CREATE", false},
				{"CREATE WITH GRANT OPTION", actor.options},
			} {
				for _, prepared := range []bool{false, true} {
					for _, query := range []string{
						"SELECT has_tablespace_privilege($1::name,'pg_global'::text,$2::text) AS v",
						"SELECT has_tablespace_privilege($1::name,oid,$2::text) AS v FROM pg_tablespace WHERE spcname='pg_global'",
					} {
						s := objectGrantAccessValue(query, privilege.want, actor.name, privilege.token)
						s.Prepared = prepared
						run(s)
					}
				}
			}
		}
	}
	checkRetainedOwner(false)
	run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT TRUE", 0))
	checkRetainedOwner(true)
	run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT FALSE", 0))
	checkRetainedOwner(false)
	run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
	checkRetainedOwner(false)
}

// PostgreSQL replay changes retained catalogs administratively. Use ordinary
// ownership and privilege DDL to repair shared role dependencies before restoring
// the exact raw ACL, including NULL/empty provenance and original grantors.
func accessNamespacesRetainedCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	var database string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	for _, o := range []struct{ table, key, name, owner, acl, target string }{
		{"pg_database", "datname", database, "datdba", "datacl", "DATABASE"},
		{"pg_namespace", "nspname", "public", "nspowner", "nspacl", "SCHEMA"},
		{"pg_namespace", "nspname", "pg_catalog", "nspowner", "nspacl", "SCHEMA"},
		{"pg_namespace", "nspname", "pg_toast", "nspowner", "nspacl", "SCHEMA"},
		{"pg_namespace", "nspname", "information_schema", "nspowner", "nspacl", "SCHEMA"},
		{"pg_tablespace", "spcname", "pg_default", "spcowner", "spcacl", "TABLESPACE"},
		{"pg_tablespace", "spcname", "pg_global", "spcowner", "spcacl", "TABLESPACE"},
	} {
		var owner string
		var acl sql.NullString
		if err := c.QueryRowContext(p.Ctx, "SELECT "+o.owner+"::regrole::text,"+o.acl+"::text FROM pg_catalog."+o.table+" WHERE "+o.key+"=$1", o.name).Scan(&owner, &acl); err != nil {
			t.Fatal(err)
		}
		// Backend cleanup precedes the scenario's schema/role cleanup. The fixture
		// admin pool is independent of replay's possibly aborted or reauthorized conn.
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			admin, err := fixture.Admin.Conn(ctx)
			if err != nil {
				t.Error("retained authority admin", err)
				return
			}
			defer admin.Close()
			accessExecutionResetConnection(t, admin)
			target := o.target + " " + pq.QuoteIdentifier(o.name)
			for _, q := range []string{
				"ALTER " + target + " OWNER TO " + pq.QuoteIdentifier(owner),
				"REVOKE ALL ON " + target + " FROM og_object_reader,og_object_other CASCADE",
			} {
				if _, err := admin.ExecContext(ctx, q); err != nil {
					t.Errorf("restore retained authority %s: %v", q, err)
				}
			}
			if _, err := admin.ExecContext(ctx, "UPDATE pg_catalog."+o.table+" SET "+o.owner+"=$1::regrole,"+o.acl+"=$2::aclitem[] WHERE "+o.key+"=$3", owner, acl, o.name); err != nil {
				t.Errorf("restore retained authority %s %q: %v", o.table, o.name, err)
			}
		})
	}
}

func TestReferenceAccessNamespacesPersistence(t *testing.T) {
	var emitted string
	objectGrantScenario(t, []string{"an_persist", "an_persist_null", "an_persist_empty", "an_persist_equal"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		if b.PG {
			accessExecutionReplayRoutineCleanup(t, p, c)
			accessNamespacesRetainedCleanup(t, p, c)
			if emitted == "" {
				t.Fatal("missing actual native dump")
			}
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("replay actual emitted stream", err)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			accessNamespacesPersistenceCheck(t, p, c, b.Recorder)
			accessNamespacesPersistenceCascade(t, p, c, b.Recorder)
			return
		}
		for range 8 {
			run(fixture.Exec("CREATE TABLE an_persist_pressure(v integer)", 0))
			run(fixture.Exec("DROP TABLE an_persist_pressure", 0))
		}
		accessNamespacesPersistenceSetup(run)
		var saved bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
			t.Fatal(err)
		}
		// Owner and independently stored generated-array ACL repairs are bounded
		// native control input, not newly supported public catalog UPDATE.
		repairs := "UPDATE pg_catalog.pg_tablespace SET spcowner='og_object_other'::pg_catalog.regrole WHERE spcname='pg_global'::pg_catalog.name;\n" +
			"UPDATE pg_catalog.pg_namespace SET nspowner='og_object_other'::pg_catalog.regrole WHERE nspname='information_schema'::pg_catalog.name;\n" +
			"UPDATE pg_catalog.pg_type SET typacl='{og_object_other=U/postgres}'::pg_catalog.aclitem[] WHERE oid='an_persist.d[]'::pg_catalog.regtype;\n"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved.String()+repairs)); err != nil {
			t.Fatal(err)
		}
		// Keep the absent-ACL owner baseline before materializing revocable
		// ordinary rights separately from the owner's implicit grant options.
		run(objectGrantAccessValue("SELECT spcacl IS NULL AS v FROM pg_tablespace WHERE spcname='pg_global'", true))
		run(objectGrantAccessValue("SELECT has_tablespace_privilege('og_object_other','pg_global','CREATE') AS v", true))
		run(objectGrantAccessValue("SELECT has_tablespace_privilege('og_object_other','pg_global','CREATE WITH GRANT OPTION') AS v", true))
		run(fixture.Exec("REVOKE ALL ON TABLESPACE pg_global FROM og_object_other", 0))
		run(fixture.Select("SELECT spcacl IS NULL AS absent,spcacl::text AS acl FROM pg_tablespace WHERE spcname='pg_global'", []string{"absent", "acl"}, []string{"BOOL", "TEXT"}, [][]any{{false, "{}"}}))
		run(fixture.Exec("SET ROLE og_object_other", 0))
		run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO postgres", 0))
		run(fixture.Exec("RESET ROLE", 0))
		accessNamespacesPersistenceCheck(t, p, c, b.Recorder)
		if err := stuntgres.Dump(p.Ctx, p.Native, accessExecutionWriteFailure{}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
		saved.Reset()
		if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
			t.Fatal(err)
		}
		emitted = saved.String()
		var sourceOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_type WHERE oid='an_persist.d'::regtype").Scan(&sourceOID); err != nil {
			t.Fatal(err)
		}
		accessNamespacesFreshPersistence(t, p, emitted, sourceOID)
	})
}

func accessNamespacesPersistenceCascade(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, o := range []struct{ target, function, name, privilege string }{
		{"SCHEMA an_persist", "has_schema_privilege", "an_persist", "USAGE"},
		{"TYPE an_persist.d", "has_type_privilege", "an_persist.d", "USAGE"},
		{"TABLESPACE pg_default", "has_tablespace_privilege", "pg_default", "CREATE"},
	} {
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("REVOKE GRANT OPTION FOR "+o.privilege+" ON "+o.target+" FROM og_object_other CASCADE", 0))
		fixture.RunStep(t, p.Ctx, db, recorder, objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',$1::text,$2::text) AS v", false, o.name, o.privilege))
	}
}
