package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type transactionalTypeAction struct {
	name, query, probe string
	setup              []string
}

func transactionalTypeActions() []transactionalTypeAction {
	return []transactionalTypeAction{
		{"create_domain", "CREATE DOMAIN tt_new AS varchar(3) DEFAULT 'ok' CHECK(VALUE<>'bad')", "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_new','_tt_new')", nil},
		{"create_composite", "CREATE TYPE tt_new AS(id integer,note text)", "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_new','_tt_new')", nil},
		{"add_check", "ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)", "SELECT count(*) FROM pg_constraint WHERE contypid='tt_d'::regtype AND conname='positive' AND convalidated", nil},
		{"add_unnamed", "ALTER DOMAIN tt_d ADD CHECK(VALUE>0)", "SELECT count(*) FROM pg_constraint WHERE contypid='tt_d'::regtype AND convalidated", nil},
		{"not_valid", "ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", "SELECT count(*) FROM pg_constraint WHERE contypid='tt_d'::regtype AND conname='positive' AND NOT convalidated", nil},
		{"validate", "ALTER DOMAIN tt_d VALIDATE CONSTRAINT positive", "SELECT count(*) FROM pg_constraint WHERE contypid='tt_d'::regtype AND conname='positive' AND convalidated", []string{"ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID"}},
		{"drop_check", "ALTER DOMAIN tt_d DROP CONSTRAINT positive RESTRICT", "SELECT count(*) FROM pg_type WHERE oid='tt_d'::regtype AND NOT EXISTS(SELECT 1 FROM pg_constraint WHERE contypid='tt_d'::regtype)", []string{"ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)"}},
		{"drop_check_cascade", "ALTER DOMAIN tt_d DROP CONSTRAINT IF EXISTS positive CASCADE", "SELECT count(*) FROM pg_type WHERE oid='tt_d'::regtype AND NOT EXISTS(SELECT 1 FROM pg_constraint WHERE contypid='tt_d'::regtype)", []string{"ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)"}},
		{"set_default", "ALTER DOMAIN tt_d SET DEFAULT 9", "SELECT count(*) FROM pg_type WHERE oid='tt_d'::regtype AND typdefault='9'", nil},
		{"drop_default", "ALTER DOMAIN tt_d DROP DEFAULT", "SELECT count(*) FROM pg_type WHERE oid='tt_d'::regtype AND typdefault IS NULL", []string{"ALTER DOMAIN tt_d SET DEFAULT 7"}},
		{"set_not_null", "ALTER DOMAIN tt_d SET NOT NULL", "SELECT count(*) FROM pg_type WHERE oid='tt_d'::regtype AND typnotnull", nil},
		{"drop_not_null", "ALTER DOMAIN tt_d DROP NOT NULL", "SELECT count(*) FROM pg_type WHERE oid='tt_d'::regtype AND NOT typnotnull", []string{"ALTER DOMAIN tt_d SET NOT NULL"}},
		{"domain_rename", "ALTER DOMAIN tt_d RENAME TO tt_new", "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_new','_tt_new')", nil},
		{"domain_move", "ALTER DOMAIN tt_d SET SCHEMA tt_dest", "SELECT count(*) FROM pg_type WHERE typnamespace='tt_dest'::regnamespace AND typname IN ('tt_d','_tt_d')", nil},
		{"add_attribute", "ALTER TYPE tt_pair ADD ATTRIBUTE extra integer CASCADE", "SELECT count(*) FROM pg_attribute WHERE attrelid='tt_typed'::regclass AND attname='extra' AND NOT attisdropped", nil},
		{"rename_attribute", "ALTER TYPE tt_pair RENAME ATTRIBUTE note TO label CASCADE", "SELECT count(*) FROM pg_attribute WHERE attrelid='tt_typed'::regclass AND attname='label' AND NOT attisdropped", nil},
		{"drop_attribute", "ALTER TYPE tt_pair DROP ATTRIBUTE IF EXISTS note CASCADE", "SELECT count(*) FROM pg_attribute WHERE attrelid='tt_typed'::regclass AND attname='id' AND NOT attisdropped", nil},
		{"attribute_type", "ALTER TYPE tt_pair ALTER ATTRIBUTE id SET DATA TYPE bigint CASCADE", "SELECT count(*) FROM pg_attribute WHERE attrelid='tt_typed'::regclass AND attname='id' AND atttypid='int8'::regtype", nil},
		{"attribute_typmod_collate", "ALTER TYPE tt_pair ALTER ATTRIBUTE note TYPE varchar(8) COLLATE \"C\" CASCADE", "SELECT count(*) FROM pg_attribute WHERE attrelid='tt_typed'::regclass AND attname='note' AND atttypmod=12", nil},
		{"multi_action", "ALTER TYPE tt_pair ADD ATTRIBUTE extra boolean CASCADE, ALTER ATTRIBUTE id TYPE bigint CASCADE", "SELECT count(*) FROM pg_attribute WHERE attrelid='tt_typed'::regclass AND attname='extra' AND NOT attisdropped", nil},
		{"type_rename", "ALTER TYPE tt_pair RENAME TO tt_new", "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_new','_tt_new')", nil},
		{"type_move", "ALTER TYPE tt_pair SET SCHEMA tt_dest", "SELECT count(*) FROM pg_type WHERE typnamespace='tt_dest'::regnamespace AND typname IN ('tt_pair','_tt_pair')", nil},
		{"drop_domain", "DROP DOMAIN IF EXISTS tt_d CASCADE", "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_d','_tt_d')", nil},
		{"drop_type", "DROP TYPE IF EXISTS tt_pair CASCADE", "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_pair','_tt_pair')", nil},
	}
}

