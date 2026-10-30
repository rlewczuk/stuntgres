package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const tsDictQuery = "SELECT oid,dictname,dictnamespace,dictowner,dicttemplate,dictinitoption FROM pg_catalog.pg_ts_dict WHERE dictname=$1"

var tsDictNames = []string{"oid", "dictname", "dictnamespace", "dictowner", "dicttemplate", "dictinitoption"}
var tsDictTypes = []string{"OID", "NAME", "OID", "OID", "OID", "TEXT"}

type tsDictIdentity struct {
	oid, namespace, owner, template int64
	name, options                   string
}

func tsDictMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, tsDictNames) {
		t.Fatalf("dictionary columns %v: %v", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 6 {
		t.Fatalf("dictionary metadata %v: %v", types, err)
	}
	for i, column := range types {
		nullable, known := column.Nullable()
		if column.Name() != tsDictNames[i] || column.DatabaseTypeName() != tsDictTypes[i] {
			t.Fatalf("dictionary column %d: %v", i, column)
		}
		if pg {
			if nullable || known {
				t.Fatal("pq must report unknown nullability", i)
			}
		} else if !known || nullable != (i == 5) {
			t.Fatal("dictionary nullability", i, nullable, known)
		}
	}
}

func tsDictConsume(t *testing.T, rows *sql.Rows, pg bool, present bool) tsDictIdentity {
	t.Helper()
	defer rows.Close()
	tsDictMetadata(t, rows, pg)
	var result tsDictIdentity
	n := 0
	for rows.Next() {
		if err := rows.Scan(&result.oid, &result.name, &result.namespace, &result.owner, &result.template, &result.options); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := 0
	if present {
		want = 1
	}
	if n != want {
		t.Fatalf("dictionary cardinality %d want %d", n, want)
	}
	if present && (result.oid <= 0 || result.namespace <= 0 || result.owner <= 0 || result.template <= 0 || result.name != "unaccent" || result.options != "rules = 'unaccent'") {
		t.Fatalf("dictionary identity/options: %+v", result)
	}
	return result
}

func tsDictRead(t *testing.T, p *fixture.Pair, c *sql.Conn, pg, present bool) tsDictIdentity {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, tsDictQuery, "unaccent")
	if err != nil {
		t.Fatal(err)
	}
	return tsDictConsume(t, rows, pg, present)
}

func tsDictHeld(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, pg, present bool) tsDictIdentity {
	t.Helper()
	rows, err := stmt.QueryContext(p.Ctx, "unaccent")
	if err != nil {
		t.Fatal(err)
	}
	return tsDictConsume(t, rows, pg, present)
}

func tsDictLinked(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) tsDictIdentity {
	t.Helper()
	id := tsDictRead(t, p, c, pg, true)
	// Resolve all referenced identities through their owning catalogs. A trusted
	// extension's dictionary owner need not equal the installing extension owner.
	q := `SELECT n.nspname,t.tmplname,d.dictnamespace=t.tmplnamespace,d.dictnamespace=e.extnamespace,d.dictowner=r.oid,m.objsubid,m.refobjsubid,m.deptype
FROM pg_catalog.pg_ts_dict d
JOIN pg_catalog.pg_namespace n ON n.oid=d.dictnamespace
JOIN pg_catalog.pg_ts_template t ON t.oid=d.dicttemplate
JOIN pg_catalog.pg_roles r ON r.oid=d.dictowner
JOIN pg_catalog.pg_depend m ON m.objid=d.oid AND m.classid=3600
JOIN pg_catalog.pg_extension e ON e.oid=m.refobjid AND m.refclassid=3079
WHERE d.dictname='unaccent' AND e.extname='unaccent' AND m.deptype='e'`
	s := fixture.Select(q, []string{"nspname", "tmplname", "?column?", "?column?", "?column?", "objsubid", "refobjsubid", "deptype"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "INT4", "INT4", "CHAR"}, [][]any{{"ext_addon", "unaccent", true, true, true, int64(0), int64(0), "e"}})
	fixture.RunStep(t, p.Ctx, c, r, s)
	return id
}

func tsDictAvailability(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, installed bool) {
	t.Helper()
	var version any
	if installed {
		version = "1.1"
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT name,default_version,installed_version FROM pg_available_extensions WHERE name='unaccent'", []string{"name", "default_version", "installed_version"}, []string{"NAME", "TEXT", "TEXT"}, [][]any{{"unaccent", "1.1", version}}))
}

func TestReferenceTSDictMetadata(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_ts_dict", []string{"count"}, []string{"INT8"}, [][]any{{int64(19)}}))
		}
		run(fixture.Select("SELECT oid::bigint,dictname,dictnamespace::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_catalog.pg_ts_dict WHERE oid=3765", tsDictNames, []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT"}, [][]any{{int64(3765), "simple", int64(11), int64(10), int64(3727), nil}}))
		held, err := c.PrepareContext(p.Ctx, tsDictQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, installed := range []bool{false, true} {
			if installed {
				run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
				run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
				tsDictLinked(t, p, c, r, pg)
				if !pg {
					run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_ts_dict", []string{"count"}, []string{"INT8"}, [][]any{{int64(20)}}))
				}
			}
			tsDictAvailability(t, p, c, r, installed)
			for _, prepared := range []bool{false, true} {
				count := 0
				if installed {
					count = 1
				}
				for _, empty := range []bool{false, true} {
					q := "SELECT oid,dictname,dictnamespace,dictowner,dicttemplate,dictinitoption FROM pg_catalog.pg_ts_dict WHERE dictname='unaccent'"
					n := count
					if empty {
						q += " AND FALSE"
						n = 0
					}
					extensionMemberMetadata(t, p, c, q, prepared, pg, tsDictNames, tsDictTypes, true, n)
				}
				for _, arg := range []any{"unaccent", "ts_dict_absent", nil} {
					var rows *sql.Rows
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, tsDictQuery)
						if err != nil {
							t.Fatal(err)
						}
						rows, err = stmt.QueryContext(p.Ctx, arg)
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
						tsDictConsume(t, rows, pg, installed && arg == "unaccent")
						stmt.Close()
					} else {
						rows, err = c.QueryContext(p.Ctx, tsDictQuery, arg)
						if err != nil {
							t.Fatal(err)
						}
						tsDictConsume(t, rows, pg, installed && arg == "unaccent")
					}
					rows, err = held.QueryContext(p.Ctx, arg)
					if err != nil {
						t.Fatal(err)
					}
					tsDictConsume(t, rows, pg, installed && arg == "unaccent")
				}
			}
		}
		attrs := fixture.Select("SELECT attname,atttypid::bigint,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid=3600 AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "attnotnull"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"oid", int64(26), true}, {"dictname", int64(19), true}, {"dictnamespace", int64(26), true}, {"dictowner", int64(26), true}, {"dicttemplate", int64(26), true}, {"dictinitoption", int64(25), false}})
		run(attrs)
		run(fixture.Prepared(attrs))
		for _, prepared := range []bool{false, true} {
			for _, options := range []string{"rules = 'unaccent'", "RULES='unaccent'"} {
				want := int64(0)
				if options == "rules = 'unaccent'" {
					want = 1
				}
				s := fixture.Select("SELECT count(*) FROM pg_catalog.pg_ts_dict WHERE dictname='unaccent' AND dictinitoption=$1", []string{"count"}, []string{"INT8"}, [][]any{{want}}, options)
				s.Prepared = prepared
				run(s)
			}
			s := fixture.Select("SELECT oid::regdictionary::oid=oid FROM pg_catalog.pg_ts_dict WHERE dictname='unaccent'", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("DROP EXTENSION unaccent", 0))
		tsDictAvailability(t, p, c, r, false)
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_ts_dict", []string{"count"}, []string{"INT8"}, [][]any{{int64(19)}}))
		}
	})
}
