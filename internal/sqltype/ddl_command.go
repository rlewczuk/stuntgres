package sqltype

import "fmt"

// DDLCommand is a bounded description of one admitted base command. It owns no
// executor, parser, catalog, or event-trigger authority.
type DDLCommand struct {
	Tag               string
	ClassID, ObjectID uint32
	SubID             int32
	SpecialKind       string
	InExtension       bool
	AlterActions      uint32
}

type ddlCommandPayload struct{ command DDLCommand }

// DDLCommandValue grants non-NULL query-result authority only to the canonical
// pg_ddl_command pseudo identity. It grants no storage or codec capability.
func DDLCommandValue(d *Descriptor, command DDLCommand) (Value, error) {
	if d == nil || d.ID() != 32 || !d.Usage().QueryNullUsage() || command.Tag == "" {
		return Value{}, fmt.Errorf("invalid DDL command datum")
	}
	if command.SpecialKind == "" && (command.ClassID == 0 || command.ObjectID == 0 || command.SubID < 0) ||
		command.SpecialKind != "" && (command.ClassID != 0 || command.ObjectID != 0 || command.SubID != 0) {
		return Value{}, fmt.Errorf("invalid DDL command address")
	}
	return Value{typ: d, ddlCommand: &ddlCommandPayload{command: command}}, nil
}
