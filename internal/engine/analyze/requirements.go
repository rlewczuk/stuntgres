package analyze

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Requirement is one semantic permission demand, not a grant or cached decision.
// WholeRelation distinguishes Check from CheckColumns. Nil and empty column
// requests both accept any live column or the relation's privilege.
// Callers borrow Columns only for the duration of the visit.
type Requirement struct {
	Ref           ids.ObjectRef
	Relation      *catalog.Relation
	Privilege     authorization.Privileges
	Columns       []int
	WholeRelation bool
	RowMark       bool
	Inherit       bool
	SystemColumns []int32
	WholeRow      bool
}

// Whole reports the actual access operation, including CheckColumns' existing
// non-SELECT sequence delegation.
func (r Requirement) Whole() bool {
	return r.WholeRelation || r.Relation != nil && r.Relation.RelationKind == catalog.SequenceRelation && r.Privilege != authorization.SelectPrivilege
}

// AccessRequirement preserves the ordinary ACL request and its independent MAC
// facts when an owner supplies the access environment directly.
func (r Requirement) AccessRequirement() access.Requirement {
	return access.Requirement{Object: r.Ref, Privileges: r.Privilege, ColumnDemand: !r.WholeRelation, Columns: r.Columns, SystemColumns: r.SystemColumns, Inherit: r.Inherit, WholeRow: r.WholeRow, RowMark: r.RowMark}
}

func (r Requirement) Check(auth Privileges) error {
	return auth.Check(r.AccessRequirement())
}

// BindingRequirements visits target requests in the checker's original order.
// Query sources and view-write delegation remain with their existing owners.
func BindingRequirements(b Binding, stmt parser.Statement, visit func(Requirement) error) error {
	if b.Relation == nil {
		return nil
	}
	inherit := false
	switch target := stmt.(type) {
	case *parser.Update:
		inherit = !target.Only
	case *parser.Delete:
		inherit = !target.Only
	case *parser.Merge:
		inherit = true
	}
	request := func(privilege authorization.Privileges, columns []int) error {
		r := Requirement{Ref: b.Ref, Relation: b.Relation, Privilege: privilege, Columns: columns, Inherit: inherit}
		if privilege == authorization.SelectPrivilege {
			if b.SystemAttributeRead {
				r.WholeRelation = true
				if err := visit(r); err != nil {
					return err
				}
				r.WholeRelation = false
			}
			r.SystemColumns, r.WholeRow = b.SystemColumns, b.WholeRowRead
		}
		return visit(r)
	}
	whole := func(privilege authorization.Privileges) error {
		return visit(Requirement{Ref: b.Ref, Relation: b.Relation, Privilege: privilege, WholeRelation: true, Inherit: inherit})
	}
	switch stmt.(type) {
	case *parser.Select, *parser.DeclareCursor, *parser.CreateQueryTable:
		return nil
	case *parser.Insert:
		if err := request(authorization.InsertPrivilege, b.Targets); err != nil {
			return err
		}
		if b.Insert.Conflict != nil && len(b.Insert.Conflict.Reads) != 0 {
			if err := request(authorization.SelectPrivilege, b.Insert.Conflict.Reads); err != nil {
				return err
			}
		}
		if b.Insert.Conflict != nil && b.Insert.Conflict.Update != nil {
			if err := request(authorization.UpdatePrivilege, b.Insert.Conflict.UpdateTargets); err != nil {
				return err
			}
			if len(b.Insert.Conflict.ActionReads) != 0 {
				if err := request(authorization.SelectPrivilege, b.Insert.Conflict.ActionReads); err != nil {
					return err
				}
			}
		}
		if b.Returning != nil && (b.Returning.ReadRequired || len(b.Returning.Reads) != 0) {
			return request(authorization.SelectPrivilege, b.Returning.Reads)
		}
	case *parser.Merge:
		if b.Merge.HasDelete {
			if err := whole(authorization.DeletePrivilege); err != nil {
				return err
			}
		}
		if b.Merge.HasInsert {
			if err := request(authorization.InsertPrivilege, b.Merge.InsertTargets); err != nil {
				return err
			}
		}
		if len(b.Merge.UpdateTargets) != 0 {
			if err := request(authorization.UpdatePrivilege, b.Merge.UpdateTargets); err != nil {
				return err
			}
		}
		if b.SystemAttributeRead || b.WholeRowRead || b.Merge.HasNothing || len(b.UsedColumns) != 0 || b.Returning != nil && b.Returning.ReadRequired {
			return request(authorization.SelectPrivilege, b.UsedColumns)
		}
	case *parser.Delete:
		if err := whole(authorization.DeletePrivilege); err != nil {
			return err
		}
		if b.SystemAttributeRead || b.WholeRowRead || len(b.UsedColumns) != 0 {
			return request(authorization.SelectPrivilege, b.UsedColumns)
		}
	case *parser.Update:
		if err := request(authorization.UpdatePrivilege, b.Targets); err != nil {
			return err
		}
		if b.SystemAttributeRead || b.WholeRowRead || len(b.UsedColumns) != 0 {
			return request(authorization.SelectPrivilege, b.UsedColumns)
		}
	default:
		return request(authorization.SelectPrivilege, b.UsedColumns)
	}
	return nil
}

func SourceReadRequirements(source *exec.Leaf, visit func(Requirement) error) error {
	if source.AnalysisOnly {
		return nil
	}
	r := Requirement{Ref: source.Ref, Relation: source.Relation, Privilege: authorization.SelectPrivilege, Inherit: !source.Only}
	if source.SystemAttributeRead {
		r.WholeRelation = true
		if err := visit(r); err != nil {
			return err
		}
		r.WholeRelation = false
	}
	r.Columns = source.Uses
	r.SystemColumns = source.SystemColumns
	r.WholeRow = source.WholeRowRead
	return visit(r)
}

func SourceRowMarkRequirements(source *exec.Leaf, visit func(Requirement) error) error {
	if exec.PrivilegeQueryRowMark(source.Relation, source.Mark).Mode != 0 {
		return visit(Requirement{Ref: source.Ref, Relation: source.Relation, Privilege: authorization.UpdatePrivilege, RowMark: true, Inherit: !source.Only})
	}
	return nil
}