func TestReferenceTransactionalTypesRoutes(t *testing.T) {
	for _, action := range transactionalTypeActions() {
		for _, route := range []string{"autocommit", "sql", "go"} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held=%v", action.name, route, held), func(t *testing.T) {
					fixture.SchemaReferenceCleanup(t, "tt_dest")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							for _, q := range append([]string{"CREATE SCHEMA tt_dest", "CREATE DOMAIN tt_d AS integer", "CREATE TYPE tt_pair AS(id integer,note text)", "CREATE TABLE tt_typed OF tt_pair", "INSERT INTO tt_typed VALUES(7,'x')"}, action.setup...) {
								count := int64(0)
								if q == "INSERT INTO tt_typed VALUES(7,'x')" {
									count = 1
								}
								run(c, fixture.Exec(q, count))
							}
							var stmt *sql.Stmt
							if held {
								var err error
								stmt, err = c.PrepareContext(p.Ctx, action.query)
								if err != nil {
									t.Fatal("quiet Prepare", err)
								}
								defer stmt.Close()
							}
							var e fixture.Executor = c
							finish := func() {}
							if route == "sql" {
								run(c, fixture.Exec("BEGIN", 0))
								defer c.ExecContext(p.Ctx, "ROLLBACK")
								finish = func() { run(c, fixture.Exec("ROLLBACK", 0)) }
							} else if route == "go" {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								finish = func() {
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
								}
							}
							var before int64
							if err := c.QueryRowContext(p.Ctx, action.probe).Scan(&before); err != nil {
								t.Fatal(err)
							}
							x := e
							if held {
								x = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: action.query}
							}
							run(x, fixture.Exec(action.query, 0))
							want := int64(1)
							switch action.name {
							case "create_domain", "create_composite", "domain_rename", "domain_move", "type_rename", "type_move":
								want = 2
							case "drop_domain", "drop_type":
								want = 0
							}
							run(e, fixture.Select(action.probe, []string{"count"}, []string{"INT8"}, [][]any{{want}}))
							if route != "autocommit" {
								run(peer, fixture.Select(action.probe, []string{"count"}, []string{"INT8"}, [][]any{{before}}))
							}
							finish()
							if route != "autocommit" {
								run(c, fixture.Select(action.probe, []string{"count"}, []string{"INT8"}, [][]any{{before}}))
							}
						})
					}
				})
			}
		}
	}
}
