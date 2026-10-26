package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintRoutineUsageLifecycle(t *testing.T) {
	ccruScenario(t, func(c *ccruCase) {
		t := c.t
		f, _ := c.seed()
		c.exec("CREATE TABLE ccru_life(v integer)", "CREATE DOMAIN ccru_life_d AS integer", "CREATE SCHEMA ccru_moved")
		held := c.hold(ccruQuery)
		check := func(rows ...[]any) { c.read(rows...); held(c.step(ccruQuery, rows)) }
		check()
		c.exec("ALTER TABLE ccru_life ADD CONSTRAINT ccru_life_ck CHECK(ccru_f(v)) NOT VALID", "ALTER DOMAIN ccru_life_d ADD CONSTRAINT ccru_life_dom CHECK(ccru_f(VALUE)) NOT VALID")
		var tableOID, domainOID, tableCheckOID, domainCheckOID int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT 'public.ccru_life'::regclass::oid::bigint,'public.ccru_life_d'::regtype::oid::bigint").Scan(&tableOID, &domainOID); err != nil || tableOID <= 0 || domainOID <= 0 {
			t.Fatalf("initial owners=%d/%d error=%v", tableOID, domainOID, err)
		}
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid=$1::oid AND contypid=0::oid AND conname='ccru_life_ck' AND contype='c'", tableOID).Scan(&tableCheckOID); err != nil || tableCheckOID <= 0 {
			t.Fatalf("initial table CHECK=%d error=%v", tableCheckOID, err)
		}
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE contypid=$1::oid AND conrelid=0::oid AND conname='ccru_life_dom' AND contype='c'", domainOID).Scan(&domainCheckOID); err != nil || domainCheckOID <= 0 || domainCheckOID == tableCheckOID {
			t.Fatalf("initial domain CHECK=%d table CHECK=%d error=%v", domainCheckOID, tableCheckOID, err)
		}
		facts := func(tableSchema, tableName, checkName, domainSchema, routineName string, domainValid bool) {
			t.Helper()
			domainDefinition := "CHECK (" + routineName + "(VALUE))"
			if !domainValid {
				domainDefinition += " NOT VALID"
			}
			q := `SELECT c.oid::bigint,c.conname,cn.nspname AS constraint_schema,c.conrelid::bigint,c.contypid::bigint,rn.nspname AS table_schema,r.relname AS table_name,tn.nspname AS domain_schema,d.typname AS domain_name,c.convalidated,c.conenforced,pg_get_constraintdef(c.oid) AS definition,pg_get_expr(c.conbin,c.conrelid) AS expression FROM pg_constraint c JOIN pg_namespace cn ON cn.oid=c.connamespace LEFT JOIN pg_class r ON r.oid=c.conrelid LEFT JOIN pg_namespace rn ON rn.oid=r.relnamespace LEFT JOIN pg_type d ON d.oid=c.contypid LEFT JOIN pg_namespace tn ON tn.oid=d.typnamespace WHERE c.oid=$1::oid OR c.oid=$2::oid`
			s := fixture.Select(q, []string{"oid", "conname", "constraint_schema", "conrelid", "contypid", "table_schema", "table_name", "domain_schema", "domain_name", "convalidated", "conenforced", "definition", "expression"}, []string{"INT8", "NAME", "NAME", "INT8", "INT8", "NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{
				{tableCheckOID, checkName, tableSchema, tableOID, int64(0), tableSchema, tableName, nil, nil, false, true, "CHECK (" + routineName + "(v)) NOT VALID", routineName + "(v)"},
				{domainCheckOID, "ccru_life_dom", domainSchema, int64(0), domainOID, nil, nil, domainSchema, "ccru_life_d", domainValid, true, domainDefinition, routineName + "(VALUE)"},
			}, tableCheckOID, domainCheckOID)
			s.Bag = true
			c.run(s)
			c.run(fixture.Prepared(s))
		}
		a, d := c.row("public", "ccru_life_ck", "public", f), c.row("public", "ccru_life_dom", "public", f)
		check(a, d)
		flags := func(domainValid bool) {
			c.run(fixture.Select("SELECT conname,convalidated,conenforced FROM pg_constraint WHERE conname IN ('ccru_life_ck','ccru_life_dom') ORDER BY conname", []string{"conname", "convalidated", "conenforced"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"ccru_life_ck", false, true}, {"ccru_life_dom", domainValid, true}}))
		}
		flags(false)
		facts("public", "ccru_life", "ccru_life_ck", "public", "ccru_f", false)
		c.exec("ALTER DOMAIN ccru_life_d VALIDATE CONSTRAINT ccru_life_dom")
		flags(true)
		check(a, d)
		facts("public", "ccru_life", "ccru_life_ck", "public", "ccru_f", true)
		c.exec("BEGIN", "ALTER TABLE ccru_life RENAME CONSTRAINT ccru_life_ck TO ccru_rollback")
		check(c.row("public", "ccru_rollback", "public", f), d)
		facts("public", "ccru_life", "ccru_rollback", "public", "ccru_f", true)
		c.exec("ROLLBACK")
		check(a, d)
		facts("public", "ccru_life", "ccru_life_ck", "public", "ccru_f", true)
		c.exec("BEGIN", "ALTER TABLE ccru_life RENAME CONSTRAINT ccru_life_ck TO ccru_renamed")
		facts("public", "ccru_life", "ccru_renamed", "public", "ccru_f", true)
		c.exec("COMMIT")
		facts("public", "ccru_life", "ccru_renamed", "public", "ccru_f", true)
		c.exec("ALTER TABLE ccru_life RENAME TO ccru_life_new")
		a = c.row("public", "ccru_renamed", "public", f)
		check(a, d)
		facts("public", "ccru_life_new", "ccru_renamed", "public", "ccru_f", true)
		c.exec("ALTER TABLE ccru_life_new SET SCHEMA ccru_moved")
		a = c.row("ccru_moved", "ccru_renamed", "public", f)
		check(a, d)
		facts("ccru_moved", "ccru_life_new", "ccru_renamed", "public", "ccru_f", true)
		c.exec("ALTER FUNCTION ccru_f(integer) RENAME TO ccru_f_new")
		renamed := c.identity("public", "ccru_f_new", "integer")
		if renamed != "ccru_f_new"+f[len("ccru_f"):] {
			t.Fatalf("routine OID changed: %s -> %s", f, renamed)
		}
		f = renamed
		a, d = c.row("ccru_moved", "ccru_renamed", "public", f), c.row("public", "ccru_life_dom", "public", f)
		check(a, d)
		facts("ccru_moved", "ccru_life_new", "ccru_renamed", "public", "ccru_f_new", true)
		c.exec("ALTER FUNCTION ccru_f_new(integer) SET SCHEMA ccru_moved")
		a, d = c.row("ccru_moved", "ccru_renamed", "ccru_moved", f), c.row("public", "ccru_life_dom", "ccru_moved", f)
		check(a, d)
		facts("ccru_moved", "ccru_life_new", "ccru_renamed", "public", "ccru_moved.ccru_f_new", true)
		c.exec("ALTER DOMAIN ccru_life_d SET SCHEMA ccru_moved")
		d = c.row("ccru_moved", "ccru_life_dom", "ccru_moved", f)
		check(a, d)
		facts("ccru_moved", "ccru_life_new", "ccru_renamed", "ccru_moved", "ccru_moved.ccru_f_new", true)
		c.exec("ALTER TABLE ccru_moved.ccru_life_new DROP CONSTRAINT ccru_renamed")
		check(d)
		c.exec("ALTER DOMAIN ccru_moved.ccru_life_d DROP CONSTRAINT ccru_life_dom")
		check()
		c.exec("DROP TABLE ccru_moved.ccru_life_new", "DROP DOMAIN ccru_moved.ccru_life_d", "DROP FUNCTION ccru_moved.ccru_f_new(integer)")
		check()
	})
	t.Run("retained_native_result", func(t *testing.T) {
		ccruScenario(t, func(c *ccruCase) {
			t := c.t
			f, _ := c.seed()
			c.exec("CREATE TABLE ccru_retained(v integer CONSTRAINT ccru_retained CHECK(ccru_f(v)))")
			want := c.row("public", "ccru_retained", "public", f)
			c.read(want)
			if c.b.PG {
				return
			}
			rows, err := c.conn.QueryContext(c.p.Ctx, ccruQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			ccruMetadata(t, rows, false)
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
				t.Fatal(err)
			}
			c.read()
			if !rows.Next() {
				t.Fatal("retained row missing", rows.Err())
			}
			var got [6]string
			if err := rows.Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5]); err != nil {
				t.Fatal(err)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("retained column %d=%q want %v", i, got[i], want[i])
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained multiplicity", rows.Err())
			}
			ccruColumnMetadata(t, columns, false)
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			restored := c.identity("public", "ccru_f", "integer")
			c.read(c.row("public", "ccru_retained", "public", restored))
		})
	})
}
