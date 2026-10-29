package core

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// EventTriggerDDLCommands opens the live complete-query collector without
// consuming it. Values are detached from lexical and catalog authority.
func (h *statementHost) EventTriggerDDLCommands() ([][]sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.EventCommand == nil || !s.EventCommand.CollectDDL {
		return nil, functions.EventTriggerDDLCommandsContextError()
	}
	ctx := s.Op.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	view := s.Inquiry.CommentCatalog
	oid, _ := view.LookupType(sqltype.Oid)
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	text, _ := view.LookupType(sqltype.Text)
	opaque, _ := view.LookupType(32)
	rows := make([][]sqltype.Value, 0, len(s.EventCommand.Commands))
	for _, command := range s.EventCommand.Commands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		class, object, sub := sqltype.Null(oid), sqltype.Null(oid), sqltype.Null(integer)
		schema, identity := sqltype.Null(text), sqltype.Null(text)
		kind := command.SpecialKind
		if kind == "" {
			a := ids.ObjectAddress{ClassID: ids.OID(command.ClassID), ObjectID: ids.OID(command.ObjectID), SubID: command.SubID}
			identified, err := regobj.IdentifyAddress(view, s.Inquiry.commentRoles, s.Inquiry.DeparsePath, a, s.Op.Session.SessionToken)
			if err != nil {
				return nil, err
			}
			if !identified.Found {
				continue
			}
			kind = identified.Kind
			if identified.RawSchema != "" {
				schema = sqltype.String(text, identified.RawSchema)
			}
			identity = sqltype.String(text, identified.Identity)
			class, err = sqltype.OIDValue(oid, command.ClassID)
			if err != nil {
				return nil, err
			}
			object, err = sqltype.OIDValue(oid, command.ObjectID)
			if err != nil {
				return nil, err
			}
			sub, err = sqltype.Integer(integer, int64(command.SubID))
			if err != nil {
				return nil, err
			}
		}
		value, err := sqltype.DDLCommandValue(opaque, command)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{class, object, sub, sqltype.String(text, command.Tag), sqltype.String(text, kind), schema, identity, sqltype.Boolean(boolean, command.InExtension), value})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
