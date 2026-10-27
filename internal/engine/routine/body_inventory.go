package routine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// Exact represented SQL-standard replacements from REL_18_6
// src/backend/catalog/system_functions.sql. As in parser.CreateFunction.Standard,
// RETURN expressions use the parser's single-target SELECT representation. These
// parsed recipes are metadata only: they are never checked executable programs.
var builtinRoutineDeclarationBodies = func() map[ids.OID]sqltype.StructuralTree {
	recipes := map[ids.OID]string{
		2325: `SELECT pg_relation_size($1, 'main')`,
		5023: `SELECT $2 + $1`,
		879:  `SELECT lpad($1, $2, ' ')`,
		880:  `SELECT rpad($1, $2, ' ')`,
		1215: `SELECT description FROM pg_description WHERE objoid = $1 AND classoid = (SELECT oid FROM pg_class WHERE relname = $2 AND relnamespace = 'pg_catalog'::regnamespace) AND objsubid = 0`,
		1216: `SELECT description FROM pg_description WHERE objoid = $1 AND classoid = 'pg_class'::regclass AND objsubid = $2`,
		1296: `SELECT $2 + $1`,
		1298: `SELECT $2 + $1`,
		1305: `SELECT ($1, ($1 + $2)) OVERLAPS ($3, ($3 + $4))`,
		1306: `SELECT ($1, $2) OVERLAPS ($3, ($3 + $4))`,
		1307: `SELECT ($1, ($1 + $2)) OVERLAPS ($3, $4)`,
		1309: `SELECT ($1, ($1 + $2)) OVERLAPS ($3, ($3 + $4))`,
		1310: `SELECT ($1, $2) OVERLAPS ($3, ($3 + $4))`,
		1311: `SELECT ($1, ($1 + $2)) OVERLAPS ($3, $4)`,
		1348: `SELECT description FROM pg_description WHERE objoid = $1 AND objsubid = 0`,
		1384: `SELECT date_part($1, CAST($2 AS timestamp))`,
		1426: `SELECT on_ppath($2, $1)`,
		1481: `SELECT log(10, $1)`,
		1544: `SELECT polygon(12, $1)`,
		1708: `SELECT round($1, 0)`,
		1710: `SELECT trunc($1, 0)`,
		1741: `SELECT log(10, $1)`,
		1810: `SELECT octet_length($1) * 8`,
		1811: `SELECT octet_length($1) * 8`,
		1812: `SELECT length($1)`,
		1848: `SELECT $2 + $1`,
		1993: `SELECT description FROM pg_shdescription WHERE objoid = $1 AND classoid = (SELECT oid FROM pg_class WHERE relname = $2 AND relnamespace = 'pg_catalog'::regnamespace)`,
		2042: `SELECT ($1, ($1 + $2)) OVERLAPS ($3, ($3 + $4))`,
		2043: `SELECT ($1, $2) OVERLAPS ($3, ($3 + $4))`,
		2044: `SELECT ($1, ($1 + $2)) OVERLAPS ($3, $4)`,
		2059: `SELECT age(CAST(current_date AS timestamp), $1)`,
		2074: `SELECT substring($1, similar_to_escape($2, $3))`,
		2546: `SELECT $2 + $1`,
		2547: `SELECT $2 + $1`,
		2548: `SELECT $2 + $1`,
		2549: `SELECT $2 + $1`,
		2550: `SELECT $2 + $1`,
		2631: `SELECT $2 + $1`,
		3935: `SELECT pg_sleep(extract(epoch FROM clock_timestamp() + $1) - extract(epoch FROM clock_timestamp()))`,
		3936: `SELECT pg_sleep(extract(epoch FROM $1) - extract(epoch FROM clock_timestamp()))`,
	}
	bodies := make(map[ids.OID]sqltype.StructuralTree, len(recipes))
	for oid, source := range recipes {
		display, ok := builtinRoutineDisplay[oid]
		if !ok || display.Language != catalog.SQLRoutineLanguage || display.Source != "" {
			panic(fmt.Sprintf("stuntgres: unexpected released routine body %d", oid))
		}
		body, err := proc.NewRoutineDeclarationBody(source)
		if err != nil {
			panic(fmt.Sprintf("stuntgres: invalid released routine body %d: %v", oid, err))
		}
		bodies[oid] = body
	}
	for oid, display := range builtinRoutineDisplay {
		if (display.Language == catalog.SQLRoutineLanguage && display.Source == "") != (bodies[oid] != nil) {
			panic(fmt.Sprintf("stuntgres: incomplete released routine body %d", oid))
		}
	}
	return bodies
}()
