package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoViewsOID is the reserved native relation identity, not a PostgreSQL allocation.
const InfoViewsOID ids.OID = 16216

// ViewCapability contains structural event bits (UPDATE=4, INSERT=8, DELETE=16)
// and actual INSTEAD OF trigger facts. Rules are not triggers.
type ViewCapability struct{ Events, Triggers uint32 }

func captureInfoViews(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoViewsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 10 {
		return nil, fmt.Errorf("stuntgres: invalid views source")
	}
	for i, column := range in.Source.Columns {
		domain, base := sqltype.ID(13246), sqltype.VarChar
		if i < 3 {
			domain, base = 13248, sqltype.Name
		} else if i >= 5 {
			domain = 13256
		}
		typ := column.Usage.Type
		if typ == nil || !typ.IsDomain() || typ.ID() != domain || typ.BaseType().ID() != base {
			return nil, fmt.Errorf("stuntgres: invalid views domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || ClassKind(in.Catalog, relation) != "v" {
			continue
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != in.Runtime.TemporaryOwner() {
			continue
		}
		owner, visible, err := infoRelationVisible(in, relation)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		facts, err := in.Runtime.ViewCapabilities(in.Context, in.Catalog, relation)
		if err != nil {
			return nil, err
		}
		definition := ""
		if owner {
			definition, err = in.Runtime.ViewDefinition(in.Context, in.Catalog, relation)
			if err != nil {
				return nil, err
			}
			definition = strings.TrimSuffix(definition, ";") + ";"
		}
		check := "NONE"
		if relation.View != nil {
			for _, option := range relation.View.Options() {
				if option == "check_option=cascaded" {
					check = "CASCADED"
				} else if option == "check_option=local" && check != "CASCADED" {
					check = "LOCAL"
				}
			}
		}
		yes := func(b bool) string {
			if b {
				return "YES"
			}
			return "NO"
		}
		texts := [10]string{in.Runtime.DatabaseName(), namespace.Name(), relation.Name(), definition, check, yes(facts.Events&20 == 20), yes(facts.Events&8 != 0), yes(facts.Triggers&4 != 0), yes(facts.Triggers&16 != 0), yes(facts.Triggers&8 != 0)}
		row := make([]sqltype.Value, 10)
		for i, text := range texts {
			typ := in.Source.Columns[i].Usage.Type
			if i == 3 && !owner {
				row[i] = sqltype.Null(typ)
				continue
			}
			row[i], err = typ.Wrap(sqltype.String(typ.BaseType(), text))
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// infoRelationVisible implements the shared tables/views relation-access
// predicate. Namespace USAGE is deliberately not part of metadata visibility.
// Ownership is returned separately for views' definition-disclosure decision.
func infoRelationVisible(in Input, relation *catalog.Relation) (owner, visible bool, err error) {
	owner, err = in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), relation.Owner)
	if err != nil || owner {
		return owner, owner, err
	}
	ref := in.Ref.Domain().Ref(in.Ref.Epoch, relation.Address())
	rights, err := in.Runtime.Effective(ref, false)
	if err != nil {
		return false, false, err
	}
	if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege|authorization.TruncatePrivilege|authorization.ReferencesPrivilege|authorization.TriggerPrivilege) != 0 {
		return false, true, nil
	}
	for _, attribute := range relation.Attributes {
		if attribute <= 0 {
			continue
		}
		if _, dropped := relation.ColumnMeta.Dropped[attribute]; dropped {
			continue
		}
		columnRef := ref
		columnRef.Address.SubID = attribute
		rights, err := in.Runtime.Effective(columnRef, false)
		if err != nil {
			return false, false, err
		}
		if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) != 0 {
			return false, true, nil
		}
	}
	return false, false, nil
}
