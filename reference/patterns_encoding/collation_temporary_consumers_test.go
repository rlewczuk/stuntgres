package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationTemporaryRetainedConsumers(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
		exec := func(queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(fixture.Exec(q, 0))
			}
		}
		exec(`CREATE COLLATION pg_temp.coll_retained FROM pg_catalog.pg_unicode_fast`,
			`CREATE TEMP TABLE coll_retained_local(id INT,v TEXT COLLATE pg_temp.coll_retained DEFAULT ('é' COLLATE pg_temp.coll_retained),seed TEXT DEFAULT 'a',g TEXT GENERATED ALWAYS AS (upper(v COLLATE pg_temp.coll_retained)) STORED,CHECK(v COLLATE pg_temp.coll_retained > ''),CONSTRAINT coll_retained_seed_nonempty CHECK(seed > ''))`)
		run(fixture.Exec(`INSERT INTO coll_retained_local(id,v) VALUES(1,'ß')`, 1))
		baseline := fixture.Select(`SELECT id,v,seed,g FROM coll_retained_local WHERE id=1`, []string{"id", "v", "seed", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "ß", "a", "SS"}})
		run(baseline)
		providerOID := fixture.CollationOID(t, b.DB, "coll_retained")
		var schemas, namespace string
		if err := owner.QueryRowContext(p.Ctx, `SELECT current_schemas(true)`).Scan(&schemas); err != nil {
			t.Fatal(err)
		}
		for _, name := range strings.Split(strings.Trim(schemas, "{}"), ",") {
			name = strings.Trim(name, `"`)
			if strings.HasPrefix(name, "pg_temp_") {
				namespace = name
			}
		}
		if namespace == "" {
			t.Fatal("missing genuine owned TEMP namespace", schemas)
		}
		dependencySQL := `SELECT COUNT(*) FROM pg_catalog.pg_depend WHERE refclassid=3456 AND refobjid=$1`
		var dependencies int64
		if err := owner.QueryRowContext(p.Ctx, dependencySQL, providerOID).Scan(&dependencies); err != nil || dependencies == 0 {
			t.Fatal("missing retained provider dependency authority", dependencies, err)
		}
		for cycle := range 4 {
			if !b.PG {
				var err error
				if cycle%2 == 0 {
					err = stuntgres.Reset(p.Ctx, p.Native)
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE COLLATION coll_retained_durable FROM pg_catalog."C";`))
				}
				if err != nil {
					t.Fatal("retained TEMP control", cycle, err)
				}
			}
			run(baseline)
			if actual := fixture.CollationOID(t, b.DB, "coll_retained"); actual != providerOID {
				t.Fatal("control changed exact TEMP provider identity", actual, providerOID)
			}
			var currentDependencies int64
			if err := owner.QueryRowContext(p.Ctx, dependencySQL, providerOID).Scan(&currentDependencies); err != nil || currentDependencies != dependencies {
				t.Fatal("control changed retained provider manifests", currentDependencies, dependencies, err)
			}
			// Every catalog publication must validate the exact retained manifests.
			exec(`CREATE TABLE coll_retained_publication(id INT)`, `DROP TABLE coll_retained_publication`)
			// Republish this exact retained relation, not merely unrelated objects.
			// Other explicit-COLLATE DEFAULT/CHECK/generated manifests stay captured.
			exec(`ALTER TABLE coll_retained_local ALTER seed SET DEFAULT 'b'`)
			run(fixture.Exec(`INSERT INTO coll_retained_local(id) VALUES($1)`, 1, int64(cycle+2)))
			run(fixture.Prepared(fixture.Exec(`UPDATE coll_retained_local SET v='ß' WHERE id=1`, 1)))
			run(baseline)
			value := fixture.Select(`SELECT v,seed,g FROM coll_retained_local WHERE id=$1`, []string{"v", "seed", "g"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"é", "b", "É"}}, int64(cycle+2))
			run(value)
			run(fixture.Prepared(value))
			failure := fixture.ErrorDiagnostic(`UPDATE coll_retained_local SET seed='' WHERE id=1`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "coll_retained_local" violates check constraint "coll_retained_seed_nonempty"`, Detail: "Failing row contains (1, ß, , SS).", Schema: namespace, Table: "coll_retained_local", Constraint: "coll_retained_seed_nonempty"})
			failure.ErrorStage = "execute"
			run(failure)
			run(fixture.Prepared(failure))
			run(baseline)
		}
		if err := owner.Close(); err != nil {
			t.Fatal("retained provider physical cleanup", err)
		}
		b.DB.SetMaxIdleConns(0)
		fresh := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Select(`SELECT COUNT(*) AS n FROM pg_catalog.pg_collation WHERE collname='coll_retained'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Select(`SELECT COUNT(*) AS n FROM pg_catalog.pg_class WHERE relname='coll_retained_local'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
