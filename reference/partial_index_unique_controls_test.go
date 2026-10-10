package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferencePartialIndexUniqueControls(t *testing.T) {
	var stream string
	// This scenario deliberately retains its native producer: PG replays the
	// actual emitted stream. It cannot use the PG-only discovery marker.
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE u(extra INT,id INT,k INT,active BOOL)", 0))
		run(referenceExec("INSERT INTO u VALUES(99,1,7,FALSE),(99,2,7,NULL),(99,3,7,TRUE)", 3))
		run(referenceExec("CREATE UNIQUE INDEX pu ON u(k) INCLUDE(id) NULLS NOT DISTINCT WHERE active", 0))
		run(referenceExec("ALTER TABLE u DROP extra", 0))
		run(referenceExec("ALTER TABLE u RENAME active TO enabled", 0))
		run(referenceExec("ALTER TABLE u RENAME k TO key", 0))
		run(referenceExec("ALTER TABLE u RENAME TO renamed", 0))
		run(referenceExec("ALTER INDEX pu RENAME TO renamed_pu", 0))
		for _, tc := range []struct{ call, want string }{
			{"pg_get_indexdef('renamed_pu'::regclass)", "CREATE UNIQUE INDEX renamed_pu ON public.renamed USING btree (key) INCLUDE (id) NULLS NOT DISTINCT WHERE enabled"},
			{"pg_get_indexdef('renamed_pu'::regclass,1,false)", "key"},
			{"pg_get_indexdef('renamed_pu'::regclass,2,false)", "id"},
			{"pg_get_indexdef('renamed_pu'::regclass,3,false)", ""},
			{"pg_get_indexdef('renamed_pu'::regclass,0,true)", "CREATE UNIQUE INDEX renamed_pu ON renamed USING btree (key) INCLUDE (id) NULLS NOT DISTINCT WHERE enabled"},
		} {
			s := deparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
			run(s)
			run(referencePrepared(s))
		}
		namespace := uniqueReferencePublicNamespace(t, p.ctx, b.db, b.pg)
		run(referenceSelect("SELECT c.conname FROM pg_constraint c JOIN pg_class t ON c.conrelid=t.oid WHERE t.relname='renamed' AND t.relnamespace=$1", []string{"conname"}, []string{"NAME"}, nil, namespace))
		dup := uniqueReferenceDuplicate("renamed", "renamed_pu", "Key (key)=(7) already exists.")
		run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(4,7,TRUE)", dup))
		run(referenceExec("REINDEX INDEX renamed_pu", 0))
		run(referencePrepared(referenceErrorDiagnostic("INSERT INTO renamed VALUES(4,7,TRUE)", dup)))
		if !b.pg {
			stream = partialIndexDump(t, p)
			if strings.Count(stream, "CREATE UNIQUE INDEX") != 1 || !strings.Contains(stream, "NULLS NOT DISTINCT") || !strings.Contains(stream, "WHERE") {
				t.Fatal("incomplete unique predicate Dump", stream)
			}
			foreign := newReferenceFixture(t, false, true)
			conn, err := foreign.memory.Conn(foreign.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			frun := func(s referenceStep) { referenceRunStep(t, foreign.ctx, conn, foreign.memoryRecord, s) }
			frun(referenceExec("CREATE TEMP SEQUENCE renamed START 777 CACHE 10", 0))
			frun(referenceSelect("SELECT nextval('pg_temp.renamed') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(777)}}))
			if err := stuntgres.Restore(foreign.ctx, foreign.native, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			frun(referenceSelect("SELECT nextval('pg_temp.renamed') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(778)}}))
			frun(referenceErrorDiagnostic("INSERT INTO public.renamed VALUES(4,7,TRUE)", dup))
			frun(referenceExec("INSERT INTO public.renamed VALUES(4,7,FALSE)", 1))
			frun(partialIndexIDs("SELECT id FROM public.renamed ORDER BY id", 1, 2, 3, 4))
			if partialIndexDump(t, p) != stream {
				t.Fatal("foreign Restore mutated source")
			}
			query := partialIndexIDs("SELECT id FROM renamed ORDER BY id", 1, 2, 3)
			held, err := b.db.QueryContext(p.ctx, query.query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("CREATE TABLE sentinel(id INT)", 0))
			run(referenceExec("INSERT INTO sentinel VALUES(42)", 1))
			before := partialIndexDump(t, p)
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(stream+"BROKEN")); err == nil {
				t.Fatal("badstream Restore succeeded")
			}
			if partialIndexDump(t, p) != before {
				t.Fatal("badstream changed sentinel")
			}
			run(partialIndexIDs("SELECT id FROM sentinel", 42))
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			deleteReferenceConsume(t, false, held, nil, query)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != stream {
				t.Fatal("Restore canonical Dump", err, redump.String())
			}
		} else {
			if stream == "" {
				t.Fatal("missing native emitted SQL")
			}
			run(referenceExec("DROP SCHEMA public CASCADE", 0))
			run(referenceExec("CREATE SCHEMA public", 0))
			if _, err := b.db.ExecContext(p.ctx, stream); err != nil {
				t.Fatal("actual PG replay", err)
			}
		}
		run(partialIndexIDs("SELECT id FROM renamed ORDER BY id", 1, 2, 3))
		run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(4,7,TRUE)", dup))
		run(referenceExec("INSERT INTO renamed VALUES(4,NULL,FALSE),(5,NULL,NULL),(6,NULL,TRUE)", 3))
		run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(7,NULL,TRUE)", uniqueReferenceDuplicate("renamed", "renamed_pu", "Key (key)=(null) already exists.")))
		run(referenceExec("DROP INDEX renamed_pu", 0))
		run(referenceExec("UPDATE renamed SET enabled=TRUE WHERE id=1", 1))
		run(partialIndexIDs("SELECT id FROM renamed WHERE enabled ORDER BY id", 1, 3, 6))
	})
}

func TestReferencePartialIndexUniqueCurrentNames(t *testing.T) {
	uniqueReferenceSchemaCleanup(t, "partial_unique_names")
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
		run(referenceExec("INSERT INTO u VALUES(1,7,TRUE),(2,7,FALSE)", 2))
		run(referenceExec("CREATE UNIQUE INDEX pu ON u(k) INCLUDE(id) WHERE active", 0))
		run(referenceExec("ALTER TABLE u RENAME k TO key", 0))
		run(referenceExec("ALTER TABLE u RENAME active TO enabled", 0))
		run(referenceExec("ALTER TABLE u RENAME TO renamed", 0))
		run(referenceExec("ALTER INDEX pu RENAME TO renamed_pu", 0))
		run(referenceExec("CREATE SCHEMA partial_unique_names", 0))
		run(referenceExec("ALTER TABLE renamed SET SCHEMA partial_unique_names", 0))
		want := uniqueReferenceDuplicate("renamed", "renamed_pu", "Key (key)=(7) already exists.")
		want.Schema = "partial_unique_names"
		run(referencePrepared(referenceErrorDiagnostic("INSERT INTO partial_unique_names.renamed VALUES(3,7,TRUE)", want)))
		run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('partial_unique_names.renamed_pu'::regclass) AS definition", "CREATE UNIQUE INDEX renamed_pu ON partial_unique_names.renamed USING btree (key) INCLUDE (id) WHERE enabled"))
		run(referenceExec("ALTER TABLE partial_unique_names.renamed DROP enabled", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='renamed_pu'", []string{"relname"}, []string{"NAME"}, nil))
		run(referenceExec("INSERT INTO partial_unique_names.renamed VALUES(3,7)", 1))
		run(partialIndexIDs("SELECT id FROM partial_unique_names.renamed ORDER BY id", 1, 2, 3))
	})
}

func TestReferencePartialIndexUniquePrivacy(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		t.Run(mode, func(t *testing.T) {
			roleReferenceCleanup(t, []string{"partial_unique_actor"})
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				a, _ := referenceLockConnections(t, p.ctx, b.db)
				run = func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
				run(referenceExec("CREATE ROLE partial_unique_actor", 0))
				if b.pg { run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO partial_unique_actor", 0)) }
				run(referenceExec("SET SESSION AUTHORIZATION partial_unique_actor", 0))
				run(referenceExec("CREATE TABLE u(k INT,j TEXT,payload TEXT,active BOOL)", 0))
				run(referenceExec("INSERT INTO u VALUES(7,'secret','hidden',TRUE),(7,'secret','other',FALSE)", 2))
				run(referenceExec("CREATE UNIQUE INDEX pu ON u(k,j) INCLUDE(payload) WHERE active", 0))
				run(referenceExec("REVOKE SELECT ON u FROM partial_unique_actor", 0))
				if mode == "partial" || mode == "all" {
					run(referenceExec("GRANT SELECT(k) ON u TO partial_unique_actor", 0))
				}
				if mode == "all" {
					run(referenceExec("GRANT SELECT(j) ON u TO partial_unique_actor", 0))
				}
				if mode == "table" {
					run(referenceExec("GRANT SELECT ON u TO partial_unique_actor", 0))
				}
				detail := ""
				buildDetail := "Duplicate keys exist."
				if mode == "all" || mode == "table" {
					detail = "Key (k, j)=(7, secret) already exists."
					buildDetail = "Key (k, j)=(7, secret) is duplicated."
				}
				s := referenceErrorDiagnostic("INSERT INTO u VALUES(7,'secret','new',TRUE)", uniqueReferenceDuplicate("u", "pu", detail))
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(referenceExec("DROP INDEX pu", 0))
				run(referenceExec("INSERT INTO u VALUES(7,'secret','new',TRUE)", 1))
				s = referenceErrorDiagnostic("CREATE UNIQUE INDEX pu ON u(k,j) INCLUDE(payload) WHERE active", uniqueIndexReferenceBuildDiagnostic("u", "pu", buildDetail))
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceSelect("SELECT k,j,payload,active FROM u ORDER BY payload", []string{"k", "j", "payload", "active"}, []string{"INT4", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(7), "secret", "hidden", true}, {int64(7), "secret", "new", true}, {int64(7), "secret", "other", false}}))
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
}

func TestReferencePartialIndexUniqueDependencyClosure(t *testing.T) {
	for _, column := range []string{"k", "id", "active"} {
		t.Run(column, func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
				run(referenceExec("INSERT INTO u VALUES(1,7,TRUE)", 1))
				run(referenceExec("CREATE UNIQUE INDEX pu ON u(k) INCLUDE(id) WHERE active", 0))
				run(referenceExec("ALTER TABLE u DROP "+column, 0))
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
				switch column {
				case "k":
					run(referenceExec("INSERT INTO u VALUES(1,TRUE)", 1))
					run(referenceSelect("SELECT id,active FROM u", []string{"id", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(1), true}}))
				case "id":
					run(referenceExec("INSERT INTO u VALUES(7,TRUE)", 1))
					run(referenceSelect("SELECT k,active FROM u", []string{"k", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(7), true}, {int64(7), true}}))
				case "active":
					run(referenceExec("INSERT INTO u VALUES(1,7)", 1))
					run(referenceSelect("SELECT id,k FROM u", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(1), int64(7)}}))
				}
			})
		})
	}
}
