package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessRelationsMixedAttributePersistence(t *testing.T) {
	for _, kind := range []string{"late_inherited_column", "interleaved_retirements"} {
		t.Run(kind, func(t *testing.T) {
			var emitted string
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				setup := []string{"CREATE TABLE ar_mix_p()", "CREATE TABLE ar_mix_c(a INT DEFAULT 7) INHERITS(ar_mix_p)", "ALTER TABLE ar_mix_p ADD COLUMN b INT DEFAULT 11", "ALTER TABLE ar_mix_c ADD COLUMN gone INT", "ALTER TABLE ONLY ar_mix_c DROP COLUMN gone", "CREATE INDEX ar_mix_expression ON ar_mix_c((a+b))", "GRANT SELECT(b) ON ar_mix_c TO ar_reader", "INSERT INTO ar_mix_c(a) VALUES(3)"}
				if kind == "interleaved_retirements" {
					setup = []string{"CREATE TABLE ar_mix_p(a INT,b INT)", "CREATE TABLE ar_mix_c() INHERITS(ar_mix_p)", "ALTER TABLE ar_mix_p DROP COLUMN a", "CREATE TABLE ar_mix_d() INHERITS(ar_mix_p)", "ALTER TABLE ar_mix_p DROP COLUMN b", "INSERT INTO ar_mix_d DEFAULT VALUES"}
				}
				for _, query := range setup {
					affected := int64(0)
					if query == setup[len(setup)-1] {
						affected = 1
					}
					run(fixture.Exec(query, affected))
				}
				check := func(run func(fixture.Step)) {
					columns := []string{"attnum", "attname", "attisdropped", "attislocal", "attinhcount"}
					types := []string{"INT2", "NAME", "BOOL", "BOOL", "INT2"}
					query := func(name string) string {
						return "SELECT attnum,attname,attisdropped,attislocal,attinhcount FROM pg_attribute WHERE attrelid='" + name + "'::regclass AND attnum>0 ORDER BY attnum"
					}
					if kind == "late_inherited_column" {
						run(fixture.Select(query("ar_mix_p"), columns, types, [][]any{{int64(1), "b", false, true, int64(0)}}))
						run(fixture.Select(query("ar_mix_c"), columns, types, [][]any{{int64(1), "a", false, true, int64(0)}, {int64(2), "b", false, false, int64(1)}, {int64(3), "........pg.dropped.3........", true, true, int64(0)}}))
						run(fixture.Select("SELECT a,b FROM ar_mix_c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(11)}}))
						run(fixture.Select("SELECT has_column_privilege('ar_reader','ar_mix_c',1::smallint,'SELECT') AS a,has_column_privilege('ar_reader','ar_mix_c',2::smallint,'SELECT') AS b,has_column_privilege('ar_reader','ar_mix_c',3::smallint,'SELECT') AS gone,has_any_column_privilege('ar_reader','ar_mix_c','SELECT') AS any,has_table_privilege('ar_reader','ar_mix_c','SELECT') AS tab", []string{"a", "b", "gone", "any", "tab"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, nil, true, false}}))
					} else {
						run(fixture.Select(query("ar_mix_p"), columns, types, [][]any{{int64(1), "........pg.dropped.1........", true, true, int64(0)}, {int64(2), "........pg.dropped.2........", true, true, int64(0)}}))
						run(fixture.Select(query("ar_mix_c"), columns, types, [][]any{{int64(1), "........pg.dropped.1........", true, false, int64(1)}, {int64(2), "........pg.dropped.2........", true, false, int64(1)}}))
						run(fixture.Select(query("ar_mix_d"), columns, types, [][]any{{int64(1), "........pg.dropped.1........", true, false, int64(1)}}))
						run(fixture.Select("SELECT count(*) AS n FROM ar_mix_d", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
				}
				check(run)
				next := func(run func(fixture.Step)) {
					name, number := "ar_mix_c", int64(4)
					if kind == "interleaved_retirements" {
						name, number = "ar_mix_d", 2
					}
					run(fixture.Exec("ALTER TABLE "+name+" ADD COLUMN next_column INT", 0))
					run(fixture.Select("SELECT attnum FROM pg_attribute WHERE attrelid='"+name+"'::regclass AND attname='next_column'", []string{"attnum"}, []string{"INT2"}, [][]any{{number}}))
				}
				if b.PG {
					if emitted != "" {
						if kind == "interleaved_retirements" {
							run(fixture.Exec("DROP TABLE ar_mix_d", 0))
						}
						run(fixture.Exec("DROP TABLE ar_mix_c,ar_mix_p,ar_t", 0))
						run(fixture.Exec("DROP SEQUENCE ar_s", 0))
						if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
							t.Fatal(err)
						}
						check(run)
					}
					next(run)
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
					t.Fatal("mixed attribute construction was not canonical")
				}
				next(rr)
			})
		})
	}
}
