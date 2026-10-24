package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/session"

	"stuntgres/internal/sqltype"
)

func (h *statementHost) CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	engine := scope.engine()
	if !scope.inquiry.commentRoles.BelongsTo(&engine.roles.domain) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign catalog role-name snapshot")
	}
	role, found := scope.inquiry.commentRoles.Find(engine.roles.domain.Principal(uint64(oid)))
	if found {
		return sqltype.String(result, role.Name), nil
	}
	return sqltype.String(result, "unknown (OID="+strconv.FormatUint(uint64(oid), 10)+")"), nil
}

// Read only the retained statement root. Comparing its immutable owner does not
// consult the live catalog frontier, which may already have been replaced.
func (h *statementHost) catalogInquiryCatalog() (catalog.Read, error) {
	scope := h.scope()
	view := scope.inquiry.commentCatalog
	if view.State() == nil {
		return catalog.Read{}, fmt.Errorf("stuntgres: missing catalog inquiry snapshot")
	}
	engine := scope.engine()
	if view.Owner() != &engine.catalog.Owner || !scope.inquiry.commentRoles.BelongsTo(&engine.roles.domain) {
		return catalog.Read{}, fmt.Errorf("stuntgres: foreign catalog inquiry snapshot")
	}
	return view, nil
}

func (h *statementHost) TablespaceLocation(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	view, err := h.catalogInquiryCatalog()
	if err != nil {
		return sqltype.Value{}, err
	}
	return ddl.TablespaceLocation(view, oid, result)
}

func (h *statementHost) TablespaceDatabase(oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, error) {
	view, err := h.catalogInquiryCatalog()
	if err != nil {
		return sqltype.Value{}, false, err
	}
	value, present, warning, err := ddl.TablespaceDatabase(view, oid, result)
	if warning != nil {
		h.scope().op.EmitNotice(session.LevelWarning, *warning)
	}
	return value, present, err
}
