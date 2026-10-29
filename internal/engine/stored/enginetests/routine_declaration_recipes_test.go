package enginetests_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func declarationRecipePolicy() stored.Presentation {
	return stored.Presentation{Deparse: true, Path: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}}
}

// Goldens are independently transcribed from pinned PostgreSQL18.6.
// Debug display includes observed analyzed casts, formal qualification and
// expanded SELECT stars; the retained structural syntax remains released SQL.
func TestRoutineDeclarationBuiltinInventory(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	expected := map[ids.OID]string{
		879:  "RETURN lpad($1, $2, ' '::text)\n",
		880:  "RETURN rpad($1, $2, ' '::text)\n",
		1215: "BEGIN ATOMIC\n SELECT pg_description.description\n    FROM pg_description\n   WHERE ((pg_description.objoid = $1) AND (pg_description.classoid = ( SELECT pg_class.oid\n            FROM pg_class\n           WHERE ((pg_class.relname = $2) AND (pg_class.relnamespace = ('pg_catalog'::regnamespace)::oid)))) AND (pg_description.objsubid = 0));\nEND\n",
		1216: "BEGIN ATOMIC\n SELECT pg_description.description\n    FROM pg_description\n   WHERE ((pg_description.objoid = $1) AND (pg_description.classoid = ('pg_class'::regclass)::oid) AND (pg_description.objsubid = $2));\nEND\n",
		1296: "RETURN ($2 + $1)\n", 1298: "RETURN ($2 + $1)\n",
		1305: "RETURN (($1, ($1 + $2)) OVERLAPS ($3, ($3 + $4)))\n",
		1306: "RETURN (($1, $2) OVERLAPS ($3, ($3 + $4)))\n",
		1307: "RETURN (($1, ($1 + $2)) OVERLAPS ($3, $4))\n",
		1309: "RETURN (($1, ($1 + $2)) OVERLAPS ($3, ($3 + $4)))\n",
		1310: "RETURN (($1, $2) OVERLAPS ($3, ($3 + $4)))\n",
		1311: "RETURN (($1, ($1 + $2)) OVERLAPS ($3, $4))\n",
		1348: "BEGIN ATOMIC\n SELECT pg_description.description\n    FROM pg_description\n   WHERE ((pg_description.objoid = $1) AND (pg_description.objsubid = 0));\nEND\n",
		1384: "RETURN date_part($1, ($2)::timestamp without time zone)\n",
		1426: "RETURN on_ppath($2, $1)\n",
		1481: "RETURN log((10)::numeric, $1)\n",
		1544: "RETURN polygon(12, $1)\n",
		1708: "RETURN round($1, 0)\n", 1710: "RETURN trunc($1, 0)\n", 1741: "RETURN log((10)::numeric, $1)\n",
		1810: "RETURN (octet_length($1) * 8)\n", 1811: "RETURN (octet_length($1) * 8)\n", 1812: "RETURN length($1)\n", 1848: "RETURN ($2 + $1)\n",
		1993: "BEGIN ATOMIC\n SELECT pg_shdescription.description\n    FROM pg_shdescription\n   WHERE ((pg_shdescription.objoid = $1) AND (pg_shdescription.classoid = ( SELECT pg_class.oid\n            FROM pg_class\n           WHERE ((pg_class.relname = $2) AND (pg_class.relnamespace = ('pg_catalog'::regnamespace)::oid)))));\nEND\n",
		2042: "RETURN (($1, ($1 + $2)) OVERLAPS ($3, ($3 + $4)))\n",
		2043: "RETURN (($1, $2) OVERLAPS ($3, ($3 + $4)))\n",
		2044: "RETURN (($1, ($1 + $2)) OVERLAPS ($3, $4))\n",
		2059: "RETURN age((CURRENT_DATE)::timestamp without time zone, $1)\n",
		2074: "RETURN \"substring\"($1, similar_to_escape($2, $3))\n",
		2325: "RETURN pg_relation_size($1, 'main'::text)\n",
		2546: "RETURN ($2 + $1)\n", 2547: "RETURN ($2 + $1)\n", 2548: "RETURN ($2 + $1)\n", 2549: "RETURN ($2 + $1)\n", 2550: "RETURN ($2 + $1)\n", 2631: "RETURN ($2 + $1)\n",
		3935: "RETURN pg_sleep(((EXTRACT(epoch FROM (clock_timestamp() + $1)) - EXTRACT(epoch FROM clock_timestamp())))::double precision)\n",
		3936: "RETURN pg_sleep(((EXTRACT(epoch FROM $1) - EXTRACT(epoch FROM clock_timestamp())))::double precision)\n",
		5023: "RETURN ($2 + $1)\n",
		6183: `BEGIN ATOMIC
 SELECT tt.alias,
     tt.description,
     parse.token,
     ARRAY( SELECT (m.mapdict)::regdictionary AS mapdict
            FROM pg_ts_config_map m
           WHERE ((m.mapcfg = (ts_debug.config)::oid) AND (m.maptokentype = parse.tokid))
           ORDER BY m.mapseqno) AS dictionaries,
     ( SELECT (m.mapdict)::regdictionary AS mapdict
            FROM pg_ts_config_map m
           WHERE ((m.mapcfg = (ts_debug.config)::oid) AND (m.maptokentype = parse.tokid))
           ORDER BY (ts_lexize((m.mapdict)::regdictionary, parse.token) IS NULL), m.mapseqno
          LIMIT 1) AS dictionary,
     ( SELECT ts_lexize((m.mapdict)::regdictionary, parse.token) AS ts_lexize
            FROM pg_ts_config_map m
           WHERE ((m.mapcfg = (ts_debug.config)::oid) AND (m.maptokentype = parse.tokid))
           ORDER BY (ts_lexize((m.mapdict)::regdictionary, parse.token) IS NULL), m.mapseqno
          LIMIT 1) AS lexemes
    FROM ts_parse(( SELECT pg_ts_config.cfgparser
            FROM pg_ts_config
           WHERE (pg_ts_config.oid = (ts_debug.config)::oid)), ts_debug.document) parse(tokid, token),
     ts_token_type(( SELECT pg_ts_config.cfgparser
            FROM pg_ts_config
           WHERE (pg_ts_config.oid = (ts_debug.config)::oid))) tt(tokid, alias, description)
   WHERE (tt.tokid = parse.tokid);
END
`,
		6184: `BEGIN ATOMIC
 SELECT ts_debug.alias,
     ts_debug.description,
     ts_debug.token,
     ts_debug.dictionaries,
     ts_debug.dictionary,
     ts_debug.lexemes
    FROM ts_debug(get_current_ts_config(), ts_debug.document) ts_debug(alias, description, token, dictionaries, dictionary, lexemes);
END
`,
	}
	seen := 0
	for _, descriptor := range read.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.Builtin == nil || d.DeclarationBody == nil {
			continue
		}
		seen++
		want, ok := expected[d.Address().ObjectID]
		if !ok {
			t.Fatalf("missing independent golden for represented recipe %d", d.Address().ObjectID)
		}
		t.Run(fmt.Sprint(d.Address().ObjectID), func(t *testing.T) {
			got, err := stored.RoutineDeclarationSQL(t.Context(), read, d, declarationRecipePolicy())
			if err != nil || got != want {
				t.Fatalf("body=%q, %v; want %q", got, err, want)
			}
			tree, err := d.DeclarationBody.StructuralOutput(t.Context())
			if err != nil || !strings.HasPrefix(tree, "{RoutineDeclarationBody {parser.Select ") {
				t.Fatalf("lost parsed structural owner: %q, %v", tree, err)
			}
			if d.Address().ObjectID == 6183 && !strings.Contains(tree, "parser.DeclarationArraySubqueryExpr") {
				t.Fatal("debug display replaced genuine parsed array query")
			}
			if d.Address().ObjectID == 6184 && !strings.Contains(tree, "parser.StarExpr") {
				t.Fatal("analyzed display expansion replaced genuine parsed SELECT star")
			}
		})
	}
	if seen != len(expected) {
		t.Fatalf("represented recipe count=%d; want %d", seen, len(expected))
	}
}

