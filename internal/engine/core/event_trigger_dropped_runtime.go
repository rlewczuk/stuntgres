package core

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// EventTriggerDroppedObjects resolves lexical state at Execute, never at bind.
func (h *statementHost) EventTriggerDroppedObjects() ([][]sqltype.Value, error) {
	s := h.scope()
	if s == nil || s.EventCommand == nil || s.EventCommand.Phase != "sql_drop" {
		return nil, functions.EventTriggerDroppedContextError()
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
	array, _ := view.LookupArray(text)
	rows := make([][]sqltype.Value, 0, len(s.EventCommand.Records))
	for _, record := range s.EventCommand.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		class, err := sqltype.OIDValue(oid, uint32(record.Address.ClassID))
		if err != nil {
			return nil, err
		}
		object, err := sqltype.OIDValue(oid, uint32(record.Address.ObjectID))
		if err != nil {
			return nil, err
		}
		sub, err := sqltype.Integer(integer, int64(record.Address.SubID))
		if err != nil {
			return nil, err
		}
		names, err := regobj.ObjectAddressTextArray(array, record.Names)
		if err != nil {
			return nil, err
		}
		args, err := regobj.ObjectAddressTextArray(array, record.Args)
		if err != nil {
			return nil, err
		}
		schema, name := sqltype.Null(text), sqltype.Null(text)
		if record.HasSchema {
			schema = sqltype.String(text, record.Schema)
		}
		if record.HasName {
			name = sqltype.String(text, record.Name)
		}
		rows = append(rows, []sqltype.Value{class, object, sub, sqltype.Boolean(boolean, record.Original), sqltype.Boolean(boolean, record.Normal), sqltype.Boolean(boolean, record.Temporary), sqltype.String(text, record.Kind), schema, name, sqltype.String(text, record.Identity), names, args})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
