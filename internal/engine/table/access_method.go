package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// AccessMethodPreparation is original-state intent, retained across root reentry.
// A no-op does not arm PostgreSQL's repeated-change guard.
type AccessMethodPreparation struct {
	Method ids.OID
	Changed bool
}

func ResolveTableMethod(read catalog.Read, name string) (ids.OID, error) {
	method, ok := read.LookupAccessMethod(name)
	if !ok { return 0, sqlError("42704", fmt.Sprintf(`access method "%s" does not exist`,name)) }
	if method.MethodKind() != "t" { return 0, sqlError("55000",fmt.Sprintf(`access method "%s" is not of type TABLE`,name)) }
	if err := (catalog.AccessMethodProvider{}).Validate(read,method); err != nil { return 0,err }
	return method.Address().ObjectID,nil
}

func PrepareAccessMethod(read catalog.Read, original *catalog.Relation, action parser.AlterTableAction, defaultName string, prior AccessMethodPreparation) (AccessMethodPreparation,error) {
	if prior.Changed { return prior,sqlError("0A000","cannot have multiple SET ACCESS METHOD subcommands") }
	var method ids.OID
	if !action.MethodDefault || original.RelationKind != catalog.PartitionedRelation {
		name:=action.Method.Text
		if action.MethodDefault {name=defaultName}
		var err error
		method,err=ResolveTableMethod(read,name)
		if err!=nil {return prior,err}
	}
	return AccessMethodPreparation{Method:method,Changed:method!=original.TableMethod},nil
}

// MetadataOnlyActions need no row capture or rewrite. Mixed physical work still
// follows the existing ALTER pipeline and publishes these properties at its end.
func MetadataOnlyActions(actions []parser.AlterTableAction) bool {
	for _,action:=range actions {
		switch action.Action {
		case parser.SetLogged, parser.SetUnlogged, parser.SetAccessMethod, parser.SetRelationOptions, parser.ResetRelationOptions:
		default:
			return false
		}
	}
	return true
}
