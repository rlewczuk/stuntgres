package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently observed on the fixture-pinned PostgreSQL18.6 UTF8/C oracle.
// These are literal oracle observations, not native renderer output or inferred
// formatting of system_views.sql.
const sepgsqlViewDefault = ` SELECT l.objoid,
    l.classoid,
    l.objsubid,
        CASE
            WHEN (rel.relkind = ANY (ARRAY['r'::"char", 'p'::"char"])) THEN 'table'::text
            WHEN (rel.relkind = 'v'::"char") THEN 'view'::text
            WHEN (rel.relkind = 'm'::"char") THEN 'materialized view'::text
            WHEN (rel.relkind = 'S'::"char") THEN 'sequence'::text
            WHEN (rel.relkind = 'f'::"char") THEN 'foreign table'::text
            ELSE NULL::text
        END AS objtype,
    rel.relnamespace AS objnamespace,
        CASE
            WHEN pg_table_is_visible(rel.oid) THEN quote_ident((rel.relname)::text)
            ELSE ((quote_ident((nsp.nspname)::text) || '.'::text) || quote_ident((rel.relname)::text))
        END AS objname,
    l.provider,
    l.label
   FROM ((pg_seclabel l
     JOIN pg_class rel ON (((l.classoid = rel.tableoid) AND (l.objoid = rel.oid))))
     JOIN pg_namespace nsp ON ((rel.relnamespace = nsp.oid)))
  WHERE (l.objsubid = 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'column'::text AS objtype,
    rel.relnamespace AS objnamespace,
    ((
        CASE
            WHEN pg_table_is_visible(rel.oid) THEN quote_ident((rel.relname)::text)
            ELSE ((quote_ident((nsp.nspname)::text) || '.'::text) || quote_ident((rel.relname)::text))
        END || '.'::text) || (att.attname)::text) AS objname,
    l.provider,
    l.label
   FROM (((pg_seclabel l
     JOIN pg_class rel ON (((l.classoid = rel.tableoid) AND (l.objoid = rel.oid))))
     JOIN pg_attribute att ON (((rel.oid = att.attrelid) AND (l.objsubid = att.attnum))))
     JOIN pg_namespace nsp ON ((rel.relnamespace = nsp.oid)))
  WHERE (l.objsubid <> 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
        CASE pro.prokind
            WHEN 'a'::"char" THEN 'aggregate'::text
            WHEN 'f'::"char" THEN 'function'::text
            WHEN 'p'::"char" THEN 'procedure'::text
            WHEN 'w'::"char" THEN 'window'::text
            ELSE NULL::text
        END AS objtype,
    pro.pronamespace AS objnamespace,
    (((
        CASE
            WHEN pg_function_is_visible(pro.oid) THEN quote_ident((pro.proname)::text)
            ELSE ((quote_ident((nsp.nspname)::text) || '.'::text) || quote_ident((pro.proname)::text))
        END || '('::text) || pg_get_function_arguments(pro.oid)) || ')'::text) AS objname,
    l.provider,
    l.label
   FROM ((pg_seclabel l
     JOIN pg_proc pro ON (((l.classoid = pro.tableoid) AND (l.objoid = pro.oid))))
     JOIN pg_namespace nsp ON ((pro.pronamespace = nsp.oid)))
  WHERE (l.objsubid = 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
        CASE
            WHEN (typ.typtype = 'd'::"char") THEN 'domain'::text
            ELSE 'type'::text
        END AS objtype,
    typ.typnamespace AS objnamespace,
        CASE
            WHEN pg_type_is_visible(typ.oid) THEN quote_ident((typ.typname)::text)
            ELSE ((quote_ident((nsp.nspname)::text) || '.'::text) || quote_ident((typ.typname)::text))
        END AS objname,
    l.provider,
    l.label
   FROM ((pg_seclabel l
     JOIN pg_type typ ON (((l.classoid = typ.tableoid) AND (l.objoid = typ.oid))))
     JOIN pg_namespace nsp ON ((typ.typnamespace = nsp.oid)))
  WHERE (l.objsubid = 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'large object'::text AS objtype,
    NULL::oid AS objnamespace,
    (l.objoid)::text AS objname,
    l.provider,
    l.label
   FROM (pg_seclabel l
     JOIN pg_largeobject_metadata lom ON ((l.objoid = lom.oid)))
  WHERE ((l.classoid = ('pg_largeobject'::regclass)::oid) AND (l.objsubid = 0))
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'language'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident((lan.lanname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_seclabel l
     JOIN pg_language lan ON (((l.classoid = lan.tableoid) AND (l.objoid = lan.oid))))
  WHERE (l.objsubid = 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'schema'::text AS objtype,
    nsp.oid AS objnamespace,
    quote_ident((nsp.nspname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_seclabel l
     JOIN pg_namespace nsp ON (((l.classoid = nsp.tableoid) AND (l.objoid = nsp.oid))))
  WHERE (l.objsubid = 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'event trigger'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident((evt.evtname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_seclabel l
     JOIN pg_event_trigger evt ON (((l.classoid = evt.tableoid) AND (l.objoid = evt.oid))))
  WHERE (l.objsubid = 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'publication'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident((p.pubname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_seclabel l
     JOIN pg_publication p ON (((l.classoid = p.tableoid) AND (l.objoid = p.oid))))
  WHERE (l.objsubid = 0)
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'subscription'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident((s.subname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_shseclabel l
     JOIN pg_subscription s ON (((l.classoid = s.tableoid) AND (l.objoid = s.oid))))
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'database'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident((dat.datname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_shseclabel l
     JOIN pg_database dat ON (((l.classoid = dat.tableoid) AND (l.objoid = dat.oid))))
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'tablespace'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident((spc.spcname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_shseclabel l
     JOIN pg_tablespace spc ON (((l.classoid = spc.tableoid) AND (l.objoid = spc.oid))))
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'role'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident((rol.rolname)::text) AS objname,
    l.provider,
    l.label
   FROM (pg_shseclabel l
     JOIN pg_authid rol ON (((l.classoid = rol.tableoid) AND (l.objoid = rol.oid))));`

