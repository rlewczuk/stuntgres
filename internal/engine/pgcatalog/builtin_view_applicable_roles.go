package pgcatalog

// Released REL_18_6 information_schema.sql presentation. These symbolic parts
// disclose a definition only; captureInfoApplicableRoles owns execution/visibility.
var applicableRolesBuiltinView = builtinView{oid: InfoApplicableRolesOID, parts: []builtinViewPart{
	viewText(" SELECT ("), viewIdentifier("a"), viewText("."), viewIdentifier("rolname"), viewText(")::"), viewDomain("information_schema.sql_identifier"), viewText(" AS "), viewIdentifier("grantee"), viewText(",\n"),
	viewText("    ("), viewIdentifier("b"), viewText("."), viewIdentifier("rolname"), viewText(")::"), viewDomain("information_schema.sql_identifier"), viewText(" AS "), viewIdentifier("role_name"), viewText(",\n"),
	viewText("    (\n        CASE\n            WHEN "), viewIdentifier("m"), viewText("."), viewIdentifier("admin_option"), viewText(" THEN 'YES'::"), viewType("text"), viewText("\n            ELSE 'NO'::"), viewType("text"), viewText("\n        END)::"), viewDomain("information_schema.yes_or_no"), viewText(" AS "), viewIdentifier("is_grantable"), viewText("\n"),
	viewText("   FROM ((( SELECT "), viewIdentifier("pg_auth_members"), viewText("."), viewIdentifier("member"), viewText(",\n"),
	viewText("            "), viewIdentifier("pg_auth_members"), viewText("."), viewIdentifier("roleid"), viewText(",\n"),
	viewText("            "), viewIdentifier("pg_auth_members"), viewText("."), viewIdentifier("admin_option"), viewText("\n"),
	viewText("           FROM "), viewClass("pg_auth_members"), viewText("\n        UNION\n         SELECT "), viewIdentifier("pg_database"), viewText("."), viewIdentifier("datdba"), viewText(",\n"),
	viewText("            "), viewIdentifier("pg_authid"), viewText("."), viewIdentifier("oid"), viewText(",\n            false\n"),
	viewText("           FROM "), viewClass("pg_database"), viewText(",\n            "), viewClass("pg_authid"), viewText("\n"),
	viewText("          WHERE (("), viewIdentifier("pg_database"), viewText("."), viewIdentifier("datname"), viewText(" = "), viewRoutine("current_database", ""), viewText("()) AND ("), viewIdentifier("pg_authid"), viewText("."), viewIdentifier("rolname"), viewText(" = 'pg_database_owner'::"), viewType("name"), viewText("))) "), viewIdentifier("m"), viewText("\n"),
	viewText("     JOIN "), viewClass("pg_authid"), viewText(" "), viewIdentifier("a"), viewText(" ON (("), viewIdentifier("m"), viewText("."), viewIdentifier("member"), viewText(" = "), viewIdentifier("a"), viewText("."), viewIdentifier("oid"), viewText(")))\n"),
	viewText("     JOIN "), viewClass("pg_authid"), viewText(" "), viewIdentifier("b"), viewText(" ON (("), viewIdentifier("m"), viewText("."), viewIdentifier("roleid"), viewText(" = "), viewIdentifier("b"), viewText("."), viewIdentifier("oid"), viewText(")))\n"),
	viewText("  WHERE "), viewRoutine("pg_has_role", "26,25"), viewText("("), viewIdentifier("a"), viewText("."), viewIdentifier("oid"), viewText(", 'USAGE'::"), viewType("text"), viewText(");"),
}}
