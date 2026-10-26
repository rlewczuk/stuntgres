package parser

import "testing"

func TestDictionaryGrammar(t *testing.T) {
	statements, err := Parse(`CREATE TEXT SEARCH DICTIONARY s."Quoted" (TEMPLATE=pg_catalog.simple,StopWords='english',accept); ALTER TEXT SEARCH DICTIONARY s."Quoted" (accept=false,accept,stopwords=''); ALTER TEXT SEARCH DICTIONARY s."Quoted" RENAME TO next; ALTER TEXT SEARCH DICTIONARY s.next OWNER TO CURRENT_ROLE; ALTER TEXT SEARCH DICTIONARY s.next SET SCHEMA target; DROP TEXT SEARCH DICTIONARY IF EXISTS target.next,public.other CASCADE`)
	if err != nil || len(statements) != 6 {
		t.Fatal(statements, err)
	}
	create := statements[0].(*CreateDictionary)
	if create.Name.Parts[1].Text != "Quoted" || create.Options[0].Source == nil || len(create.Options[0].Source.Parts) != 2 || create.Options[1].Value != "english" || create.Options[2].Supplied {
		t.Fatal("positioned dictionary options", create)
	}
	alter := statements[1].(*AlterDictionary)
	if alter.Action != "options" || len(alter.Options) != 3 || alter.Options[1].Supplied || !alter.Options[2].Supplied || alter.Options[2].Value != "" {
		t.Fatal("bare/empty option distinction", alter)
	}
	owner := statements[3].(*AlterDictionary)
	if owner.Action != "owner" || !owner.OwnerIdentity {
		t.Fatal(owner)
	}
	drop := statements[5].(*DropDictionary)
	if !drop.IfExists || len(drop.Names) != 2 || drop.Mode != DropCascade {
		t.Fatal(drop)
	}
	statements, err = Parse(`CREATE TEXT SEARCH DICTIONARY d (TEMPLATE='pg_catalog.simple',"Accept"=1)`)
	if err != nil {
		t.Fatal(err)
	}
	quoted := statements[0].(*CreateDictionary)
	if quoted.Options[0].Source != nil || quoted.Options[1].Name.Text != "Accept" || !quoted.Options[1].Numeric {
		t.Fatal("decoded spelling or source category lost", quoted)
	}
	for _, q := range []string{`CREATE TEXT SEARCH DICTIONARY d (TEMPLATE=simple) suffix`, `CREATE TEXT SEARCH DICTIONARY d (TEMPLATE=$1)`, `ALTER TEXT SEARCH DICTIONARY d SET TEMPLATE simple`, `DROP TEXT SEARCH DICTIONARY d CASCADE suffix`} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("invalid whole dictionary grammar admitted", q, statements, err)
		}
	}
}
