package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const userIndexDefinition = ` SELECT relid,
    indexrelid,
    schemaname,
    relname,
    indexrelname,
    idx_scan,
    last_idx_scan,
    idx_tup_read,
    idx_tup_fetch
   FROM pg_stat_all_indexes
  WHERE ((schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND (schemaname !~ '^pg_toast'::text));`

func TestReferencePGStatUserIndexesIdentityAndMetadata(t *testing.T) {
	for _, schema := range []string{"PG_TOAST", "xpg_toast", "information_schema_extra"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "public", "uix_identity")
		sysIndexCreate(t, p, b, c, "information_schema", "uix_public")
		fixture.StatisticsExec(t, p, b, c, "CREATE INDEX uix_identity_second ON public.uix_identity(n)", 0)
		a := userIndexGet(t, p, c, "uix_identity_hash")
		second := userIndexGet(t, p, c, "uix_identity_second")
		if a.Parent <= 0 || a.Index <= 0 || a.Parent == a.Index || second.Parent != a.Parent || second.Index == a.Index || a.Schema != "public" || a.Table != "uix_identity" || a.Name != "uix_identity_hash" {
			t.Fatal("exact USER index/parent identities", a, second)
		}
		indexStatWant(t, a, 0, 0, 0)
		indexStatWant(t, second, 0, 0, 0)
		var parent, index int64
		if err := c.QueryRowContext(p.Ctx, "SELECT indrelid,indexrelid FROM pg_index WHERE indexrelid='public.uix_identity_hash'::regclass").Scan(&parent, &index); err != nil || parent != a.Parent || index != a.Index {
			t.Fatal("pg_index relationship", parent, index, err)
		}
		var parentName, indexName string
		if err := c.QueryRowContext(p.Ctx, "SELECT p.oid,p.relname,i.oid,i.relname FROM pg_class p JOIN pg_index x ON x.indrelid=p.oid JOIN pg_class i ON i.oid=x.indexrelid WHERE i.oid='public.uix_identity_hash'::regclass").Scan(&parent, &parentName, &index, &indexName); err != nil || parent != a.Parent || parentName != a.Table || index != a.Index || indexName != a.Name {
			t.Fatal("pg_class parent/index cross-links", parent, parentName, index, indexName, err)
		}
		var id, namespace, rowType, array, element, owner int64
		var kind string
		err := c.QueryRowContext(p.Ctx, "SELECT c.oid,c.relnamespace,c.relkind,c.reltype,t.typarray,a.typelem,t.typrelid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_user_indexes'::regclass").Scan(&id, &namespace, &kind, &rowType, &array, &element, &owner)
		if err != nil || id <= 0 || namespace != 11 || kind != "v" || rowType <= 0 || array <= 0 || element != rowType || owner != id {
			t.Fatal("USER relation/row/array relationship", id, namespace, kind, rowType, array, element, owner, err)
		}
		if !b.PG && (id != 12196 || rowType != 12198 || array != 12197) {
			t.Fatal("native fixed identities", id, rowType, array)
		}
		var regType int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.pg_stat_user_indexes'::regtype::oid").Scan(&regType); err != nil || regType != rowType {
			t.Fatal("regtype identity", regType, err)
		}
		attributes, err := c.QueryContext(p.Ctx, "SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_user_indexes'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for attributes.Next() {
			var number, typ, mod, collation int64
			var name string
			var notnull bool
			if err := attributes.Scan(&number, &name, &typ, &mod, &notnull, &collation); err != nil {
				t.Fatal(err)
			}
			want, coll := int64(20), int64(0)
			if n < 2 {
				want = 26
			} else if n < 5 {
				want, coll = 19, 950
			} else if n == 6 {
				want = 1184
			}
			if n >= 9 || number != int64(n+1) || name != indexStatNames[n] || typ != want || mod != -1 || notnull || collation != coll {
				t.Fatal("USER attribute metadata", n, number, name, typ, mod, notnull, collation)
			}
			n++
		}
		if err := attributes.Err(); err != nil {
			t.Fatal(err)
		}
		attributes.Close()
		if n != 9 {
			t.Fatal("USER attribute count", n)
		}
		for _, qualified := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					q := "SELECT * FROM pg_stat_user_indexes WHERE indexrelname=$1"
					if qualified {
						q = userIndexSelect
					}
					if empty {
						q += " AND FALSE"
					}
					var rows *sql.Rows
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err == nil {
							rows, err = stmt.QueryContext(p.Ctx, "uix_identity_hash")
						}
					} else {
						rows, err = c.QueryContext(p.Ctx, q, "uix_identity_hash")
					}
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					indexStatMetadata(t, columns, b.PG)
					count := 0
					for rows.Next() {
						var raw [9]any
						dest := make([]any, len(raw))
						for i := range raw {
							dest[i] = &raw[i]
						}
						if err := rows.Scan(dest...); err != nil {
							t.Fatal(err)
						}
						for i, v := range raw {
							switch {
							case i == 6:
								if v != nil {
									t.Fatal("absent scan instant must be NULL", v)
								}
							case b.PG && i < 5:
								if _, ok := v.([]byte); !ok {
									t.Fatalf("pq raw %s=%T", indexStatNames[i], v)
								}
							case i >= 2 && i < 5:
								if _, ok := v.(string); !ok {
									t.Fatalf("native raw NAME=%T", v)
								}
							default:
								if _, ok := v.(int64); !ok || (i >= 5 && v != int64(0)) {
									t.Fatal("typed identity/zero", i, v)
								}
							}
						}
						count++
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					indexStatMetadata(t, columns, b.PG)
					if stmt != nil {
						stmt.Close()
					}
					want := 1
					if empty {
						want = 0
					}
					if count != want {
						t.Fatal("USER direct/prepared cardinality", count, want)
					}
				}
			}
		}
		for i, schema := range []string{"PG_TOAST", "xpg_toast", "information_schema_extra"} {
			quoted := `"`+schema+`"`
			fixture.StatisticsExec(t, p, b, c, "CREATE SCHEMA "+quoted, 0)
			name := []string{"uix_upper", "uix_embedded", "uix_extra"}[i]
			indexStatCreate(t, p, b, c, quoted+"."+name, name+"_hash")
			got := userIndexGet(t, p, c, name+"_hash")
			if got.Schema != schema || got.Table != name {
				t.Fatal("case-sensitive parent namespace membership", got, schema)
			}
			indexStatWant(t, got, 0, 0, 0)
		}
		sysIndexPartition(t, p, c, "uix_public_hash", "uix_identity_hash")
		userIndexDisplay(t, p, b, c)
	})
}

func userIndexDisplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT pg_get_viewdef('pg_catalog.pg_stat_user_indexes'::regclass,$1::boolean)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, pretty := range []bool{false, true} {
		want := userIndexDefinition
		if pretty {
			want = strings.Replace(want, "  WHERE ((schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND (schemaname !~ '^pg_toast'::text));", "  WHERE (schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND schemaname !~ '^pg_toast'::text;", 1)
		}
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(q, []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{want}}, pretty)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
	}
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{
			"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_user_indexes'",
			"SELECT view_definition FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_stat_user_indexes'",
		} {
			var definition string
			if prepared {
				s, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				err = s.QueryRowContext(p.Ctx).Scan(&definition)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if err := c.QueryRowContext(p.Ctx, query).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if definition != userIndexDefinition {
				t.Fatal("owning view display", definition, userIndexDefinition)
			}
		}
		step := fixture.Select("SELECT is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_stat_user_indexes'", []string{"is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"}, []string{"VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"NO", "NO", "NO", "NO", "NO"}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
	fixture.StatisticsExec(t, p, b, c, "SET quote_all_identifiers=on", 0)
	var quoted string
	if err := stmt.QueryRowContext(p.Ctx, false).Scan(&quoted); err != nil || !strings.Contains(quoted, `FROM "pg_stat_all_indexes"`) || !strings.Contains(quoted, `"schemaname"`) {
		t.Fatal("held display uses current quoting", quoted, err)
	}
	fixture.StatisticsExec(t, p, b, c, "SET quote_all_identifiers=off", 0)
	sysIndexOwned(t, b, "pg_stat_all_indexes")
	fixture.StatisticsExec(t, p, b, c, "CREATE TABLE public.pg_stat_all_indexes(n integer)", 0)
	fixture.StatisticsExec(t, p, b, c, "SET search_path=public,pg_catalog", 0)
	var shadowed string
	wantShadow := strings.Replace(userIndexDefinition, "FROM pg_stat_all_indexes", "FROM pg_catalog.pg_stat_all_indexes", 1)
	if err := stmt.QueryRowContext(p.Ctx, false).Scan(&shadowed); err != nil || shadowed != wantShadow {
		t.Fatal("held display uses current dependency visibility", shadowed, wantShadow, err)
	}
	fixture.StatisticsExec(t, p, b, c, "DROP TABLE public.pg_stat_all_indexes", 0)
	fixture.StatisticsExec(t, p, b, c, "RESET search_path", 0)
}

func userIndexPositiveMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, active bool) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, userIndexSelect, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	indexStatMetadata(t, columns, b.PG)
	if !rows.Next() {
		t.Fatal("USER metadata row absent", rows.Err())
	}
	var raw [9]any
	dest := make([]any, len(raw))
	for i := range raw {
		dest[i] = &raw[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	if active {
		if _, ok := raw[6].(time.Time); !ok {
			t.Fatalf("nonnull USER TIMESTAMPTZ=%T", raw[6])
		}
	} else if raw[6] != nil {
		t.Fatal("inactive USER instant must be NULL", raw[6])
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("USER metadata cardinality", rows.Err())
	}
}
