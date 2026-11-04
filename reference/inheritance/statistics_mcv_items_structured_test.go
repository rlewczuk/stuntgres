package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsMCVItemsStructured(t *testing.T) {
	for _, c := range []struct{ name, literal, text string }{
		{"json-null", `'null'`, "null"},
		{"json-bool", `'true'`, "true"},
		{"json-number", `'12.50'`, "12.50"},
		{"json-string", `'"text"'`, `"text"`},
		{"json-nested", `'{"x": [true, null, 12.50], "long": {"a": false}}'`, `{"x": [true, null, 12.50], "long": {"a": false}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				mcvItemsTypeSetup(s, "jsonb", c.literal)
				mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{c.text, "1"}, 1, 1})
				s.run(fixture.Exec("INSERT INTO mcv_item_type VALUES(NULL,1)", 1))
				s.run(fixture.Exec("ANALYZE mcv_item_type", 0))
				mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{c.text, "1"}, 2.0 / 3, 2.0 / 3}, mcvItemsExpected{[]any{nil, "1"}, 1.0 / 3, 1.0 / 3})
			})
		})
	}
	for _, typ := range []string{"mcv_item_enum", "mcv_item_domain", "mcv_item_chain"} {
		t.Run(typ, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				for _, q := range []string{"CREATE TYPE mcv_item_enum AS ENUM('z','a')", "CREATE DOMAIN mcv_item_domain AS mcv_item_enum", "CREATE DOMAIN mcv_item_chain AS mcv_item_domain"} {
					s.run(fixture.Exec(q, 0))
				}
				mcvItemsTypeSetup(s, typ, "'z'")
				s.run(fixture.Exec("INSERT INTO mcv_item_type VALUES('a',2)", 1))
				s.run(fixture.Exec("ANALYZE mcv_item_type", 0))
				mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{"z", "1"}, 2.0 / 3, 4.0 / 9}, mcvItemsExpected{[]any{"a", "2"}, 1.0 / 3, 1.0 / 9})
			})
		})
	}
	for _, c := range []struct{ name, typ, literal, text string }{
		{"empty-array", "int[]", "'{}'", "{}"},
		{"lower-bound", "int[]", "'[0:2]={1,NULL,3}'", "[0:2]={1,NULL,3}"},
		{"rank-two", "int[]", "'{{1,2},{3,4}}'", "{{1,2},{3,4}}"},
		{"domain-array", "mcv_item_array_domain", "'[0:2]={1,NULL,3}'", "[0:2]={1,NULL,3}"},
		{"array-domain-elements", "mcv_item_int_domain[]", "'{1,NULL,3}'", "{1,NULL,3}"},
	} {
		t.Run(c.name, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				for _, q := range []string{"CREATE DOMAIN mcv_item_array_domain AS int[]", "CREATE DOMAIN mcv_item_int_domain AS int"} {
					s.run(fixture.Exec(q, 0))
				}
				if !s.pg {
					classType := "int[]"
					if c.typ == "mcv_item_int_domain[]" {
						classType = c.typ
					}
					s.run(fixture.Exec("CREATE OPERATOR CLASS mcv_item_array_ops DEFAULT FOR TYPE "+classType+" USING btree AS STORAGE "+classType, 0))
				}
				mcvItemsTypeSetup(s, c.typ, c.literal)
				// An inner NULL element is array text, not an outer MCV NULL flag.
				mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{c.text, "1"}, 1, 1})
			})
		})
	}
	for _, c := range []struct{ name, extension, literal, text string }{
		{"cube-point", "cube", "'(1,2)'", "(1, 2)"},
		{"cube-box", "cube", "'(1,2),(3,4)'", "(1, 2),(3, 4)"},
		{"hstore-nulls", "hstore", `'"a"=>NULL,"b"=>"","c"=>"NULL"'`, `"a"=>NULL, "b"=>"", "c"=>"NULL"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				s.run(fixture.Exec("CREATE EXTENSION "+c.extension, 0))
				mcvItemsTypeSetup(s, c.extension, c.literal)
				mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{c.text, "1"}, 1, 1})
			})
		})
	}
}

func TestReferenceStatisticsMCVItemsReachable(t *testing.T) {
	for _, typ := range []string{"xid", "cid", "oidvector", "int2vector"} {
		t.Run(typ, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				if !s.pg || typ != "oidvector" {
					s.run(fixture.Exec("CREATE OPERATOR CLASS mcv_item_custom_ops DEFAULT FOR TYPE "+typ+" USING btree AS STORAGE "+typ, 0))
				}
				s.run(fixture.Exec("CREATE TABLE mcv_item_custom(a "+typ+",b INT)", 0))
				literal, text := "'4294967294'", "4294967294"
				if typ == "oidvector" || typ == "int2vector" {
					literal, text = "'1 2'", "1 2"
				}
				s.run(fixture.Exec("INSERT INTO mcv_item_custom VALUES("+literal+",1),("+literal+",1)", 2))
				if s.pg && typ != "oidvector" {
					s.run(statisticsError("CREATE STATISTICS mcv_item_custom_s(mcv) ON a,b FROM mcv_item_custom", "0A000", `column "a" cannot be used in statistics because its type `+typ+` has no default btree operator class`))
					s.run(fixture.Select("SELECT count(*) FROM pg_statistic_ext WHERE stxname='mcv_item_custom_s'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					s.run(fixture.Select("SELECT count(*) FROM mcv_item_custom", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
					return // Asserted released provider boundary, not skipped coverage.
				}
				s.run(fixture.Exec("CREATE STATISTICS mcv_item_custom_s(mcv) ON a,b FROM mcv_item_custom", 0))
				s.run(fixture.Exec("ANALYZE mcv_item_custom", 0))
				mcvItemsCheck(t, s, "mcv_item_custom_s", mcvItemsExpected{[]any{text, "1"}, 1, 1})
			})
		})
	}
	t.Run("retained-tree", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			for _, q := range []string{"CREATE TABLE mcv_item_tree_source(a INT DEFAULT 7)", "CREATE TABLE mcv_item_tree(a pg_node_tree,b INT)", "CREATE STATISTICS mcv_item_tree_s(mcv) ON a,b FROM mcv_item_tree"} {
				s.run(fixture.Exec(q, 0))
			}
			s.run(fixture.Exec("INSERT INTO mcv_item_tree SELECT adbin,1 FROM pg_attrdef WHERE adrelid='mcv_item_tree_source'::regclass", 1))
			var original string
			if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT a::text FROM mcv_item_tree").Scan(&original); err != nil || original == "" {
				t.Fatal(original, err)
			}
			s.run(fixture.Exec("ANALYZE mcv_item_tree", 0))
			s.run(fixture.Exec("DELETE FROM mcv_item_tree", 1))
			mcvItemsCheck(t, s, "mcv_item_tree_s", mcvItemsExpected{[]any{original, "1"}, 1, 1})
			stmt, err := s.conn.PrepareContext(s.pair.Ctx, mcvItemsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for range 2 {
				rows, err := stmt.QueryContext(s.pair.Ctx, "mcv_item_tree_s")
				if err != nil {
					t.Fatal(err)
				}
				mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), mcvItemsExpected{[]any{original, "1"}, 1, 1})
			}
			if !s.pg {
				mcvItemsRoundTrip(t, s, "mcv_item_tree_s", []mcvItemsExpected{{[]any{original, "1"}, 1, 1}})
			}
		})
	})
}
