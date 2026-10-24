package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type transactionalPrivilegeFamily struct {
	name           string
	setup, cleanup []string
	grant, revoke  string
	query          string
	drop           string
	columns, types []string
	value          any
	denied         fixture.Diagnostic
}

func TestReferenceTransactionalPrivilegesFamilies(t *testing.T) {
	transactionalPrivilegesLanguageDeclaration(t)
	transactionalPrivilegesConnect(t)
	transactionalPrivilegesWarnings(t)
	transactionalPrivilegesProcedureBoundary(t)
	cases := []transactionalPrivilegeFamily{
		{name: "table", setup: []string{"CREATE TABLE tx_family(a INT,b INT)", "INSERT INTO tx_family VALUES(7,9)"}, grant: "SELECT ON tx_family", query: "SELECT a FROM tx_family", columns: []string{"a"}, types: []string{"INT4"}, value: int64(7), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for table tx_family"}},
		{name: "column", setup: []string{"CREATE TABLE tx_family(a INT,b INT)", "INSERT INTO tx_family VALUES(7,9)"}, grant: "SELECT(a) ON tx_family", query: "SELECT a FROM tx_family", columns: []string{"a"}, types: []string{"INT4"}, value: int64(7), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for table tx_family"}},
		{name: "schema-usage", setup: []string{"CREATE SCHEMA tx_family_ns", "REVOKE ALL ON SCHEMA tx_family_ns FROM PUBLIC", "CREATE TABLE tx_family_ns.t(a INT)", "INSERT INTO tx_family_ns.t VALUES(7)", "GRANT SELECT ON tx_family_ns.t TO og_object_reader"}, grant: "USAGE ON SCHEMA tx_family_ns", query: "SELECT a FROM tx_family_ns.t", columns: []string{"a"}, types: []string{"INT4"}, value: int64(7), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for schema tx_family_ns", Position: fixture.Position(15)}},
		{name: "schema-create", setup: []string{"CREATE SCHEMA tx_family_ns", "GRANT USAGE ON SCHEMA tx_family_ns TO og_object_reader"}, grant: "CREATE ON SCHEMA tx_family_ns", query: "CREATE TABLE tx_family_ns.consumer(a INT)", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for schema tx_family_ns", Position: fixture.Position(14)}},
		{name: "sequence", setup: []string{"CREATE SEQUENCE tx_family START 100"}, grant: "USAGE ON SEQUENCE tx_family", query: "SELECT nextval('tx_family') AS v", columns: []string{"v"}, types: []string{"INT8"}, value: int64(100), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence tx_family"}},
		{name: "type", setup: []string{"CREATE TYPE tx_family AS ENUM ('a','b')", "REVOKE ALL ON TYPE tx_family FROM PUBLIC", "GRANT CREATE ON SCHEMA public TO og_object_reader"}, grant: "USAGE ON TYPE tx_family", query: "CREATE TABLE tx_family_consumer(a tx_family)", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for type tx_family"}},
		{name: "domain", setup: []string{"CREATE DOMAIN tx_family AS INT", "REVOKE ALL ON DOMAIN tx_family FROM PUBLIC", "GRANT CREATE ON SCHEMA public TO og_object_reader"}, grant: "USAGE ON DOMAIN tx_family", query: "CREATE TABLE tx_family_consumer(a tx_family)", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for type tx_family"}},
		{name: "routine", setup: []string{"REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC"}, cleanup: []string{"GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC", "REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader"}, grant: "EXECUTE ON FUNCTION pg_catalog.abs(integer)", query: "SELECT abs($1::integer) AS v", columns: []string{"v"}, types: []string{"INT4"}, value: int64(7), denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for function abs"}},
		{name: "parameter", cleanup: []string{"REVOKE ALL ON PARAMETER lc_messages FROM og_object_reader"}, grant: "SET ON PARAMETER lc_messages", query: "SET lc_messages='C'", denied: fixture.Diagnostic{Code: "42501", Message: `permission denied to set parameter "lc_messages"`}},
		{name: "language", setup: []string{"REVOKE USAGE ON LANGUAGE sql FROM PUBLIC"}, cleanup: []string{"GRANT USAGE ON LANGUAGE sql TO PUBLIC", "REVOKE ALL ON LANGUAGE sql FROM og_object_reader CASCADE"}, grant: "USAGE ON LANGUAGE sql", query: "GRANT USAGE ON LANGUAGE sql TO og_object_other", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for language sql"}},
		{name: "tablespace", cleanup: []string{"REVOKE ALL ON TABLESPACE pg_default FROM og_object_reader CASCADE"}, grant: "CREATE ON TABLESPACE pg_default", query: "GRANT CREATE ON TABLESPACE pg_default TO og_object_other", denied: fixture.Diagnostic{Code: "42501", Message: "permission denied for tablespace pg_default"}},
	}
	cases = append(cases, transactionalPrivilegeAdditionalFamilies()...)
	for _, class := range []string{"TABLES", "SEQUENCES", "FUNCTIONS", "ROUTINES"} {
		c := transactionalPrivilegeFamily{name: "all-" + class, setup: []string{"CREATE SCHEMA tx_family_ns"}}
		switch class {
		case "TABLES":
			c.setup = append(c.setup, "CREATE TABLE tx_family_ns.t(a INT)", "INSERT INTO tx_family_ns.t VALUES(7)", "GRANT USAGE ON SCHEMA tx_family_ns TO og_object_reader")
			c.grant = "SELECT ON ALL TABLES IN SCHEMA tx_family_ns"
			c.query = "SELECT a FROM tx_family_ns.t"
			c.columns = []string{"a"}
			c.types = []string{"INT4"}
			c.value = int64(7)
			c.denied = fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}
		case "SEQUENCES":
			c.setup = append(c.setup, "CREATE SEQUENCE tx_family_ns.s START 100", "GRANT USAGE ON SCHEMA tx_family_ns TO og_object_reader")
			c.grant = "USAGE ON ALL SEQUENCES IN SCHEMA tx_family_ns"
			c.query = "SELECT nextval('tx_family_ns.s') AS v"
			c.columns = []string{"v"}
			c.types = []string{"INT8"}
			c.value = int64(100)
			c.denied = fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence s"}
		default:
			c.setup = append(c.setup, "CREATE FUNCTION tx_family_ns.f(x INT) RETURNS INT LANGUAGE SQL RETURN x", "REVOKE EXECUTE ON FUNCTION tx_family_ns.f(integer) FROM PUBLIC", "GRANT USAGE ON SCHEMA tx_family_ns TO og_object_reader")
			c.grant = "EXECUTE ON ALL " + class + " IN SCHEMA tx_family_ns"
			c.query = "SELECT tx_family_ns.f($1::integer) AS v"
			c.columns = []string{"v"}
			c.types = []string{"INT4"}
			c.value = int64(-7)
			c.denied = fixture.Diagnostic{Code: "42501", Message: "permission denied for function f"}
		}
		cases = append(cases, c)
	}
	for _, family := range cases {
		for _, control := range []string{"autocommit", "sql", "go"} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held=%v", family.name, control, held), func(t *testing.T) {
					objectGrantScenario(t, []string{"tx_family_ns", "tx_family_dbcreate"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ func(fixture.Step), _ bool) {
						family := family
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						defer func() {
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
							defer cancel()
							c.ExecContext(ctx, "ROLLBACK")
							c.ExecContext(ctx, "RESET ROLE")
							peer.ExecContext(ctx, "RESET ROLE")
							for _, q := range family.cleanup {
								if _, err := c.ExecContext(ctx, q); err != nil {
									t.Error(q, err)
								}
							}
						}()
						run(c, fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
						transactionalPrivilegeConfigure(t, p, c, &family, func(s fixture.Step) { run(c, s) })
						for _, q := range family.setup {
							n := int64(0)
							if len(q) > 6 && q[:6] == "INSERT" {
								n = 1
							}
							run(c, fixture.Exec(q, n))
						}
						grant := "GRANT " + family.grant + " TO og_object_reader"
						if family.name == "language" || family.name == "tablespace" {
							grant += " WITH GRANT OPTION"
						}
						revoke := "REVOKE " + family.grant + " FROM og_object_reader CASCADE"
						var gs, rs *sql.Stmt
						if held {
							gs, err = c.PrepareContext(p.Ctx, grant)
							if err != nil {
								t.Fatal(err)
							}
							defer gs.Close()
							rs, err = c.PrepareContext(p.Ctx, revoke)
							if err != nil {
								t.Fatal(err)
							}
							defer rs.Close()
						}
						next := int64(100)
						var consumer *sql.Stmt
						defer func() {
							if consumer != nil {
								consumer.Close()
							}
						}()
						holdConsumer := func() {
							if !held || family.columns == nil {
								return
							}
							var err error
							consumer, err = peer.PrepareContext(p.Ctx, family.query)
							if err != nil {
								t.Fatal("held authorized consumer", err)
							}
						}
						probe := func(e fixture.Executor, allow bool) {
							// Delegating from a peer would update the same catalog tuple held by
							// the owner, turning a visibility assertion into a lock deadlock.
							// Peer inquiries are nonmutating; real delegation remains owner-private.
							if e == peer && (family.name == "language" || family.name == "tablespace") {
								q := transactionalPrivilegeACLProbe(family.name, "og_object_reader", true)
								run(e, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{allow}}))
								return
							}
							args := []any(nil)
							if family.name == "routine" || family.name == "all-FUNCTIONS" || family.name == "all-ROUTINES" {
								args = []any{-7}
							}
							if consumer != nil && e == peer {
								e = fixture.HeldStatement{Executor: e, Statement: consumer, SQL: family.query}
							}
							if !allow {
								diagnostic := transactionalPrivilegeDenied(t, p, e, family)
								s := fixture.ImplicitError(family.query, diagnostic, args...)
								s.SelectRows = family.columns != nil
								run(e, s)
								return
							}
							if family.columns != nil {
								value := family.value
								if family.name == "sequence" || family.name == "all-SEQUENCES" {
									value = next
									next++
								}
								run(e, fixture.Select(family.query, family.columns, family.types, [][]any{{value}}, args...))
							} else {
								run(e, fixture.Exec(family.query, 0))
								if family.name == "language" || family.name == "tablespace" {
									run(e, fixture.Select(transactionalPrivilegeACLProbe(family.name, "og_object_other", false), []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
								}
								if family.drop != "" {
									run(e, fixture.Exec(family.drop, 0))
								}
								if family.name == "schema-create" {
									run(e, fixture.Exec("DROP TABLE tx_family_ns.consumer", 0))
								}
								if family.name == "type" || family.name == "domain" {
									run(e, fixture.Exec("DROP TABLE tx_family_consumer", 0))
								}
							}
							if family.name == "large-object-write" {
								run(e, fixture.Select("SELECT encode(lo_get(735492::oid),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"6364"}}))
							}
						}
						run(peer, fixture.Exec("SET ROLE og_object_reader", 0))
						probe(peer, false)
						if control == "autocommit" {
							var source fixture.Executor = c
							if held {
								source = fixture.HeldStatement{Executor: c, Statement: gs, SQL: grant}
							}
							run(source, fixture.Exec(grant, 0))
							holdConsumer()
							probe(peer, true)
							source = c
							if held {
								source = fixture.HeldStatement{Executor: c, Statement: rs, SQL: revoke}
							}
							run(source, fixture.Exec(revoke, 0))
							probe(peer, false)
							return
						}
						for _, commit := range []bool{false, true} {
							var e fixture.Executor = c
							finish := func() { run(c, fixture.Exec(map[bool]string{false: "ROLLBACK", true: "COMMIT"}[commit], 0)) }
							if control == "go" {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								finish = func() {
									var err error
									if commit {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								}
							} else {
								run(c, fixture.Exec("BEGIN", 0))
							}
							source := e
							if held {
								source = fixture.HeldStatement{Executor: e, Statement: gs, SQL: grant}
							}
							run(source, fixture.Exec(grant, 0))
							run(e, fixture.Exec("SET ROLE og_object_reader", 0))
							probe(e, true)
							run(e, fixture.Exec("RESET ROLE", 0))
							probe(peer, false)
							run(e, fixture.Exec("SAVEPOINT acl_mark", 0))
							source = e
							if held {
								source = fixture.HeldStatement{Executor: e, Statement: rs, SQL: revoke}
							}
							run(source, fixture.Exec(revoke, 0))
							run(e, fixture.Exec("SET ROLE og_object_reader", 0))
							probe(e, false)
							run(e, fixture.Exec("ROLLBACK TO acl_mark", 0))
							run(e, fixture.Exec("ROLLBACK TO acl_mark", 0))
							run(e, fixture.Exec("SET ROLE og_object_reader", 0))
							probe(e, true)
							run(e, fixture.Exec("RESET ROLE", 0))
							run(e, fixture.Exec("RELEASE acl_mark", 0))
							finish()
							if commit {
								holdConsumer()
							}
							probe(peer, commit)
						}
						// Private revoke is not peer-visible; rolling it back restores permission.
						run(c, fixture.Exec("BEGIN", 0))
						run(c, fixture.Exec(revoke, 0))
						probe(peer, true)
						run(c, fixture.Exec("SAVEPOINT revoke_probe", 0))
						run(c, fixture.Exec("SET ROLE og_object_reader", 0))
						probe(c, false)
						run(c, fixture.Exec("ROLLBACK TO revoke_probe", 0))
						run(c, fixture.Exec("ROLLBACK", 0))
						probe(peer, true)
						run(c, fixture.Exec("BEGIN", 0))
						run(c, fixture.Exec(revoke, 0))
						run(c, fixture.Exec("COMMIT", 0))
						probe(peer, false)
					})
				})
			}
		}
	}
}
