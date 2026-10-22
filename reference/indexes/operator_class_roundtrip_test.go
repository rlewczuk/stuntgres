package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Resolve names independently, then compare catalog references to those exact
// identities. Rediscover after replacement: user OIDs are not stable across
// engines or Restore. Single-catalog probes avoid multiplying unfiltered
// pg_attribute/pg_class/pg_type rows just to check these relationships.
func classRoundtripIdentity(t *testing.T, ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any) int64 {
	t.Helper()
	var oid int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&oid); err != nil {
		t.Fatalf("identity query %s args=%v: %v", query, args, err)
	}
	if oid == 0 {
		t.Fatalf("zero identity from %s args=%v", query, args)
	}
	return oid
}

func TestReferenceOperatorClassRoundtrip(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"class_rt_owner"})
	fixture.SchemaReferenceCleanup(t, "class_rt_family", "class_rt_class")
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, execute := classConn(t, p, b)
		run := func(s fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("roundtrip SQL: %s args=%v", s.Query, s.Args)
				}
			}()
			execute(s)
		}
		for _, q := range []string{
			`CREATE ROLE class_rt_owner`, `CREATE SCHEMA class_rt_family`, `CREATE SCHEMA class_rt_class`,
			`CREATE TYPE class_rt_class.label AS ENUM('low','high')`,
			`CREATE OPERATOR FAMILY class_rt_family.ints USING btree`,
			classCreate("class_rt_class.old_name", "class_rt_family.ints", "btint4cmp"),
			`ALTER OPERATOR CLASS class_rt_class.old_name USING btree RENAME TO current_name`,
			`ALTER OPERATOR CLASS class_rt_class.current_name USING btree OWNER TO class_rt_owner`,
			`ALTER OPERATOR FAMILY class_rt_family.ints USING btree ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal), FUNCTION 1 pg_catalog.btint48cmp(integer,bigint)`,
			`CREATE OPERATOR CLASS class_rt_class.labels FOR TYPE anyenum USING btree AS OPERATOR 1 pg_catalog.< (anyenum,anyenum), OPERATOR 2 pg_catalog.<= (anyenum,anyenum), OPERATOR 3 pg_catalog.= (anyenum,anyenum), OPERATOR 4 pg_catalog.>= (anyenum,anyenum), OPERATOR 5 pg_catalog.> (anyenum,anyenum), FUNCTION 1 (anyenum,anyenum) pg_catalog.enum_cmp(anyenum,anyenum)`,
			`CREATE TABLE class_rt_class.rows(id integer,n integer,e class_rt_class.label)`,
			`INSERT INTO class_rt_class.rows VALUES(1,2,'high'),(2,1,'low'),(3,NULL,'low')`,
			`CREATE INDEX class_rt_int_index ON class_rt_class.rows(n class_rt_class.current_name)`,
			`CREATE INDEX class_rt_enum_index ON class_rt_class.rows(e class_rt_class.labels)`,
			`INSERT INTO class_rt_class.rows VALUES(4,3,'high')`,
		} {
			n := int64(0)
			if strings.HasPrefix(q, "INSERT INTO") {
				n = 1
				if strings.Contains(q, "(3,NULL") {
					n = 3
				}
			}
			run(fixture.Exec(q, n))
		}
		const meta = `SELECT opcname,opcnamespace::bigint,opcowner::bigint,opcmethod::bigint,opcfamily::bigint,opcintype::bigint,opckeytype::bigint,opcdefault FROM pg_opclass WHERE opcname='current_name' AND opcnamespace=$1::oid`
		metadata := func(ctx context.Context, db interface {
			QueryRowContext(context.Context, string, ...any) *sql.Row
		}) fixture.Step {
			ns := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_namespace WHERE nspname='class_rt_class'`)
			familyNS := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_namespace WHERE nspname='class_rt_family'`)
			owner := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_roles WHERE rolname='class_rt_owner'`)
			family := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='ints' AND opfnamespace=$1::oid AND opfmethod=403`, familyNS)
			return fixture.Select(meta, []string{"opcname", "opcnamespace", "opcowner", "opcmethod", "opcfamily", "opcintype", "opckeytype", "opcdefault"}, []string{"NAME", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{"current_name", ns, owner, int64(403), family, int64(23), int64(0), false}}, ns)
		}
		enumMetadata := func(ctx context.Context, db interface {
			QueryRowContext(context.Context, string, ...any) *sql.Row
		}, run func(fixture.Step)) {
			ns := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_namespace WHERE nspname='class_rt_class'`)
			family := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='labels' AND opfnamespace=$1::oid AND opfmethod=403`, ns)
			label := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_type WHERE typname='label' AND typnamespace=$1::oid`, ns)
			relation := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_class WHERE relname='rows' AND relnamespace=$1::oid`, ns)
			run(fixture.Select(`SELECT opcintype::bigint,opcfamily::bigint,opcmethod::bigint FROM pg_opclass WHERE opcname='labels' AND opcnamespace=$1::oid`, []string{"opcintype", "opcfamily", "opcmethod"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3500), family, int64(403)}}, ns))
			run(fixture.Select(`SELECT atttypid::bigint FROM pg_attribute WHERE attrelid=$1::oid AND attname='e'`, []string{"atttypid"}, []string{"INT8"}, [][]any{{label}}, relation))
			run(fixture.Select(`SELECT amopstrategy,amoplefttype::bigint,amoprighttype::bigint FROM pg_amop WHERE amopfamily=$1::oid ORDER BY amopstrategy`, []string{"amopstrategy", "amoplefttype", "amoprighttype"}, []string{"INT2", "INT8", "INT8"}, [][]any{{int64(1), int64(3500), int64(3500)}, {int64(2), int64(3500), int64(3500)}, {int64(3), int64(3500), int64(3500)}, {int64(4), int64(3500), int64(3500)}, {int64(5), int64(3500), int64(3500)}}, family))
			run(fixture.Select(`SELECT amprocnum,amproclefttype::bigint,amprocrighttype::bigint,amproc::text FROM pg_amproc WHERE amprocfamily=$1::oid`, []string{"amprocnum", "amproclefttype", "amprocrighttype", "amproc"}, []string{"INT2", "INT8", "INT8", "TEXT"}, [][]any{{int64(1), int64(3500), int64(3500), "enum_cmp"}}, family))
		}
		const data = `SELECT id,n FROM class_rt_class.rows WHERE n >= $1 ORDER BY n,id`
		mh := classHeld(t, p, conn, meta)
		dh := classHeld(t, p, conn, data)
		check := func() {
			m := metadata(p.Ctx, conn)
			classRoutes(t, p, b, conn, m)
			fixture.RunStep(t, p.Ctx, mh, b.Recorder, m)
			s := fixture.Select(data, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(4), int64(3)}}, 2)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, dh, b.Recorder, s)
			enumMetadata(p.Ctx, conn, run)
			run(fixture.Select(`SELECT id FROM class_rt_class.rows ORDER BY e,id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(1)}, {int64(4)}}))
		}
		check()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			for _, s := range []string{`CREATE OPERATOR CLASS "class_rt_class"."current_name"`, `FAMILY "class_rt_family"."ints"`, `CREATE INDEX "class_rt_int_index"`} {
				if !strings.Contains(emitted, s) {
					t.Fatalf("missing %q in dump\n%s", s, emitted)
				}
			}
			if strings.Contains(emitted, "old_name") {
				t.Fatal("stale class name in dump")
			}
			retained, err := p.Native.QueryContext(p.Ctx, `SELECT opcname FROM pg_opclass WHERE opcname='current_name'`)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			foreign := fixture.NewFixture(t, false, true)
			fr := func(s fixture.Step) { fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, s) }
			fr(fixture.Exec(`CREATE ROLE class_rt_owner`, 0))
			fr(fixture.Exec(`CREATE TYPE destination_label AS ENUM('occupied')`, 0))
			fr(fixture.Exec(`CREATE OPERATOR FAMILY public.class_rt_occupied USING hash`, 0))
			if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("foreign Restore", err)
			}
			fr(metadata(foreign.Ctx, foreign.Memory))
			enumMetadata(foreign.Ctx, foreign.Memory, fr)
			fr(fixture.Exec(`INSERT INTO class_rt_class.rows VALUES(5,0,'low')`, 1))
			fr(fixture.Select(`SELECT id FROM class_rt_class.rows ORDER BY n NULLS LAST,id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(2)}, {int64(1)}, {int64(4)}, {int64(3)}}))
			fr(fixture.Select(`SELECT id FROM class_rt_class.rows ORDER BY e,id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(5)}, {int64(1)}, {int64(4)}}))
			fr(fixture.Exec(`ALTER OPERATOR CLASS class_rt_class.current_name USING btree RENAME TO destination_only`, 0))
			fr(fixture.Exec(`ALTER OPERATOR FAMILY class_rt_family.ints USING btree DROP FUNCTION 2(integer)`, 0))
			check()
			var before, after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+`CREATE OPERATOR CLASS public.class_rt_invalid FOR TYPE integer USING btree AS STORAGE bigint;`))
			want := fixture.ControlsReferenceDiagnostic("42P17", `storage type cannot be different from data type for access method "btree"`)
			fixture.AssertDiagnostic(t, err, &want)
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
				t.Fatalf("failed Restore changed source: %v\nbefore:\n%s\nafter:\n%s", err, before.String(), after.String())
			}
			check()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcname='current_name'`, []string{"opcname"}, []string{"NAME"}, nil))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			if !retained.Next() {
				t.Fatal("retained source", retained.Err())
			}
			var name string
			if err := retained.Scan(&name); err != nil || name != "current_name" {
				t.Fatal(name, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained rows", retained.Err())
			}
			retained.Close()
		} else if emitted != "" {
			run(fixture.Exec(`DROP SCHEMA class_rt_class CASCADE`, 0))
			run(fixture.Exec(`DROP SCHEMA class_rt_family CASCADE`, 0))
			if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("PostgreSQL rejected actual native class dump", err)
			}
		}
		check()
		run(fixture.Exec(`INSERT INTO class_rt_class.rows VALUES(5,0,'low')`, 1))
		run(fixture.Select(`SELECT id FROM class_rt_class.rows ORDER BY n NULLS LAST,id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(2)}, {int64(1)}, {int64(4)}, {int64(3)}}))
	})
}
