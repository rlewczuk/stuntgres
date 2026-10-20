package engine

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) CommentInquiry(c *exec.Context, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if err := admitInquiryBody(c, provenance); err != nil {
		return sqltype.Value{}, err
	}
	if scope.inquiry.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing comment snapshot")
	}
	oid, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	var text string
	switch kind {
	case functions.LegacyDescription:
		var class ids.OID
		for a, d := range scope.inquiry.commentCatalog.Descriptors() {
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
			return sqltype.Value{}, err
		}
		text = pgcatalog.DescriptorComment(scope.inquiry.commentCatalog.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), int32(sub))
	case functions.ObjectDescription, functions.SharedDescription:
		name, err := values[1].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		if kind == functions.SharedDescription {
			if name == "pg_authid" {
				r := scope.inquiry.commentRoles
				role, _ := r.Find(scope.inquiry.commentCatalog.Owner().Principals.Principal(uint64(oid)))
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
				text = pgcatalog.DescriptorComment(scope.inquiry.commentCatalog.Get(ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(oid)}), 0)
			}
		}
	}
	if text == "" {
		return sqltype.Null(result), nil
	}
	return sqltype.String(result, text), nil
}
