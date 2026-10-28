package catalog

import "fmt"

// ValidateRelationTableMethod checks exact identity against the same immutable
// catalog as the relation. No missing authority is interpreted as built-in heap.
func ValidateRelationTableMethod(read Read, r *Relation) error {
	switch r.RelationKind {
	case TableRelation, MaterializedViewRelation:
		if r.TableMethod == 0 { return fmt.Errorf("stuntgres: stored relation requires a table access method") }
	case PartitionedRelation:
		if r.TableMethod == 0 { return nil }
	default:
		if r.TableMethod != 0 { return fmt.Errorf("stuntgres: relation kind cannot have a table access method") }
		return nil
	}
	method,ok:=read.AccessMethod(r.TableMethod)
	if !ok || method.Native != HeapAccessMethod { return fmt.Errorf("stuntgres: invalid relation table access method") }
	return (AccessMethodProvider{}).Validate(read,method)
}
