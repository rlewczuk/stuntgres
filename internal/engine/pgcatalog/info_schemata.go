package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoSchemataOID is the native relation identity, independent of PostgreSQL's
// allocated information-schema relation OID.
const InfoSchemataOID ids.OID = 16230

func captureInfoSchemata(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoSchemataOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid schemata source")
	}
	identifier, identifierOK := in.Catalog.LookupType(13248)
	name, nameOK := in.Catalog.LookupType(sqltype.Name)
	characterData, characterDataOK := in.Catalog.LookupType(13246)
	varchar, varcharOK := in.Catalog.LookupType(sqltype.VarChar)
	if !identifierOK || !nameOK || !identifier.IsDomain() || identifier.BaseType() != name || !characterDataOK || !varcharOK || !characterData.IsDomain() || characterData.BaseType() != varchar {
		return nil, fmt.Errorf("stuntgres: invalid schemata domains")
	}
	for i, column := range in.Source.Columns {
		typ := identifier
		if i == 6 {
			typ = characterData
		}
		if column.Usage != typ.Usage() {
			return nil, fmt.Errorf("stuntgres: foreign schemata domain usage")
		}
	}
	database, ok := in.Catalog.Database(in.Owner.DatabaseAddress())
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	if err := (catalog.DatabaseProvider{}).Validate(in.Catalog, database); err != nil {
		return nil, err
	}
	catalogName, err := identifier.Wrap(sqltype.String(name, in.Runtime.DatabaseName()))
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		ns, ok := descriptor.(*catalog.Namespace)
		if !ok {
			continue
		}
		if err := (catalog.NamespaceProvider{}).Validate(in.Catalog, ns); err != nil {
			return nil, err
		}
		if address != ns.Address() {
			return nil, fmt.Errorf("stuntgres: invalid schemata namespace address")
		}
		owner, err := in.Roles.Descriptor(ns.Owner)
		if err != nil {
			return nil, err
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), ns.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Owner.Domain.Ref(in.Ref.Epoch, ns.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&(authorization.CreatePrivilege|authorization.UsagePrivilege) == 0 {
				continue
			}
		}
		schemaName, err := identifier.Wrap(sqltype.String(name, ns.Name()))
		if err != nil {
			return nil, err
		}
		ownerName, err := identifier.Wrap(sqltype.String(name, owner.Name))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{catalogName, schemaName, ownerName, sqltype.Null(identifier), sqltype.Null(identifier), sqltype.Null(identifier), sqltype.Null(characterData)})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
