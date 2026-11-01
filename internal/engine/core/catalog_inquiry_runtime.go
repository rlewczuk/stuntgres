package core

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/session"

	"stuntgres/internal/sqltype"
)

func CatalogUserName(h CatalogHost, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	read, roles := h.CommentSnapshot()
	if read.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	domain := h.RoleDomain()
	if !roles.BelongsTo(domain) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign catalog role-name snapshot")
	}
	role, found := roles.Find(domain.Principal(uint64(oid)))
	if found {
		return sqltype.String(result, role.Name), nil
	}
	return sqltype.String(result, "unknown (OID="+strconv.FormatUint(uint64(oid), 10)+")"), nil
}

func TablespaceLocation(h CatalogHost, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	view, err := h.InquiryCatalog()
	if err != nil {
		return sqltype.Value{}, err
	}
	return ddl.TablespaceLocation(view, oid, result)
}

func TablespaceDatabase(h CatalogHost, oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, error) {
	view, err := h.InquiryCatalog()
	if err != nil {
		return sqltype.Value{}, false, err
	}
	value, present, warning, err := ddl.TablespaceDatabase(view, oid, result)
	if warning != nil {
		h.Operation().EmitNotice(session.LevelWarning, *warning)
	}
	return value, present, err
}
