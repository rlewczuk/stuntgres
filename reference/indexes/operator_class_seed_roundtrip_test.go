package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorClassSeedRoundtrip(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"class_seed_owner"})
	fixture.SchemaReferenceCleanup(t, "class_seed_dest")
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		bootstrapOwner := "postgres"
		if b.PG {
			bootstrapOwner = "stuntgres"
		}
		current := func(oid int) (string, error) {
			var schema, name string
			err := conn.QueryRowContext(p.Ctx, `SELECT n.nspname,c.opcname FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace WHERE c.oid=$1::oid`, oid).Scan(&schema, &name)
			return `"` + strings.ReplaceAll(schema, `"`, `""`) + `"."` + strings.ReplaceAll(name, `"`, `""`) + `"`, err
		}
		cleanup := func() error {
			for _, oid := range []int{1978, 1979} {
				name, err := current(oid)
				if err != nil {
					return err
				}
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR CLASS `+name+` USING btree RENAME TO `+fmt.Sprintf("class_seed_cleanup_%d", oid)); err != nil {
					return err
				}
				name, err = current(oid)
				if err != nil {
					return err
				}
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR CLASS `+name+` USING btree SET SCHEMA pg_catalog`); err != nil {
					return err
				}
				name, err = current(oid)
				if err != nil {
					return err
				}
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR CLASS `+name+` USING btree OWNER TO `+bootstrapOwner); err != nil {
					return err
				}
			}
			for _, s := range []struct {
				oid  int
				name string
			}{{1978, "int4_ops"}, {1979, "int2_ops"}} {
				name, err := current(s.oid)
				if err != nil {
					return err
				}
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR CLASS `+name+` USING btree RENAME TO `+s.name); err != nil {
					return err
				}
			}
			if _, err := conn.ExecContext(p.Ctx, `DROP OPERATOR CLASS IF EXISTS pg_catalog.__stuntgres_class_seed_1978 USING btree`); err != nil {
				return err
			}
			_, err := conn.ExecContext(p.Ctx, `DROP OPERATOR FAMILY IF EXISTS pg_catalog.__stuntgres_class_seed_1978 USING btree`)
			return err
		}
		defer func() {
			if err := cleanup(); err != nil {
				t.Error("restore seeded class metadata", err)
			}
		}()
		for _, q := range []string{
			`CREATE ROLE class_seed_owner`, `CREATE SCHEMA class_seed_dest`,
			`CREATE OPERATOR CLASS pg_catalog.__stuntgres_class_seed_1978 FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
			`ALTER OPERATOR CLASS pg_catalog.int4_ops USING btree RENAME TO class_seed_swap`,
			`ALTER OPERATOR CLASS pg_catalog.int2_ops USING btree RENAME TO int4_ops`,
			`ALTER OPERATOR CLASS pg_catalog.class_seed_swap USING btree RENAME TO int2_ops`,
			`ALTER OPERATOR CLASS pg_catalog.int2_ops USING btree SET SCHEMA class_seed_dest`,
			`ALTER OPERATOR CLASS class_seed_dest.int2_ops USING btree OWNER TO class_seed_owner`,
		} {
			run(fixture.Exec(q, 0))
		}
		const q = `SELECT c.oid::bigint,c.opcname,n.nspname,r.rolname,c.opcmethod::bigint,c.opcfamily::bigint,c.opcintype::bigint,c.opcdefault,c.opckeytype::bigint FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace JOIN pg_roles r ON r.oid=c.opcowner WHERE c.oid IN(1978,1979) ORDER BY c.oid`
		s := fixture.Select(q, []string{"oid", "opcname", "nspname", "rolname", "opcmethod", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"INT8", "NAME", "NAME", "NAME", "INT8", "INT8", "INT8", "BOOL", "INT8"}, [][]any{{int64(1978), "int2_ops", "class_seed_dest", "class_seed_owner", int64(403), int64(1976), int64(23), true, int64(0)}, {int64(1979), "int4_ops", "pg_catalog", bootstrapOwner, int64(403), int64(1976), int64(21), true, int64(0)}})
		held := classHeld(t, p, conn, q)
		check := func() { classRoutes(t, p, b, conn, s); fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
		check()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			foreign := fixture.NewFixture(t, false, true)
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec(`CREATE ROLE class_seed_owner`, 0))
			if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("foreign seed Restore", err)
			}
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, s)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
		} else if emitted != "" {
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec(`DROP SCHEMA class_seed_dest CASCADE`, 0))
			if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("PostgreSQL rejected actual native seed dump", err)
			}
		}
		check()
		t.Run("pinned", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.ImplicitError(`DROP OPERATOR CLASS class_seed_dest.int2_ops USING btree CASCADE`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop operator class class_seed_dest.int2_ops for access method btree because it is required by the database system`}))
		})
		run(fixture.Exec(`CREATE TABLE class_seed_rows(n integer)`, 0))
		run(fixture.Exec(`INSERT INTO class_seed_rows VALUES(2),(1)`, 2))
		run(fixture.Exec(`CREATE INDEX class_seed_index ON class_seed_rows(n)`, 0))
		run(fixture.Exec(`INSERT INTO class_seed_rows VALUES(3)`, 1))
		run(fixture.Select(`SELECT n FROM class_seed_rows ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	})
}
