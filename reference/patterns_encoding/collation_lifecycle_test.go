package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"testing/iotest"
)

func TestReferenceCollationVersion(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		db, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.DB, b.Recorder = db, recorder
		_ = conn.Close()
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_version (PROVIDER=builtin,LOCALE='C',VERSION='stale')`)
		want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `collation "coll_version" has version mismatch`, Detail: "The collation in the database was created using version stale, but the operating system provides version 1.", Hint: "Rebuild all objects affected by this collation and run ALTER COLLATION public.coll_version REFRESH VERSION, or build PostgreSQL with the right library version."}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices got %#v want %#v", notices, want)
		}
		oid := fixture.CollationOID(t, b.DB, "coll_version")
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname='coll_version'`, []string{"collversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{"stale", "1"}}))
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_version_copy FROM coll_version`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT collversion FROM pg_catalog.pg_collation WHERE collname='coll_version_copy'`, []string{"collversion"}, []string{"TEXT"}, [][]any{{"1"}}))
		notices = nil
		fixture.CollationExec(t, p, b, `ALTER COLLATION coll_version REFRESH VERSION`)
		want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "changing version from stale to 1"}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices got %#v want %#v", notices, want)
		}
		if fixture.CollationOID(t, b.DB, "coll_version") != oid {
			t.Fatal("refresh changed identity")
		}
		notices = nil
		fixture.CollationExec(t, p, b, `ALTER COLLATION coll_version REFRESH VERSION`)
		want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "version has not changed"}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices got %#v want %#v", notices, want)
		}
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_version_c FROM pg_catalog."C"`)
		notices = nil
		fixture.CollationExec(t, p, b, `ALTER COLLATION coll_version_c REFRESH VERSION`)
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("C refresh notices %#v", notices)
		}
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname='coll_version_c'`, []string{"collversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}))
		for _, version := range []string{"", "future!?/1.0"} {
			notices = nil
			fixture.CollationExec(t, p, b, `CREATE COLLATION coll_version_arbitrary (PROVIDER=builtin,LOCALE='C',VERSION='`+version+`')`)
			want = []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `collation "coll_version_arbitrary" has version mismatch`, Detail: "The collation in the database was created using version " + version + ", but the operating system provides version 1.", Hint: "Rebuild all objects affected by this collation and run ALTER COLLATION public.coll_version_arbitrary REFRESH VERSION, or build PostgreSQL with the right library version."}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("arbitrary version notices %#v want %#v", notices, want)
			}
			fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname='coll_version_arbitrary'`, []string{"collversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{version, "1"}}))
			fixture.CollationExec(t, p, b, `DROP COLLATION coll_version_arbitrary`)
		}
		notices = nil
		fixture.CollationExec(t, p, b, `CREATE COLLATION IF NOT EXISTS coll_version_copy FROM pg_catalog."POSIX"`, `DROP COLLATION IF EXISTS coll_version_absent`)
		want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `collation "coll_version_copy" already exists, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `collation "coll_version_absent" does not exist, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("conditional utility notices %#v want %#v", notices, want)
		}
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT collprovider,colllocale,collversion FROM pg_catalog.pg_collation WHERE collname='coll_version_copy'`, []string{"collprovider", "colllocale", "collversion"}, []string{"CHAR", "TEXT", "TEXT"}, [][]any{{"b", "C", "1"}}))
		missing := fixture.ErrorDiagnostic(`SELECT pg_collation_actual_version($1::oid) AS actual`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "collation with OID 4294967295 does not exist"}, int64(4294967295))
		missing.SelectRows, missing.ErrorStage = true, "execute"
		fixture.CollationRoutes(t, p, b, missing)
		fixture.CollationExec(t, p, b, `DROP COLLATION coll_version`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT upper('é' COLLATE coll_version_copy) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
		fixture.CollationError(t, p, b, `ALTER COLLATION pg_catalog."default" REFRESH VERSION`, fixture.Diagnostic{Code: "XX000", Message: "cannot refresh version of default collation", Hint: "Use ALTER DATABASE ... REFRESH COLLATION VERSION instead."})
	})
}

