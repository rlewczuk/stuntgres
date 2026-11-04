package access

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
)

// SepgsqlAuditSink queues events; it may not execute application callbacks.
type SepgsqlAuditSink interface {
	SepgsqlAudit(*sepgsql.Runtime, sepgsql.AuditEvent)
}
type SepgsqlAuthority struct {
	Policy      *sepgsql.Runtime
	Subject     string
	Generation  uint64
	Frame       uint64
	Depth       int
	Predecessor *catalog.State
	Debug       bool
	Audit       SepgsqlAuditSink
	Observer    SepgsqlObservationSink
}

func (v View) SepgsqlContext(address ids.ObjectAddress) string {
	if record := v.Catalog.State().SecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"}); record != nil {
		return record.Label
	}
	return v.Sepgsql.Policy.Unlabeled()
}
func (v View) SepgsqlDecision(target, class, name string, mask sepgsql.Mask, abort bool) (bool, error) {
	if v.Sepgsql.Policy == nil || mask == 0 {
		return true, nil
	}
	allowed, event := v.Sepgsql.Policy.Decide(v.Sepgsql.Subject, target, class, name, mask, v.Sepgsql.Debug)
	if event != nil && v.Sepgsql.Audit != nil {
		v.Sepgsql.Audit.SepgsqlAudit(v.Sepgsql.Policy, *event)
	}
	if !allowed && abort {
		return false, sqlError("42501", "SELinux: security policy violation")
	}
	return allowed, nil
}
func (v View) SepgsqlCheck(address ids.ObjectAddress, mask sepgsql.Mask) error {
	if v.Sepgsql.Policy == nil || mask == 0 {
		return nil
	}
	object := address
	object.SubID = 0
	d, err := v.Object(v.Ref(object))
	if err != nil {
		return err
	}
	class := catalog.SepgsqlClass(d, address.SubID)
	if class == "" {
		return nil
	}
	_, err = v.SepgsqlDecision(v.SepgsqlContext(address), class, catalog.SepgsqlObjectKey(v.Catalog, d, address.SubID), mask, true)
	if err != nil {
		return err
	}
	return v.observeSepgsql(address, class, mask)
}
func (v View) SepgsqlSearch(address ids.ObjectAddress, abort bool) (bool, error) {
	if v.Sepgsql.Policy == nil {
		return true, nil
	}
	if _, err := v.Object(v.Ref(address)); err != nil {
		return false, err
	}
	allowed, err := v.SepgsqlDecision(v.SepgsqlContext(address), "db_schema", catalog.SepgsqlObjectKey(v.Catalog, v.Catalog.Get(address), 0), sepgsql.Permission("db_schema", "search"), abort)
	if err == nil {
		err = v.observeSepgsql(address, "db_schema", sepgsql.Permission("db_schema", "search"))
	}
	return allowed, err
}
func (v View) Check(env Environment, req Requirement) error {
	if req.ColumnDemand || req.RowMark {
		relation, ok := v.Catalog.Relation(req.Object.Address)
		if !ok {
			return fmt.Errorf("stuntgres: invalid semantic relation demand")
		}
		acl := v
		acl.Sepgsql = SepgsqlAuthority{}
		if err := acl.checkColumnsACL(env, req.Object, relation, req.Privileges, req.Columns); err != nil {
			return err
		}
		if req.RowMark {
			if v.Sepgsql.Policy == nil {
				return nil
			}
			if catalog.SepgsqlClass(relation, 0) == "db_view" {
				return v.SepgsqlCheck(relation.Address(), sepgsql.Permission("db_view", "expand"))
			}
			return v.sepgsqlRelations(relation, sepgsql.Permission("db_table", "lock"), req.Inherit)
		}
		return v.SepgsqlColumns(relation, req.Privileges, req.Columns, req.SystemColumns, req.Inherit, req.WholeRow)
	}
	if err := v.checkACL(env, req); err != nil {
		return err
	}
	if req.OwnerOnly || v.Sepgsql.Policy == nil {
		return nil
	}
	d := v.Catalog.Get(ids.ObjectAddress{ClassID: req.Object.Address.ClassID, ObjectID: req.Object.Address.ObjectID})
	var mask sepgsql.Mask
	class := catalog.SepgsqlClass(d, req.Object.Address.SubID)
	if class == "db_schema" && req.Privileges&authorization.UsagePrivilege != 0 {
		mask |= sepgsql.Permission(class, "search")
	}
	if class == "db_procedure" && req.Privileges&authorization.ExecutePrivilege != 0 && !req.RoutineACLOnly {
		mask |= sepgsql.Permission(class, "execute")
	}
	if class == "db_database" && req.Privileges&authorization.ConnectPrivilege != 0 {
		mask |= sepgsql.Permission(class, "access")
	}
	if _, ok := d.(*catalog.Relation); ok {
		mask |= sepgsqlRelationMask(class, req.Privileges)
	}
	if relation, ok := d.(*catalog.Relation); ok {
		return v.sepgsqlRelations(relation, mask, req.Inherit)
	}
	return v.SepgsqlCheck(req.Object.Address, mask)
}
func sepgsqlRelationMask(class string, privileges authorization.Privileges) sepgsql.Mask {
	if class == "db_sequence" {
		if privileges&authorization.SelectPrivilege != 0 {
			return sepgsql.Permission(class, "get_value")
		}
		return 0
	}
	if class == "db_view" {
		if privileges&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege) != 0 {
			return sepgsql.Permission(class, "expand")
		}
		return 0
	}
	var mask sepgsql.Mask
	for _, p := range [...]struct {
		sql authorization.Privileges
		mac string
	}{{authorization.SelectPrivilege, "select"}, {authorization.InsertPrivilege, "insert"}, {authorization.UpdatePrivilege, "update"}, {authorization.DeletePrivilege, "delete"}, {authorization.TruncatePrivilege, "truncate"}} {
		if privileges&p.sql != 0 {
			mask |= sepgsql.Permission(class, p.mac)
		}
	}
	return mask
}
func (v View) CheckColumns(env Environment, ref ids.ObjectRef, relation *catalog.Relation, mask authorization.Privileges, indices []int) error {
	acl := v
	acl.Sepgsql = SepgsqlAuthority{}
	if err := acl.checkColumnsACL(env, ref, relation, mask, indices); err != nil {
		return err
	}
	return v.SepgsqlColumns(relation, mask, indices, nil, false, false)
}
func (v View) SepgsqlColumns(relation *catalog.Relation, privileges authorization.Privileges, indices []int, system []int32, inherit, wholeRow bool) error {
	if v.Sepgsql.Policy == nil {
		return nil
	}
	return v.sepgsqlColumns(relation, relation, privileges, indices, system, inherit, wholeRow)
}

