package stored

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgvisibility(n plan.InstalledPgvisibilityExpr, v catalog.Read) error {
	if !ReferenceEpochValid(v,nil,n.Ref){return fmt.Errorf("stuntgres: invalid installed pg_visibility reference")}
	proc,_,err:=currentInstalledProcedure(v,n.Ref,n.Procedure)
	if err!=nil{return err}
	if n.CallRoutineReference()!=n.Procedure||extension.PgvisibilityProviderOf(proc)!=extension.StockPgvisibility {return fmt.Errorf("stuntgres: invalid installed pg_visibility carrier")}
	boundary:=n.Pgvisibility;boundary.RoutineReference=proc
	return extension.ValidatePgvisibilityCall(v,proc,boundary)
}
func pgvisibilityCallSQL(n plan.InstalledPgvisibilityExpr,proc *catalog.Procedure,name string,quote func(string)string,render func(expr.Expr)(string,error))(string,error){
	var out strings.Builder
	out.WriteString(name);out.WriteByte('(')
	for i,arg:=range n.Args {
		if i!=0 {out.WriteString(", ")}
		label:=proc.OutputDeclaration().Name(int(n.Slots[i]))
		// The released first formal is unnamed. It cannot acquire a synthetic
		// relation=> spelling merely because the second formal has a name.
		if label!="" {out.WriteString(quote(label));out.WriteString(" => ")}
		text,err:=render(arg);if err!=nil{return "",err};out.WriteString(text)
	}
	out.WriteByte(')');return out.String(),nil
}