func TestReferenceCollationLifecycle(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.SchemaReferenceCleanup(t, "coll_life_schema")
		fixture.CollationExec(t, p, b, `CREATE SCHEMA coll_life_schema`, `CREATE COLLATION coll_life FROM pg_catalog."C"`, `CREATE TABLE coll_life_t(id INT,v TEXT COLLATE coll_life)`, `CREATE VIEW coll_life_v AS SELECT v COLLATE coll_life AS v FROM coll_life_t`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO coll_life_t VALUES(1,'é'),(2,'a')`, 2))
		oid := fixture.CollationOID(t, b.DB, "coll_life")
		held, err := b.DB.Query(`SELECT v FROM coll_life_v ORDER BY v`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		stmt, err := b.DB.Prepare(`SELECT v FROM coll_life_v ORDER BY v`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.CollationExec(t, p, b, `ALTER COLLATION coll_life RENAME TO coll_life_renamed`, `ALTER COLLATION coll_life_renamed SET SCHEMA coll_life_schema`)
		if fixture.CollationOID(t, b.DB, "coll_life_renamed") != oid {
			t.Fatal("rename/move changed identity")
		}
		fixture.CollationError(t, p, b, `SELECT 'a' COLLATE coll_life`, fixture.Diagnostic{Code: "42704", Message: `collation "coll_life" for encoding "UTF8" does not exist`, Position: "12"})
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT upper('é' COLLATE coll_life_schema.coll_life_renamed) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
		var definition string
		if err = b.DB.QueryRow(`SELECT pg_get_viewdef('coll_life_v'::regclass,true)`).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "coll_life_schema.coll_life_renamed") {
			t.Fatal("stale view identity", definition)
		}
		s := fixture.Step{Columns: []string{"v"}, Types: []string{"TEXT"}, Rows: [][]any{{"a"}, {"é"}}}
		fixture.DeleteReferenceConsume(t, b.PG, held, nil, s)
		rows, err := stmt.Query()
		fixture.DeleteReferenceConsume(t, b.PG, rows, err, s)
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_life FROM pg_catalog."POSIX"`)
		if fixture.CollationOID(t, b.DB, "coll_life") == oid {
			t.Fatal("recreated name reused identity")
		}
	})
}

