package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalPrivilegeAdditionalFamilies() []transactionalPrivilegeFamily {
	return []transactionalPrivilegeFamily{
		{name: "sequence-select", setup: []string{"CREATE SEQUENCE tx_family START 100"}, grant: "SELECT ON SEQUENCE tx_family", query: "SELECT last_value FROM tx_family", columns: []string{"last_value"}, types: []string{"INT8"}, value: int64(100), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence tx_family"}},
		{name: "sequence-column", setup: []string{"CREATE SEQUENCE tx_family START 100"}, grant: "SELECT(last_value) ON TABLE tx_family", query: "SELECT last_value FROM tx_family", columns: []string{"last_value"}, types: []string{"INT8"}, value: int64(100), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence tx_family"}},
		{name: "sequence-update", setup: []string{"CREATE SEQUENCE tx_family START 100"}, grant: "UPDATE ON SEQUENCE tx_family", query: "SELECT setval('tx_family',123) AS v", columns: []string{"v"}, types: []string{"INT8"}, value: int64(123), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence tx_family"}},
		{name: "large-object-read", grant: "SELECT ON LARGE OBJECT 735492", query: "SELECT encode(lo_get(735492::oid),'hex') AS v", columns: []string{"v"}, types: []string{"TEXT"}, value: "6162", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for large object 735492"}},
		{name: "large-object-write", grant: "UPDATE ON LARGE OBJECT 735492", query: "SELECT lo_put(735492::oid,0::bigint,decode('6364','hex')) IS NULL AS v", columns: []string{"v"}, types: []string{"BOOL"}, value: false, denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for large object 735492"}},
		{name: "database-create", query: "CREATE SCHEMA tx_family_dbcreate", drop: "DROP SCHEMA tx_family_dbcreate"},
		{name: "database-temp", query: "CREATE TEMP TABLE tx_family_temp(a INT)", drop: "DROP TABLE tx_family_temp"},
		{name: "foreign-wrapper", setup: []string{"CREATE FOREIGN DATA WRAPPER tx_family_fdw NO HANDLER NO VALIDATOR"}, cleanup: []string{"DROP FOREIGN DATA WRAPPER IF EXISTS tx_family_fdw CASCADE"}, grant: "USAGE ON FOREIGN DATA WRAPPER tx_family_fdw", query: "CREATE SERVER tx_family_consumer FOREIGN DATA WRAPPER tx_family_fdw", drop: "DROP SERVER tx_family_consumer", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for foreign-data wrapper tx_family_fdw"}},
		{name: "foreign-server", setup: []string{"CREATE FOREIGN DATA WRAPPER tx_family_fdw NO HANDLER NO VALIDATOR", "CREATE SERVER tx_family_server FOREIGN DATA WRAPPER tx_family_fdw"}, cleanup: []string{"DROP SERVER IF EXISTS tx_family_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS tx_family_fdw CASCADE"}, grant: "USAGE ON FOREIGN SERVER tx_family_server", query: "CREATE USER MAPPING FOR CURRENT_USER SERVER tx_family_server", drop: "DROP USER MAPPING FOR CURRENT_USER SERVER tx_family_server", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for foreign server tx_family_server"}},
	}
}

func transactionalPrivilegeConfigure(t *testing.T, p *fixture.Pair, c *sql.Conn, f *transactionalPrivilegeFamily, run func(fixture.Step)) {
	t.Helper()
	if strings.HasPrefix(f.name, "large-object") {
		run(fixture.Select("SELECT lo_from_bytea(735492::oid,decode('6162','hex'))::bigint AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(735492)}}))
		f.cleanup = append(f.cleanup, "SELECT lo_unlink(735492::oid)")
		if f.name == "large-object-write" {
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 735492 TO og_object_reader", 0))
		}
	}
	if strings.HasPrefix(f.name, "database-") {
		_, database := fixture.RoleGrantEnvironment(t, p, c)
		quoted := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
		f.grant = "CREATE ON DATABASE " + quoted
		f.denied = fixture.Diagnostic{Code: "42501", Message: "permission denied for database " + database}
		f.cleanup = append(f.cleanup, "REVOKE ALL ON DATABASE "+quoted+" FROM og_object_reader CASCADE")
		if f.name == "database-temp" {
			public := objectGrantDatabasePublic(t, p.Ctx, c)
			run(fixture.Exec("REVOKE TEMPORARY ON DATABASE "+quoted+" FROM PUBLIC", 0))
			for _, priv := range public {
				if priv == "TEMPORARY" || priv == "TEMP" {
					f.cleanup = append(f.cleanup, "GRANT TEMPORARY ON DATABASE "+quoted+" TO PUBLIC")
				}
			}
			f.grant = "TEMPORARY ON DATABASE " + quoted
			// First TEMP denial has the database diagnostic; after successful allocation
			// the same physical owner's temp namespace remains and has schema authority.
			f.denied = fixture.Diagnostic{Code: "42501", Message: `permission denied to create temporary tables in database "` + database + `"`, Position: fixture.Position(19)}
		}
	}
}

func transactionalPrivilegesProcedureBoundary(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("all-procedures/goTx=%v/held=%v", goTx, held), func(t *testing.T) {
				objectGrantScenario(t, []string{"tx_empty_procedures"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), _ bool) {
					run(fixture.Exec("CREATE SCHEMA tx_empty_procedures", 0))
					run(fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace='tx_empty_procedures'::regnamespace AND prokind='p'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					for _, command := range []string{"GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA tx_empty_procedures TO og_object_reader", "REVOKE EXECUTE ON ALL PROCEDURES IN SCHEMA tx_empty_procedures FROM og_object_reader"} {
						stmt, err := c.PrepareContext(p.Ctx, command)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for _, control := range []string{"autocommit", "ROLLBACK", "COMMIT"} {
							var e fixture.Executor = c
							finish := func() {}
							if control != "autocommit" {
								if goTx {
									tx, err := c.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									e = tx
									finish = func() {
										var err error
										if control == "COMMIT" {
											err = tx.Commit()
										} else {
											err = tx.Rollback()
										}
										if err != nil {
											t.Fatal(err)
										}
									}
								} else {
									run(fixture.Exec("BEGIN", 0))
									finish = func() { run(fixture.Exec(control, 0)) }
								}
							}
							if held {
								e = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: command}
							}
							fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(command, 0))
							finish()
						}
					}
				})
			})
		}
	}
}

func transactionalPrivilegesWarnings(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE SEQUENCE tx_warning START 100", 0))
		run(fixture.Exec("GRANT USAGE ON SEQUENCE tx_warning TO og_object_reader", 0))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, nc, recorder, s) }
		nrun(fixture.Exec("SET ROLE og_object_reader", 0))
		nrun(fixture.Exec("BEGIN", 0))
		defer nc.ExecContext(p.Ctx, "ROLLBACK")
		nrun(fixture.Exec("SAVEPOINT warning_mark", 0))
		q := "GRANT SELECT,UPDATE(last_value) ON TABLE tx_warning TO og_object_other"
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, q,
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "tx_warning"`},
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: `sequence "tx_warning" only supports SELECT column privileges`})
		// Named targets are all resolved before ACL traversal; the missing second
		// relation prevents reaching either warning from the otherwise-valid first.
		nrun(fixture.Exec("ROLLBACK TO warning_mark", 0))
		notices = nil
		late := "GRANT SELECT,UPDATE(last_value) ON TABLE tx_warning,tx_missing TO og_object_other"
		s := fixture.ImplicitError(late, fixture.Diagnostic{Code: "42P01", Message: `relation "tx_missing" does not exist`})
		s.Prepared = prepared
		nrun(s)
		if len(notices) != 0 {
			t.Fatalf("missing-target statement reached notices: %+v", notices)
		}
		nrun(fixture.Exec("ROLLBACK TO warning_mark", 0))
		nrun(fixture.Exec("ROLLBACK", 0))
		nrun(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE SEQUENCE tx_warning_other START 100", 0))
		run(fixture.Exec("GRANT SELECT ON SEQUENCE tx_warning TO og_object_reader WITH GRANT OPTION", 0))
		nrun(fixture.Exec("SET ROLE og_object_reader", 0))
		nrun(fixture.Exec("BEGIN", 0))
		nrun(fixture.Exec("SAVEPOINT reached_mark", 0))
		notices = nil
		late = "GRANT SELECT,UPDATE(last_value) ON TABLE tx_warning,tx_warning_other TO og_object_other"
		s = fixture.ImplicitError(late, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence tx_warning_other"})
		s.Prepared = prepared
		nrun(s)
		want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: `sequence "tx_warning" only supports SELECT column privileges`}
		if len(notices) != 1 || notices[0] != want {
			t.Fatalf("reached warnings %+v want %+v", notices, want)
		}
		nrun(fixture.Exec("ROLLBACK TO reached_mark", 0))
		nrun(fixture.Exec("COMMIT", 0))
		nrun(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE og_object_other", 0))
		run(fixture.ImplicitError("SELECT last_value FROM tx_warning", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence tx_warning"}))
		run(fixture.Exec("RESET ROLE", 0))
	})
}

func transactionalPrivilegeDenied(t *testing.T, p *fixture.Pair, e fixture.Executor, f transactionalPrivilegeFamily) fixture.Diagnostic {
	t.Helper()
	d := f.denied
	if f.name != "database-temp" {
		return d
	}
	rows, err := e.QueryContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		d.Message = "permission denied for schema " + name
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	return d
}

func transactionalPrivilegeACLProbe(family, grantee string, options bool) string {
	source := "pg_language o CROSS JOIN LATERAL aclexplode(o.lanacl) a"
	target := "o.lanname='sql' AND a.privilege_type='USAGE'"
	if family == "tablespace" {
		source = "pg_tablespace o CROSS JOIN LATERAL aclexplode(o.spcacl) a"
		target = "o.spcname='pg_default' AND a.privilege_type='CREATE'"
	}
	query := "SELECT count(*) > 0 AS v FROM " + source + " JOIN pg_roles r ON r.oid=a.grantee JOIN pg_roles g ON g.oid=a.grantor WHERE " + target + " AND r.rolname='" + grantee + "'"
	if options {
		query += " AND a.is_grantable"
	} else {
		query += " AND g.rolname='og_object_reader'"
	}
	return query
}
