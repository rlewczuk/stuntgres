package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalPrivilegesDefaultConsumers(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, class := range []string{"sequences", "serial", "identity", "schemas", "large objects", "routines", "types", "partition", "ctas"} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("creation/%s/goTx=%v/commit=%v", class, goTx, commit), func(t *testing.T) {
					dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
						// The issuer is bootstrap but explicit schema defaults belong to the named
						// actual owner; the creator-only rule must not borrow issuer policy.
						policy := "ALTER DEFAULT PRIVILEGES GRANT SELECT,INSERT ON TABLES TO dp_reader"
						if class == "sequences" || class == "serial" || class == "identity" {
							policy = "ALTER DEFAULT PRIVILEGES GRANT USAGE,SELECT,UPDATE ON SEQUENCES TO dp_reader"
						}
						if class == "schemas" {
							policy = "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT USAGE,CREATE ON SCHEMAS TO dp_reader"
						}
						if class == "large objects" {
							policy = "ALTER DEFAULT PRIVILEGES GRANT SELECT,UPDATE ON LARGE OBJECTS TO dp_reader"
						}
						if class == "routines" {
							run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON ROUTINES FROM PUBLIC", 0))
							policy = "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON ROUTINES TO dp_reader"
						}
						if class == "types" {
							run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON TYPES FROM PUBLIC", 0))
							run(fixture.Exec("GRANT CREATE ON SCHEMA public TO dp_reader", 0))
							policy = "ALTER DEFAULT PRIVILEGES GRANT USAGE ON TYPES TO dp_reader"
						}
						utility, err := c.PrepareContext(p.Ctx, policy)
						if err != nil {
							t.Fatal(err)
						}
						defer utility.Close()
						base := run
						var e fixture.Executor = c
						finish := func() { base(fixture.Exec(map[bool]string{true: "COMMIT", false: "ROLLBACK"}[commit], 0)); run = base }
						if goTx {
							tx, err := c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
							run = func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
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
								run = base
							}
						} else {
							base(fixture.Exec("BEGIN", 0))
							defer c.ExecContext(p.Ctx, "ROLLBACK")
						}
						source := e
						if prepared {
							source = fixture.HeldStatement{Executor: e, Statement: utility, SQL: policy}
						}
						fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(policy, 0))
						create := func() {
							switch class {
							case "sequences":
								run(fixture.Exec("CREATE SEQUENCE dp_private_seq START 100", 0))
							case "serial":
								run(fixture.Exec("CREATE TABLE dp_private_serial(id SERIAL,x INT)", 0))
								run(fixture.Exec("GRANT INSERT,SELECT ON dp_private_serial TO dp_reader", 0))
							case "identity":
								run(fixture.Exec("CREATE TABLE dp_private_identity(id INT GENERATED ALWAYS AS IDENTITY,x INT)", 0))
								run(fixture.Exec("GRANT INSERT,SELECT ON dp_private_identity TO dp_reader", 0))
							case "schemas":
								run(fixture.Exec("CREATE SCHEMA dp_owner_ns AUTHORIZATION dp_owner", 0))
							case "large objects":
								run(fixture.Select("SELECT lo_create(735491::oid)::bigint AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(735491)}}))
								run(fixture.Select("SELECT lo_put(735491::oid,0::bigint,decode('6162','hex')) IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
							case "routines":
								run(fixture.Exec("CREATE FUNCTION dp_private_function(x INT) RETURNS INT LANGUAGE SQL RETURN x", 0))
							case "types":
								run(fixture.Exec("CREATE DOMAIN dp_private_domain AS INT", 0))
							case "partition":
								run(fixture.Exec("CREATE TABLE dp_private_parent(x INT) PARTITION BY RANGE(x)", 0))
								run(fixture.Exec("CREATE TABLE dp_private_child PARTITION OF dp_private_parent FOR VALUES FROM(0) TO(20)", 0))
								run(fixture.Exec("INSERT INTO dp_private_parent VALUES(7)", 1))
							case "ctas":
								run(fixture.Exec("CREATE TABLE dp_private_ctas AS SELECT 7::integer AS x", 1))
							}
						}
						consume := func(allowed bool) {
							run(fixture.Exec("SET ROLE dp_reader", 0))
							var s fixture.Step
							switch class {
							case "sequences":
								s = fixture.Select("SELECT nextval('dp_private_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}})
							case "serial":
								s = fixture.Exec("INSERT INTO dp_private_serial(x) VALUES(7)", 1)
							case "identity":
								s = fixture.Select("SELECT nextval('dp_private_identity_id_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}})
							case "schemas":
								s = fixture.Exec("CREATE TABLE dp_owner_ns.consumer(x INT)", 0)
							case "large objects":
								s = fixture.Select("SELECT encode(lo_get(735491::oid),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"6162"}})
							case "routines":
								s = fixture.Select("SELECT dp_private_function($1::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, 7)
							case "types":
								s = fixture.Exec("CREATE TABLE dp_private_type_use(x dp_private_domain)", 0)
							case "partition":
								s = fixture.Select("SELECT x FROM dp_private_child", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}})
							case "ctas":
								s = fixture.Select("SELECT x FROM dp_private_ctas", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}})
							}
							if !allowed {
								kind, name := "function", "dp_private_function"
								if class == "types" {
									kind, name = "type", "dp_private_domain"
								}
								denied := fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + kind + " " + name}, s.Args...)
								denied.SelectRows = s.SelectRows
								run(denied)
							} else {
								run(s)
							}
							run(fixture.Exec("RESET ROLE", 0))
						}
						// Independently restricted creators remain autocommit providers. Only their
						// directives move into the block; do not widen CREATE FUNCTION/TYPE gates.
						if class == "routines" || class == "types" {
							finish()
							create()
							consume(commit)
						} else {
							create()
							consume(true)
							if class == "large objects" {
								run(fixture.Select("SELECT lo_put(735491::oid,0::bigint,decode('6364','hex')) IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
							}
							finish()
							name := "dp_private_seq"
							switch class {
							case "serial":
								name = "dp_private_serial"
							case "identity":
								name = "dp_private_identity"
							case "partition":
								name = "dp_private_child"
							case "ctas":
								name = "dp_private_ctas"
							}
							if class == "large objects" {
								n := int64(0)
								if commit {
									n = 1
								}
								run(fixture.Select("SELECT count(*) FROM pg_largeobject_metadata WHERE oid=735491::oid", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
								if commit {
									run(fixture.Select("SELECT encode(lo_get(735491::oid),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"6364"}}))
									run(fixture.Select("SELECT lo_unlink(735491::oid) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
								}
							} else if class == "schemas" {
								n := int64(0)
								if commit {
									n = 1
								}
								run(fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='dp_owner_ns'", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
							} else {
								n := int64(0)
								if commit {
									n = 1
								}
								run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='"+name+"'", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
							}
						}
					})
				})
			}
		}
	}
}
