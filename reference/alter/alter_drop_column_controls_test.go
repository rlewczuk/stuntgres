package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDropColumnControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"drop_table_owner"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "S")
	p.Step(t, fixture.Exec("CREATE USER drop_table_owner", 0))
	p.Step(t, fixture.Exec(`CREATE SCHEMA "S" AUTHORIZATION drop_table_owner`, 0))
	for _, backend := range p.Backends() {
		db := backend.DB
		conn, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			record := backend.Recorder
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
			run(fixture.Exec("SET SESSION AUTHORIZATION drop_table_owner", 0))
			run(fixture.Exec(`CREATE TABLE "S"."T"(a INT NOT NULL,b TEXT,s TEXT,c BIGINT,second_drop TEXT)`, 0))
			run(fixture.Exec(`INSERT INTO "S"."T" VALUES(1,'sentinel',$1,9223372036854775807,'second sentinel'),(2,NULL,NULL,NULL,NULL)`, 2, "O'Brien\n\\tail"))
			run(fixture.Exec(`ALTER TABLE "S"."T" DROP COLUMN b CASCADE`, 0))
			run(fixture.Prepared(fixture.Exec(`ALTER TABLE "S"."T" DROP COLUMN second_drop RESTRICT`, 0)))
		}()
	}
	p.Step(t, fixture.Exec("CREATE TABLE z(v TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO z VALUES('one'),('two')", 2))
	p.Step(t, fixture.Exec("ALTER TABLE ONLY(z) DROP v", 0))
	checkLayout := func() {
		t.Helper()
		p.Step(t, fixture.Select(`SELECT attnum,attname,attisdropped FROM pg_attribute WHERE attrelid='"S"."T"'::regclass AND attnum>0 ORDER BY attnum`, []string{"attnum", "attname", "attisdropped"}, []string{"INT2", "NAME", "BOOL"}, [][]any{{int64(1), "a", false}, {int64(2), "........pg.dropped.2........", true}, {int64(3), "s", false}, {int64(4), "c", false}, {int64(5), "........pg.dropped.5........", true}}))
		p.Step(t, fixture.Select("SELECT attnum,attname,attisdropped FROM pg_attribute WHERE attrelid='z'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attisdropped"}, []string{"INT2", "NAME", "BOOL"}, [][]any{{int64(1), "........pg.dropped.1........", true}}))
	}
	checkLayout()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "sentinel") || strings.Contains(dump.String(), "VALUES ()") || strings.Count(dump.String(), `INSERT INTO "public"."z" DEFAULT VALUES;`) != 2 {
		t.Fatal(dump.String())
	}
	schemaDirective := strings.Index(dump.String(), `CREATE SCHEMA "S" AUTHORIZATION "drop_table_owner";`)
	ownerDirective := strings.Index(dump.String(), `SET SESSION AUTHORIZATION 'drop_table_owner';`)
	tableDirective := strings.Index(dump.String(), `CREATE TABLE "S"."T"`)
	if schemaDirective < 0 || ownerDirective <= schemaDirective || tableDirective <= ownerDirective {
		t.Fatal(dump.String())
	}
	if p.Postgres != nil {
		oracle, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, err = oracle.ExecContext(p.Ctx, `DROP SCHEMA "S" CASCADE; DROP TABLE z`); err != nil {
				t.Fatal(err)
			}
			// The shared fixture supplies postgres independently of the stuntgres
			// authenticator. Replay the canonical owner-bearing bytes unchanged.
			if _, err = oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("real PostgreSQL rejected same owner-bearing live dump", err)
			}
		}()
		var schemaOwner, tableOwner string
		if err = fixture.Admin.QueryRowContext(p.Ctx, `SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='S'`).Scan(&schemaOwner); err != nil || schemaOwner != "drop_table_owner" {
			t.Fatal(schemaOwner, err)
		}
		if err = fixture.Admin.QueryRowContext(p.Ctx, `SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='S' AND c.relname='T'`).Scan(&tableOwner); err != nil || tableOwner != "drop_table_owner" {
			t.Fatal(tableOwner, err)
		}
		var bootstrapTableOwner string
		if err = fixture.Admin.QueryRowContext(p.Ctx, `SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='public' AND c.relname='z'`).Scan(&bootstrapTableOwner); err != nil || bootstrapTableOwner != "postgres" {
			t.Fatal(bootstrapTableOwner, err)
		}
	}
	for range 2 {
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		expected := fixture.Select(`SELECT * FROM "S"."T" ORDER BY a`, []string{"a", "s", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "O'Brien\n\\tail", int64(9223372036854775807)}, {int64(2), nil, nil}})
		p.Step(t, expected)
		p.Step(t, fixture.Prepared(expected))
		checkLayout()
		for _, backend := range p.Backends() {
			db := backend.DB
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				record := backend.Recorder
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
				run(fixture.Exec("SET SESSION AUTHORIZATION drop_table_owner", 0))
				run(expected)
				run(fixture.Prepared(expected))
				for _, prepared := range []bool{false, true} {
					fixture.TableDDLMetadata(t, p.Ctx, conn, backend.PG, prepared, expected.Query, []string{"a", "s", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{false, true, true}, 2)
				}
				run(fixture.Exec(`ALTER TABLE "S"."T" DROP COLUMN IF EXISTS b`, 0))
				run(fixture.Prepared(fixture.Exec(`ALTER TABLE "S"."T" DROP COLUMN IF EXISTS b`, 0)))
			}()
		}
		empty := fixture.Select("SELECT * FROM z", []string{}, []string{}, [][]any{{}, {}})
		p.Step(t, empty)
		p.Step(t, fixture.Prepared(empty))
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
			t.Fatal(err, again.String())
		}
	}
	p.Step(t, fixture.Exec(`ALTER TABLE "S"."T" ADD next_slot INT`, 0))
	p.Step(t, fixture.Select(`SELECT attnum FROM pg_attribute WHERE attrelid='"S"."T"'::regclass AND attname='next_slot'`, []string{"attnum"}, []string{"INT2"}, [][]any{{int64(6)}}))
}

