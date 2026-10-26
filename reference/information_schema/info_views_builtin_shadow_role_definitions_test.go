package reference_test

import "strings"

// Independently prescribed from REL_18_0 system_views.sql and ruleutils.c;
// formatting remains subject to pinned-oracle resolution before verification.
const ivShadowDefinition = ` SELECT pg_authid.rolname AS usename,
    pg_authid.oid AS usesysid,
    pg_authid.rolcreatedb AS usecreatedb,
    pg_authid.rolsuper AS usesuper,
    pg_authid.rolreplication AS userepl,
    pg_authid.rolbypassrls AS usebypassrls,
    pg_authid.rolpassword AS passwd,
    pg_authid.rolvaliduntil AS valuntil,
    s.setconfig AS useconfig
   FROM (pg_authid
     LEFT JOIN pg_db_role_setting s ON (((pg_authid.oid = s.setrole) AND (s.setdatabase = (0)::oid))))
  WHERE pg_authid.rolcanlogin;`

const ivShadowQuoteDefinition = ` SELECT "pg_authid"."rolname" AS "usename",
    "pg_authid"."oid" AS "usesysid",
    "pg_authid"."rolcreatedb" AS "usecreatedb",
    "pg_authid"."rolsuper" AS "usesuper",
    "pg_authid"."rolreplication" AS "userepl",
    "pg_authid"."rolbypassrls" AS "usebypassrls",
    "pg_authid"."rolpassword" AS "passwd",
    "pg_authid"."rolvaliduntil" AS "valuntil",
    "s"."setconfig" AS "useconfig"
   FROM ("pg_authid"
     LEFT JOIN "pg_db_role_setting" "s" ON ((("pg_authid"."oid" = "s"."setrole") AND ("s"."setdatabase" = (0)::"oid"))))
  WHERE "pg_authid"."rolcanlogin";`

var ivShadowQualifiedDefinition = strings.Replace(ivShadowDefinition, "FROM (pg_authid", "FROM (pg_catalog.pg_authid", 1)
var ivShadowQuoteQualifiedDefinition = strings.Replace(ivShadowQuoteDefinition, `FROM ("pg_authid"`, `FROM ("pg_catalog"."pg_authid"`, 1)
