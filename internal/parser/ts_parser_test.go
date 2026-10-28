package parser

import "testing"

func TestTSParserGrammar(t *testing.T) {
	statements, err := Parse(`CREATE TEXT SEARCH PARSER public."Quoted" (START=pg_catalog.prsd_start,START=missing,END=123,LEXTYPES); ALTER TEXT SEARCH PARSER public."Quoted" RENAME TO next; ALTER TEXT SEARCH PARSER next SET SCHEMA public; DROP TEXT SEARCH PARSER IF EXISTS public.next,missing CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	create := statements[0].(*CreateParser)
	if create.Name.Parts[1].Text != "Quoted" || len(create.Options) != 4 || create.Options[0].Value.Parts[0].Text != "pg_catalog" || create.Options[1].Value.Parts[0].Text != "missing" || !create.Options[2].Numeric || create.Options[3].Supplied || create.Options[0].Name.Pos.Character >= create.Options[1].Name.Pos.Character {
		t.Fatal("ordered located utility options", create)
	}
	rename := statements[1].(*AlterParser)
	move := statements[2].(*AlterParser)
	if rename.Action != "rename" || rename.Target.Text != "next" || move.Action != "schema" || move.Target.Text != "public" {
		t.Fatal(rename, move)
	}
	drop := statements[3].(*DropParser)
	if !drop.IfExists || len(drop.Names) != 2 || drop.Mode != DropCascade {
		t.Fatal(drop)
	}
	for _, query := range []string{
		"CREATE TEXT SEARCH PARSER p ()",
		"CREATE TEXT SEARCH PARSER p (START=pg_catalog.prsd_start) trailing",
		"CREATE OR REPLACE TEXT SEARCH PARSER p (START=pg_catalog.prsd_start)",
		"CREATE TEXT SEARCH PARSER IF NOT EXISTS p (START=pg_catalog.prsd_start)",
		"ALTER TEXT SEARCH PARSER p OWNER TO postgres",
		"ALTER TEXT SEARCH PARSER p (START=pg_catalog.prsd_start)",
		"DROP TEXT SEARCH PARSER p trailing",
	} {
		if _, err := Parse(query); err == nil {
			t.Fatal("invalid complete input accepted", query)
		}
	}
}
