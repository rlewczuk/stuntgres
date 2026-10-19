package reference_test

import (
	"database/sql"
	"io"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently specified from REL_18_0 aclchk.c, guc.c, acl.c and
// tablespace.c. Main observes the pinned oracle before enabling native proof.
func TestReferenceObjectGrantParametersTablespaces(t *testing.T) {
	objectGrantScenario(t, []string{"og_parameter_space"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer func() {
			for _, q := range []string{
				"RESET SESSION AUTHORIZATION", "RESET lc_messages", "RESET application_name",
				"DROP OWNED BY og_object_other CASCADE", "DROP OWNED BY og_object_reader CASCADE",
			} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("global parameter/tablespace cleanup", q, err)
				}
			}
		}()
		run(fixture.Exec("CREATE SCHEMA og_parameter_space", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_parameter_space TO og_object_reader,og_object_other", 0))
		objectGrantParameterConsumers(run)
		objectGrantParameterDelegation(t, p, b, c, run, prepared)
		objectGrantTablespaceACL(t, p, b, run, prepared)
		objectGrantParameterSystemBoundary(t, p, b, c, run, prepared)
		objectGrantTablespacePlacement(t, p, b, c, run, prepared)
	})
}

func objectGrantParameterConsumers(run func(fixture.Step)) {
	denied := func() {
		for _, q := range []string{"SET lc_messages='C'", "RESET lc_messages", "SELECT set_config('lc_messages','C',false)"} {
			fixture.RoleGrantError(run, q, "42501", `permission denied to set parameter "lc_messages"`)
		}
	}
	allowed := func() {
		run(fixture.Exec("SET lc_messages='C'", 0))
		run(fixture.Select("SHOW lc_messages", []string{"lc_messages"}, []string{"TEXT"}, [][]any{{"C"}}))
		run(fixture.Exec("RESET lc_messages", 0))
		run(fixture.Select("SELECT set_config('lc_messages','C',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"C"}}))
	}
	fixture.RoleGrantActor(run, "og_object_reader", denied)
	run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", allowed)
	run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", denied)
	run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_other", 0))
	run(fixture.Exec("GRANT og_object_other TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", allowed)
	run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", denied)
	run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM og_object_other", 0))

	// USERSET authority is independent of an ACL; a SET grant cannot make an
	// INTERNAL/read-only parameter writable.
	run(fixture.Exec("GRANT SET ON PARAMETER application_name TO og_object_reader", 0))
	run(fixture.Exec("REVOKE SET ON PARAMETER application_name FROM og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		run(fixture.Exec("SET application_name='1MB'", 0))
		run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"1MB"}}))
		run(fixture.Select("SELECT set_config('application_name','2MB',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"2MB"}}))
		run(fixture.Exec("RESET application_name", 0))
	})
	run(fixture.Exec("GRANT ALL ON PARAMETER server_encoding TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		fixture.RoleGrantError(run, "SET server_encoding='UTF8'", "55P02", `parameter "server_encoding" cannot be changed`)
		fixture.RoleGrantError(run, "RESET server_encoding", "55P02", `parameter "server_encoding" cannot be changed`)
		fixture.RoleGrantError(run, "SELECT set_config('server_encoding','UTF8',false)", "55P02", `parameter "server_encoding" cannot be changed`)
	})
	run(fixture.Exec("REVOKE ALL ON PARAMETER server_encoding FROM og_object_reader", 0))
}

func objectGrantParameterDelegation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
	t.Helper()
	// ALTER SYSTEM alone does not permit SET, and SET alone does not confer
	// delegation. The no-option grant succeeds with a complete warning, not
	// with invented privileges.
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		fixture.RoleGrantPermissionDenied(run, "GRANT SET ON PARAMETER lc_messages TO og_object_other", "parameter", "lc_messages")
	})
	run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER lc_messages TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		fixture.RoleGrantError(run, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
	})
	run(fixture.Exec("REVOKE ALL ON PARAMETER lc_messages FROM og_object_reader", 0))
	run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader", 0))
	var notices []fixture.Diagnostic
	_, nc, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	fixture.RoleGrantNoticeExec(t, p, nc, record, prepared, &notices, "SET SESSION AUTHORIZATION og_object_reader")
	fixture.RoleGrantNoticeExec(t, p, nc, record, prepared, &notices, "GRANT SET ON PARAMETER lc_messages TO og_object_other",
		fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "lc_messages"`})
	fixture.RoleGrantNoticeExec(t, p, nc, record, prepared, &notices, "RESET SESSION AUTHORIZATION")
	fixture.RoleGrantActor(run, "og_object_other", func() {
		fixture.RoleGrantError(run, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
	})
	run(fixture.Exec(`GRANT ALL PRIVILEGES ON PARAMETER "LC_MESSAGES" TO og_object_reader WITH GRANT OPTION`, 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		run(fixture.Exec("GRANT SET,ALTER SYSTEM ON PARAMETER lc_messages TO og_object_other", 0))
	})
	fixture.RoleGrantActor(run, "og_object_other", func() { run(fixture.Exec("SET lc_messages='C'", 0)) })
	run(fixture.Exec("REVOKE GRANT OPTION FOR ALL ON PARAMETER lc_messages FROM og_object_reader CASCADE", 0))
	fixture.RoleGrantActor(run, "og_object_other", func() {
		fixture.RoleGrantError(run, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
	})
	run(fixture.Exec("REVOKE ALL ON PARAMETER lc_messages FROM og_object_reader", 0))

	// No SET/custom placeholder or ALTER SYSTEM side effect is needed to store
	// aliases/custom ACLs. Revoking through another spelling removes the same
	// dependency, rather than leaving a second identity behind.
	for _, names := range [][2]string{{`"LC_MESSAGES"`, "lc_messages"}, {"sort_mem", "work_mem"}, {`"OG_CUSTOM"."Value"`, "og_custom.value"}} {
		// Native has no work_mem setter/provider; grants cannot fabricate one.
		// The legacy alias mapping is supplemental PostgreSQL evidence only.
		if names[0] == "sort_mem" && !b.PG {
			for _, name := range names {
				fixture.RoleGrantError(run, "GRANT ALL ON PARAMETER "+name+" TO og_object_reader WITH GRANT OPTION", "42704", `unrecognized configuration parameter "`+name+`"`)
			}
			continue
		}
		run(fixture.Exec("GRANT ALL ON PARAMETER "+names[0]+" TO og_object_reader WITH GRANT OPTION", 0))
		if b.PG {
			acl := "{stuntgres=sA/stuntgres,og_object_reader=s*A*/stuntgres}"
			run(fixture.Select("SELECT parname,paracl::TEXT AS acl FROM pg_parameter_acl WHERE parname='"+names[1]+"'", []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, [][]any{{names[1], acl}}))
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("GRANT SET,ALTER SYSTEM ON PARAMETER "+names[1]+" TO og_object_other", 0))
		})
		run(fixture.Exec("REVOKE ALL ON PARAMETER "+names[1]+" FROM og_object_reader CASCADE", 0))
	}
	// Real-only supplemental catalog evidence, not a new embedded API contract.
	if b.PG {
		run(fixture.Select("SELECT parname FROM pg_parameter_acl WHERE parname IN ('lc_messages','work_mem','og_custom.value') ORDER BY parname", []string{"parname"}, []string{"TEXT"}, [][]any{}))
		run(fixture.Select("SELECT current_setting('og_custom.value',true) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{nil}}))
	}
	run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA og_parameter_space FROM og_object_other", 0))
	run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER og_custom.value TO og_object_other", 0))
	run(fixture.RoleReferenceError("DROP ROLE og_object_other", "2BP01", `role "og_object_other" cannot be dropped because some objects depend on it`, "privileges for parameter og_custom.value", prepared, "execute"))
	run(fixture.Exec("REVOKE ALL ON PARAMETER og_custom.value FROM og_object_other", 0))
	run(fixture.Exec("DROP ROLE og_object_other", 0))
	run(fixture.Exec("CREATE ROLE og_object_other", 0))
	run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_parameter_space TO og_object_other", 0))
}

func objectGrantTablespaceACL(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step), prepared bool) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	run(fixture.Select("SELECT oid,spcname,spcacl FROM pg_tablespace WHERE FALSE", []string{"oid", "spcname", "spcacl"}, []string{"OID", "NAME", "_ACLITEM"}, [][]any{}))
	for _, name := range []string{"pg_default", "pg_global"} {
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "GRANT CREATE ON TABLESPACE "+name+" TO og_object_other", "tablespace", name)
		})
		run(fixture.Exec("GRANT CREATE ON TABLESPACE "+name+" TO og_object_reader", 0))
		var notices []fixture.Diagnostic
		_, nc, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RoleGrantNoticeExec(t, p, nc, record, prepared, &notices, "SET SESSION AUTHORIZATION og_object_reader")
		fixture.RoleGrantNoticeExec(t, p, nc, record, prepared, &notices, "GRANT CREATE ON TABLESPACE "+name+" TO og_object_other",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "` + name + `"`})
		fixture.RoleGrantNoticeExec(t, p, nc, record, prepared, &notices, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("GRANT ALL PRIVILEGES ON TABLESPACE "+name+" TO og_object_reader WITH GRANT OPTION", 0))
		acl := "{" + owner + "=C/" + owner + ",og_object_reader=C*/" + owner + "}"
		run(fixture.Select("SELECT spcacl::TEXT AS acl FROM pg_tablespace WHERE spcname='"+name+"'", []string{"acl"}, []string{"TEXT"}, [][]any{{acl}}))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("GRANT CREATE ON TABLESPACE "+name+" TO og_object_other", 0))
		})
		run(fixture.Exec("REVOKE GRANT OPTION FOR CREATE ON TABLESPACE "+name+" FROM og_object_reader CASCADE", 0))
		acl = "{" + owner + "=C/" + owner + ",og_object_reader=C/" + owner + "}"
		run(fixture.Select("SELECT spcacl::TEXT AS acl FROM pg_tablespace WHERE spcname='"+name+"'", []string{"acl"}, []string{"TEXT"}, [][]any{{acl}}))
		run(fixture.Exec("REVOKE ALL ON TABLESPACE "+name+" FROM og_object_reader", 0))
	}
	run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA og_parameter_space FROM og_object_other", 0))
	run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO og_object_other", 0))
	run(fixture.RoleReferenceError("DROP ROLE og_object_other", "2BP01", `role "og_object_other" cannot be dropped because some objects depend on it`, "privileges for tablespace pg_global", prepared, "execute"))
	run(fixture.Exec("DROP OWNED BY og_object_other", 0))
	run(fixture.Exec("DROP ROLE og_object_other", 0))
	run(fixture.Exec("CREATE ROLE og_object_other", 0))
	run(fixture.Select("SELECT oid::bigint AS oid,spcname FROM pg_tablespace WHERE spcname IN ('pg_default','pg_global') ORDER BY oid", []string{"oid", "spcname"}, []string{"INT8", "NAME"}, [][]any{{int64(1663), "pg_default"}, {int64(1664), "pg_global"}}))
}