func TestReferenceCollationDependencies(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_dep FROM pg_catalog."C"`, `CREATE TABLE coll_dep_t(id INT,v TEXT COLLATE coll_dep)`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO coll_dep_t VALUES(7,'é')`, 1))
		fixture.CollationError(t, p, b, `DROP COLLATION coll_dep`, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop collation coll_dep because other objects depend on it", Detail: "column v of table coll_dep_t depends on collation coll_dep", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		fixture.CollationExec(t, p, b, `DROP COLLATION coll_dep CASCADE`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT * FROM coll_dep_t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_dep_source FROM pg_catalog."C"`, `CREATE COLLATION coll_dep_copy FROM coll_dep_source`, `DROP COLLATION coll_dep_source`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT upper('é' COLLATE coll_dep_copy) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
		for _, tc := range []struct{ name, consumer string }{
			{"domain", `CREATE DOMAIN coll_dep_domain AS TEXT COLLATE coll_dep_extra`},
			{"view", `CREATE VIEW coll_dep_view AS SELECT 'a' COLLATE coll_dep_extra AS v`},
			{"default", `CREATE TABLE coll_dep_default(id INT,v TEXT DEFAULT ('a' COLLATE coll_dep_extra))`},
			{"check", `CREATE TABLE coll_dep_check(id INT,v TEXT CHECK(v COLLATE coll_dep_extra > ''))`},
			{"index", `CREATE INDEX coll_dep_index ON coll_dep_t((id::text COLLATE coll_dep_extra))`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fixture.CollationExec(t, p, b, `CREATE COLLATION coll_dep_extra FROM pg_catalog."C"`, tc.consumer, `DROP COLLATION coll_dep_extra CASCADE`)
				fixture.CollationAbsent(t, p, b, "coll_dep_extra")
				fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id FROM coll_dep_t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		}
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_dep_batch_a FROM pg_catalog."C"`, `CREATE COLLATION coll_dep_batch_b FROM pg_catalog."C"`, `CREATE TABLE coll_dep_batch_t(v TEXT COLLATE coll_dep_batch_b)`)
		fixture.CollationError(t, p, b, `DROP COLLATION coll_dep_batch_a,coll_dep_batch_b`, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "column v of table coll_dep_batch_t depends on collation coll_dep_batch_b", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		fixture.CollationOID(t, b.DB, "coll_dep_batch_a")
		fixture.CollationOID(t, b.DB, "coll_dep_batch_b")
	})
}

func TestReferenceCollationNativeLifecycle(t *testing.T) {
	dsn := "reference/" + t.Name()
	fixture.RoleReferenceCleanup(t, []string{"coll_control_owner"})
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.SchemaReferenceCleanup(t, "coll_control_schema")
		fixture.CollationExec(t, p, b, `CREATE USER coll_control_owner`, `CREATE SCHEMA coll_control_schema AUTHORIZATION coll_control_owner`)
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_control_c FROM pg_catalog."C"`, `CREATE COLLATION coll_control_full FROM pg_catalog.pg_unicode_fast`, `CREATE DOMAIN coll_control_domain AS TEXT COLLATE coll_control_c`, `CREATE TABLE coll_control_t(id INT,v coll_control_domain DEFAULT ('a' COLLATE coll_control_c),CONSTRAINT coll_control_ck CHECK(v COLLATE coll_control_c > ''))`, `CREATE INDEX coll_control_ix ON coll_control_t(v COLLATE coll_control_c)`, `CREATE VIEW coll_control_v AS SELECT id,upper(v COLLATE coll_control_full) AS v FROM coll_control_t`)
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_control_explicit_builtin (PROVIDER=builtin,LOCALE='PG_UNICODE_FAST')`, `CREATE COLLATION coll_control_explicit_libc (LOCALE='C')`, `CREATE COLLATION coll_control_ucs FROM pg_catalog.ucs_basic`, `CREATE COLLATION coll_control_posix FROM pg_catalog."POSIX"`)
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_actor_identity FROM pg_catalog."C"`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO coll_control_t VALUES(1,'ß'),(2,'é')`, 2))
		fixture.CollationExec(t, p, b, `ALTER COLLATION coll_control_c RENAME TO coll_control_renamed`)
		fixture.CollationExec(t, p, b, `ALTER COLLATION coll_control_renamed SET SCHEMA coll_control_schema`, `ALTER COLLATION coll_control_schema.coll_control_renamed OWNER TO coll_control_owner`)
		if b.PG {
			fixture.CollationExec(t, p, b, `ALTER ROLE coll_control_owner PASSWORD 'role-core-fixture'`)
		}
		actor := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "coll_control_owner")
		result, actorErr := actor.ExecContext(p.Ctx, `CREATE COLLATION coll_control_schema.coll_role_identity FROM pg_catalog."C"`)
		fixture.DeleteReferenceCount(t, result, actorErr, 0)
		result, actorErr = actor.ExecContext(p.Ctx, `ALTER COLLATION coll_control_schema.coll_control_renamed REFRESH VERSION`)
		fixture.DeleteReferenceCount(t, result, actorErr, 0)
		if err := actor.Close(); err != nil {
			t.Fatal(err)
		}
		witness := fixture.Select(`SELECT id,v FROM coll_control_v ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "SS"}, {int64(2), "É"}})
		properties := fixture.Select(`SELECT collname,collencoding,collprovider,collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname LIKE 'coll_control_%' ORDER BY collname`, []string{"collname", "collencoding", "collprovider", "collversion", "actual"}, []string{"NAME", "INT4", "CHAR", "TEXT", "TEXT"}, [][]any{
			{"coll_control_explicit_builtin", int64(6), "b", "1", "1"},
			{"coll_control_explicit_libc", int64(6), "c", nil, nil},
			{"coll_control_full", int64(6), "b", "1", "1"},
			{"coll_control_posix", int64(-1), "c", nil, nil},
			{"coll_control_renamed", int64(-1), "c", nil, nil},
			{"coll_control_ucs", int64(6), "b", "1", "1"},
		})
		independent := fixture.Select(`SELECT COUNT(*) AS n FROM pg_catalog.pg_depend WHERE classid=3456 AND refclassid=3456`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.CollationRoutes(t, p, b, properties)
		fixture.CollationRoutes(t, p, b, independent)
		owners := fixture.Select(`SELECT collname,collowner=(SELECT collowner FROM pg_catalog.pg_collation WHERE collname='coll_actor_identity') AS creator,collowner=(SELECT collowner FROM pg_catalog.pg_collation WHERE collname='coll_role_identity') AS transferred FROM pg_catalog.pg_collation WHERE collname LIKE 'coll_control_%' ORDER BY collname`, []string{"collname", "creator", "transferred"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"coll_control_explicit_builtin", true, false}, {"coll_control_explicit_libc", true, false}, {"coll_control_full", true, false}, {"coll_control_posix", true, false}, {"coll_control_renamed", false, true}, {"coll_control_ucs", true, false}})
		fixture.CollationRoutes(t, p, b, owners)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT upper('é' COLLATE coll_control_ucs) AS ascii`, []string{"ascii"}, []string{"TEXT"}, [][]any{{"é"}}))
		fixture.CollationRoutes(t, p, b, witness)
		if b.PG {
			return
		}
		held, err := b.DB.Query(witness.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE COLLATION") || strings.Contains(dump.String(), `COLLATE "public"."coll_control_c"`) {
			t.Fatal("missing or stale identity", dump.String())
		}
		if p.Postgres != nil {
			if _, err = p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err = p.Postgres.ExecContext(p.Ctx, "CREATE USER coll_control_owner"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, cleanupErr := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; DROP SCHEMA IF EXISTS coll_control_schema CASCADE; CREATE SCHEMA public; DROP ROLE coll_control_owner"); cleanupErr != nil {
					t.Error("PostgreSQL replay owned role cleanup", cleanupErr)
				}
			}()
			if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL emitted SQL replay", err)
			}
			pg := fixture.Backend{Name: "postgres replay", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
			fixture.CollationRoutes(t, p, pg, witness)
			fixture.CollationRoutes(t, p, pg, properties)
			fixture.CollationRoutes(t, p, pg, independent)
			fixture.CollationRoutes(t, p, pg, owners)
		}
		for range 2 {
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.CollationRoutes(t, p, b, witness)
			fixture.CollationRoutes(t, p, b, properties)
			fixture.CollationRoutes(t, p, b, independent)
			fixture.CollationRoutes(t, p, b, owners)
		}
		fresh := fixture.UpdateReturningNativeDestination(t)
		if _, err = fresh.Exec("CREATE USER coll_control_owner"); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rows, err := fresh.Query(witness.Query)
		fixture.DeleteReferenceConsume(t, false, rows, err, witness)
		propertyRows, propertyErr := fresh.Query(properties.Query)
		fixture.DeleteReferenceConsume(t, false, propertyRows, propertyErr, properties)
		dependencyRows, dependencyErr := fresh.Query(independent.Query)
		fixture.DeleteReferenceConsume(t, false, dependencyRows, dependencyErr, independent)
		ownerRows, ownerErr := fresh.Query(owners.Query)
		fixture.DeleteReferenceConsume(t, false, ownerRows, ownerErr, owners)
		freshActor := fixture.RoleScenarioLoginPool(t, fresh.Driver().(*stuntgres.Driver), false, t.Name(), "coll_control_owner")
		result, actorErr = freshActor.ExecContext(p.Ctx, `ALTER COLLATION coll_control_schema.coll_control_renamed REFRESH VERSION`)
		fixture.DeleteReferenceCount(t, result, actorErr, 0)
		if err = freshActor.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.DeleteReferenceConsume(t, false, held, nil, witness)
		for _, bad := range []string{`CREATE COLLATION broken (PROVIDER=icu,LOCALE='und');`, `CREATE COLLATION broken FROM pg_catalog."C"; ALTER COLLATION broken OWNER TO coll_unknown_owner;`} {
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid restore succeeded")
			}
			fixture.CollationRoutes(t, p, b, witness)
			fixture.CollationAbsent(t, p, b, "broken")
		}
		readFailure := errors.New("collation restore reader failure")
		if err = stuntgres.Restore(p.Ctx, p.Native, iotest.ErrReader(readFailure)); !errors.Is(err, readFailure) {
			t.Fatal("reader failure lost", err)
		}
		fixture.CollationRoutes(t, p, b, witness)
		fixture.CollationRoutes(t, p, b, properties)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore changed target", err)
		}
		fixture.CollationRoutes(t, p, b, witness)
		fixture.CollationRoutes(t, p, b, properties)
	})
}
