package pgcatalog

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoSequencesOID is a reserved native relation identity, not a PostgreSQL allocation.
const InfoSequencesOID ids.OID = 16212

// captureInfoSequences reads declared options and relation-level authority from
// one immutable statement frontier. It never opens or reads a sequence generator.
func captureInfoSequences(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoSequencesOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 12 {
		return nil, fmt.Errorf("stuntgres: invalid sequences source")
	}
	for i, column := range in.Source.Columns {
		domain, base := sqltype.ID(13246), sqltype.VarChar
		if i < 3 {
			domain, base = 13248, sqltype.Name
		}
		if i >= 4 && i <= 6 {
			domain, base = 13243, sqltype.Int4
		}
		if i == 11 {
			domain = 13256
		}
		typ := column.Usage.Type
		if typ == nil || !typ.IsDomain() || typ.ID() != domain || typ.BaseType().ID() != base {
			return nil, fmt.Errorf("stuntgres: invalid sequences domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.SequenceRelation {
			continue
		}
		options := relation.Sequence
		if options == nil {
			return nil, fmt.Errorf("stuntgres: missing sequences options")
		}
		if options.Identity {
			continue
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing sequences namespace")
		}
		if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != in.Runtime.TemporaryOwner() {
			continue
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), relation.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, relation.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&(authorization.SelectPrivilege|authorization.UpdatePrivilege|authorization.UsagePrivilege) == 0 {
				continue
			}
		}
		var name string
		var precision int64
		switch options.SQLType {
		case sqltype.Int2:
			name, precision = "smallint", 16
		case sqltype.Int4:
			name, precision = "integer", 32
		case sqltype.Int8:
			name, precision = "bigint", 64
		default:
			return nil, fmt.Errorf("stuntgres: invalid sequences numeric type")
		}
		cycle := "NO"
		if options.Cycle {
			cycle = "YES"
		}
		texts := [12]string{database, namespace.Name(), relation.Name(), name, "", "", "", strconv.FormatInt(options.Start, 10), strconv.FormatInt(options.Min, 10), strconv.FormatInt(options.Max, 10), strconv.FormatInt(options.Increment, 10), cycle}
		row := make([]sqltype.Value, 12)
		for i, text := range texts {
			typ := in.Source.Columns[i].Usage.Type
			var base sqltype.Value
			if i >= 4 && i <= 6 {
				n := precision
				if i == 5 {
					n = 2
				}
				if i == 6 {
					n = 0
				}
				base, err = sqltype.Integer(typ.BaseType(), n)
				if err != nil {
					return nil, err
				}
			} else {
				base = sqltype.String(typ.BaseType(), text)
			}
			row[i], err = typ.Wrap(base)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
