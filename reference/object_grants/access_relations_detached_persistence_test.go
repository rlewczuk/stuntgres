package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessRelationsDetachedAttributePersistence(t *testing.T) {
	for _, keep := range []bool{false, true} {
		name := "parent_dropped"
		if keep {
			name = "parent_retained"
		}
		t.Run(name, func(t *testing.T) {
			var emitted string
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				for _, query := range []string{"CREATE TABLE ar_detach_p(a INT)", "CREATE TABLE ar_detach_c() INHERITS(ar_detach_p)", "ALTER TABLE ar_detach_p DROP COLUMN a", "ALTER TABLE ar_detach_c NO INHERIT ar_detach_p"} {
					run(fixture.Exec(query, 0))
				}
				if !keep {
					run(fixture.Exec("DROP TABLE ar_detach_p", 0))
				}
				run(fixture.Exec("INSERT INTO ar_detach_c DEFAULT VALUES", 1))
				check := func(run func(fixture.Step)) {
					run(fixture.Select("SELECT attnum,attname,attisdropped,attislocal,attinhcount FROM pg_attribute WHERE attrelid='ar_detach_c'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attisdropped", "attislocal", "attinhcount"}, []string{"INT2", "NAME", "BOOL", "BOOL", "INT2"}, [][]any{{int64(1), "........pg.dropped.1........", true, false, int64(1)}}))
					run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_detach_c',1::smallint,'SELECT') AS v", nil))
					run(fixture.Select("SELECT count(*) AS n FROM ar_detach_c", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Select("SELECT count(*) AS n FROM pg_inherits WHERE inhrelid='ar_detach_c'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname LIKE '__stuntgres_attribute_history_%'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				check(run)
				if b.PG {
					if emitted != "" {
						run(fixture.Exec("DROP TABLE ar_detach_c,ar_t", 0))
						if keep {
							run(fixture.Exec("DROP TABLE ar_detach_p", 0))
						}
						run(fixture.Exec("DROP SEQUENCE ar_s", 0))
						if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
							t.Fatal(err)
						}
						check(run)
					}
					return
				}
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				emitted = saved.String()
				fresh, recorded, recorder := accessRelationsFresh(t, p)
				if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				rr := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, step) }
				check(rr)
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
					t.Fatal(err)
				}
				if redump.String() != saved.String() {
					t.Fatal("erased-edge provenance replay was not canonical")
				}
				if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(saved.String()+"DROP TABLE public.__stuntgres_attribute_history_dead;")); err == nil {
					t.Fatal("unpaired helper teardown admitted")
				}
				check(rr)
				rr(fixture.Exec("ALTER TABLE ar_detach_c ADD COLUMN next_column INT", 0))
				rr(fixture.Select("SELECT attnum FROM pg_attribute WHERE attrelid='ar_detach_c'::regclass AND attname='next_column'", []string{"attnum"}, []string{"INT2"}, [][]any{{int64(2)}}))
			})
		})
	}
}
