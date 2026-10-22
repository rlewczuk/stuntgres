package index

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func KeyCollation(u sqltype.Usage, key catalog.IndexKey) sqltype.Usage {
	if key.Collation != 0 {
		u.Collation = sqltype.CollationRef{ID: key.Collation, Derivation: sqltype.Default}
		if key.CollationRef.Valid() && key.CollationRef.ID == key.Collation {
			u.Collation = key.CollationRef
		}
	}
	return u
}

func ReconstructIndexKey(view catalog.Read, key catalog.IndexKey, oldUsage, newUsage sqltype.Usage, methodOID ids.OID) catalog.IndexKey {
	oldDefault, _ := view.DefaultIndexClass(methodOID, oldUsage.Type)
	if key.Class.Name == "" || key.Class.SameIdentity(oldDefault) {
		key.Class, _ = view.DefaultIndexClass(methodOID, newUsage.Type)
		key.ExplicitClass = false
	}
	if key.Collation == oldUsage.Collation.ID {
		key.Collation = 0
		key.CollationRef = sqltype.CollationRef{}
	}
	return key
}

// Release18 utility semantic errors have no expression/parser location. Keep
// shared namespace lookup's authority but not its expression attribution.
func SemanticError(err error) error {
	if d, ok := err.(*sqlstate.Error); ok && d.Position != "" {
		copy := *d
		copy.Position = ""
		return &copy
	}
	return err
}

func DefaultClassError(name string, method catalog.IndexMethod) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704",
		Message: fmt.Sprintf("data type %s has no default operator class for access method %q", name, method.String()),
		Hint:    "You must specify an operator class for the index or define a default operator class for the data type."}
}

func ValidateIndexInclude(raw parser.IndexKey) error {
	switch {
	case raw.Collation != nil:
		return sqlError("42P17", "including column does not support a collation")
	case raw.Opclass != nil:
		return sqlError("42P17", "including column does not support an operator class")
	case raw.DirectionExplicit:
		return sqlError("42P17", "including column does not support ASC/DESC options")
	case raw.Nulls != parser.NullsDefault:
		return sqlError("42P17", "including column does not support NULLS FIRST/LAST options")
	}
	return nil
}

func ValidateHashKeyDirection(key parser.IndexKey) error {
	if key.DirectionExplicit {
		return sqlError("0A000", `access method "hash" does not support ASC/DESC options`)
	}
	if key.Nulls != parser.NullsDefault {
		return sqlError("0A000", `access method "hash" does not support NULLS FIRST/LAST options`)
	}
	return nil
}

func ValidateIndexMethod(s *parser.CreateIndex) error {
	switch s.Method {
	case "btree":
		return nil
	case "hash":
		if s.Unique {
			return sqlError("0A000", `access method "hash" does not support unique indexes`)
		}
		if len(s.Include) > 0 {
			return sqlError("0A000", `access method "hash" does not support included columns`)
		}
		if len(s.Keys) > 1 {
			return sqlError("0A000", `access method "hash" does not support multicolumn indexes`)
		}
		if s.Predicate != nil {
			return sqlError("0A000", "partial indexes are unsupported")
		}
		return nil
	case "gist":
		if s.Unique {
			return sqlError("0A000", `access method "gist" does not support unique indexes`)
		}
		for _, key := range s.Keys {
			if key.DirectionExplicit {
				return sqlError("0A000", `access method "gist" does not support ASC/DESC options`)
			}
			if key.Nulls != parser.NullsDefault {
				return sqlError("0A000", `access method "gist" does not support NULLS FIRST/LAST options`)
			}
		}
		return nil
	case "gin", "spgist", "brin":
		if len(s.Include) > 0 && s.Method == "gin" {
			return sqlError("0A000", `access method "gin" does not support included columns`)
		}
		if len(s.Keys) > 1 && s.Method == "spgist" {
			return sqlError("0A000", `access method "spgist" does not support multicolumn indexes`)
		}
		for _, key := range s.Keys {
			if key.DirectionExplicit {
				return sqlError("0A000", fmt.Sprintf("access method %q does not support ASC/DESC options", s.Method))
			}
			if key.Nulls != parser.NullsDefault {
				return sqlError("0A000", fmt.Sprintf("access method %q does not support NULLS FIRST/LAST options", s.Method))
			}
		}
		return sqlError("0A000", "non-btree indexes are unsupported")
	default:
		return sqlError("42704", fmt.Sprintf(`access method "%s" does not exist`, s.Method))
	}
}

// MethodOf maps a CREATE INDEX access method name to its method. Names that
// ValidateIndexMethod rejects never reach it; anything else is B-tree.
func MethodOf(name string) catalog.IndexMethod {
	switch name {
	case "hash":
		return catalog.HashMethod
	case "gist":
		return catalog.GistMethod
	}
	return catalog.BtreeMethod
}

// RejectVirtualUses rejects an index that reads a virtual generated column
// through any of slots.
func RejectVirtualUses(parent *catalog.Relation, slots []int) error {
	for _, slot := range slots {
		if definition, ok := parent.ColumnMeta.Generations[parent.Attributes[slot]]; ok && definition.Kind == catalog.GenerationVirtual {
			return sqlError("0A000", "indexes on virtual generated columns are not supported")
		}
	}
	return nil
}