const sepgsqlViewPretty = ` SELECT l.objoid,
    l.classoid,
    l.objsubid,
        CASE
            WHEN rel.relkind = ANY (ARRAY['r'::"char", 'p'::"char"]) THEN 'table'::text
            WHEN rel.relkind = 'v'::"char" THEN 'view'::text
            WHEN rel.relkind = 'm'::"char" THEN 'materialized view'::text
            WHEN rel.relkind = 'S'::"char" THEN 'sequence'::text
            WHEN rel.relkind = 'f'::"char" THEN 'foreign table'::text
            ELSE NULL::text
        END AS objtype,
    rel.relnamespace AS objnamespace,
        CASE
            WHEN pg_table_is_visible(rel.oid) THEN quote_ident(rel.relname::text)
            ELSE (quote_ident(nsp.nspname::text) || '.'::text) || quote_ident(rel.relname::text)
        END AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_class rel ON l.classoid = rel.tableoid AND l.objoid = rel.oid
     JOIN pg_namespace nsp ON rel.relnamespace = nsp.oid
  WHERE l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'column'::text AS objtype,
    rel.relnamespace AS objnamespace,
    (
        CASE
            WHEN pg_table_is_visible(rel.oid) THEN quote_ident(rel.relname::text)
            ELSE (quote_ident(nsp.nspname::text) || '.'::text) || quote_ident(rel.relname::text)
        END || '.'::text) || att.attname::text AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_class rel ON l.classoid = rel.tableoid AND l.objoid = rel.oid
     JOIN pg_attribute att ON rel.oid = att.attrelid AND l.objsubid = att.attnum
     JOIN pg_namespace nsp ON rel.relnamespace = nsp.oid
  WHERE l.objsubid <> 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
        CASE pro.prokind
            WHEN 'a'::"char" THEN 'aggregate'::text
            WHEN 'f'::"char" THEN 'function'::text
            WHEN 'p'::"char" THEN 'procedure'::text
            WHEN 'w'::"char" THEN 'window'::text
            ELSE NULL::text
        END AS objtype,
    pro.pronamespace AS objnamespace,
    ((
        CASE
            WHEN pg_function_is_visible(pro.oid) THEN quote_ident(pro.proname::text)
            ELSE (quote_ident(nsp.nspname::text) || '.'::text) || quote_ident(pro.proname::text)
        END || '('::text) || pg_get_function_arguments(pro.oid)) || ')'::text AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_proc pro ON l.classoid = pro.tableoid AND l.objoid = pro.oid
     JOIN pg_namespace nsp ON pro.pronamespace = nsp.oid
  WHERE l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
        CASE
            WHEN typ.typtype = 'd'::"char" THEN 'domain'::text
            ELSE 'type'::text
        END AS objtype,
    typ.typnamespace AS objnamespace,
        CASE
            WHEN pg_type_is_visible(typ.oid) THEN quote_ident(typ.typname::text)
            ELSE (quote_ident(nsp.nspname::text) || '.'::text) || quote_ident(typ.typname::text)
        END AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_type typ ON l.classoid = typ.tableoid AND l.objoid = typ.oid
     JOIN pg_namespace nsp ON typ.typnamespace = nsp.oid
  WHERE l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'large object'::text AS objtype,
    NULL::oid AS objnamespace,
    l.objoid::text AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_largeobject_metadata lom ON l.objoid = lom.oid
  WHERE l.classoid = 'pg_largeobject'::regclass::oid AND l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'language'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident(lan.lanname::text) AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_language lan ON l.classoid = lan.tableoid AND l.objoid = lan.oid
  WHERE l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'schema'::text AS objtype,
    nsp.oid AS objnamespace,
    quote_ident(nsp.nspname::text) AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_namespace nsp ON l.classoid = nsp.tableoid AND l.objoid = nsp.oid
  WHERE l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'event trigger'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident(evt.evtname::text) AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_event_trigger evt ON l.classoid = evt.tableoid AND l.objoid = evt.oid
  WHERE l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    l.objsubid,
    'publication'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident(p.pubname::text) AS objname,
    l.provider,
    l.label
   FROM pg_seclabel l
     JOIN pg_publication p ON l.classoid = p.tableoid AND l.objoid = p.oid
  WHERE l.objsubid = 0
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'subscription'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident(s.subname::text) AS objname,
    l.provider,
    l.label
   FROM pg_shseclabel l
     JOIN pg_subscription s ON l.classoid = s.tableoid AND l.objoid = s.oid
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'database'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident(dat.datname::text) AS objname,
    l.provider,
    l.label
   FROM pg_shseclabel l
     JOIN pg_database dat ON l.classoid = dat.tableoid AND l.objoid = dat.oid
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'tablespace'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident(spc.spcname::text) AS objname,
    l.provider,
    l.label
   FROM pg_shseclabel l
     JOIN pg_tablespace spc ON l.classoid = spc.tableoid AND l.objoid = spc.oid
UNION ALL
 SELECT l.objoid,
    l.classoid,
    0 AS objsubid,
    'role'::text AS objtype,
    NULL::oid AS objnamespace,
    quote_ident(rol.rolname::text) AS objname,
    l.provider,
    l.label
   FROM pg_shseclabel l
     JOIN pg_authid rol ON l.classoid = rol.tableoid AND l.objoid = rol.oid;`

