package parser

import "testing"

func TestTemplateGrammar(t *testing.T) {
	statements,err:=Parse(`CREATE TEXT SEARCH TEMPLATE public."Quoted" (LEXIZE=pg_catalog.dsimple_lexize,INIT=dsimple_init,INIT='dsimple_init'); ALTER TEXT SEARCH TEMPLATE public."Quoted" RENAME TO moved; ALTER TEXT SEARCH TEMPLATE public.moved SET SCHEMA target; DROP TEXT SEARCH TEMPLATE IF EXISTS target.moved,public.other CASCADE`)
	if err!=nil || len(statements)!=4 { t.Fatal(statements,err) }
	create:=statements[0].(*CreateTemplate)
	if create.Name.Parts[1].Text!="Quoted" || len(create.Options)!=3 || create.Options[0].Source==nil || len(create.Options[0].Source.Parts)!=2 || create.Options[2].Source!=nil || create.Options[0].Name.Pos.Character==0 { t.Fatal("positioned ordered template options",create) }
	rename:=statements[1].(*AlterTemplate)
	move:=statements[2].(*AlterTemplate)
	if rename.Action!="rename" || rename.Target.Text!="moved" || move.Action!="schema" || move.Target.Text!="target" { t.Fatal(rename,move) }
	drop:=statements[3].(*DropTemplate)
	if !drop.IfExists || drop.Mode!=DropCascade || len(drop.Names)!=2 { t.Fatal(drop) }
	for _,q:=range []string{`CREATE OR REPLACE TEXT SEARCH TEMPLATE x (LEXIZE=f)`,`CREATE TEXT SEARCH TEMPLATE IF NOT EXISTS x (LEXIZE=f)`,`CREATE TEXT SEARCH TEMPLATE x (LEXIZE=f(internal))`,`ALTER TEXT SEARCH TEMPLATE x OWNER TO postgres`,`ALTER TEXT SEARCH TEMPLATE x (LEXIZE=f)`,`CREATE TEXT SEARCH TEMPLATE x (LEXIZE=f) trailing`} {
		if statements,err:=Parse(q);err==nil { t.Fatal("unsupported template grammar accepted",q,statements) }
	}
}
