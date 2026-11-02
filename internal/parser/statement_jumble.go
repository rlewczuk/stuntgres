package parser

// UtilityTypeName applies the parser's existing released raw type-name aliases.
// It does not bind a type, inspect a descriptor, or format SQL.
func UtilityTypeName(raw TypeName) TypeName { return standaloneType(raw) }
