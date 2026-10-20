package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func indexKeyCollation(u sqltype.Usage, key catalog.IndexKey) sqltype.Usage {
	if key.Collation != 0 {
		u.Collation = sqltype.CollationRef{ID: key.Collation, Derivation: sqltype.Default}
		if key.CollationRef.Valid() && key.CollationRef.ID == key.Collation {
			u.Collation = key.CollationRef
		}
	}
	return u
}

func reconstructIndexKey(key catalog.IndexKey, oldUsage, newUsage sqltype.Usage, method catalog.IndexMethod) catalog.IndexKey {
	oldDefault, _ := method.DefaultClass(oldUsage.Type)
	if key.Class.Name == "" || key.Class == oldDefault {
		key.Class, _ = method.DefaultClass(newUsage.Type)
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
func indexSemanticError(err error) error {
	if d, ok := err.(*sqlstate.Error); ok && d.Position != "" {
		copy := *d
		copy.Position = ""
		return &copy
	}
	return err
}

func (e *Engine) lookupIndexClass(auth AuthorizationView, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error) {
	path, err := e.lookupNamespaces(auth, raw, analyze.OperatorLookup)
	if err != nil {
		return sqltype.IndexClass{}, indexSemanticError(err)
	}
	for i := range path.Count() {
		if path.At(i) == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
			if c, ok := method.LookupClass(raw.Parts[len(raw.Parts)-1].Text); ok {
				return c, nil
			}
		}
	}
	return sqltype.IndexClass{}, sqlError("42704", fmt.Sprintf(`operator class "%s" does not exist for access method "%s"`, objectNameText(raw), method))
}

func (e *Engine) bindIndexKey(op *Operation, auth AuthorizationView, u sqltype.Usage, raw parser.IndexKey, method catalog.IndexMethod) (catalog.IndexKey, error) {
	key := catalog.IndexKey{Desc: raw.Desc, NullsFirst: raw.Nulls == parser.NullsFirst || raw.Nulls == parser.NullsDefault && raw.Desc}
	if raw.Collation != nil {
		id, err := e.lookupCollation(*raw.Collation, op)
		if err != nil {
			return key, indexSemanticError(err)
		}
		if u.Type.Usage().Collation.ID == 0 {
			return key, sqlError("42804", "collations are not supported by type "+u.Type.DiagnosticName())
		}
		key.Collation = id
		d := auth.Catalog.MustCollation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)})
		key.CollationRef, err = d.Usage()
		if err != nil {
			return key, err
		}
	}
	c, exists := method.DefaultClass(u.Type)
	if raw.Opclass != nil {
		var err error
		c, err = e.lookupIndexClass(auth, *raw.Opclass, method)
		if err != nil {
			return key, err
		}
		if !c.Accepts(u.Type) {
			return key, sqlError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, objectNameText(*raw.Opclass), u.Type.DiagnosticName()))
		}
		key.ExplicitClass = true
		exists = true
	}
	if !exists {
		return key, indexDefaultClassError(u.Type.DiagnosticName(), method)
	}
	key.Class = c
	if method == catalog.GistMethod {
		if raw.DirectionExplicit || raw.Nulls != parser.NullsDefault {
			return key, sqlError("0A000", `access method "gist" does not support ordering options`)
		}
		return key, nil
	}
	if method == catalog.HashMethod {
		if err := validateHashKeyDirection(raw); err != nil {
			return key, err
		}
		_, err := sqltype.BindIndexHash(indexKeyCollation(u, key), c)
		return key, err
	}
	_, err := sqltype.BindIndexOrdering(indexKeyCollation(u, key), c)
	return key, err
}

func indexDefaultClassError(name string, method catalog.IndexMethod) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704",
		Message: fmt.Sprintf("data type %s has no default operator class for access method %q", name, method.String()),
		Hint:    "You must specify an operator class for the index or define a default operator class for the data type."}
}

// Validate released system attribute identity without manufacturing an owned
// storage descriptor or comparator. Final system prohibition stays after all
// attributes, so a later ordinary missing column can still win.
func (e *Engine) validateSystemIndexKey(op *Operation, auth AuthorizationView, raw parser.IndexKey, method catalog.IndexMethod) error {
	id, name := sqltype.SystemAttributeType(raw.Column.Text)
	if raw.Collation != nil {
		_, err := e.lookupCollation(*raw.Collation, op)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); !ok || d.Code != "0A000" {
				return indexSemanticError(err)
			}
		}
		return sqlError("42804", "collations are not supported by type "+name)
	}
	if raw.Opclass != nil {
		class, err := e.lookupIndexClass(auth, *raw.Opclass, method)
		if err != nil {
			return err
		}
		if !class.AcceptsID(id) {
			return sqlError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, objectNameText(*raw.Opclass), name))
		}
	} else if _, exists := method.DefaultClassID(id); !exists {
		return indexDefaultClassError(name, method)
	}
	return nil
}

