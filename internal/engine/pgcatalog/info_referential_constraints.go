package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// InfoReferentialConstraintsOID is a native identity, not a PostgreSQL allocation.
const InfoReferentialConstraintsOID ids.OID = 16239

// Rows consume one paired immutable catalog/authorization frontier. Foreign keys
// own population; this projection neither discovers nor invents constraints.
func captureInfoReferentialConstraints(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoReferentialConstraintsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 9 {
		return nil, fmt.Errorf("stuntgres: invalid referential_constraints source")
	}
	for i, column := range in.Source.Columns {
		domainID, baseID := sqltype.ID(13248), sqltype.Name
		if i >= 6 {
			domainID, baseID = 13246, sqltype.VarChar
		}
		domain, ok := in.Catalog.LookupType(domainID)
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: invalid referential_constraints domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		child, ok := descriptor.(*catalog.Relation)
		if !ok || len(child.Constraints.ForeignKeys) == 0 {
			continue
		}
		visible, err := referentialConstraintsVisible(in, child)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		namespace, ok := in.Catalog.Namespace(child.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing referential_constraints namespace")
		}
		for i := range child.Constraints.ForeignKeys {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			fk := &child.Constraints.ForeignKeys[i]
			target, ok := in.Catalog.Relation(fk.Target)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing referential_constraints target")
			}
			index, ok := in.Catalog.Relation(fk.Index)
			if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
				return nil, fmt.Errorf("stuntgres: invalid referential_constraints supporting index")
			}
			targetNamespace, ok := in.Catalog.Namespace(target.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing referential_constraints target namespace")
			}
			var unique *catalog.UniqueConstraint
			for j := range target.Constraints.Unique {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				candidate := &target.Constraints.Unique[j]
				if candidate.Index == fk.Index {
					unique = candidate
					break
				}
			}
			update, err := referentialConstraintAction(fk.UpdateAction)
			if err != nil {
				return nil, err
			}
			deleteRule, err := referentialConstraintAction(fk.DeleteAction)
			if err != nil {
				return nil, err
			}
			match := "NONE"
			if fk.Full {
				match = "FULL"
			}
			names := [9]string{in.Runtime.DatabaseName(), namespace.Name(), fk.Name, "", "", "", match, update, deleteRule}
			if unique != nil {
				names[3], names[4], names[5] = in.Runtime.DatabaseName(), targetNamespace.Name(), unique.Name
			}
			row := make([]sqltype.Value, len(names))
			for j, name := range names {
				typ := in.Source.Columns[j].Usage.Type
				value := sqltype.Null(typ.BaseType())
				if unique != nil || j < 3 || j >= 6 {
					value = sqltype.String(typ.BaseType(), name)
				}
				row[j], err = typ.Wrap(value)
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func referentialConstraintsVisible(in Input, child *catalog.Relation) (bool, error) {
	owner, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), child.Owner)
	if err != nil || owner {
		return owner, err
	}
	ref := in.Ref.Domain().Ref(in.Ref.Epoch, child.Address())
	rights, err := in.Runtime.Effective(ref, false)
	if err != nil {
		return false, err
	}
	const tableRights = authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.ReferencesPrivilege | authorization.TriggerPrivilege
	if rights&tableRights != 0 {
		return true, nil
	}
	const columnRights = authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.ReferencesPrivilege
	for _, attribute := range child.Attributes {
		if err := in.Context.Err(); err != nil {
			return false, err
		}
		if attribute <= 0 || child.IndexForAttribute(attribute) < 0 {
			continue
		}
		ref.Address.SubID = attribute
		rights, err := in.Runtime.Effective(ref, false)
		if err != nil {
			return false, err
		}
		if rights&columnRights != 0 {
			return true, nil
		}
	}
	return false, nil
}

func referentialConstraintAction(action parser.ForeignKeyAction) (string, error) {
	switch action {
	case parser.ForeignKeyNoAction:
		return "NO ACTION", nil
	case parser.ForeignKeyRestrict:
		return "RESTRICT", nil
	case parser.ForeignKeyCascade:
		return "CASCADE", nil
	case parser.ForeignKeySetNull:
		return "SET NULL", nil
	case parser.ForeignKeySetDefault:
		return "SET DEFAULT", nil
	default:
		return "", fmt.Errorf("stuntgres: invalid referential_constraints action %d", action)
	}
}