func objectGrantParameterSystemBoundary(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
	t.Helper()
	if b.PG {
		// Keep the exact pre-test file, including unrelated fixture settings.
		// No reload occurs: the physical file operation is the actual mechanism.
		objectGrantSpaceContainer(t, p, `test ! -e /tmp/og_parameter_auto_backup && cp "$PGDATA/postgresql.auto.conf" /tmp/og_parameter_auto_backup`)
		defer objectGrantSpaceContainer(t, p, `cp /tmp/og_parameter_auto_backup "$PGDATA/postgresql.auto.conf" && chown postgres:postgres "$PGDATA/postgresql.auto.conf" && rm /tmp/og_parameter_auto_backup`)
	}
	run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER lc_messages TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		q := "ALTER SYSTEM SET lc_messages='C'"
		if b.PG {
			run(fixture.Exec(q, 0))
		} else {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "SYSTEM"`, Position: "7"})
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
			s.Query = "ALTER SYSTEM RESET lc_messages"
			run(s)
		}
	})
	if b.PG {
		run(fixture.Select("SELECT name,setting FROM pg_file_settings WHERE name='lc_messages' AND sourcefile=current_setting('data_directory')||'/postgresql.auto.conf'", []string{"name", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"lc_messages", "C"}}))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("ALTER SYSTEM RESET lc_messages", 0)) })
		run(fixture.Select("SELECT name,setting FROM pg_file_settings WHERE name='lc_messages' AND sourcefile=current_setting('data_directory')||'/postgresql.auto.conf'", []string{"name", "setting"}, []string{"TEXT", "TEXT"}, [][]any{}))
	}
	run(fixture.Exec("REVOKE ALTER SYSTEM ON PARAMETER lc_messages FROM og_object_reader", 0))
	if b.PG {
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantError(run, "ALTER SYSTEM SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
			fixture.RoleGrantError(run, "ALTER SYSTEM RESET lc_messages", "42501", `permission denied to set parameter "lc_messages"`)
		})
	}
}

func objectGrantTablespacePlacement(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
	t.Helper()
	const directory = "/tmp/og_parameter_tablespace_owned"
	const create = "CREATE TABLESPACE og_parameter_physical LOCATION '" + directory + "'"
	if !b.PG {
		fixture.RoleGrantError(run, create, "0A000", "CREATE TABLESPACE physical locations are unsupported in embedded mode")
		run(fixture.Select("SELECT spcname FROM pg_tablespace WHERE spcname='og_parameter_physical'", []string{"spcname"}, []string{"NAME"}, [][]any{}))
		q := "CREATE TABLE og_parameter_space.placed(x INT) TABLESPACE pg_default"
		// Ordinary CREATE TABLE has no placement grammar. The existing
		// "table tablespaces are unsupported" guard belongs to CREATE TABLE AS.
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "TABLESPACE"`, Position: fixture.Position(strings.Index(q, "TABLESPACE") + 1)})
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
		return
	}
	objectGrantSpaceContainer(t, p, "test ! -e "+directory+" && mkdir "+directory+" && chown postgres:postgres "+directory)
	defer objectGrantSpaceContainer(t, p, "rmdir "+directory)
	defer func() {
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP TABLE IF EXISTS og_parameter_space.revoke_denied", "DROP TABLE IF EXISTS og_parameter_space.placed", "DROP TABLESPACE IF EXISTS og_parameter_physical"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error("physical tablespace cleanup", q, err)
			}
		}
	}()
	run(fixture.Exec(create, 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_parameter_space.placed(x INT) TABLESPACE og_parameter_physical", "tablespace", "og_parameter_physical")
	})
	run(fixture.Exec("GRANT CREATE ON TABLESPACE og_parameter_physical TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		run(fixture.Exec("CREATE TABLE og_parameter_space.placed(x INT) TABLESPACE og_parameter_physical", 0))
		run(fixture.Exec("INSERT INTO og_parameter_space.placed VALUES(42)", 1))
		run(fixture.Select("SELECT x FROM og_parameter_space.placed", []string{"x"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
	run(fixture.Exec("REVOKE CREATE ON TABLESPACE og_parameter_physical FROM og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_parameter_space.revoke_denied(x INT) TABLESPACE og_parameter_physical", "tablespace", "og_parameter_physical")
		run(fixture.Select("SELECT x FROM og_parameter_space.placed", []string{"x"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
	run(fixture.Select("SELECT t.spcname FROM pg_class c JOIN pg_tablespace t ON t.oid=c.reltablespace WHERE c.oid='og_parameter_space.placed'::regclass", []string{"spcname"}, []string{"NAME"}, [][]any{{"og_parameter_physical"}}))
	run(fixture.Select("SELECT relname FROM pg_class WHERE relname='revoke_denied' AND relnamespace='og_parameter_space'::regnamespace", []string{"relname"}, []string{"NAME"}, [][]any{}))
}

// The existing public fixture Container facility owns the filesystem namespace;
// fixed scenario-private paths are never host paths or external providers.
func objectGrantSpaceContainer(t *testing.T, p *fixture.Pair, script string) {
	t.Helper()
	code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("object grant fixture command exit %d: %s", code, body)
	}
}
