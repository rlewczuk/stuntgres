package table

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ColumnStorageAction(kind parser.AlterTableActionKind) bool {
	return kind == parser.SetStorage || kind == parser.SetCompression
}

func ColumnStorageOnly(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 { return false }
	for _, action := range actions { if !ColumnStorageAction(action.Action) { return false } }
	return true
}

// ColumnStorageCandidate resolves against the completed shape in the misc pass.
// The caller holds the engine mutex for the diagnostic reference-path capture.
func ColumnStorageCandidate(view catalog.Read, env access.Environment, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, int, error) {
	name := action.Column.Text
	if catalog.IsSystemColumn(name) { return nil, 0, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, name)) }
	slot, exists := old.Index[name]
	if !exists { return nil, 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, old.Name())) }
	typ := old.Columns[slot].Usage.Type
	defaults := typ.AttributeFacts().Storage
	value := old.ColumnStorage(slot)
	property := action.Property.Text
	if action.Action == parser.SetStorage {
		switch strings.ToLower(property) {
		case "plain": value.Storage = "p"
		case "external": value.Storage = "e"
		case "extended": value.Storage = "x"
		case "main": value.Storage = "m"
		case "default": value.Storage = defaults
		default: return nil, 0, sqlError("22023", fmt.Sprintf(`invalid storage type "%s"`, property))
		}
		if value.Storage != "p" && defaults == "p" { return nil, 0, columnStorageTypeError(view, env, typ, "can only have storage PLAIN") }
	} else {
		if property == "default" { value.Compression = "" } else {
			if defaults == "p" { return nil, 0, columnStorageTypeError(view, env, typ, "does not support compression") }
			switch property {
			case "pglz": value.Compression = "p"
			case "lz4": value.Compression = "l"
			default: return nil, 0, sqlError("22023", fmt.Sprintf(`invalid compression method "%s"`, property))
			}
		}
	}
	return old.WithColumnStorage(slot, value), slot, nil
}

func columnStorageTypeError(view catalog.Read, env access.Environment, typ *sqltype.Descriptor, reason string) error {
	path, err := env.ReferencePath()
	if err != nil { return err }
	descriptor := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	return sqlError("0A000", fmt.Sprintf("column data type %s %s", catalog.TypeDiagnosticNameAt(view, nil, descriptor, path), reason))
}