func TestReferenceAlterDropColumnDistinctOwnerRestoreBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"drop_schema_owner", "drop_table_owner"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "S")
	p.Step(t, fixture.Exec("CREATE USER drop_schema_owner", 0))
	p.Step(t, fixture.Exec("CREATE USER drop_table_owner", 0))
	script := `CREATE SCHEMA "S" AUTHORIZATION drop_schema_owner; SET SESSION AUTHORIZATION drop_table_owner; CREATE TABLE "S"."T"(a INT NOT NULL,b TEXT,c BIGINT); INSERT INTO "S"."T" VALUES(1,'gone',9);`
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Native.ExecContext(p.Ctx, `ALTER TABLE "S"."T" DROP b`); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CREATE SCHEMA "S" AUTHORIZATION "drop_schema_owner";`) || !strings.Contains(dump.String(), `SET SESSION AUTHORIZATION 'drop_table_owner';`) {
		t.Fatal(dump.String())
	}
	var err error
	if p.Postgres != nil {
		oracle, err := fixture.Admin.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			// Supply this oracle replay's independent schema CREATE prerequisite;
			// supported namespace ACL serialization remains part of logical replay.
			for _, line := range strings.Split(dump.String(), "\n") {
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "SET SESSION AUTHORIZATION") {
					if _, err = oracle.ExecContext(p.Ctx, `GRANT USAGE,CREATE ON SCHEMA "S" TO drop_table_owner`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = oracle.ExecContext(p.Ctx, line); err != nil {
					t.Fatal(line, err)
				}
			}
		}()
		var schemaOwner, tableOwner string
		if err = fixture.Admin.QueryRowContext(p.Ctx, `SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='S'`).Scan(&schemaOwner); err != nil || schemaOwner != "drop_schema_owner" {
			t.Fatal(schemaOwner, err)
		}
		if err = fixture.Admin.QueryRowContext(p.Ctx, `SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='S' AND c.relname='T'`).Scan(&tableOwner); err != nil || tableOwner != "drop_table_owner" {
			t.Fatal(tableOwner, err)
		}
	}
	for range 2 {
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		p.Step(t, fixture.Select(`SELECT * FROM "S"."T"`, []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}}))
		for _, backend := range p.Backends() {
			db := backend.DB
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				record := backend.Recorder
				fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("SET SESSION AUTHORIZATION drop_schema_owner", 0))
				for _, prepared := range []bool{false, true} {
					failure := fixture.ImplicitError(`ALTER TABLE "S"."T" DROP IF EXISTS missing`, fixture.Diagnostic{Code: "42501", Message: "must be owner of table T"})
					failure.ErrorStage = "execute"
					failure.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, record, failure)
				}
			}()
		}
		p.Step(t, fixture.Select(`SELECT * FROM "S"."T"`, []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}}))
		var again bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
			t.Fatal(err, again.String())
		}
	}
}