func sepgsqlViewMetadata(run sepgsqlRun, requireKnown bool) {
	empty := fixture.Select("SELECT * FROM pg_catalog.pg_seclabels WHERE false", []string{"objoid", "classoid", "objsubid", "objtype", "objnamespace", "objname", "provider", "label"}, []string{"OID", "OID", "INT4", "TEXT", "OID", "TEXT", "TEXT", "TEXT"}, nil)
	empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 8 {
			t.Fatal(columns, err)
		}
		for _, column := range columns {
			nullable, known := column.Nullable()
			if requireKnown && !known || known && !nullable {
				t.Fatal("all eight view columns are nullable", column.Name(), nullable, known)
			}
		}
	}
	run(empty)
	run(fixture.Prepared(empty))
	run(sepgsqlText("SELECT pg_get_viewdef('pg_catalog.pg_seclabels'::regclass,false) AS definition", "definition", sepgsqlViewDefault))
	run(sepgsqlText("SELECT pg_get_viewdef('pg_catalog.pg_seclabels'::regclass,true) AS definition", "definition", sepgsqlViewPretty))
	// The pinned discovery observed these exact rule prefixes followed by the
	// corresponding literal view definitions above, including their semicolon.
	run(sepgsqlText("SELECT pg_get_ruledef(oid,false) AS definition FROM pg_rewrite WHERE ev_class='pg_catalog.pg_seclabels'::regclass AND rulename='_RETURN'", "definition", "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO pg_catalog.pg_seclabels DO INSTEAD "+sepgsqlViewDefault))
	run(sepgsqlText("SELECT pg_get_ruledef(oid,true) AS definition FROM pg_rewrite WHERE ev_class='pg_catalog.pg_seclabels'::regclass AND rulename='_RETURN'", "definition", "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO pg_seclabels DO INSTEAD "+sepgsqlViewPretty))
	run(fixture.Select("SELECT attname::text,attnotnull,attcollation::bigint FROM pg_attribute WHERE attrelid='pg_catalog.pg_seclabels'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnotnull", "attcollation"}, []string{"TEXT", "BOOL", "INT8"}, [][]any{
		{"objoid", false, int64(0)},
		{"classoid", false, int64(0)},
		{"objsubid", false, int64(0)},
		{"objtype", false, int64(100)},
		{"objnamespace", false, int64(0)},
		{"objname", false, int64(950)},
		{"provider", false, int64(950)},
		{"label", false, int64(950)},
	}))
}
