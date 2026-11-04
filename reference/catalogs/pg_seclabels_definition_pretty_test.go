package reference_test

// Independently reasoned from REL_18_6 system_views.sql and ruleutils.c
// get_setop_query/get_target_list/get_coercion_expr/isSimpleNode. Unlike the
// released default regression string, pretty layout awaits root's pinned proof.
const pgSeclabelsPrettyDefinition = ` SELECT l.objoid,
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
