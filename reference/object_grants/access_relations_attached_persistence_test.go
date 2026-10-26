package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessRelationsAttachedAttributePersistence(t *testing.T) {
	for _, kind := range []string{"typed", "partition"} {
		t.Run(kind, func(t *testing.T) {
			var emitted string
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				if kind == "typed" {
					run(fixture.Exec("CREATE TYPE ar_attach_type AS(a INT,b INT)", 0))
				} else {
					run(fixture.Exec("CREATE TABLE ar_attach_parent(a INT,b INT) PARTITION BY RANGE(a)", 0))
				}
				for _, query := range []string{"CREATE TABLE ar_attach(a INT,gone INT,b INT,tail INT)", "ALTER TABLE ar_attach DROP COLUMN gone", "ALTER TABLE ar_attach DROP COLUMN tail"} {
					run(fixture.Exec(query, 0))
				}
				if kind == "typed" {
					run(fixture.Exec("ALTER TABLE ar_attach OF ar_attach_type", 0))
				} else {
					run(fixture.Exec("ALTER TABLE ar_attach_parent ATTACH PARTITION ar_attach FOR VALUES FROM(0) TO(10)", 0))
				}
				if kind == "typed" {
					run(fixture.Exec("INSERT INTO ar_attach VALUES(1,3)", 1))
				} else {
					run(fixture.Exec("INSERT INTO ar_attach_parent VALUES(1,3)", 1))
				}
				run(fixture.Exec("GRANT SELECT(b) ON ar_attach TO ar_reader", 0))
				check := func(run func(fixture.Step), rows [][]any) {
					attrs := [][]any{{int64(1), "a", false}, {int64(2), "........pg.dropped.2........", true}, {int64(3), "b", false}, {int64(4), "........pg.dropped.4........", true}}
					run(fixture.Select("SELECT attnum,attname,attisdropped FROM pg_attribute WHERE attrelid='ar_attach'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attisdropped"}, []string{"INT2", "NAME", "BOOL"}, attrs))
					if kind == "typed" {
						run(fixture.TypedTableAttachment("ar_attach", "ar_attach_type", true))
					} else {
						run(fixture.Select("SELECT inhparent='ar_attach_parent'::regclass::oid AS parent_identity,inhseqno FROM pg_inherits WHERE inhrelid='ar_attach'::regclass::oid ORDER BY inhseqno", []string{"parent_identity", "inhseqno"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}))
						if b.PG {
							run(fixture.Select("SELECT pg_get_expr(relpartbound,oid) AS bound FROM pg_class WHERE oid='ar_attach'::regclass::oid", []string{"bound"}, []string{"TEXT"}, [][]any{{"FOR VALUES FROM (0) TO (10)"}}))
						} else {
							run(fixture.Select("SELECT relpartbound FROM pg_class WHERE oid='ar_attach'::regclass::oid", []string{"relpartbound"}, []string{"PG_NODE_TREE"}, [][]any{{`{PartitionBound strategy range default false lower ({type 23 typmod -1 collation 0 value "0"}) upper ({type 23 typmod -1 collation 0 value "10"})}`}}))
						}
					}
					run(fixture.Select("SELECT a,b FROM ar_attach ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, rows))
					privileges := [][]any{{false, nil, true, nil}}
					run(fixture.Select("SELECT has_column_privilege('ar_reader','ar_attach',1::smallint,'SELECT') AS a,has_column_privilege('ar_reader','ar_attach',2::smallint,'SELECT') AS gone,has_column_privilege('ar_reader','ar_attach',3::smallint,'SELECT') AS b,has_column_privilege('ar_reader','ar_attach',4::smallint,'SELECT') AS tail", []string{"a", "gone", "b", "tail"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, privileges))
				}
				sourceRows := [][]any{{int64(1), int64(3)}}
				future := func(run func(fixture.Step)) {
					target := "ar_attach"
					if kind == "partition" {
						target = "ar_attach_parent"
					}
					run(fixture.Exec("INSERT INTO "+target+" VALUES(2,5)", 1))
					check(run, [][]any{{int64(1), int64(3)}, {int64(2), int64(5)}})
					run(fixture.Select("SELECT a,b,tableoid='ar_attach'::regclass::oid AS physical FROM "+target+" ORDER BY a", []string{"a", "b", "physical"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(3), true}, {int64(2), int64(5), true}}))
				}
				check(run, sourceRows)
				if b.PG {
					if emitted != "" {
						run(fixture.Exec("DROP TABLE ar_attach,ar_t", 0))
						run(fixture.Exec("DROP SEQUENCE ar_s", 0))
						if kind == "typed" {
							run(fixture.Exec("DROP TYPE ar_attach_type", 0))
						} else {
							run(fixture.Exec("DROP TABLE ar_attach_parent", 0))
						}
						if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
							t.Fatal(err)
						}
						check(run, sourceRows)
						future(run)
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
				check(rr, sourceRows)
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
					t.Fatal(err)
				}
				if redump.String() != saved.String() {
					t.Fatal("attached attribute layout was not canonical")
				}
				future(rr)
			})
		})
	}
}
