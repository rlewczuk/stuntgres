package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// This scenario changes lexical/owner metadata only on live builtin classes.
// The new genuine integer comparator is in bool_ops' otherwise classless
// integer pair: no INTERNAL callback or existing class support is replaced.
func TestReferenceOperatorFamilySeedRoundtrip(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"family_seed_owner"})
	fixture.SchemaReferenceCleanup(t, "family_seed_roundtrip")
	var emitted string
	familyBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		bootstrapOwner := "postgres"
		if b.PG {
			bootstrapOwner = "stuntgres"
		}
		type seed struct {
			oid          int
			name, method string
		}
		seeds := []seed{{1976, "integer_ops", "btree"}, {1970, "float_ops", "btree"}, {424, "bool_ops", "btree"}, {1995, "text_ops", "hash"}}
		currentName := func(s seed) (string, error) {
			var schema, name string
			err := conn.QueryRowContext(p.Ctx, `SELECT n.nspname,f.opfname FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE f.oid=$1`, s.oid).Scan(&schema, &name)
			if err != nil {
				return "", err
			}
			return `"` + strings.ReplaceAll(schema, `"`, `""`) + `"."` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
		}
		cleanup := func() error {
			for _, s := range seeds {
				name, err := currentName(s)
				if err != nil {
					return err
				}
				// The one new pair in moved bool_ops has no corresponding class.
				if s.oid == 424 {
					var count int
					if err = conn.QueryRowContext(p.Ctx, `SELECT count(*) FROM pg_amproc WHERE amprocfamily=424 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum=1`).Scan(&count); err != nil {
						return err
					}
					if count != 0 {
						if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR FAMILY `+name+` USING btree DROP FUNCTION 1(integer)`); err != nil {
							return err
						}
					}
				}
				temporary := fmt.Sprintf("family_seed_cleanup_%d", s.oid)
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR FAMILY `+name+` USING `+s.method+` RENAME TO `+temporary); err != nil {
					return err
				}
				name, err = currentName(s)
				if err != nil {
					return err
				}
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR FAMILY `+name+` USING `+s.method+` SET SCHEMA pg_catalog`); err != nil {
					return err
				}
				name, err = currentName(s)
				if err != nil {
					return err
				}
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR FAMILY `+name+` USING `+s.method+` OWNER TO `+bootstrapOwner); err != nil {
					return err
				}
			}
			for _, s := range seeds {
				name, err := currentName(s)
				if err != nil {
					return err
				}
				if _, err = conn.ExecContext(p.Ctx, `ALTER OPERATOR FAMILY `+name+` USING `+s.method+` RENAME TO `+s.name); err != nil {
					return err
				}
			}
			_, err := conn.ExecContext(p.Ctx, `DROP OPERATOR FAMILY IF EXISTS pg_catalog.__stuntgres_family_seed_1976 USING btree`)
			return err
		}
		defer func() {
			if err := cleanup(); err != nil {
				t.Error("restore builtin family metadata", err)
			}
		}()
		run(fixture.Exec(`CREATE ROLE family_seed_owner`, 0))
		run(fixture.Exec(`CREATE SCHEMA family_seed_roundtrip`, 0))
		run(fixture.Exec(`CREATE OPERATOR FAMILY pg_catalog.__stuntgres_family_seed_1976 USING btree`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY pg_catalog.integer_ops USING btree RENAME TO family_seed_swap`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY pg_catalog.float_ops USING btree RENAME TO integer_ops`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY pg_catalog.family_seed_swap USING btree RENAME TO float_ops`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY pg_catalog.bool_ops USING btree SET SCHEMA family_seed_roundtrip`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY family_seed_roundtrip.bool_ops USING btree RENAME TO moved_bool_ops`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY family_seed_roundtrip.moved_bool_ops USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY family_seed_roundtrip.moved_bool_ops USING btree OWNER TO family_seed_owner`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY pg_catalog.text_ops USING hash OWNER TO family_seed_owner`, 0))
		const q = `SELECT f.oid::bigint,f.opfname,n.nspname,r.rolname FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace JOIN pg_roles r ON r.oid=f.opfowner WHERE f.oid IN (424,1970,1976,1995) ORDER BY f.oid`
		expected := [][]any{{int64(424), "moved_bool_ops", "family_seed_roundtrip", "family_seed_owner"}, {int64(1970), "integer_ops", "pg_catalog", bootstrapOwner}, {int64(1976), "float_ops", "pg_catalog", bootstrapOwner}, {int64(1995), "text_ops", "pg_catalog", "family_seed_owner"}}
		check := func() {
			run(fixture.Select(q, []string{"oid", "opfname", "nspname", "rolname"}, []string{"INT8", "NAME", "NAME", "NAME"}, expected))
			run(fixture.Select(`SELECT amproclefttype::bigint,amprocrighttype::bigint,amprocnum,amproc::oid::bigint FROM pg_amproc WHERE amprocfamily=424 AND amproclefttype=23 AND amprocrighttype=23`, []string{"amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"INT8", "INT8", "INT2", "INT8"}, [][]any{{int64(23), int64(23), int64(1), int64(351)}}))
		}
		check()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			// Observe source immutability through a real retained source cursor.
			retained, err := p.Native.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			check()
			foreign := fixture.NewFixture(t, false, true)
			// Dumps preserve object ownership, not cluster-global role creation.
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec(`CREATE ROLE family_seed_owner`, 0))
			if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("seed destination Restore", err)
			}
			fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select(q, []string{"oid", "opfname", "nspname", "rolname"}, []string{"INT8", "NAME", "NAME", "NAME"}, expected))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			count := 0
			for retained.Next() {
				var oid int64
				var name, schema, owner string
				if err := retained.Scan(&oid, &name, &schema, &owner); err != nil {
					t.Fatal(err)
				}
				want := expected[count]
				if oid != want[0] || name != want[1] || schema != want[2] || owner != want[3] {
					t.Fatal("source cursor mutated", oid, name, schema, owner)
				}
				count++
			}
			if retained.Err() != nil || count != 4 {
				t.Fatal("retained source rows", count, retained.Err())
			}
			retained.Close()
		} else if emitted != "" {
			// Reset only this scenario's live builtin metadata before executing the
			// exact native bytes; no reconstructed PostgreSQL dump imitation.
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec(`DROP SCHEMA family_seed_roundtrip CASCADE`, 0))
			if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("PostgreSQL seed replay", err)
			}
		}
		check()
		// Original members retain pins despite family lexical/owner changes.
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY pg_catalog.float_ops USING btree DROP FUNCTION 1(integer)`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop function 1 (integer, integer) of operator family float_ops for access method btree: btint4cmp(integer,integer) because it is required by the database system`}))
		run(fixture.Select(`SELECT 42 AS sentinel`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
}
