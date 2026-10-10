package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"testing/iotest"
)

func TestReferenceCollationVersion(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		var notices []referenceDiagnostic
		db, conn, recorder := noticeReferencePool(t, p, b.pg, &notices)
		b.db, b.recorder = db, recorder
		_ = conn.Close()
		collationExec(t, p, b, `CREATE COLLATION coll_version (PROVIDER=builtin,LOCALE='C',VERSION='stale')`)
		want := []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `collation "coll_version" has version mismatch`, Detail: "The collation in the database was created using version stale, but the operating system provides version 1.", Hint: "Rebuild all objects affected by this collation and run ALTER COLLATION public.coll_version REFRESH VERSION, or build PostgreSQL with the right library version."}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices got %#v want %#v", notices, want)
		}
		oid := collationOID(t, b.db, "coll_version")
		collationRoutes(t, p, b, referenceSelect(`SELECT collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname='coll_version'`, []string{"collversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{"stale", "1"}}))
		collationExec(t, p, b, `CREATE COLLATION coll_version_copy FROM coll_version`)
		collationRoutes(t, p, b, referenceSelect(`SELECT collversion FROM pg_catalog.pg_collation WHERE collname='coll_version_copy'`, []string{"collversion"}, []string{"TEXT"}, [][]any{{"1"}}))
		notices = nil
		collationExec(t, p, b, `ALTER COLLATION coll_version REFRESH VERSION`)
		want = []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "changing version from stale to 1"}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices got %#v want %#v", notices, want)
		}
		if collationOID(t, b.db, "coll_version") != oid {
			t.Fatal("refresh changed identity")
		}
		notices = nil
		collationExec(t, p, b, `ALTER COLLATION coll_version REFRESH VERSION`)
		want = []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "version has not changed"}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices got %#v want %#v", notices, want)
		}
		collationExec(t, p, b, `CREATE COLLATION coll_version_c FROM pg_catalog."C"`)
		notices = nil
		collationExec(t, p, b, `ALTER COLLATION coll_version_c REFRESH VERSION`)
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("C refresh notices %#v", notices)
		}
		collationRoutes(t, p, b, referenceSelect(`SELECT collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname='coll_version_c'`, []string{"collversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}))
		for _, version := range []string{"", "future!?/1.0"} {
			notices = nil
			collationExec(t, p, b, `CREATE COLLATION coll_version_arbitrary (PROVIDER=builtin,LOCALE='C',VERSION='`+version+`')`)
			want = []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `collation "coll_version_arbitrary" has version mismatch`, Detail: "The collation in the database was created using version " + version + ", but the operating system provides version 1.", Hint: "Rebuild all objects affected by this collation and run ALTER COLLATION public.coll_version_arbitrary REFRESH VERSION, or build PostgreSQL with the right library version."}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("arbitrary version notices %#v want %#v", notices, want)
			}
			collationRoutes(t, p, b, referenceSelect(`SELECT collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname='coll_version_arbitrary'`, []string{"collversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{version, "1"}}))
			collationExec(t, p, b, `DROP COLLATION coll_version_arbitrary`)
		}
		notices = nil
		collationExec(t, p, b, `CREATE COLLATION IF NOT EXISTS coll_version_copy FROM pg_catalog."POSIX"`, `DROP COLLATION IF EXISTS coll_version_absent`)
		want = []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `collation "coll_version_copy" already exists, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `collation "coll_version_absent" does not exist, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("conditional utility notices %#v want %#v", notices, want)
		}
		collationRoutes(t, p, b, referenceSelect(`SELECT collprovider,colllocale,collversion FROM pg_catalog.pg_collation WHERE collname='coll_version_copy'`, []string{"collprovider", "colllocale", "collversion"}, []string{"CHAR", "TEXT", "TEXT"}, [][]any{{"b", "C", "1"}}))
		missing := referenceErrorDiagnostic(`SELECT pg_collation_actual_version($1::oid) AS actual`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "collation with OID 4294967295 does not exist"}, int64(4294967295))
		missing.selectRows, missing.errorStage = true, "execute"
		collationRoutes(t, p, b, missing)
		collationExec(t, p, b, `DROP COLLATION coll_version`)
		collationRoutes(t, p, b, referenceSelect(`SELECT upper('é' COLLATE coll_version_copy) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
		collationError(t, p, b, `ALTER COLLATION pg_catalog."default" REFRESH VERSION`, referenceDiagnostic{Code: "XX000", Message: "cannot refresh version of default collation", Hint: "Use ALTER DATABASE ... REFRESH COLLATION VERSION instead."})
	})
}

func TestReferenceCollationLifecycle(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		schemaReferenceCleanup(t, "coll_life_schema")
		collationExec(t, p, b, `CREATE SCHEMA coll_life_schema`, `CREATE COLLATION coll_life FROM pg_catalog."C"`, `CREATE TABLE coll_life_t(id INT,v TEXT COLLATE coll_life)`, `CREATE VIEW coll_life_v AS SELECT v COLLATE coll_life AS v FROM coll_life_t`)
		collationStep(t, p, b, referenceExec(`INSERT INTO coll_life_t VALUES(1,'é'),(2,'a')`, 2))
		oid := collationOID(t, b.db, "coll_life")
		held, err := b.db.Query(`SELECT v FROM coll_life_v ORDER BY v`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		stmt, err := b.db.Prepare(`SELECT v FROM coll_life_v ORDER BY v`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		collationExec(t, p, b, `ALTER COLLATION coll_life RENAME TO coll_life_renamed`, `ALTER COLLATION coll_life_renamed SET SCHEMA coll_life_schema`)
		if collationOID(t, b.db, "coll_life_renamed") != oid {
			t.Fatal("rename/move changed identity")
		}
		collationError(t, p, b, `SELECT 'a' COLLATE coll_life`, referenceDiagnostic{Code: "42704", Message: `collation "coll_life" for encoding "UTF8" does not exist`, Position: "12"})
		collationRoutes(t, p, b, referenceSelect(`SELECT upper('é' COLLATE coll_life_schema.coll_life_renamed) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
		var definition string
		if err = b.db.QueryRow(`SELECT pg_get_viewdef('coll_life_v'::regclass,true)`).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "coll_life_schema.coll_life_renamed") {
			t.Fatal("stale view identity", definition)
		}
		s := referenceStep{columns: []string{"v"}, types: []string{"TEXT"}, rows: [][]any{{"a"}, {"é"}}}
		deleteReferenceConsume(t, b.pg, held, nil, s)
		rows, err := stmt.Query()
		deleteReferenceConsume(t, b.pg, rows, err, s)
		collationExec(t, p, b, `CREATE COLLATION coll_life FROM pg_catalog."POSIX"`)
		if collationOID(t, b.db, "coll_life") == oid {
			t.Fatal("recreated name reused identity")
		}
	})
}

