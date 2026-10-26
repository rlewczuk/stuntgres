package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainIdentityNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"di_replay_owner", "di_replay_new"})
	domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		domainIdentityDDL(t, p, c, r, "CREATE ROLE di_replay_owner", "CREATE ROLE di_replay_new", "CREATE SCHEMA di_src AUTHORIZATION di_replay_owner", "CREATE SCHEMA di_dst", "CREATE SCHEMA di_other",
			"SET ROLE di_replay_owner", "CREATE DOMAIN di_src.d AS integer DEFAULT 7 CONSTRAINT positive CHECK(VALUE>0)", "RESET ROLE",
			"CREATE DOMAIN di_other.child AS di_src.d", "CREATE DOMAIN di_other.required AS integer NOT NULL", "CREATE DOMAIN di_other.number AS numeric(5,2)",
			"CREATE TYPE di_other.box AS(v di_src.d,keep integer)",
			"CREATE TABLE di_other.t(id integer,v di_src.d,c di_other.child,a di_src.d[],box di_other.box,n di_other.number,required di_other.required DEFAULT 1)",
			"CREATE TABLE di_other.recipes(v integer DEFAULT 8::di_src.d CONSTRAINT recipe_check CHECK(v>1::di_src.d))",
			"CREATE VIEW di_other.v AS SELECT id,(v+1)::di_src.d AS v FROM di_other.t",
			"CREATE DOMAIN di_src.history AS integer", "CREATE TABLE di_other.history(v di_src.history)",
			"CREATE DOMAIN di_src.removed AS integer", "CREATE TABLE di_other.retired(v di_src.removed,keep integer)")
		run(fixture.Exec("INSERT INTO di_other.t VALUES(1,7,8,'[0:1]={9,NULL}','(10,11)',12.345,1),(2,NULL,NULL,NULL,NULL,NULL,2)", 2))
		run(fixture.Exec("INSERT INTO di_other.history VALUES(-1)", 1))
		run(fixture.Exec("INSERT INTO di_other.retired VALUES(5,42)", 1))
		domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.history ADD CONSTRAINT later CHECK(VALUE>0) NOT VALID", "ALTER DOMAIN di_src.d RENAME TO renamed", "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst", "ALTER DOMAIN di_dst.renamed OWNER TO di_replay_new", "ALTER DOMAIN di_src.history RENAME TO historical", "ALTER DOMAIN di_src.historical SET SCHEMA di_dst", "DROP DOMAIN di_src.removed CASCADE", "SET search_path TO public")
		rowsStep := fixture.Select("SELECT id,v,c,a::text,box::text,n,required FROM di_other.t ORDER BY id", []string{"id", "v", "c", "a", "box", "n", "required"}, []string{"INT4", "INT4", "INT4", "TEXT", "TEXT", "NUMERIC", "INT4"}, [][]any{{int64(1), int64(7), int64(8), "[0:1]={9,NULL}", "(10,11)", "12.35", int64(1)}, {int64(2), nil, nil, nil, nil, nil, int64(2)}})
		check := func(pair *fixture.Pair, x fixture.Executor, record *fixture.Recorder) {
			fixture.RunStep(t, pair.Ctx, x, record, rowsStep)
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Prepared(rowsStep))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT id,v FROM di_other.v ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(2), nil}}))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT pg_get_userbyid(typowner) AS owner FROM pg_type WHERE oid='di_dst.renamed'::regtype::oid", []string{"owner"}, []string{"NAME"}, [][]any{{"di_replay_new"}}))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT v FROM di_other.history", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT convalidated FROM pg_constraint WHERE contypid='di_dst.historical'::regtype::oid", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT keep FROM di_other.retired", []string{"keep"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Error("SELECT 1::di_src.d", "42704"))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Error("SELECT 1::di_src.removed", "42704"))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT 9::di_dst.renamed AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
			acl := fixture.Select("SELECT unnest(typacl)::text AS acl FROM pg_type WHERE oid='di_dst.renamed'::regtype", []string{"acl"}, []string{"TEXT"}, [][]any{{"di_replay_new=U/di_replay_new"}, {"=U/di_replay_new"}, {"di_replay_owner=U/di_replay_new"}})
			acl.Bag = true
			fixture.RunStep(t, pair.Ctx, x, record, acl)
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT has_type_privilege('di_replay_owner','di_dst.renamed','USAGE') AS usage,has_type_privilege('di_replay_owner','di_dst.renamed','USAGE WITH GRANT OPTION') AS options", []string{"usage", "options"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		}
		domainIdentityDDL(t, p, c, r, "GRANT USAGE ON DOMAIN di_dst.renamed TO di_replay_owner")
		check(p, c, r)
		// PostgreSQL supplies the ordinary SQL oracle above; process-local
		// replacement controls have no invented PostgreSQL API counterpart.
		if b.PG {
			return
		}
		// Retain an eager native cursor through identity generation changes AND
		// replacement. Its cells and detached column names are immutable.
		retained, err := c.QueryContext(p.Ctx, rowsStep.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.Columns()
		if err != nil {
			t.Fatal(err)
		}
		domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_dst.renamed RENAME TO intermediate", "ALTER DOMAIN di_dst.intermediate RENAME TO renamed")
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		var rawACL string
		if err := c.QueryRowContext(p.Ctx, "SELECT typacl::text FROM pg_type WHERE oid='di_dst.renamed'::regtype").Scan(&rawACL); err != nil {
			t.Fatal(err)
		}
		repair := "UPDATE pg_catalog.pg_type SET typacl=E'" + rawACL + "'::pg_catalog.aclitem[] WHERE oid=E'di_dst.renamed'::pg_catalog.regtype;"
		if !strings.Contains(dump.String(), repair) {
			t.Fatalf("dump omitted exact current-domain ACL repair %q", repair)
		}
		check(p, c, r)
		if !strings.Contains(dump.String(), "renamed") {
			t.Fatal("dump omitted current domain declaration")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET search_path TO public", 0))
		check(p, c, r)
		if !reflect.DeepEqual(columns, rowsStep.Columns) {
			t.Fatalf("retained columns=%v", columns)
		}
		var old [][]any
		for retained.Next() {
			row := make([]any, len(rowsStep.Columns))
			dest := make([]any, len(row))
			for i := range row {
				dest[i] = &row[i]
			}
			if err := retained.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for i := range row {
				row[i] = fixture.Normalize(row[i])
			}
			old = append(old, row)
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		retained.Close()
		if !reflect.DeepEqual(old, rowsStep.Rows) {
			t.Fatalf("retained rows=%#v", old)
		}
		// Distinct public Driver, deliberately offset role allocation.
		target := fixture.NewFixture(t, false, true)
		domainIdentityDDL(t, target, target.Memory, target.MemoryRecord, "CREATE ROLE di_spacer", "CREATE ROLE di_replay_owner", "CREATE ROLE di_replay_new")
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		check(target, target.Memory, target.MemoryRecord)
		for _, pair := range []*fixture.Pair{p, target} {
			x := pair.Memory
			record := pair.MemoryRecord
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Exec("INSERT INTO di_other.t(id) VALUES(3)", 1))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT v,required FROM di_other.t WHERE id=3", []string{"v", "required"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(1)}}))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Exec("INSERT INTO di_other.recipes DEFAULT VALUES", 1))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT v FROM di_other.recipes", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Error("INSERT INTO di_other.t(id,required) VALUES(4,NULL)", "23502"))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Error("INSERT INTO di_other.t(id,v) VALUES(4,0)", "23514"))
			fixture.RunStep(t, pair.Ctx, x, record, fixture.Error("INSERT INTO di_other.history VALUES(-2)", "23514"))
		}
		// Failed replacements preserve an independent sentinel plus ALL replayed
		// definitions, not only a successful read of one preexisting table.
		domainIdentityDDL(t, target, target.Memory, target.MemoryRecord, "CREATE TABLE public.sentinel(v integer)")
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.sentinel VALUES(42)", 1))
		bad := "CREATE SCHEMA staged; CREATE DOMAIN staged.d AS integer; CREATE TABLE staged.t(v staged.d); INSERT INTO staged.t VALUES('bad');"
		fixture.State(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), "22P02")
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT 1::staged.d", "3F000"))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM di_other.t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}, {int64(7)}}))
	})
}
