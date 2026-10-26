package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func accessRelationsPersistSetup(run func(fixture.Step)) {
	run(fixture.Select("SELECT setval('ar_s',31,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}}))
	for _, q := range []string{
		"GRANT SELECT ON ar_t TO ar_delegate WITH GRANT OPTION",
		"GRANT UPDATE(b) ON ar_t TO ar_delegate WITH GRANT OPTION",
		"SET ROLE ar_delegate",
		"GRANT SELECT ON ar_t TO ar_reader",
		"GRANT UPDATE(b) ON ar_t TO ar_reader",
		"RESET ROLE",
		"GRANT SELECT(a) ON ar_t TO PUBLIC",
		"GRANT SELECT ON SEQUENCE ar_s TO ar_reader",
		"CREATE TABLE ar_null(v INT)", "ALTER TABLE ar_null OWNER TO ar_owner",
		"CREATE TABLE ar_empty(v INT)", "ALTER TABLE ar_empty OWNER TO ar_owner", "REVOKE ALL ON ar_empty FROM ar_owner",
		"CREATE TABLE ar_equivalent(v INT)", "ALTER TABLE ar_equivalent OWNER TO ar_owner", "GRANT ALL ON ar_equivalent TO ar_owner",
	} {
		run(fixture.Exec(q, 0))
	}
}
func accessRelationsPersistCheck(run func(fixture.Step)) {
	run(fixture.Select("SELECT a,b FROM ar_t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
	for _, tc := range []struct {
		q    string
		rows [][]any
	}{
		{"SELECT unnest(relacl)::text AS acl FROM pg_class WHERE oid='ar_t'::regclass", [][]any{{"ar_owner=arwdDxtm/ar_owner"}, {"ar_delegate=r*/ar_owner"}, {"ar_reader=r/ar_delegate"}}},
		{"SELECT unnest(attacl)::text AS acl FROM pg_attribute WHERE attrelid='ar_t'::regclass AND attname='a'", [][]any{{"=r/ar_owner"}}},
		{"SELECT unnest(attacl)::text AS acl FROM pg_attribute WHERE attrelid='ar_t'::regclass AND attname='b'", [][]any{{"ar_delegate=w*/ar_owner"}, {"ar_reader=w/ar_delegate"}}},
		{"SELECT unnest(relacl)::text AS acl FROM pg_class WHERE oid='ar_s'::regclass", [][]any{{"ar_owner=rwU/ar_owner"}, {"ar_reader=r/ar_owner"}}},
	} {
		s := fixture.Select(tc.q, []string{"acl"}, []string{"TEXT"}, tc.rows)
		s.Bag = true
		run(s)
	}
	run(fixture.Select("SELECT relname,relacl IS NULL AS absent,relowner='ar_owner'::regrole AS owned FROM pg_class WHERE relname IN('ar_null','ar_empty','ar_equivalent') ORDER BY relname", []string{"relname", "absent", "owned"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"ar_empty", false, true}, {"ar_equivalent", false, true}, {"ar_null", true, true}}))
	run(fixture.Select("SELECT relacl::text AS acl FROM pg_class WHERE relname='ar_empty'", []string{"acl"}, []string{"TEXT"}, [][]any{{"{}"}}))
	run(fixture.Select("SELECT unnest(relacl)::text AS acl FROM pg_class WHERE relname='ar_equivalent'", []string{"acl"}, []string{"TEXT"}, [][]any{{"ar_owner=arwdDxtm/ar_owner"}}))
	run(fixture.Select("SELECT pg_get_acl(1259,'ar_t'::regclass,0)=relacl AS same FROM pg_class WHERE oid='ar_t'::regclass", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT pg_get_acl(1259,'ar_t'::regclass,attnum)=attacl AS same FROM pg_attribute WHERE attrelid='ar_t'::regclass AND attname IN('a','b') ORDER BY attname", []string{"same"}, []string{"BOOL"}, [][]any{{true}, {true}}))
	run(objectGrantAccessValue("SELECT has_table_privilege('ar_reader','ar_t','SELECT') AS v", true))
	run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_t','b','UPDATE') AS v", true))
	run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_t','b','UPDATE WITH GRANT OPTION') AS v", false))
	run(objectGrantAccessValue("SELECT has_any_column_privilege(4294967294::oid,'ar_t','SELECT') AS v", true))
	run(objectGrantAccessValue("SELECT has_table_privilege('ar_owner','ar_empty','SELECT') AS v", false))
	run(objectGrantAccessValue("SELECT has_table_privilege('ar_owner','ar_empty','SELECT WITH GRANT OPTION') AS v", true))
	run(fixture.Select("SELECT last_value,is_called FROM ar_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(31), true}}))
}
func accessRelationsFresh(t *testing.T, p *fixture.Pair) (*sql.DB, *sql.DB, *fixture.Recorder) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector("relations-persistence/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	t.Cleanup(func() { fresh.Close() })
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	collision, err := fresh.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { collision.Close() })
	if _, err := collision.ExecContext(p.Ctx, "CREATE TEMP TABLE ar_t(shadow INT)"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE ROLE ar_delegate", "CREATE ROLE ar_reader", "CREATE ROLE ar_owner", "CREATE TABLE ar_sentinel(v INT)", "INSERT INTO ar_sentinel VALUES(91)", "GRANT SELECT(v) ON ar_sentinel TO ar_reader"} {
		n := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			n = 1
		}
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec(q, n))
	}
	return fresh, recorded, recorder
}
func accessRelationsRestoreFailures(t *testing.T, p *fixture.Pair, fresh, recorded *sql.DB, recorder *fixture.Recorder, stream string) {
	t.Helper()
	sentinel := func() {
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT v FROM ar_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_sentinel','v','SELECT') AS v", true))
	}
	for _, suffix := range []string{
		"UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid='ar_missing'::pg_catalog.regclass;",
		"UPDATE pg_catalog.pg_class SET relacl='{ar_missing=r/ar_owner}'::pg_catalog.aclitem[] WHERE oid='public.ar_t'::pg_catalog.regclass;",
		"UPDATE pg_catalog.pg_class SET relacl='{ar_reader=U/ar_owner}'::pg_catalog.aclitem[] WHERE oid='public.ar_t'::pg_catalog.regclass;",
		"UPDATE pg_catalog.pg_class SET relacl='{=r*/ar_owner}'::pg_catalog.aclitem[] WHERE oid='public.ar_t'::pg_catalog.regclass;",
		"UPDATE pg_catalog.pg_class SET relacl='{}'::pg_catalog.aclitem[] WHERE oid='public.ar_t'::pg_catalog.regclass;",
		"UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid='public.ar_t'::pg_catalog.regclass OR TRUE;",
		"UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[] WHERE attrelid='public.ar_t'::pg_catalog.regclass AND attname='missing'::pg_catalog.name;",
		"UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[],attname='x' WHERE attrelid='public.ar_t'::pg_catalog.regclass AND attname='a'::pg_catalog.name;",
		"UPDATE pg_catalog.pg_attribute SET attacl=acldefault('c',0) WHERE attrelid='public.ar_t'::pg_catalog.regclass AND attname='a'::pg_catalog.name AND TRUE;",
		"UPDATE pg_catalog.pg_class SET relacl=(SELECT NULL::pg_catalog.aclitem[]) WHERE oid='public.ar_t'::pg_catalog.regclass;",
		"UPDATE pg_catalog.pg_class SET relacl=$1 WHERE oid='public.ar_t'::pg_catalog.regclass;",
		"UPDATE pg_catalog.pg_class SET relacl=aclmerge(NULL,NULL) WHERE oid='public.ar_t'::pg_catalog.regclass;",
		"ALTER TABLE ONLY public.ar_t DROP COLUMN b;",
		"ALTER TABLE ONLY public.ar_sentinel DROP COLUMN v;",
	} {
		if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(stream+suffix)); err == nil {
			t.Fatal("invalid late restore accepted", suffix)
		}
		sentinel()
	}
	if err := stuntgres.Restore(p.Ctx, fresh, io.MultiReader(strings.NewReader(stream), accessExecutionReadFailure{})); err == nil {
		t.Fatal("reader error accepted")
	}
	sentinel()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, fresh, strings.NewReader(stream)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sentinel()
}
func TestReferenceAccessRelationsPersistence(t *testing.T) {
	var emitted string
	t.Run("delegation", func(t *testing.T) {
		accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			accessRelationsPersistSetup(run)
			accessRelationsPersistCheck(run)
			if b.PG {
				// During final dual-mode proof independently replay the actual native dump.
				if emitted != "" {
					run(fixture.Exec("DROP TABLE ar_t,ar_null,ar_empty,ar_equivalent", 0))
					run(fixture.Exec("DROP SEQUENCE ar_s", 0))
					if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
						t.Fatal(err)
					}
					accessRelationsPersistCheck(run)
				}
				return
			}
			if err := stuntgres.Dump(p.Ctx, p.Native, accessExecutionWriteFailure{}); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal(err)
			}
			var saved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			emitted = saved.String()
			run(fixture.Exec("CREATE SCHEMA ar_scope", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA ar_scope TO ar_reader", 0))
			var namespaceSaved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &namespaceSaved); err != nil {
				t.Fatal("supported namespace ACL rejected", err)
			}
			if !strings.Contains(namespaceSaved.String(), "UPDATE pg_catalog.pg_namespace SET nspacl=") {
				t.Fatal("namespace ACL repair missing")
			}
			run(fixture.Exec("DROP SCHEMA ar_scope", 0))
			fresh, recorded, recorder := accessRelationsFresh(t, p)
			accessRelationsRestoreFailures(t, p, fresh, recorded, recorder, emitted)
			retained, err := c.QueryContext(p.Ctx, "SELECT a,b FROM ar_t")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			description, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			accessRelationsPersistCheck(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) })
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
				t.Fatal(err)
			}
			if redump.String() != emitted {
				t.Fatal("redump changed under identity remap")
			}
			if err := stuntgres.Reset(p.Ctx, fresh); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='ar_t' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT count(*) AS n FROM pg_roles WHERE rolname IN('ar_owner','ar_reader','ar_delegate')", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.ScalarRows(t, false, retained, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}, false)
			fixture.ScalarAssertDescription(t, description, []string{"a", "b"}, []string{"INT4", "INT4"})
			run(fixture.Exec("REVOKE GRANT OPTION FOR UPDATE(b) ON ar_t FROM ar_delegate CASCADE", 0))
			run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_t','b','UPDATE') AS v", false))
			run(fixture.Error("UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid='ar_t'::regclass", "0A000"))
			run(fixture.Error("UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[] WHERE attrelid='ar_t'::regclass AND attname='a'", "0A000"))
		})
	})
	t.Run("explicit_empty_column", func(t *testing.T) {
		accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			repair := `UPDATE pg_catalog.pg_attribute SET attacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE attrelid='public.ar_t'::pg_catalog.regclass AND attname='a'::pg_catalog.name;`
			if b.PG {
				run(fixture.Exec(repair, 1))
			} else {
				var baseline bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline.String()+repair)); err != nil {
					t.Fatal(err)
				}
			}
			checks := []fixture.Step{
				fixture.Select("SELECT attname,attacl IS NULL AS absent,attacl::text AS acl FROM pg_attribute WHERE attrelid='ar_t'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "absent", "acl"}, []string{"NAME", "BOOL", "TEXT"}, [][]any{{"a", false, "{}"}, {"b", true, nil}}),
				objectGrantAccessValue("SELECT has_column_privilege('ar_owner','ar_t','a','SELECT WITH GRANT OPTION') AS v", true),
			}
			for _, s := range checks {
				accessRelationsRoutes(t, p, b, c, s)
			}
			if b.PG {
				return
			}
			var saved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			fresh, recorded, recorder := accessRelationsFresh(t, p)
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, s := range checks {
				fixture.RunStep(t, p.Ctx, recorded, recorder, s)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
				t.Fatal(err)
			}
			if redump.String() != saved.String() {
				t.Fatal("empty attribute ACL became absent")
			}
		})
	})
}
