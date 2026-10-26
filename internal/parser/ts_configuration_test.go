package parser

import "testing"

func TestTSConfigurationGrammar(t *testing.T) {
	statements, err := Parse("CREATE TEXT SEARCH CONFIGURATION public.\"Quoted\" (COPY=pg_catalog.simple,COPY=public.other); ALTER TEXT SEARCH CONFIGURATION public.\"Quoted\" ADD MAPPING FOR asciiword,word WITH pg_catalog.simple,public.dict; ALTER TEXT SEARCH CONFIGURATION public.\"Quoted\" ALTER MAPPING FOR word REPLACE public.dict WITH simple; ALTER TEXT SEARCH CONFIGURATION public.\"Quoted\" ALTER MAPPING REPLACE public.dict WITH simple; ALTER TEXT SEARCH CONFIGURATION public.\"Quoted\" DROP MAPPING IF EXISTS FOR blank; DROP TEXT SEARCH CONFIGURATION IF EXISTS public.\"Quoted\",public.other CASCADE")
	if err != nil || len(statements) != 6 {
		t.Fatal(statements, err)
	}
	create := statements[0].(*CreateConfiguration)
	if create.Name.Parts[1].Text != "Quoted" || len(create.Options) != 2 || create.Options[0].Value.Parts[0].Text != "pg_catalog" || create.Options[1].Value.Parts[0].Text != "public" || create.Options[0].Name.Pos.Character <= 0 {
		t.Fatal("ordered positioned options", create)
	}
	add := statements[1].(*AlterConfiguration)
	if add.MappingAction != ConfigurationAddMapping || len(add.Tokens) != 2 || len(add.Dictionaries) != 2 || add.Dictionaries[1].Parts[1].Text != "dict" {
		t.Fatal(add)
	}
	replace := statements[2].(*AlterConfiguration)
	if replace.MappingAction != ConfigurationReplaceMapping || len(replace.Tokens) != 1 || replace.OldDictionary.Parts[1].Text != "dict" || replace.Dictionaries[0].Parts[0].Text != "simple" {
		t.Fatal(replace)
	}
	if global := statements[3].(*AlterConfiguration); len(global.Tokens) != 0 || global.MappingAction != ConfigurationReplaceMapping {
		t.Fatal(global)
	}
	if drop := statements[4].(*AlterConfiguration); !drop.IfExists || drop.MappingAction != ConfigurationDropMapping || drop.Tokens[0].Text != "blank" {
		t.Fatal(drop)
	}
	if drop := statements[5].(*DropConfiguration); !drop.IfExists || drop.Mode != DropCascade || len(drop.Names) != 2 {
		t.Fatal(drop)
	}
	for _, q := range []string{"CREATE TEXT SEARCH CONFIGURATION public.bad", "CREATE TEXT SEARCH CONFIGURATION public.bad ()", "CREATE TEXT SEARCH CONFIGURATION public.bad (COPY=simple) garbage", "ALTER TEXT SEARCH CONFIGURATION public.bad ADD MAPPING FOR asciiword WITH", "ALTER TEXT SEARCH CONFIGURATION public.bad DROP MAPPING FOR", "DROP TEXT SEARCH CONFIGURATION public.bad trailing"} {
		if got, err := Parse(q); err == nil || got != nil {
			t.Fatal("partial configuration grammar admitted", q, got, err)
		}
	}
}
