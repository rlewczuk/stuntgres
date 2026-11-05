package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func databaseAlterScenario(t *testing.T, body func(*databaseCreateCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"da_owner", "da_next", "da_actor", "da_plain", "da_inherit"})
	names := []string{}
	fixture.DatabaseReferenceCleanup(t, &names)
	// DROP refuses templates. Restore admission/template attributes after every
	// selected physical pool closes, before the ordinary database/role teardown.
	t.Cleanup(func() {
		if !fixture.Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			var count int
			if err := fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_database WHERE datname=$1", name).Scan(&count); err != nil {
				t.Error(err)
				continue
			}
			if count == 0 {
				continue
			}
			if _, err := fixture.Admin.ExecContext(ctx, "ALTER DATABASE "+pq.QuoteIdentifier(name)+" IS_TEMPLATE false ALLOW_CONNECTIONS true"); err != nil {
				t.Errorf("restore database attributes %q: %v", name, err)
			}
		}
	})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			defer conn.ExecContext(p.Ctx, "RESET ROLE")
			c := &databaseCreateCase{t, p, backend, conn, &names}
			for _, q := range []string{
				"CREATE ROLE da_owner LOGIN CREATEDB PASSWORD 'role-core-fixture'",
				"CREATE ROLE da_next NOLOGIN NOCREATEDB",
				"CREATE ROLE da_actor LOGIN CREATEDB PASSWORD 'role-core-fixture'",
				"CREATE ROLE da_plain LOGIN NOCREATEDB PASSWORD 'role-core-fixture'",
				"CREATE ROLE da_inherit LOGIN NOCREATEDB PASSWORD 'role-core-fixture'",
			} {
				c.run(fixture.Exec(q, 0))
			}
			body(c)
		})
	}
}

func databaseAlterRun(c *databaseCreateCase, x fixture.Executor, rec *fixture.Recorder, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, x, rec, s)
}

func databaseAlterFlags(c *databaseCreateCase, name string, template, allow bool, limit int64) {
	c.t.Helper()
	c.run(fixture.Select("SELECT datistemplate,datallowconn,datconnlimit,datcollversion FROM pg_database WHERE datname=$1",
		[]string{"datistemplate", "datallowconn", "datconnlimit", "datcollversion"},
		[]string{"BOOL", "BOOL", "INT4", "TEXT"}, [][]any{{template, allow, limit, nil}}, name))
}

func databaseAlterHeld(c *databaseCreateCase, x fixture.Executor, query string, body func(fixture.HeldStatement)) {
	c.t.Helper()
	stmt, err := x.PrepareContext(c.p.Ctx, query)
	if err != nil {
		c.t.Fatal(err)
	}
	defer stmt.Close()
	body(fixture.HeldStatement{Executor: x, Statement: stmt, SQL: query})
}

func databaseAlterStartup(c *databaseCreateCase, name, role, code, message, detail string) {
	c.t.Helper()
	db, raw, _ := c.pool(name, role)
	defer db.Close()
	defer raw.Close()
	err := db.PingContext(c.p.Ctx)
	if code == "" {
		fixture.State(c.t, err, "")
		return
	}
	want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message, Detail: detail}
	fixture.AssertDiagnostic(c.t, err, &want)
}

func databaseAlterSeed(c *databaseCreateCase, name string) int64 {
	c.t.Helper()
	db, raw, rec := c.pool(name, "")
	defer db.Close()
	defer raw.Close()
	conn, err := db.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	defer conn.Close()
	for _, s := range []fixture.Step{
		fixture.Exec("SET ROLE da_owner", 0),
		fixture.Exec("CREATE TABLE da_value(v integer)", 0),
		fixture.Exec("CREATE UNIQUE INDEX da_unique ON da_value(v)", 0),
		fixture.Exec("INSERT INTO da_value VALUES (42)", 1),
		fixture.Exec("CREATE SEQUENCE da_sequence START 41", 0),
		fixture.Select("SELECT nextval('da_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(41)}}),
	} {
		databaseAlterRun(c, conn, rec, s)
	}
	var oid int64
	if err := conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&oid); err != nil {
		c.t.Fatal(err)
	}
	return oid
}

