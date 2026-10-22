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

func DefaultClassError(name, method string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704",
		Message: fmt.Sprintf(`data type %s has no default operator class for access method "%s"`, name, method),
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

func ValidateHashKeyDirection(key parser.IndexKey, methodName string) error {
	if key.DirectionExplicit {
		return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support ASC/DESC options`, methodName))
	}
	if key.Nulls != parser.NullsDefault {
		return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support NULLS FIRST/LAST options`, methodName))
	}
	return nil
}

// Resolve once from the captured catalog, retaining SQL identity separately
// from physical validation. Absent advertised-unsupported builtins retain their
// existing clause diagnostics; no unknown method defaults to B-tree.
func ResolveIndexMethod(view catalog.Read, s *parser.CreateIndex) (*catalog.AccessMethod, error) {
	method, exists := view.LookupAccessMethod(s.Method)
	if !exists {
		return nil, unresolvedIndexMethod(s)
	}
	physical, ok := method.IndexImplementation()
	if !ok {
		return nil, sqlError("55000", fmt.Sprintf(`access method "%s" is not of type INDEX`, method.Name()))
	}
	if err := validateIndexMethod(s, physical, method.Name()); err != nil {
		return nil, err
	}
	return method, nil
}
func validateIndexMethod(s *parser.CreateIndex, physical catalog.IndexMethod, methodName string) error {
	switch physical {
	case catalog.BtreeMethod:
		return nil
	case catalog.HashMethod:
		if s.Unique {
			return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support unique indexes`, methodName))
		}
		if len(s.Include) > 0 {
			return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support included columns`, methodName))
		}
		if len(s.Keys) > 1 {
			return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support multicolumn indexes`, methodName))
		}
		if s.Predicate != nil {
			return sqlError("0A000", "partial indexes are unsupported")
		}
		return nil
	case catalog.GistMethod:
		if s.Unique {
			return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support unique indexes`, methodName))
		}
		for _, key := range s.Keys {
			if key.DirectionExplicit {
				return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support ASC/DESC options`, methodName))
			}
			if key.Nulls != parser.NullsDefault {
				return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support NULLS FIRST/LAST options`, methodName))
			}
		}
		return nil
	default:
		return sqlError("42704", fmt.Sprintf(`access method "%s" does not exist`, methodName))
	}
}

// Only unresolved advertised-unsupported builtin names retain the historical
// native capability boundary. A registered descriptor always wins first.
func unresolvedIndexMethod(s *parser.CreateIndex) error {
	switch s.Method {
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
