package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckIndexAliasSnapshotCollision(t *testing.T) {
	for _, test := range []struct {
		name, alias, literal string
	}{
		{"dictionary", "regdictionary", "'alias_snapshot.unaccent'::regdictionary"},
		{"procedure", "regprocedure", "'alias_snapshot.unaccent(text)'::regprocedure"},
		{"proc", "regproc", "'alias_snapshot.unaccent(text)'::regprocedure::regproc"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "alias_snapshot")
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					ordinal := 0
					run := func(step fixture.Step) {
						ordinal++
						t.Run(fmt.Sprintf("%03d %s", ordinal, step.Query), func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
						})
						if t.Failed() {
							t.FailNow()
						}
					}
					for _, query := range []string{
						"CREATE SCHEMA alias_snapshot",
						"CREATE TABLE public.alias_snapshot_allocator(v integer)",
						"CREATE EXTENSION unaccent WITH SCHEMA alias_snapshot",
						"CREATE SEQUENCE public.alias_snapshot_steps",
						"CREATE DOMAIN public.alias_snapshot_checked AS integer CHECK(nextval('public.alias_snapshot_steps') > 0)",
						"CREATE DOMAIN public.alias_snapshot_value AS " + test.alias,
						"CREATE TABLE public.alias_snapshot_btree(id integer,v public.alias_snapshot_value)",
						"CREATE TABLE public.alias_snapshot_hash(id integer,v public.alias_snapshot_value)",
						"CREATE UNIQUE INDEX alias_snapshot_unique ON public.alias_snapshot_btree(v) WHERE ((id::public.alias_snapshot_checked)::integer > 0)",
						"CREATE INDEX alias_snapshot_hash_index ON public.alias_snapshot_hash USING hash(v)",
					} {
						run(fixture.Exec(query, 0))
					}
					for _, table := range []string{"alias_snapshot_btree", "alias_snapshot_hash"} {
						run(fixture.Exec("INSERT INTO public."+table+" VALUES(1,"+test.literal+"),(2,NULL)", 2))
					}
					// These SQL assertions are shared with the displaced native target.
					// PostgreSQL exercises the same ordinary index/row contract, not a
					// fictitious equivalent of the public native replacement API.
					check := func(t *testing.T, pair *fixture.Pair, b fixture.Backend, c *sql.Conn, representedOID int64) {
						t.Helper()
						for _, prepared := range []bool{false, true} {
							for _, table := range []string{"alias_snapshot_btree", "alias_snapshot_hash"} {
								step := fixture.Select("SELECT id,v::oid="+test.literal+"::oid AS identity FROM public."+table+" ORDER BY id", []string{"id", "identity"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}})
								step.Prepared = prepared
								fixture.RunStep(t, pair.Ctx, c, b.Recorder, step)
								step = fixture.Select("SELECT id FROM public."+table+" WHERE v="+test.literal+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
								step.Prepared = prepared
								fixture.RunStep(t, pair.Ctx, c, b.Recorder, step)
							}
						}
						// This separate table has only a HASH index: its equality
						// and mutation witnesses cannot use the UNIQUE B-tree.
						for _, prepared := range []bool{false, true} {
							for _, step := range []fixture.Step{
								fixture.Exec("UPDATE public.alias_snapshot_hash SET v='0'::"+test.alias+" WHERE id=1", 1),
								fixture.Select("SELECT id FROM public.alias_snapshot_hash WHERE v="+test.literal, []string{"id"}, []string{"INT4"}, nil),
								fixture.Select("SELECT id FROM public.alias_snapshot_hash WHERE v='0'::"+test.alias, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
								fixture.Exec("UPDATE public.alias_snapshot_hash SET v="+test.literal+" WHERE id=1", 1),
								fixture.Exec("DELETE FROM public.alias_snapshot_hash WHERE v="+test.literal, 1),
								fixture.Select("SELECT id FROM public.alias_snapshot_hash WHERE v="+test.literal, []string{"id"}, []string{"INT4"}, nil),
								fixture.Exec("INSERT INTO public.alias_snapshot_hash VALUES(1,"+test.literal+")", 1),
								fixture.Select("SELECT id FROM public.alias_snapshot_hash WHERE v="+test.literal, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
							} {
								step.Prepared = prepared
								fixture.RunStep(t, pair.Ctx, c, b.Recorder, step)
							}
						}
						duplicate := fixture.ImplicitError("INSERT INTO public.alias_snapshot_btree VALUES(3,"+test.literal+")", fixture.Diagnostic{
							Code: "23505", Message: `duplicate key value violates unique constraint "alias_snapshot_unique"`,
							Detail: fmt.Sprintf("Key (v)=(%d) already exists.", representedOID), Schema: "public", Table: "alias_snapshot_btree", Constraint: "alias_snapshot_unique",
						})
						duplicate.ErrorStage = "execute"
						fixture.RunStep(t, pair.Ctx, c, b.Recorder, duplicate)
						fixture.RunStep(t, pair.Ctx, c, b.Recorder, fixture.Select("SELECT count(*)::bigint AS n FROM public.alias_snapshot_btree", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					}
					var sourceOID int64
					if err := conn.QueryRowContext(p.Ctx, "SELECT "+test.literal+"::oid::bigint").Scan(&sourceOID); err != nil {
						t.Fatal(err)
					}
					check(t, p, backend, conn, sourceOID)
					run(fixture.Select("SELECT currval('public.alias_snapshot_steps') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
					if !backend.PG {
						t.Run("native-retained-collision", func(t *testing.T) {
							var original bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
								t.Fatal(err)
							}
							target := fixture.NewFixture(t, false, true)
							c, err := target.Memory.Conn(target.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							b := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
							var collider string
							for i := int64(0); collider == "" && i <= sourceOID; i++ {
								name := fmt.Sprintf("alias_snapshot_displace_%d", i)
								fixture.RunStep(t, target.Ctx, c, b.Recorder, fixture.Exec("CREATE TEMP SEQUENCE "+name, 0))
								var oid int64
								if err := c.QueryRowContext(target.Ctx, "SELECT 'pg_temp."+name+"'::regclass::oid::bigint").Scan(&oid); err != nil {
									t.Fatal(err)
								}
								if oid == sourceOID {
									collider = name
								}
								if oid > sourceOID {
									t.Fatalf("retained allocation passed represented source identity: retained=%d source=%d", oid, sourceOID)
								}
							}
							if collider == "" {
								t.Fatal("no retained identity collision")
							}
							fixture.RunStep(t, target.Ctx, c, b.Recorder, fixture.Select("SELECT setval('pg_temp."+collider+"',41,true) AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(41)}}))
							if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(original.Bytes())); err != nil {
								t.Fatal("displaced alias-index Restore", err)
							}
							var destinationOID int64
							if err := c.QueryRowContext(target.Ctx, "SELECT "+test.literal+"::oid::bigint").Scan(&destinationOID); err != nil {
								t.Fatal(err)
							}
							if destinationOID == sourceOID {
								t.Fatalf("collision did not remint represented identity %d", sourceOID)
							}
							fixture.RunStep(t, target.Ctx, c, b.Recorder, fixture.Select("SELECT 'pg_temp."+collider+"'::regclass::oid::bigint AS id,nextval('pg_temp."+collider+"') AS n", []string{"id", "n"}, []string{"INT8", "INT8"}, [][]any{{sourceOID, int64(42)}}))
							fixture.RunStep(t, target.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('public.alias_snapshot_steps') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
							check(t, target, b, c, destinationOID)
							// Replacement and destination probes must never mutate source
							// cells, dictionary/routine identities or CHECK sequence state.
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT "+test.literal+"::oid::bigint AS id,currval('public.alias_snapshot_steps') AS n", []string{"id", "n"}, []string{"INT8", "INT8"}, [][]any{{sourceOID, int64(3)}}))
							var unchanged bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil {
								t.Fatal(err)
							}
							if !bytes.Equal(original.Bytes(), unchanged.Bytes()) {
								t.Fatal("destination replacement mutated source dump")
							}
						})
					}
				})
			}
		})
	}
}
