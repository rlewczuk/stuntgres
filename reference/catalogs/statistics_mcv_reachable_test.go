package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"math"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsMCVReachableTypes(t *testing.T) {
	for _, typ := range []string{"xid", "cid", "oidvector", "int2vector"} {
		t.Run(typ, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				// OIDVECTOR already has a released PostgreSQL default. Other
				// STORAGE-only exact classes are admitted metadata, not executable
				// PostgreSQL btree providers. Assert that boundary explicitly while
				// retaining native positive codec coverage for its actual grouping.
				if !s.pg || typ != "oidvector" {
					s.run(fixture.Exec("CREATE OPERATOR CLASS mcv_custom_ops DEFAULT FOR TYPE "+typ+" USING btree AS STORAGE "+typ, 0))
				}
				s.run(fixture.Exec("CREATE TABLE mcv_custom(a "+typ+",b INT)", 0))
				first, second := "'4294967294'", "'7'"
				if typ == "oidvector" || typ == "int2vector" {
					first, second = "'1 2'", "'3 4'"
				}
				s.run(fixture.Exec("INSERT INTO mcv_custom VALUES("+first+",1),("+first+",1),("+first+",2),("+second+",2),(NULL,NULL)", 5))
				if s.pg && typ != "oidvector" {
					s.run(statisticsError("CREATE STATISTICS mcv_cs(mcv) ON a,b FROM mcv_custom", "0A000", `column "a" cannot be used in statistics because its type `+typ+` has no default btree operator class`))
					s.run(fixture.Select("SELECT count(*) FROM mcv_custom", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
					return // Explicit rejected-provider phase; no PostgreSQL MCV exists.
				}
				s.run(fixture.Exec("CREATE STATISTICS mcv_cs(mcv) ON a,b FROM mcv_custom", 0))
				s.run(fixture.Exec("ANALYZE mcv_custom", 0))
				before, v := readMCV(t, s, "mcv_cs", false)
				mcvCapabilityImage(t, typ, v)
				// Remove both b=2 rows without refreshing: remeasurement would
				// have two groups at 2/3 and 1/3, not these four retained groups.
				s.run(fixture.Exec("DELETE FROM mcv_custom WHERE b=2", 2))
				stale, retained := readMCV(t, s, "mcv_cs", false)
				mcvSame(t, before, stale)
				mcvCapabilityImage(t, typ, retained)
				s.run(fixture.Select("SELECT b FROM mcv_custom ORDER BY b", []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
				if !s.pg {
					mcvCapabilityRestore(t, s, typ)
				}
			})
		})
	}
	t.Run("money", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			s.run(fixture.Exec("CREATE TABLE mcv_money(a MONEY,b INT)", 0))
			s.run(fixture.Exec("INSERT INTO mcv_money VALUES('12.34',1)", 1))
			s.run(fixture.Exec("CREATE STATISTICS mcv_money_s(mcv) ON a,b FROM mcv_money", 0))
			s.run(fixture.Exec("ANALYZE mcv_money", 0))
			_, v := readMCV(t, s, "mcv_money_s", false)
			if !bytes.Equal(v.items[0].values[0], mcvWord(1234, 8)) {
				t.Fatal(v.items)
			}
		})
	})
	t.Run("float-equality", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			s.run(fixture.Exec("CREATE TABLE mcv_float(a FLOAT8,b INT)", 0))
			s.run(fixture.Exec("INSERT INTO mcv_float VALUES('NaN',1),('NaN',1),('-0',2),('0',2)", 4))
			s.run(fixture.Exec("CREATE STATISTICS mcv_fs(mcv) ON a,b FROM mcv_float", 0))
			s.run(fixture.Exec("ANALYZE mcv_float", 0))
			_, v := readMCV(t, s, "mcv_fs", false)
			if len(v.items) != 2 {
				t.Fatal(v.items)
			}
			nan, zero := false, false
			for _, item := range v.items {
				n := math.Float64frombits(binary.LittleEndian.Uint64(item.values[0]))
				if math.IsNaN(n) {
					nan = true
				} else if n == 0 {
					zero = true
				} else {
					t.Fatal(n)
				}
				if item.frequency != .5 || item.baseline != .25 {
					t.Fatal(item)
				}
			}
			if !nan || !zero {
				t.Fatal("missing equality group")
			}
		})
	})
	t.Run("jsonb-scalars", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			for _, q := range []string{"CREATE TABLE mcv_json(a JSONB,b INT)", "CREATE STATISTICS mcv_json_s(mcv) ON a,b FROM mcv_json"} {
				s.run(fixture.Exec(q, 0))
			}
			s.run(fixture.Exec(`INSERT INTO mcv_json VALUES('null',1),('true',1),('12.50',1),('"text"',1)`, 4))
			s.run(fixture.Exec("ANALYZE mcv_json", 0))
			_, v := readMCV(t, s, "mcv_json_s", false)
			if len(v.items) != 4 {
				t.Fatal(v.items)
			}
			for _, item := range v.items {
				p := item.values[0]
				if binary.LittleEndian.Uint32(p) != 0x50000001 {
					t.Fatal("JSONB scalar-root wrapper", p)
				}
				mcvJSONContainer(t, p)
				if item.frequency != .25 || item.baseline != .25 {
					t.Fatal(item)
				}
			}
		})
	})
	t.Run("retained-tree", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			for _, q := range []string{"CREATE TABLE mcv_tree_source(a INT DEFAULT 7)", "CREATE TABLE mcv_tree(a pg_node_tree,b INT)", "CREATE STATISTICS mcv_tree_s(mcv) ON a,b FROM mcv_tree"} {
				s.run(fixture.Exec(q, 0))
			}
			s.run(fixture.Exec("INSERT INTO mcv_tree SELECT adbin,1 FROM pg_attrdef WHERE adrelid='mcv_tree_source'::regclass", 1))
			s.run(fixture.Exec("ANALYZE mcv_tree", 0))
			_, v := readMCV(t, s, "mcv_tree_s", false)
			var text string
			if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT a::text FROM mcv_tree").Scan(&text); err != nil {
				t.Fatal(err)
			}
			if string(v.items[0].values[0]) != text || text == "" || v.items[0].frequency != 1 || v.items[0].baseline != 1 {
				t.Fatal("backend-local tree physical bytes", v, text)
			}
			// Delete the live carrier before dump: only retained statistics need the
			// narrow tree reconstruction capability, never ordinary row SQL input.
			s.run(fixture.Exec("DELETE FROM mcv_tree", 1))
			if !s.pg {
				var saved bytes.Buffer
				if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				_, restored := readMCV(t, s, "mcv_tree_s", false)
				if string(restored.items[0].values[0]) != text {
					t.Fatal("retained tree changed")
				}
			}
		})
	})
	t.Run("domain-array-remint", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			for _, q := range []string{"CREATE TYPE mcv_enum AS ENUM('z','a')", "CREATE DOMAIN mcv_element AS mcv_enum", "CREATE DOMAIN mcv_array_domain AS int[]"} {
				s.run(fixture.Exec(q, 0))
			}
			if !s.pg {
				s.run(fixture.Exec("CREATE OPERATOR CLASS mcv_int_array_ops DEFAULT FOR TYPE int[] USING btree AS STORAGE int[]", 0))
			}
			// Enum-element arrays remain outside native column construction.
			// Exercise the reachable domain-over-array and domain-over-enum
			// dimensions together instead of granting a new array constructor.
			s.run(fixture.Exec("CREATE TABLE mcv_remint(a mcv_array_domain,b INT,c mcv_element)", 0))
			s.run(fixture.Exec("CREATE STATISTICS mcv_rs(mcv) ON a,b,c FROM mcv_remint", 0))
			s.run(fixture.Exec("INSERT INTO mcv_remint VALUES('[0:2]={7,NULL,9}',1,'z'),('[0:2]={7,NULL,9}',1,'z'),('[0:2]={7,NULL,9}',2,'a')", 3))
			s.run(fixture.Exec("ANALYZE mcv_remint", 0))
			_, v := readMCV(t, s, "mcv_rs", false)
			mcvArrayEnumIdentity(t, s, v)
			s.run(fixture.Exec("DELETE FROM mcv_remint", 3))
			if !s.pg {
				var saved bytes.Buffer
				if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
					t.Fatal(err)
				}
				driver := stuntgres.NewDriver()
				connector, err := driver.OpenConnector(t.Name() + "-remint")
				if err != nil {
					t.Fatal(err)
				}
				dst := sql.OpenDB(connector)
				defer dst.Close()
				if _, err := dst.ExecContext(s.pair.Ctx, "CREATE TYPE collision AS ENUM('x','y'); CREATE DOMAIN collision_domain AS collision"); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(s.pair.Ctx, dst, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				conn, err := dst.Conn(s.pair.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				target := s
				target.conn = conn
				_, restored := readMCV(t, target, "mcv_rs", false)
				mcvArrayEnumIdentity(t, target, restored)
			}
		})
	})
}
func mcvArrayEnumIdentity(t *testing.T, s ndistinctScenario, v mcvImage) {
	t.Helper()
	if len(v.items) != 2 || v.items[0].frequency != 2.0/3 || math.Abs(v.items[0].baseline-4.0/9) > 1e-14 || v.items[1].frequency != 1.0/3 || math.Abs(v.items[1].baseline-1.0/9) > 1e-14 {
		t.Fatal(v.items)
	}
	var element, z, a int64
	var array int64
	if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT atttypid::bigint FROM pg_attribute WHERE attrelid='mcv_remint'::regclass AND attname='a'").Scan(&array); err != nil {
		t.Fatal(err)
	}
	if v.dimensions[0].oid != uint32(array) {
		t.Fatal("destination logical array identity", v.dimensions[0].oid, array)
	}
	if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT oid::bigint FROM pg_type WHERE typname='mcv_element'").Scan(&element); err != nil {
		t.Fatal(err)
	}
	if v.dimensions[2].oid != uint32(element) {
		t.Fatal("destination scalar enum-domain identity", v.dimensions[2].oid, element)
	}
	if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT oid::bigint FROM pg_enum WHERE enumtypid='mcv_enum'::regtype AND enumlabel='z'").Scan(&z); err != nil {
		t.Fatal(err)
	}
	if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT oid::bigint FROM pg_enum WHERE enumtypid='mcv_enum'::regtype AND enumlabel='a'").Scan(&a); err != nil {
		t.Fatal(err)
	}
	for i, item := range v.items {
		p := item.values[0]
		if len(p) != 36 || binary.LittleEndian.Uint32(p[8:]) != 23 || p[20]&7 != 5 || binary.LittleEndian.Uint32(p[28:]) != 7 || binary.LittleEndian.Uint32(p[32:]) != 9 {
			t.Fatal("destination domain-array native shape", p)
		}
		member := z
		if i == 1 {
			member = a
		}
		if len(item.values[2]) != 4 || binary.LittleEndian.Uint32(item.values[2]) != uint32(member) {
			t.Fatal("destination enum-member identity", item.values[2], member)
		}
	}
}
