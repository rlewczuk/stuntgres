package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoUserDefinedTypesOID is a native virtual relation identity, not a PostgreSQL allocation.
const InfoUserDefinedTypesOID ids.OID = 16234

var infoUserDefinedTypesColumnTypes = [...]sqltype.ID{13248, 13248, 13248, 13246, 13256, 13256, 13246, 13246, 13248, 13248, 13248, 13246, 13246, 13243, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13243, 13243, 13243, 13246, 13243, 13248, 13248}

// Caller owns the capture lock. Relation, type and ACL authority share one frontier.
func captureInfoUserDefinedTypes(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoUserDefinedTypesOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != len(infoUserDefinedTypesColumnTypes) {
		return nil, fmt.Errorf("stuntgres: invalid user_defined_types source")
	}
	for i, id := range infoUserDefinedTypesColumnTypes {
		typ, ok := in.Catalog.LookupType(id)
		if !ok || !typ.IsDomain() || in.Source.Columns[i].Usage != typ.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid user_defined_types source domain")
		}
		base := typ.DomainBase()
		current, ok := in.Catalog.LookupType(base.Type.ID())
		expected, mod, collation := sqltype.Name, int32(-1), uint32(950)
		switch id {
		case 13246:
			expected = sqltype.VarChar
		case 13256:
			expected, mod = sqltype.VarChar, 7
		case 13243:
			expected, collation = sqltype.Int4, 0
		}
		if !ok || base.Type != current || base.Type.ID() != expected || base.Typmod != mod || base.Collation.ID != collation {
			return nil, fmt.Errorf("stuntgres: foreign user_defined_types source base")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.CompositeRelation {
			continue
		}
		if relation.Address() != address {
			return nil, fmt.Errorf("stuntgres: invalid user_defined_types relation address")
		}
		typ, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
		if !ok || !typ.SQLType.IsComposite() || typ.OwningRelation != address || typ.Namespace() != relation.Namespace() || typ.Name() != relation.Name() || typ.Owner != relation.Owner {
			return nil, fmt.Errorf("stuntgres: invalid user_defined_types reciprocal type")
		}
		if err := in.Runtime.ValidateType(in.Catalog, typ); err != nil {
			return nil, err
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing user_defined_types namespace")
		}
		if namespace.Address() != relation.Namespace() {
			return nil, fmt.Errorf("stuntgres: invalid user_defined_types namespace identity")
		}
		if err := (catalog.NamespaceProvider{}).Validate(in.Catalog, namespace); err != nil {
			return nil, err
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), typ.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, typ.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&authorization.UsagePrivilege == 0 {
				continue
			}
		}
		values := [5]string{in.Runtime.DatabaseName(), namespace.Name(), relation.Name(), "STRUCTURED", "YES"}
		row := make([]sqltype.Value, len(infoUserDefinedTypesColumnTypes))
		for i := range row {
			domain := in.Source.Columns[i].Usage.Type
			value := sqltype.Null(domain.BaseType())
			if i < len(values) {
				value = sqltype.String(domain.BaseType(), values[i])
			}
			row[i], err = domain.Wrap(value)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