func validateIndexInclude(raw parser.IndexKey) error {
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

func validateHashKeyDirection(key parser.IndexKey) error {
	if key.DirectionExplicit {
		return sqlError("0A000", `access method "hash" does not support ASC/DESC options`)
	}
	if key.Nulls != parser.NullsDefault {
		return sqlError("0A000", `access method "hash" does not support NULLS FIRST/LAST options`)
	}
	return nil
}

func validateIndexMethod(s *parser.CreateIndex) error {
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

func (e *Engine) bindIndexAttributes(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation, s *parser.CreateIndex) (*catalog.IndexDefinition, []catalog.Column, []int32, map[string]int, error) {
	total := len(s.Keys) + len(s.Include)
	d := &catalog.IndexDefinition{Method: catalog.BtreeMethod, Parent: parent.Address(), Keys: make([]catalog.IndexKey, len(s.Keys)), Include: make([]int32, len(s.Include)), Ready: true, Valid: true, Live: true}
	if s.Method == "hash" {
		d.Method = catalog.HashMethod
	}
	if s.Method == "gist" {
		d.Method = catalog.GistMethod
	}
	columns := make([]catalog.Column, total)
	attributes := make([]int32, total)
	lookup := make(map[string]int, total)
	system := false
	for i := range total {
		included := i >= len(s.Keys)
		var raw parser.IndexKey
		if included {
			raw = s.Include[i-len(s.Keys)]
		} else {
			raw = s.Keys[i]
		}
		fail := func(err error) (*catalog.IndexDefinition, []catalog.Column, []int32, map[string]int, error) {
			return nil, nil, nil, nil, err
		}
		label := raw.Column.Text
		if raw.Expression != nil && included {
			return fail(sqlError("0A000", "expressions are not supported in included columns"))
		}
		if raw.Expression != nil {
			derived, name, err := e.bindIndexExpression(op, input, auth, parent, raw.Expression, nil)
			if err != nil {
				return fail(err)
			}
			label = name
			key, err := e.bindIndexKey(op, auth, derived.Execution.ResultUsage(), raw, d.Method)
			if err != nil {
				return fail(err)
			}
			if column, ok := derived.Definition.(plan.ColumnExpr); ok {
				key.Attribute = column.Origin.Address.SubID
				columns[i] = parent.Columns[column.Slot]
			} else {
				key.Derived = derived
			}
			d.Keys[i] = key
			columns[i].Usage = indexKeyCollation(derived.Execution.ResultUsage(), key)
		} else {
			if isSystemColumn(raw.Column.Text) {
				system = true
				if included {
					if err := validateIndexInclude(raw); err != nil {
						return fail(err)
					}
				} else if err := e.validateSystemIndexKey(op, auth, raw, d.Method); err != nil {
					return fail(err)
				}
				if !included && d.Method == catalog.HashMethod {
					if err := validateHashKeyDirection(raw); err != nil {
						return fail(err)
					}
				}
				continue
			}
			slot, exists := parent.Index[raw.Column.Text]
			if !exists {
				return fail(sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, raw.Column.Text)))
			}
			if err := rejectVirtualIndexUses(parent, []int{slot}); err != nil {
				return fail(err)
			}
			columns[i] = parent.Columns[slot]
			if included {
				if err := validateIndexInclude(raw); err != nil {
					return fail(err)
				}
				d.Include[i-len(s.Keys)] = parent.Attributes[slot]
			} else {
				key, err := e.bindIndexKey(op, auth, columns[i].Usage, raw, d.Method)
				if err != nil {
					return fail(err)
				}
				key.Attribute = parent.Attributes[slot]
				d.Keys[i] = key
				columns[i].Usage = indexKeyCollation(columns[i].Usage, key)
			}
		}
		baseLabel := label
		for suffix := 1; ; suffix++ {
			if _, exists := lookup[label]; !exists {
				break
			}
			label = indexColumnLabel(baseLabel, suffix)
		}
		columns[i].Name = label
		lookup[label] = i
		attributes[i] = int32(i + 1)
	}
	if system {
		return nil, nil, nil, nil, sqlError("0A000", "index creation on system columns is not supported")
	}
	return d, columns, attributes, lookup, nil
}