// Keep the original selected names across the entire closure. Mapping one
// demanded column at a time avoids allocating a remapped slice for every child.
func (v View) sepgsqlColumns(selected, relation *catalog.Relation, privileges authorization.Privileges, indices []int, system []int32, inherit, wholeRow bool) error {
	if relation.RelationKind == catalog.CatalogRelation && privileges&(authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege) != 0 && !v.Sepgsql.Policy.Permissive(v.Sepgsql.Subject) {
		return sqlError("42501", "SELinux: hardwired security policy violation")
	}
	if err := v.SepgsqlCheck(relation.Address(), sepgsqlRelationMask(catalog.SepgsqlClass(relation, 0), privileges)); err != nil {
		return err
	}
	if relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation && relation.RelationKind != catalog.CatalogRelation {
		return nil
	}
	if wholeRow {
		for _, subid := range relation.Attributes {
			address := relation.Address()
			address.SubID = subid
			if err := v.SepgsqlCheck(address, sepgsqlRelationMask("db_column", privileges)); err != nil {
				return err
			}
		}
	} else {
		for _, selectedIndex := range indices {
			if selectedIndex < 0 || selectedIndex >= len(selected.Attributes) {
				return fmt.Errorf("stuntgres: invalid sepgsql column demand")
			}
			index := selectedIndex
			if selected != relation {
				var ok bool
				index, ok = relation.Index[selected.Columns[selectedIndex].Name]
				if !ok {
					return catalog.Conflict{}
				}
			}
			address := relation.Address()
			address.SubID = relation.Attributes[index]
			if err := v.SepgsqlCheck(address, sepgsqlRelationMask("db_column", privileges)); err != nil {
				return err
			}
		}
	}
	for _, subid := range system {
		if catalog.SepgsqlAttributeName(relation, subid) == "" {
			return fmt.Errorf("stuntgres: invalid simulated system attribute demand")
		}
		address := relation.Address()
		address.SubID = subid
		if err := v.SepgsqlCheck(address, sepgsqlRelationMask("db_column", privileges)); err != nil {
			return err
		}
	}
	if !inherit {
		return nil
	}
	// Stock checks the complete inheritance/partition permission closure even
	// when no tuple is produced. Child attribute identity is mapped by name.
	for _, child := range catalog.InheritanceChildren(v.Catalog, relation.Address()) {
		if err := v.sepgsqlColumns(selected, child, privileges, indices, system, true, wholeRow); err != nil {
			return err
		}
	}
	if relation.Partition != nil {
		for _, address := range relation.Partition.Children {
			child, ok := v.Catalog.Relation(address)
			if !ok {
				return catalog.Conflict{}
			}
			if err := v.sepgsqlColumns(selected, child, privileges, indices, system, true, wholeRow); err != nil {
				return err
			}
		}
	}
	return nil
}

// SepgsqlRowMark is independent of the SQL UPDATE ACL required for row locks.
func (v View) SepgsqlRowMark(address ids.ObjectAddress) error {
	return v.SepgsqlCheck(address, sepgsql.Permission("db_table", "lock"))
}
func (v View) sepgsqlRelations(relation *catalog.Relation, mask sepgsql.Mask, inherit bool) error {
	if v.Sepgsql.Policy == nil || mask == 0 {
		return nil
	}
	if relation.RelationKind == catalog.CatalogRelation && mask&(sepgsql.Permission("db_table", "insert")|sepgsql.Permission("db_table", "update")|sepgsql.Permission("db_table", "delete")) != 0 && !v.Sepgsql.Policy.Permissive(v.Sepgsql.Subject) {
		return sqlError("42501", "SELinux: hardwired security policy violation")
	}
	if err := v.SepgsqlCheck(relation.Address(), mask); err != nil {
		return err
	}
	if !inherit {
		return nil
	}
	for _, child := range catalog.InheritanceChildren(v.Catalog, relation.Address()) {
		if err := v.sepgsqlRelations(child, mask, true); err != nil {
			return err
		}
	}
	if relation.Partition != nil {
		for _, address := range relation.Partition.Children {
			child, ok := v.Catalog.Relation(address)
			if !ok {
				return catalog.Conflict{}
			}
			if err := v.sepgsqlRelations(child, mask, true); err != nil {
				return err
			}
		}
	}
	return nil
}
