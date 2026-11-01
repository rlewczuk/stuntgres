package parser

import "testing"

func TestSnapshotImportGrammar(t *testing.T) {
	for _,literal:=range []string{"ABC-12","","a'b"}{
		quoted:="'";for _,r:=range literal{quoted+=string(r);if r=='\''{quoted+="'"}};quoted+="'"
		statements,err:=Parse("SET TRANSACTION SNAPSHOT "+quoted)
		if err!=nil||len(statements)!=1{t.Fatal(statements,err)}
		s,ok:=statements[0].(*TransactionCharacteristics)
		if !ok||s.Snapshot==nil||*s.Snapshot!=literal||len(s.Modes)!=0||s.Session{t.Fatalf("literal alternative: %#v",statements[0])}
	}
	for _,q:=range []string{"SET TRANSACTION SNAPSHOT NULL","SET TRANSACTION SNAPSHOT $1","SET TRANSACTION SNAPSHOT ABC","SET TRANSACTION SNAPSHOT 'ABC'::text","SET TRANSACTION SNAPSHOT 'ABC' junk","SET TRANSACTION READ ONLY SNAPSHOT 'ABC'","SET TRANSACTION SNAPSHOT 'ABC' READ ONLY"}{controlParseError(t,q,"42601","","")}
}

func TestSnapshotImportPLGrammar(t *testing.T) {
	block,err:=ParsePLpgSQL("BEGIN SET TRANSACTION SNAPSHOT 'ABC';END",PLReturnPolicy{})
	if err!=nil{t.Fatal(err)}
	instruction:=block.Statements[0]
	if instruction.Block!=nil{instruction=instruction.Block.Statements[0]}
	if instruction.SQL==nil{t.Fatal("snapshot import is not a static SQL instruction")}
	statement,ok:=instruction.SQL.Query.(*TransactionCharacteristics)
	if !ok||statement.Snapshot==nil||*statement.Snapshot!="ABC"||len(statement.Modes)!=0{t.Fatal("lost literal snapshot form")}
	for _,text:=range []string{"NULL","$1","'ABC'::text","'ABC' READ ONLY"}{
		if _,err:=ParsePLpgSQL("BEGIN SET TRANSACTION SNAPSHOT "+text+";END",PLReturnPolicy{});err==nil{t.Fatal("accepted nonliteral procedural import",text)}
	}
}