func databaseAlterData(c *databaseCreateCase, name string, oid, next int64) {
	c.t.Helper()
	db, raw, rec := c.pool(name, "")
	defer db.Close()
	defer raw.Close()
	var got int64
	if err := db.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&got); err != nil || got != oid {
		c.t.Fatalf("OID: got %d want %d: %v", got, oid, err)
	}
	databaseAlterRun(c, db, rec, fixture.Select("SELECT v FROM da_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	databaseAlterRun(c, db, rec, fixture.Select("SELECT nextval('da_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{next}}))
	want := databaseCreateDiagnostic("23505", "duplicate key value violates unique constraint \"da_unique\"")
	want.Detail = "Key (v)=(42) already exists."
	want.Schema, want.Table, want.Constraint = "public", "da_value", "da_unique"
	databaseAlterRun(c, db, rec, fixture.ErrorDiagnostic("INSERT INTO da_value VALUES (42)", want))
	databaseAlterRun(c, db, rec, fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname IN ('da_value','da_unique','da_sequence') ORDER BY c.relname", []string{"rolname"}, []string{"NAME"}, [][]any{{"da_owner"}, {"da_owner"}, {"da_owner"}}))
}

func databaseAlterRename(c *databaseCreateCase, old, next string, prepared bool) {
	c.t.Helper()
	c.remember(old)
	c.remember(next)
	s := fixture.Exec("ALTER DATABASE "+pq.QuoteIdentifier(old)+" RENAME TO "+pq.QuoteIdentifier(next), 0)
	if prepared {
		s = fixture.Prepared(s)
	}
	c.run(s)
	c.absent(old)
}

func databaseAlterBusy(c *databaseCreateCase, query, source string, sessions, prepared int) {
	c.t.Helper()
	prefix := "database "
	if source != "" {
		prefix = "source database "
	}
	name := source
	if name == "" {
		name = "da_source"
	}
	want := databaseCreateDiagnostic("55006", prefix+pq.QuoteIdentifier(name)+" is being accessed by other users")
	noun := func(n int, singular string) string {
		if n == 1 {
			return singular
		}
		return singular + "s"
	}
	switch {
	case sessions > 0 && prepared > 0:
		want.Detail = fmt.Sprintf("There are %d other session(s) and %d prepared transaction(s) using the database.", sessions, prepared)
	case sessions > 0:
		verb := "are"
		if sessions == 1 {
			verb = "is"
		}
		want.Detail = fmt.Sprintf("There %s %d other %s using the database.", verb, sessions, noun(sessions, "session"))
	default:
		verb := "are"
		if prepared == 1 {
			verb = "is"
		}
		want.Detail = fmt.Sprintf("There %s %d prepared %s using the database.", verb, prepared, noun(prepared, "transaction"))
	}
	c.run(fixture.ErrorDiagnostic(query, want))
}

func databaseAlterClose(c *databaseCreateCase, pools ...*sql.DB) {
	c.t.Helper()
	for _, db := range pools {
		if err := db.Close(); err != nil {
			c.t.Fatal(err)
		}
	}
}

func databaseAlterBuiltinACLCleanup(c *databaseCreateCase) func() {
	c.t.Helper()
	originals := make(map[string]sql.NullString)
	if c.backend.PG {
		for _, name := range []string{"pg_default", "pg_global"} {
			var acl sql.NullString
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT spcacl::text FROM pg_tablespace WHERE spcname=$1", name).Scan(&acl); err != nil {
				c.t.Fatal(err)
			}
			originals[name] = acl
		}
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, query := range []string{"ROLLBACK", "RESET ROLE", "REVOKE CREATE ON TABLESPACE pg_default,pg_global FROM da_owner,da_plain"} {
			if _, err := c.conn.ExecContext(ctx, query); err != nil {
				c.t.Errorf("builtin ACL cleanup %s: %v", query, err)
			}
		}
		// Revocation removes shared dependencies; restore the oracle's exact nullable
		// catalog value as well, rather than leaving a materialized default ACL.
		for name, acl := range originals {
			var value any
			if acl.Valid {
				value = acl.String
			}
			if _, err := c.conn.ExecContext(ctx, "UPDATE pg_catalog.pg_tablespace SET spcacl=$1::aclitem[] WHERE spcname=$2", value, name); err != nil {
				c.t.Errorf("restore builtin ACL %s: %v", name, err)
			}
		}
	}
}
