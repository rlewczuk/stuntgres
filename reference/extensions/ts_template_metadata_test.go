package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const tsTemplateQuery = "SELECT oid,tmplname,tmplnamespace,tmplinit,tmpllexize FROM pg_catalog.pg_ts_template WHERE tmplname=$1"

var tsTemplateNames = []string{"oid", "tmplname", "tmplnamespace", "tmplinit", "tmpllexize"}
var tsTemplateTypes = []string{"OID", "NAME", "OID", "REGPROC", "REGPROC"}

type tsTemplateIdentity struct {
	oid, namespace     int64
	name, init, lexize string
}

type tsTemplateHandlers struct{ init, lexize int64 }

func tsTemplateMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, tsTemplateNames) {
		t.Fatalf("template columns %v: %v", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 5 {
		t.Fatalf("template metadata %v: %v", types, err)
	}
	for i, column := range types {
		if column.Name() != tsTemplateNames[i] || column.DatabaseTypeName() != tsTemplateTypes[i] {
			t.Fatal("template column", i, column)
		}
		nullable, known := column.Nullable()
		if pg {
			if nullable || known {
				t.Fatal("pq must report unknown nullability", i)
			}
			if column.ScanType() != reflect.TypeOf((*any)(nil)).Elem() {
				t.Fatal("pq template ScanType", i, column.ScanType())
			}
		} else {
			if nullable || !known {
				t.Fatal("native template nullability", i, nullable, known)
			}
			want := reflect.TypeOf("")
			if i == 0 || i == 2 {
				want = reflect.TypeOf(int64(0))
			}
			if column.ScanType() != want {
				t.Fatal("native template ScanType", i, column.ScanType())
			}
		}
	}
}

func tsTemplateConsume(t *testing.T, rows *sql.Rows, pg, present bool) tsTemplateIdentity {
	t.Helper()
	defer rows.Close()
	tsTemplateMetadata(t, rows, pg)
	var id tsTemplateIdentity
	n := 0
	for rows.Next() {
		if err := rows.Scan(&id.oid, &id.name, &id.namespace, &id.init, &id.lexize); err != nil {
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
		t.Fatalf("template cardinality %d want %d", n, want)
	}
	if present && (id.oid <= 0 || id.oid > 4294967295 || id.namespace <= 0 || id.namespace > 4294967295 || id.name != "unaccent" || id.init != "ext_addon.unaccent_init" || id.lexize != "ext_addon.unaccent_lexize") {
		t.Fatalf("template identity %+v", id)
	}
	return id
}

func tsTemplateRead(t *testing.T, p *fixture.Pair, c *sql.Conn, pg, present bool) tsTemplateIdentity {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, tsTemplateQuery, "unaccent")
	if err != nil {
		t.Fatal(err)
	}
	return tsTemplateConsume(t, rows, pg, present)
}

func tsTemplateHeld(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, pg, present bool) tsTemplateIdentity {
	t.Helper()
	rows, err := stmt.QueryContext(p.Ctx, "unaccent")
	if err != nil {
		t.Fatal(err)
	}
	return tsTemplateConsume(t, rows, pg, present)
}

func tsTemplateNumeric(t *testing.T, p *fixture.Pair, c *sql.Conn) tsTemplateHandlers {
	t.Helper()
	var h tsTemplateHandlers
	if err := c.QueryRowContext(p.Ctx, "SELECT tmplinit::oid,tmpllexize::oid FROM pg_catalog.pg_ts_template WHERE tmplname='unaccent'").Scan(&h.init, &h.lexize); err != nil {
		t.Fatal(err)
	}
	if h.init <= 0 || h.init > 4294967295 || h.lexize <= 0 || h.lexize > 4294967295 || h.init == h.lexize {
		t.Fatal("template handler identities", h)
	}
	return h
}

func tsTemplateLinked(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) tsTemplateIdentity {
	t.Helper()
	id := tsTemplateRead(t, p, c, pg, true)
	tsTemplateNumeric(t, p, c)
	q := `SELECT n.nspname,i.proname,l.proname,i.pronamespace=t.tmplnamespace,l.pronamespace=t.tmplnamespace,i.pronargs,l.pronargs,i.proargtypes::text,l.proargtypes::text,i.prorettype::bigint,l.prorettype::bigint,t.tmplinit::oid=i.oid,t.tmpllexize::oid=l.oid,d.dicttemplate=t.oid
FROM pg_catalog.pg_ts_template t
JOIN pg_catalog.pg_namespace n ON n.oid=t.tmplnamespace
JOIN pg_catalog.pg_proc i ON i.oid=t.tmplinit::oid
JOIN pg_catalog.pg_proc l ON l.oid=t.tmpllexize::oid
JOIN pg_catalog.pg_ts_dict d ON d.dicttemplate=t.oid
WHERE t.tmplname='unaccent' AND d.dictname='unaccent'`
	s := fixture.Select(q, []string{"nspname", "proname", "proname", "?column?", "?column?", "pronargs", "pronargs", "proargtypes", "proargtypes", "prorettype", "prorettype", "?column?", "?column?", "?column?"}, []string{"NAME", "NAME", "NAME", "BOOL", "BOOL", "INT2", "INT2", "TEXT", "TEXT", "INT8", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{"ext_addon", "unaccent_init", "unaccent_lexize", true, true, int64(1), int64(4), "2281", "2281 2281 2281 2281", int64(2281), int64(2281), true, true, true}})
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	return id
}

func tsTemplateCapability(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	s := fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
}

func TestReferenceTSTemplateMetadata(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_ts_template", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT oid::bigint,tmplname,tmplnamespace::bigint,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_catalog.pg_ts_template WHERE oid=3727", tsTemplateNames, []string{"INT8", "NAME", "INT8", "INT8", "INT8"}, [][]any{{int64(3727), "simple", int64(11), int64(3725), int64(3726)}})
			s.Prepared = prepared
			run(s)
			synonym := fixture.Select("SELECT oid::bigint,tmplname,tmplnamespace::bigint,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_catalog.pg_ts_template WHERE oid=3730", tsTemplateNames, []string{"INT8", "NAME", "INT8", "INT8", "INT8"}, [][]any{{int64(3730), "synonym", int64(11), int64(3728), int64(3729)}})
			synonym.Prepared = prepared
			run(synonym)
		}
		held, err := c.PrepareContext(p.Ctx, tsTemplateQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, installed := range []bool{false, true} {
			if installed {
				run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
				run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
				tsTemplateLinked(t, p, c, r, pg)
				if !pg {
					run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_ts_template", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
				}
			}
			for _, prepared := range []bool{false, true} {
				for _, table := range []string{"pg_ts_template", "pg_catalog.pg_ts_template"} {
					s := fixture.Select("SELECT * FROM "+table+" WHERE FALSE", tsTemplateNames, tsTemplateTypes, nil)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { tsTemplateMetadata(t, rows, pg) }
					run(s)
				}
				for _, arg := range []any{"unaccent", "ts_template_absent", nil} {
					var rows *sql.Rows
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, tsTemplateQuery)
						if err != nil {
							t.Fatal(err)
						}
						rows, err = stmt.QueryContext(p.Ctx, arg)
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
						tsTemplateConsume(t, rows, pg, installed && arg == "unaccent")
						stmt.Close()
					} else {
						rows, err = c.QueryContext(p.Ctx, tsTemplateQuery, arg)
						if err != nil {
							t.Fatal(err)
						}
						tsTemplateConsume(t, rows, pg, installed && arg == "unaccent")
					}
					rows, err = held.QueryContext(p.Ctx, arg)
					if err != nil {
						t.Fatal(err)
					}
					tsTemplateConsume(t, rows, pg, installed && arg == "unaccent")
				}
			}
		}
		attrs := fixture.Select("SELECT attname,atttypid::bigint,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid=3764 AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "attnotnull"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"oid", int64(26), true}, {"tmplname", int64(19), true}, {"tmplnamespace", int64(26), true}, {"tmplinit", int64(24), true}, {"tmpllexize", int64(24), true}})
		run(attrs)
		run(fixture.Prepared(attrs))
		before := tsTemplateNumeric(t, p, c)
		run(fixture.Exec("SET search_path TO ext_addon,pg_catalog", 0))
		defer c.ExecContext(p.Ctx, "RESET search_path")
		s := fixture.Select("SELECT tmplinit,tmpllexize FROM pg_catalog.pg_ts_template WHERE tmplname='unaccent'", []string{"tmplinit", "tmpllexize"}, []string{"REGPROC", "REGPROC"}, [][]any{{"unaccent_init", "unaccent_lexize"}})
		run(s)
		run(fixture.Prepared(s))
		if tsTemplateNumeric(t, p, c) != before {
			t.Fatal("search path changed handler identity")
		}
		run(fixture.Exec("RESET search_path", 0))
		tsTemplateLinked(t, p, c, r, pg)
		tsTemplateCapability(t, p, c, r)
	})
}
