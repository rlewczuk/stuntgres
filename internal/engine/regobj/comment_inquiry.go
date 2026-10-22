package regobj

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// CommentText answers obj_description, col_description and shobj_description
// over the statement's comment catalog and roles. An empty result is NULL.
func CommentText(view catalog.Read, roles authorization.Read, kind functions.CommentInquiryKind, values [2]sqltype.Value) (string, error) {
	oid, err := values[0].OID()
	if err != nil {
		return "", err
	}
	var text string
	switch kind {
	case functions.LegacyDescription:
		var class ids.OID
		for a, d := range view.Descriptors() {
			if a.ObjectID == ids.OID(oid) && a.SubID == 0 && (text == "" || a.ClassID < class) {
				if candidate := pgcatalog.DescriptorComment(d, 0); candidate != "" {
					// Immutable address ordering keeps equal calls STABLE; it
					// does not establish portable catalog precedence.
					text, class = candidate, a.ClassID
				}
			}
		}
	case functions.ColumnDescription:
		sub, err := values[1].Integer()
		if err != nil {
			return "", err
		}
		text = pgcatalog.DescriptorComment(view.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), int32(sub))
	case functions.ObjectDescription, functions.SharedDescription:
		name, err := values[1].Text()
		if err != nil {
			return "", err
		}
		if kind == functions.SharedDescription {
			if name == "pg_authid" {
				role, _ := roles.Find(view.Owner().Principals.Principal(uint64(oid)))
				text = role.Comment
			}
		} else {
			var class ids.OID
			switch name {
			case "pg_class":
				class = ids.RelationClassID
			case "pg_namespace":
				class = ids.NamespaceClassID
			case "pg_type":
				class = ids.TypeClassID
			}
			if class != 0 {
				text = pgcatalog.DescriptorComment(view.Get(ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(oid)}), 0)
			}
		}
	}
	return text, nil
}
