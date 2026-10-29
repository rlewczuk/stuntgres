package routine

import "stuntgres/internal/engine/proc"

// Released SQL remains the parsed structural owner. The separate analyzed
// display slots retain the pinned18.6 star expansion/casts and follow ordinary
// identifier quoting, current routine identities and formal-name presentation.
func debugDeclarationRecipe(explicit bool) proc.DeclarationRecipe {
	const source = `SELECT tt.alias AS alias, tt.description AS description, parse.token AS token,
 ARRAY(SELECT m.mapdict::regdictionary FROM pg_ts_config_map AS m WHERE m.mapcfg = $1 AND m.maptokentype = parse.tokid ORDER BY m.mapseqno) AS dictionaries,
 (SELECT mapdict::regdictionary FROM pg_ts_config_map AS m WHERE m.mapcfg = $1 AND m.maptokentype = parse.tokid ORDER BY ts_lexize(mapdict, parse.token) IS NULL, m.mapseqno LIMIT 1) AS dictionary,
 (SELECT ts_lexize(mapdict, parse.token) FROM pg_ts_config_map AS m WHERE m.mapcfg = $1 AND m.maptokentype = parse.tokid ORDER BY ts_lexize(mapdict, parse.token) IS NULL, m.mapseqno LIMIT 1) AS lexemes
 FROM ts_parse((SELECT cfgparser FROM pg_ts_config WHERE oid = $1), $2) AS parse,
 ts_token_type((SELECT cfgparser FROM pg_ts_config WHERE oid = $1)) AS tt WHERE tt.tokid = parse.tokid`
	recipe := proc.DeclarationRecipe{Source: source}
	var parts []proc.DeclarationPart
	text := func(s string) { parts = append(parts, proc.DeclarationText(s)) }
	identifier := func(s string) { parts = append(parts, proc.DeclarationIdentifierName(s)) }
	column := func(alias, name string) { identifier(alias); text("."); identifier(name) }
	selfArgument := func(slot int) {
		parts = append(parts, proc.DeclarationSelfRoutineName("ts_debug"))
		text(".")
		parts = append(parts, proc.DeclarationArgument(slot))
	}
	function := func(name, signature string) { parts = append(parts, proc.DeclarationFunction(name, signature)) }
	typ := func(oid uint32, name string) { parts = append(parts, proc.DeclarationTypeName(oid, name)) }
	fields := []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}
	if !explicit {
		recipe.Source = "SELECT * FROM ts_debug(get_current_ts_config(), $1)"
		text("SELECT ")
		for i, field := range fields {
			if i != 0 {
				text(",\n     ")
			}
			column("ts_debug", field)
		}
		text("\n    FROM ")
		function("ts_debug", "3734,25")
		text("(")
		function("get_current_ts_config", "")
		text("(), ")
		selfArgument(0)
		text(") ")
		identifier("ts_debug")
		text("(")
		for i, field := range fields {
			if i != 0 {
				text(", ")
			}
			identifier(field)
		}
		text(")")
		recipe.Parts = parts
		return recipe
	}
	configOID := func() { text("("); selfArgument(0); text(")::"); typ(26, "oid") }
	dictionary := func() { text("("); column("m", "mapdict"); text(")::"); typ(3769, "regdictionary") }
	lexize := func() {
		function("ts_lexize", "3769,25")
		text("(")
		dictionary()
		text(", ")
		column("parse", "token")
		text(")")
	}
	mapping := func() {
		text("\n            FROM ")
		parts = append(parts, proc.DeclarationTable(3603, "pg_ts_config_map"))
		text(" ")
		identifier("m")
		text("\n           WHERE ((")
		column("m", "mapcfg")
		text(" = ")
		configOID()
		text(") AND (")
		column("m", "maptokentype")
		text(" = ")
		column("parse", "tokid")
		text("))")
	}
	parser := func() {
		text("( SELECT ")
		parts = append(parts, proc.DeclarationAttribute(3602, "pg_ts_config", 5, "cfgparser"))
		text("\n            FROM ")
		parts = append(parts, proc.DeclarationTable(3602, "pg_ts_config"))
		text("\n           WHERE (")
		parts = append(parts, proc.DeclarationAttribute(3602, "pg_ts_config", 1, "oid"))
		text(" = ")
		configOID()
		text("))")
	}
	text("SELECT ")
	column("tt", "alias")
	text(",\n     ")
	column("tt", "description")
	text(",\n     ")
	column("parse", "token")
	text(",\n     ARRAY( SELECT ")
	dictionary()
	text(" AS ")
	identifier("mapdict")
	mapping()
	text("\n           ORDER BY ")
	column("m", "mapseqno")
	text(") AS ")
	identifier("dictionaries")
	text(",\n     ( SELECT ")
	dictionary()
	text(" AS ")
	identifier("mapdict")
	mapping()
	text("\n           ORDER BY (")
	lexize()
	text(" IS NULL), ")
	column("m", "mapseqno")
	text("\n          LIMIT 1) AS ")
	identifier("dictionary")
	text(",\n     ( SELECT ")
	lexize()
	text(" AS ")
	identifier("ts_lexize")
	mapping()
	text("\n           ORDER BY (")
	lexize()
	text(" IS NULL), ")
	column("m", "mapseqno")
	text("\n          LIMIT 1) AS ")
	identifier("lexemes")
	text("\n    FROM ")
	function("ts_parse", "26,25")
	text("(")
	parser()
	text(", ")
	selfArgument(1)
	text(") ")
	identifier("parse")
	text("(")
	identifier("tokid")
	text(", ")
	identifier("token")
	text("),\n     ")
	function("ts_token_type", "26")
	text("(")
	parser()
	text(") ")
	identifier("tt")
	text("(")
	identifier("tokid")
	text(", ")
	identifier("alias")
	text(", ")
	identifier("description")
	text(")\n   WHERE (")
	column("tt", "tokid")
	text(" = ")
	column("parse", "tokid")
	text(")")
	recipe.Parts = parts
	return recipe
}