func TestRoutineDeclarationInstalledInventory(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{"CREATE EXTENSION citext", "CREATE EXTENSION cube", "CREATE EXTENSION earthdistance"} {
		enginetest.SessionSQL(t, session, sql)
	}
	read := e.CatalogRead()
	citext := []string{
		"RETURN regexp_match((string)::text, (pattern)::text, 'i'::text)\n",
		"RETURN regexp_match((string)::text, (pattern)::text, CASE WHEN (strpos(flags, 'c'::text) = 0) THEN (flags || 'i'::text) ELSE flags END)\n",
		"RETURN regexp_matches((string)::text, (pattern)::text, 'i'::text)\n",
		"RETURN regexp_matches((string)::text, (pattern)::text, CASE WHEN (strpos(flags, 'c'::text) = 0) THEN (flags || 'i'::text) ELSE flags END)\n",
		"RETURN regexp_replace((string)::text, (pattern)::text, replacement, 'i'::text)\n",
		"RETURN regexp_replace((string)::text, (pattern)::text, replacement, CASE WHEN (strpos(flags, 'c'::text) = 0) THEN (flags || 'i'::text) ELSE flags END)\n",
		"RETURN regexp_split_to_array((string)::text, (pattern)::text, 'i'::text)\n",
		"RETURN regexp_split_to_array((string)::text, (pattern)::text, CASE WHEN (strpos(flags, 'c'::text) = 0) THEN (flags || 'i'::text) ELSE flags END)\n",
		"RETURN regexp_split_to_table((string)::text, (pattern)::text, 'i'::text)\n",
		"RETURN regexp_split_to_table((string)::text, (pattern)::text, CASE WHEN (strpos(flags, 'c'::text) = 0) THEN (flags || 'i'::text) ELSE flags END)\n",
		"RETURN strpos(lower(($1)::text), lower(($2)::text))\n",
		"RETURN regexp_replace(($1)::text, regexp_replace(($2)::text, '([^a-zA-Z_0-9])'::text, '\\\\\\1'::text, 'g'::text), ($3)::text, 'gi'::text)\n",
		"RETURN (regexp_split_to_array(($1)::text, regexp_replace(($2)::text, '([^a-zA-Z_0-9])'::text, '\\\\\\1'::text, 'g'::text), 'i'::text))[$3]\n",
		"RETURN translate(translate(($1)::text, lower(($2)::text), $3), upper(($2)::text), $3)\n",
	}
	earth := []string{
		"RETURN '6378168'::double precision\n",
		"RETURN CASE WHEN ($1 < '0'::double precision) THEN '0'::double precision WHEN (($1 / ('2'::double precision * earth())) > '1'::double precision) THEN (pi() * earth()) ELSE (('2'::double precision * earth()) * asin(($1 / ('2'::double precision * earth())))) END\n",
		"RETURN CASE WHEN ($1 < '0'::double precision) THEN '0'::double precision WHEN (($1 / earth()) > pi()) THEN ('2'::double precision * earth()) ELSE (('2'::double precision * earth()) * sin(($1 / ('2'::double precision * earth())))) END\n",
		"RETURN (cube(cube(cube(((earth() * cos(radians($1))) * cos(radians($2)))), ((earth() * cos(radians($1))) * sin(radians($2)))), (earth() * sin(radians($1)))))::earth\n",
		"RETURN CASE WHEN ((cube_ll_coord(($1)::cube, 3) / earth()) < '-1'::double precision) THEN '-90'::double precision WHEN ((cube_ll_coord(($1)::cube, 3) / earth()) > '1'::double precision) THEN '90'::double precision ELSE degrees(asin((cube_ll_coord(($1)::cube, 3) / earth()))) END\n",
		"RETURN degrees(atan2(cube_ll_coord(($1)::cube, 2), cube_ll_coord(($1)::cube, 1)))\n",
	}
	for _, family := range []struct {
		name     string
		start    int
		expected []string
	}{{"citext", 42, citext}, {"earthdistance", 2, earth}} {
		ext, ok := read.Extension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: family.name}))
		if !ok {
			t.Fatal("missing installation", family.name)
		}
		for index, want := range family.expected {
			d, ok := read.Procedure(ext.Members[family.start+index])
			if !ok {
				t.Fatal("missing installed routine", index)
			}
			t.Run(fmt.Sprintf("%s/%d", family.name, index), func(t *testing.T) {
				got, err := stored.RoutineDeclarationSQL(t.Context(), read, d, declarationRecipePolicy())
				if err != nil || got != want {
					t.Fatalf("body=%q, %v; want %q", got, err, want)
				}
			})
		}
	}
}

