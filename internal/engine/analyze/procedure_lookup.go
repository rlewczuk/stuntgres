package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ProcedureInNamespaces returns the first procedure with name and signature in
// path order, or nil.
func ProcedureInNamespaces(catalog catalog.Read, path NamespaceSelection, name string, signature catalog.ProcedureSignature) *catalog.Procedure {
	for i := range path.Count() {
		address := catalog.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: path.At(i), Name: name, Signature: string(signature)})
		if address != (ids.ObjectAddress{}) {
			procedure, _ := catalog.Procedure(address)
			return procedure
		}
	}
	return nil
}
