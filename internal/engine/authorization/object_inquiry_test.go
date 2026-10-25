package authorization

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestAccessExecutionPrivilegeParsing(t *testing.T) {
	for _,tc:=range []struct{text,privilege string; ordinary,options bool}{
		{"execute","EXECUTE",true,false},{" USage \t","USAGE",true,false},{"EXECUTE WITH GRANT OPTION","EXECUTE",false,true},{" execute, EXECUTE WITH GRANT OPTION ","EXECUTE",true,true},{"USAGE,usage","USAGE",true,false},
	} {ordinary,options,err:=ParseObjectPrivileges(tc.text,tc.privilege);if err!=nil || ordinary!=tc.ordinary || options!=tc.options {t.Fatal(tc,ordinary,options,err)}}
	for _,tc:=range []struct{text,offending string}{
		{"",""},{",",""},{"EXECUTE,",""},{",EXECUTE",""},{"EXECUTE,bad","bad"},{"ALL","ALL"},{"USAGE","USAGE"},{"EXECUTE  WITH GRANT OPTION","EXECUTE  WITH GRANT OPTION"},{"EXECUTE\tWITH GRANT OPTION","EXECUTE\tWITH GRANT OPTION"},{"EXECUTE WITH GRANT OPTION suffix","EXECUTE WITH GRANT OPTION suffix"},
	} {_,_,err:=ParseObjectPrivileges(tc.text,"EXECUTE");var diagnostic *sqlstate.Error;if !errors.As(err,&diagnostic) || diagnostic.Code!="22023" || diagnostic.Message!=`unrecognized privilege type: "`+tc.offending+`"` {t.Fatal(tc,err)}}
}