func TestRoutineDeclarationQuotedMetadataSlots(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE EXTENSION citext")
	read := e.CatalogRead()
	policy := declarationRecipePolicy()
	policy.Output.QuoteAllIdentifiers = true
	for oid, want := range map[ids.OID]string{
		1216: "BEGIN ATOMIC\n SELECT \"pg_description\".\"description\"\n    FROM \"pg_description\"\n   WHERE ((\"pg_description\".\"objoid\" = $1) AND (\"pg_description\".\"classoid\" = ('\"pg_class\"'::\"regclass\")::\"oid\") AND (\"pg_description\".\"objsubid\" = $2));\nEND\n",
		3936: "RETURN \"pg_sleep\"(((EXTRACT(\"epoch\" FROM $1) - EXTRACT(\"epoch\" FROM \"clock_timestamp\"())))::double precision)\n",
	} {
		d, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		if !ok {
			t.Fatal("missing builtin", oid)
		}
		got, err := stored.RoutineDeclarationSQL(t.Context(), read, d, policy)
		if err != nil || got != want {
			t.Fatalf("quoted %d=%q, %v; want %q", oid, got, err, want)
		}
	}
	ext, ok := read.Extension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "citext"}))
	if !ok {
		t.Fatal("missing citext")
	}
	d, ok := read.Procedure(ext.Members[42])
	if !ok {
		t.Fatal("missing regexp_match")
	}
	got, err := stored.RoutineDeclarationSQL(t.Context(), read, d, policy)
	want := "RETURN \"regexp_match\"((\"string\")::\"text\", (\"pattern\")::\"text\", 'i'::\"text\")\n"
	if err != nil || got != want {
		t.Fatalf("quoted formal=%q, %v; want %q", got, err, want)
	}
}
