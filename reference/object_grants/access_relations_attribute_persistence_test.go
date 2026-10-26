package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func accessRelationsAttributeSetup(run func(fixture.Step), hierarchy bool) {
	for _, q := range []string{"CREATE TABLE ar_gap(a INT,gone INT,b INT,tail INT)", "ALTER TABLE ar_gap OWNER TO ar_owner", "INSERT INTO ar_gap VALUES(1,2,3,4)", "GRANT SELECT(b) ON ar_gap TO ar_reader", "ALTER TABLE ar_gap DROP COLUMN gone", "ALTER TABLE ar_gap DROP COLUMN tail"} {
		n := int64(0)
		if q == "INSERT INTO ar_gap VALUES(1,2,3,4)" {
			n = 1
		}
		run(fixture.Exec(q, n))
	}
	if hierarchy {
		for _, q := range []string{
			"CREATE TABLE ar_parent(p INT DEFAULT 11,retired INT)",
			"CREATE TABLE ar_child(local INT,gone INT) INHERITS(ar_parent)",
			"ALTER TABLE ONLY ar_child DROP COLUMN gone",
			"ALTER TABLE ar_parent DROP COLUMN retired",
			"ALTER TABLE ar_child ADD COLUMN z INT DEFAULT 23",
			"ALTER TABLE ar_child ADD COLUMN tail_column INT",
			"ALTER TABLE ONLY ar_child DROP COLUMN tail_column",
			"CREATE INDEX ar_child_expression ON ar_child((local+z))",
			"GRANT SELECT(z) ON ar_child TO ar_reader",
			"INSERT INTO ar_child(local) VALUES(7)",
		} {
			n := int64(0)
			if q == "INSERT INTO ar_child(local) VALUES(7)" {
				n = 1
			}
			run(fixture.Exec(q, n))
		}
	}
}
func accessRelationsAttributeCheck(run func(fixture.Step), hierarchy bool) {
	run(fixture.Select("SELECT attnum,attname,attisdropped FROM pg_attribute WHERE attrelid='ar_gap'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attisdropped"}, []string{"INT2", "NAME", "BOOL"}, [][]any{{int64(1), "a", false}, {int64(2), "........pg.dropped.2........", true}, {int64(3), "b", false}, {int64(4), "........pg.dropped.4........", true}}))
	run(fixture.Select("SELECT has_column_privilege('ar_reader','ar_gap',1::smallint,'SELECT') AS a,has_column_privilege('ar_reader','ar_gap',2::smallint,'SELECT') AS gone,has_column_privilege('ar_reader','ar_gap',3::smallint,'SELECT') AS b,has_column_privilege('ar_reader','ar_gap',4::smallint,'SELECT') AS tail,has_any_column_privilege('ar_reader','ar_gap','SELECT') AS any,has_table_privilege('ar_reader','ar_gap','SELECT') AS tab", []string{"a", "gone", "b", "tail", "any", "tab"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, nil, true, nil, true, false}}))
	run(fixture.Select("SELECT a,b FROM ar_gap", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}}))
	if hierarchy {
		run(fixture.Select("SELECT attnum,attname,attisdropped,attislocal,attinhcount FROM pg_attribute WHERE attrelid='ar_child'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attisdropped", "attislocal", "attinhcount"}, []string{"INT2", "NAME", "BOOL", "BOOL", "INT2"}, [][]any{{int64(1), "p", false, false, int64(1)}, {int64(2), "........pg.dropped.2........", true, false, int64(1)}, {int64(3), "local", false, true, int64(0)}, {int64(4), "........pg.dropped.4........", true, true, int64(0)}, {int64(5), "z", false, true, int64(0)}, {int64(6), "........pg.dropped.6........", true, true, int64(0)}}))
		run(fixture.Select("SELECT p,local,z FROM ar_child", []string{"p", "local", "z"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(7), int64(23)}}))
		run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_child',5::smallint,'SELECT') AS v", true))
		run(fixture.Select("SELECT count(*) AS n FROM pg_inherits WHERE inhrelid='ar_child'::regclass AND inhparent='ar_parent'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT count(*) AS n FROM pg_index WHERE indrelid='ar_child'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
	}
}
func TestReferenceAccessRelationsAttributePersistence(t *testing.T) {
	for _, hierarchy := range []bool{false, true} {
		name := "ordinary"
		if hierarchy {
			name = "inheritance"
		}
		t.Run(name, func(t *testing.T) {
			var emitted string
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				accessRelationsAttributeSetup(run, hierarchy)
				accessRelationsAttributeCheck(run, hierarchy)
				if b.PG {
					if emitted != "" {
						run(fixture.Exec("DROP TABLE ar_gap,ar_t", 0))
						run(fixture.Exec("DROP SEQUENCE ar_s", 0))
						if hierarchy {
							run(fixture.Exec("DROP TABLE ar_child,ar_parent", 0))
						}
						if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
							t.Fatal(err)
						}
						accessRelationsAttributeCheck(run, hierarchy)
					}
					run(fixture.Exec("ALTER TABLE ar_gap ADD COLUMN c INT", 0))
					run(fixture.Select("SELECT attnum FROM pg_attribute WHERE attrelid='ar_gap'::regclass AND attname='c'", []string{"attnum"}, []string{"INT2"}, [][]any{{int64(5)}}))
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
				rr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
				accessRelationsAttributeCheck(rr, hierarchy)
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
					t.Fatal(err)
				}
				if redump.String() != saved.String() {
					t.Fatal("attribute history redump changed")
				}
				rr(fixture.Exec("ALTER TABLE ar_gap ADD COLUMN c INT", 0))
				rr(fixture.Select("SELECT attnum FROM pg_attribute WHERE attrelid='ar_gap'::regclass AND attname='c'", []string{"attnum"}, []string{"INT2"}, [][]any{{int64(5)}}))
				if hierarchy {
					rr(fixture.Exec("INSERT INTO ar_child(local) VALUES(8)", 1))
					rr(fixture.Select("SELECT p,local,z FROM ar_child ORDER BY local", []string{"p", "local", "z"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(7), int64(23)}, {int64(11), int64(8), int64(23)}}))
				}
			})
		})
	}
}
func TestReferenceAccessRelationsTemporaryPersistence(t *testing.T) {
	accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"CREATE TEMP TABLE ar_temp(a INT,b INT)", "INSERT INTO ar_temp VALUES(5,6)", "GRANT SELECT(a) ON ar_temp TO ar_reader", "GRANT UPDATE ON ar_temp TO ar_reader"} {
			n := int64(0)
			if q == "INSERT INTO ar_temp VALUES(5,6)" {
				n = 1
			}
			run(fixture.Exec(q, n))
		}
		run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_temp','a','SELECT') AS v", true))
		run(objectGrantAccessValue("SELECT has_table_privilege('ar_reader','ar_temp','UPDATE') AS v", true))
		if b.PG {
			return
		}
		var saved bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
			t.Fatal(err)
		}
		fresh, recorded, recorder := accessRelationsFresh(t, p)
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='ar_temp'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT a,b FROM ar_temp", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(6)}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
			t.Fatal(err)
		}
		if redump.String() != saved.String() {
			t.Fatal("temporary ACL affected canonical dump")
		}
	})
}
