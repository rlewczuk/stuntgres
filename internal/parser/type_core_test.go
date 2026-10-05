package parser

import "testing"

func TestTypeCoreRawTypeNamesAndPositions(t *testing.T) {
 statements,err:=Parse("CREATE TABLE t(a INT4,b INT8,c TEXT,d BOOL)")
 if err!=nil{t.Fatal(err)}
 create:=statements[0].(*CreateTable)
 for i,tc:=range []struct{name string;position int}{{"integer",18},{"bigint",25},{"text",32},{"boolean",39}}{
  raw:=create.Columns[i].TypeName
  if raw.Name!=tc.name||raw.Pos.Character!=tc.position||raw.Pos.Offset!=tc.position-1{t.Fatal(raw,tc)}
 }
 statements,err=Parse("SELECT a FROM t WHERE a=TRUE")
 if err!=nil{t.Fatal(err)}
 if pos:=statements[0].(*Select).Where.Pos.Character;pos!=24{t.Fatal(pos)}
}
