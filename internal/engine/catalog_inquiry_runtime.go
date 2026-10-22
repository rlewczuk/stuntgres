package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"

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
func (h *statementHost) tablespaceInquiryCatalog() (catalog.Read, error) {
	scope := h.scope()
	view := scope.inquiry.commentCatalog
	if view.State() == nil {
		return catalog.Read{}, fmt.Errorf("stuntgres: missing tablespace inquiry snapshot")
	}
	engine := scope.engine()
	if view.Owner() != engine.catalog.owner() || !scope.inquiry.commentRoles.BelongsTo(&engine.roles.domain) {
		return catalog.Read{}, fmt.Errorf("stuntgres: foreign tablespace inquiry snapshot")
	}
	return view, nil
}

func (h *statementHost) TablespaceLocation(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	view, err := h.tablespaceInquiryCatalog()
	if err != nil {
		return sqltype.Value{}, err
	}
	if oid == 0 {
		oid = uint32(catalog.DefaultTablespaceOID)
	}
	if ids.OID(oid) != catalog.DefaultTablespaceOID && ids.OID(oid) != catalog.GlobalTablespaceOID {
		return sqltype.Value{}, sqlError("0A000", "pg_tablespace_location physical directory inspection is unsupported in embedded mode")
	}
	descriptor := view.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: ids.OID(oid)})
	if err := (catalog.TablespaceProvider{}).Validate(view, descriptor); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, ""), nil
}

func (h *statementHost) TablespaceDatabase(oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, error) {
	view, err := h.tablespaceInquiryCatalog()
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if ids.OID(oid) == catalog.DefaultTablespaceOID || ids.OID(oid) == catalog.GlobalTablespaceOID {
		descriptor := view.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: ids.OID(oid)})
		if err := (catalog.TablespaceProvider{}).Validate(view, descriptor); err != nil {
			return sqltype.Value{}, false, err
		}
	}
	if ids.OID(oid) == catalog.DefaultTablespaceOID {
		descriptor, ok := view.Database(catalog.CurrentDatabaseAddress)
		if !ok || descriptor == nil {
			return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing tablespace database authority")
		}
		if err := (catalog.DatabaseProvider{}).Validate(view, descriptor); err != nil {
			return sqltype.Value{}, false, err
		}
		// Initialized native databases contain nonshared system catalog relations.
		// They prove logical occupancy even without any user relation or user row.
		value, err := sqltype.OIDValue(result, uint32(descriptor.Address().ObjectID))
		return value, true, err
	}
	message := "global tablespace never has databases"
	if ids.OID(oid) != catalog.GlobalTablespaceOID {
		message = strconv.FormatUint(uint64(oid), 10) + " is not a tablespace OID"
	}
	h.scope().op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: message})
	return sqltype.Null(result), false, nil
}
