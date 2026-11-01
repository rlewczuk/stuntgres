package pgcatalog

// information_schema.udt_privileges — REL_18_6 information_schema.sql and
// pinned PostgreSQL 18.6 pg_get_viewdef presentation. Tokens retain captured
// object-name and identifier authority under search-path and quote settings.
var infoUdtPrivilegesView = builtinView{
	oid: InfoUdtPrivilegesOID,
	parts: []builtinViewPart{
		viewText(" SELECT ("), viewIdentifier("u_grantor"), viewText("."), viewIdentifier("rolname"), viewText(")::"), viewDomain("information_schema.sql_identifier"), viewText(" AS "), viewIdentifier("grantor"), viewText(",\n"),
		viewText("    ("), viewIdentifier("grantee"), viewText("."), viewIdentifier("rolname"), viewText(")::"), viewDomain("information_schema.sql_identifier"), viewText(" AS "), viewIdentifier("grantee"), viewText(",\n"),
		viewText("    ("), viewRoutine("current_database", ""), viewText("())::"), viewDomain("information_schema.sql_identifier"), viewText(" AS "), viewIdentifier("udt_catalog"), viewText(",\n"),
		viewText("    ("), viewIdentifier("n"), viewText("."), viewIdentifier("nspname"), viewText(")::"), viewDomain("information_schema.sql_identifier"), viewText(" AS "), viewIdentifier("udt_schema"), viewText(",\n"),
		viewText("    ("), viewIdentifier("t"), viewText("."), viewIdentifier("typname"), viewText(")::"), viewDomain("information_schema.sql_identifier"), viewText(" AS "), viewIdentifier("udt_name"), viewText(",\n"),
		viewText("    ('TYPE USAGE'::character varying)::"), viewDomain("information_schema.character_data"), viewText(" AS "), viewIdentifier("privilege_type"), viewText(",\n"),
		viewText("    (\n"),
		viewText("        CASE\n"),
		viewText("            WHEN ("), viewRoutine("pg_has_role", "26,26,25"), viewText("("), viewIdentifier("grantee"), viewText("."), viewIdentifier("oid"), viewText(", "), viewIdentifier("t"), viewText("."), viewIdentifier("typowner"), viewText(", 'USAGE'::"), viewType("text"), viewText(") OR "), viewIdentifier("t"), viewText("."), viewIdentifier("grantable"), viewText(") THEN 'YES'::"), viewType("text"), viewText("\n"),
		viewText("            ELSE 'NO'::"), viewType("text"), viewText("\n"),
		viewText("        END)::"), viewDomain("information_schema.yes_or_no"), viewText(" AS "), viewIdentifier("is_grantable"), viewText("\n"),
		viewText("   FROM ( SELECT "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("oid"), viewText(",\n"),
		viewText("            "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typname"), viewText(",\n"),
		viewText("            "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typnamespace"), viewText(",\n"),
		viewText("            "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typtype"), viewText(",\n"),
		viewText("            "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typowner"), viewText(",\n"),
		viewText("            ("), viewRoutine("aclexplode", "1034"), viewText("(COALESCE("), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typacl"), viewText(", "), viewRoutine("acldefault", "18,26"), viewText("('T'::"), viewType("\"char\""), viewText(", "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typowner"), viewText("))))."), viewIdentifier("grantor"), viewText(" AS "), viewIdentifier("grantor"), viewText(",\n"),
		viewText("            ("), viewRoutine("aclexplode", "1034"), viewText("(COALESCE("), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typacl"), viewText(", "), viewRoutine("acldefault", "18,26"), viewText("('T'::"), viewType("\"char\""), viewText(", "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typowner"), viewText("))))."), viewIdentifier("grantee"), viewText(" AS "), viewIdentifier("grantee"), viewText(",\n"),
		viewText("            ("), viewRoutine("aclexplode", "1034"), viewText("(COALESCE("), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typacl"), viewText(", "), viewRoutine("acldefault", "18,26"), viewText("('T'::"), viewType("\"char\""), viewText(", "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typowner"), viewText("))))."), viewIdentifier("privilege_type"), viewText(" AS "), viewIdentifier("privilege_type"), viewText(",\n"),
		viewText("            ("), viewRoutine("aclexplode", "1034"), viewText("(COALESCE("), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typacl"), viewText(", "), viewRoutine("acldefault", "18,26"), viewText("('T'::"), viewType("\"char\""), viewText(", "), viewIdentifier("pg_type"), viewText("."), viewIdentifier("typowner"), viewText("))))."), viewIdentifier("is_grantable"), viewText(" AS "), viewIdentifier("is_grantable"), viewText("\n"),
		viewText("           FROM "), viewClass("pg_type"), viewText(") "), viewIdentifier("t"), viewText("("), viewIdentifier("oid"), viewText(", "), viewIdentifier("typname"), viewText(", "), viewIdentifier("typnamespace"), viewText(", "), viewIdentifier("typtype"), viewText(", "), viewIdentifier("typowner"), viewText(", "), viewIdentifier("grantor"), viewText(", "), viewIdentifier("grantee"), viewText(", "), viewIdentifier("prtype"), viewText(", "), viewIdentifier("grantable"), viewText("),\n"),
		viewText("    "), viewClass("pg_namespace"), viewText(" "), viewIdentifier("n"), viewText(",\n"),
		viewText("    "), viewClass("pg_authid"), viewText(" "), viewIdentifier("u_grantor"), viewText(",\n"),
		viewText("    ( SELECT "), viewIdentifier("pg_authid"), viewText("."), viewIdentifier("oid"), viewText(",\n"),
		viewText("            "), viewIdentifier("pg_authid"), viewText("."), viewIdentifier("rolname"), viewText("\n"),
		viewText("           FROM "), viewClass("pg_authid"), viewText("\n"),
		viewText("        UNION ALL\n"),
		viewText("         SELECT (0)::"), viewType("oid"), viewText(" AS "), viewIdentifier("oid"), viewText(",\n"),
		viewText("            'PUBLIC'::"), viewType("name"), viewText(") "), viewIdentifier("grantee"), viewText("("), viewIdentifier("oid"), viewText(", "), viewIdentifier("rolname"), viewText(")\n"),
		viewText("  WHERE (("), viewIdentifier("t"), viewText("."), viewIdentifier("typnamespace"), viewText(" = "), viewIdentifier("n"), viewText("."), viewIdentifier("oid"), viewText(") AND ("), viewIdentifier("t"), viewText("."), viewIdentifier("typtype"), viewText(" = 'c'::"), viewType("\"char\""), viewText(") AND ("), viewIdentifier("t"), viewText("."), viewIdentifier("grantee"), viewText(" = "), viewIdentifier("grantee"), viewText("."), viewIdentifier("oid"), viewText(") AND ("), viewIdentifier("t"), viewText("."), viewIdentifier("grantor"), viewText(" = "), viewIdentifier("u_grantor"), viewText("."), viewIdentifier("oid"), viewText(") AND ("), viewIdentifier("t"), viewText("."), viewIdentifier("prtype"), viewText(" = 'USAGE'::"), viewType("text"), viewText(") AND ("), viewRoutine("pg_has_role", "26,25"), viewText("("), viewIdentifier("u_grantor"), viewText("."), viewIdentifier("oid"), viewText(", 'USAGE'::"), viewType("text"), viewText(") OR "), viewRoutine("pg_has_role", "26,25"), viewText("("), viewIdentifier("grantee"), viewText("."), viewIdentifier("oid"), viewText(", 'USAGE'::"), viewType("text"), viewText(") OR ("), viewIdentifier("grantee"), viewText("."), viewIdentifier("rolname"), viewText(" = 'PUBLIC'::"), viewType("name"), viewText(")));"),
	},
}