func TestReferenceCollationDependencies(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		collationExec(t, p, b, `CREATE COLLATION coll_dep FROM pg_catalog."C"`, `CREATE TABLE coll_dep_t(id INT,v TEXT COLLATE coll_dep)`)
		collationStep(t, p, b, referenceExec(`INSERT INTO coll_dep_t VALUES(7,'é')`, 1))
		collationError(t, p, b, `DROP COLLATION coll_dep`, referenceDiagnostic{Code: "2BP01", Message: "cannot drop collation coll_dep because other objects depend on it", Detail: "column v of table coll_dep_t depends on collation coll_dep", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		collationExec(t, p, b, `DROP COLLATION coll_dep CASCADE`)
		collationRoutes(t, p, b, referenceSelect(`SELECT * FROM coll_dep_t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		collationExec(t, p, b, `CREATE COLLATION coll_dep_source FROM pg_catalog."C"`, `CREATE COLLATION coll_dep_copy FROM coll_dep_source`, `DROP COLLATION coll_dep_source`)
		collationRoutes(t, p, b, referenceSelect(`SELECT upper('é' COLLATE coll_dep_copy) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}}))
		for _, tc := range []struct{ name, consumer string }{
			{"domain", `CREATE DOMAIN coll_dep_domain AS TEXT COLLATE coll_dep_extra`},
			{"view", `CREATE VIEW coll_dep_view AS SELECT 'a' COLLATE coll_dep_extra AS v`},
			{"default", `CREATE TABLE coll_dep_default(id INT,v TEXT DEFAULT ('a' COLLATE coll_dep_extra))`},
			{"check", `CREATE TABLE coll_dep_check(id INT,v TEXT CHECK(v COLLATE coll_dep_extra > ''))`},
			{"index", `CREATE INDEX coll_dep_index ON coll_dep_t((id::text COLLATE coll_dep_extra))`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				collationExec(t, p, b, `CREATE COLLATION coll_dep_extra FROM pg_catalog."C"`, tc.consumer, `DROP COLLATION coll_dep_extra CASCADE`)
				collationAbsent(t, p, b, "coll_dep_extra")
				collationRoutes(t, p, b, referenceSelect(`SELECT id FROM coll_dep_t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		}
		collationExec(t, p, b, `CREATE COLLATION coll_dep_batch_a FROM pg_catalog."C"`, `CREATE COLLATION coll_dep_batch_b FROM pg_catalog."C"`, `CREATE TABLE coll_dep_batch_t(v TEXT COLLATE coll_dep_batch_b)`)
		collationError(t, p, b, `DROP COLLATION coll_dep_batch_a,coll_dep_batch_b`, referenceDiagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "column v of table coll_dep_batch_t depends on collation coll_dep_batch_b", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		collationOID(t, b.db, "coll_dep_batch_a")
		collationOID(t, b.db, "coll_dep_batch_b")
	})
}

func TestReferenceCollationNativeLifecycle(t *testing.T) {
	dsn := "reference/" + t.Name()
	roleReferenceCleanup(t, []string{"coll_control_owner"})
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		schemaReferenceCleanup(t, "coll_control_schema")
		collationExec(t, p, b, `CREATE USER coll_control_owner`, `CREATE SCHEMA coll_control_schema AUTHORIZATION coll_control_owner`)
		collationExec(t, p, b, `CREATE COLLATION coll_control_c FROM pg_catalog."C"`, `CREATE COLLATION coll_control_full FROM pg_catalog.pg_unicode_fast`, `CREATE DOMAIN coll_control_domain AS TEXT COLLATE coll_control_c`, `CREATE TABLE coll_control_t(id INT,v coll_control_domain DEFAULT ('a' COLLATE coll_control_c),CONSTRAINT coll_control_ck CHECK(v COLLATE coll_control_c > ''))`, `CREATE INDEX coll_control_ix ON coll_control_t(v COLLATE coll_control_c)`, `CREATE VIEW coll_control_v AS SELECT id,upper(v COLLATE coll_control_full) AS v FROM coll_control_t`)
		collationExec(t, p, b, `CREATE COLLATION coll_control_explicit_builtin (PROVIDER=builtin,LOCALE='PG_UNICODE_FAST')`, `CREATE COLLATION coll_control_explicit_libc (LOCALE='C')`, `CREATE COLLATION coll_control_ucs FROM pg_catalog.ucs_basic`, `CREATE COLLATION coll_control_posix FROM pg_catalog."POSIX"`)
		collationExec(t, p, b, `CREATE COLLATION coll_actor_identity FROM pg_catalog."C"`)
		collationStep(t, p, b, referenceExec(`INSERT INTO coll_control_t VALUES(1,'ß'),(2,'é')`, 2))
		collationExec(t, p, b, `ALTER COLLATION coll_control_c RENAME TO coll_control_renamed`)
		collationExec(t, p, b, `ALTER COLLATION coll_control_renamed SET SCHEMA coll_control_schema`, `ALTER COLLATION coll_control_schema.coll_control_renamed OWNER TO coll_control_owner`)
		if b.pg {
			collationExec(t, p, b, `ALTER ROLE coll_control_owner PASSWORD 'role-core-fixture'`)
		}
		actor := roleScenarioLoginPool(t, p.nativeConnector.Driver().(*stuntgres.Driver), b.pg, dsn, "coll_control_owner")
		result, actorErr := actor.ExecContext(p.ctx, `CREATE COLLATION coll_control_schema.coll_role_identity FROM pg_catalog."C"`)
		deleteReferenceCount(t, result, actorErr, 0)
		result, actorErr = actor.ExecContext(p.ctx, `ALTER COLLATION coll_control_schema.coll_control_renamed REFRESH VERSION`)
		deleteReferenceCount(t, result, actorErr, 0)
		if err := actor.Close(); err != nil {
			t.Fatal(err)
		}
		witness := referenceSelect(`SELECT id,v FROM coll_control_v ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "SS"}, {int64(2), "É"}})
		properties := referenceSelect(`SELECT collname,collencoding,collprovider,collversion,pg_collation_actual_version(oid) AS actual FROM pg_catalog.pg_collation WHERE collname LIKE 'coll_control_%' ORDER BY collname`, []string{"collname", "collencoding", "collprovider", "collversion", "actual"}, []string{"NAME", "INT4", "CHAR", "TEXT", "TEXT"}, [][]any{
			{"coll_control_explicit_builtin", int64(6), "b", "1", "1"},
			{"coll_control_explicit_libc", int64(6), "c", nil, nil},
			{"coll_control_full", int64(6), "b", "1", "1"},
			{"coll_control_posix", int64(-1), "c", nil, nil},
			{"coll_control_renamed", int64(-1), "c", nil, nil},
			{"coll_control_ucs", int64(6), "b", "1", "1"},
		})
		independent := referenceSelect(`SELECT COUNT(*) AS n FROM pg_catalog.pg_depend WHERE classid=3456 AND refclassid=3456`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		collationRoutes(t, p, b, properties)
		collationRoutes(t, p, b, independent)
		owners := referenceSelect(`SELECT collname,collowner=(SELECT collowner FROM pg_catalog.pg_collation WHERE collname='coll_actor_identity') AS creator,collowner=(SELECT collowner FROM pg_catalog.pg_collation WHERE collname='coll_role_identity') AS transferred FROM pg_catalog.pg_collation WHERE collname LIKE 'coll_control_%' ORDER BY collname`, []string{"collname", "creator", "transferred"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"coll_control_explicit_builtin", true, false}, {"coll_control_explicit_libc", true, false}, {"coll_control_full", true, false}, {"coll_control_posix", true, false}, {"coll_control_renamed", false, true}, {"coll_control_ucs", true, false}})
		collationRoutes(t, p, b, owners)
		collationRoutes(t, p, b, referenceSelect(`SELECT upper('é' COLLATE coll_control_ucs) AS ascii`, []string{"ascii"}, []string{"TEXT"}, [][]any{{"é"}}))
		collationRoutes(t, p, b, witness)
		if b.pg {
			return
		}
		held, err := b.db.Query(witness.query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE COLLATION") || strings.Contains(dump.String(), `COLLATE "public"."coll_control_c"`) {
			t.Fatal("missing or stale identity", dump.String())
		}
		if p.postgres != nil {
			if _, err = p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err = p.postgres.ExecContext(p.ctx, "CREATE USER coll_control_owner"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, cleanupErr := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; DROP SCHEMA IF EXISTS coll_control_schema CASCADE; CREATE SCHEMA public; DROP ROLE coll_control_owner"); cleanupErr != nil {
					t.Error("PostgreSQL replay owned role cleanup", cleanupErr)
				}
			}()
			if _, err = p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL emitted SQL replay", err)
			}
			pg := referenceBackend{name: "postgres replay", db: p.postgres, recorder: p.postgresRecord, pg: true}
			collationRoutes(t, p, pg, witness)
			collationRoutes(t, p, pg, properties)
			collationRoutes(t, p, pg, independent)
			collationRoutes(t, p, pg, owners)
		}
		for range 2 {
			if err = stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			collationRoutes(t, p, b, witness)
			collationRoutes(t, p, b, properties)
			collationRoutes(t, p, b, independent)
			collationRoutes(t, p, b, owners)
		}
		fresh := updateReturningNativeDestination(t)
		if _, err = fresh.Exec("CREATE USER coll_control_owner"); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rows, err := fresh.Query(witness.query)
		deleteReferenceConsume(t, false, rows, err, witness)
		propertyRows, propertyErr := fresh.Query(properties.query)
		deleteReferenceConsume(t, false, propertyRows, propertyErr, properties)
		dependencyRows, dependencyErr := fresh.Query(independent.query)
		deleteReferenceConsume(t, false, dependencyRows, dependencyErr, independent)
		ownerRows, ownerErr := fresh.Query(owners.query)
		deleteReferenceConsume(t, false, ownerRows, ownerErr, owners)
		freshActor := roleScenarioLoginPool(t, fresh.Driver().(*stuntgres.Driver), false, t.Name(), "coll_control_owner")
		result, actorErr = freshActor.ExecContext(p.ctx, `ALTER COLLATION coll_control_schema.coll_control_renamed REFRESH VERSION`)
		deleteReferenceCount(t, result, actorErr, 0)
		if err = freshActor.Close(); err != nil {
			t.Fatal(err)
		}
		deleteReferenceConsume(t, false, held, nil, witness)
		for _, bad := range []string{`CREATE COLLATION broken (PROVIDER=icu,LOCALE='und');`, `CREATE COLLATION broken FROM pg_catalog."C"; ALTER COLLATION broken OWNER TO coll_unknown_owner;`} {
			if err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid restore succeeded")
			}
			collationRoutes(t, p, b, witness)
			collationAbsent(t, p, b, "broken")
		}
		readFailure := errors.New("collation restore reader failure")
		if err = stuntgres.Restore(p.ctx, p.native, iotest.ErrReader(readFailure)); !errors.Is(err, readFailure) {
			t.Fatal("reader failure lost", err)
		}
		collationRoutes(t, p, b, witness)
		collationRoutes(t, p, b, properties)
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		if err = stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore changed target", err)
		}
		collationRoutes(t, p, b, witness)
		collationRoutes(t, p, b, properties)
	})
}
