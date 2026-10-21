package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) lookupIndexClass(auth AuthorizationView, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error) {
	path, err := e.lookupNamespaces(auth, raw, analyze.OperatorLookup)
	if err != nil {
		return sqltype.IndexClass{}, index.SemanticError(err)
	}
	for i := range path.Count() {
		if path.At(i) == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
			if c, ok := method.LookupClass(raw.Parts[len(raw.Parts)-1].Text); ok {
				return c, nil
			}
		}
	}
	return sqltype.IndexClass{}, sqlError("42704", fmt.Sprintf(`operator class "%s" does not exist for access method "%s"`, catalog.ObjectNameText(raw), method))
}

func (e *Engine) bindIndexKey(op *session.Operation, auth AuthorizationView, u sqltype.Usage, raw parser.IndexKey, method catalog.IndexMethod) (catalog.IndexKey, error) {
	key := catalog.IndexKey{Desc: raw.Desc, NullsFirst: raw.Nulls == parser.NullsFirst || raw.Nulls == parser.NullsDefault && raw.Desc}
	if raw.Collation != nil {
		id, err := e.lookupCollation(*raw.Collation, op)
		if err != nil {
			return key, index.SemanticError(err)
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
			return key, sqlError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, catalog.ObjectNameText(*raw.Opclass), u.Type.DiagnosticName()))
		}
		key.ExplicitClass = true
		exists = true
	}
	if !exists {
		return key, index.DefaultClassError(u.Type.DiagnosticName(), method)
	}
	key.Class = c
	if method == catalog.GistMethod {
		if raw.DirectionExplicit || raw.Nulls != parser.NullsDefault {
			return key, sqlError("0A000", `access method "gist" does not support ordering options`)
		}
		return key, nil
	}
	if method == catalog.HashMethod {
		if err := index.ValidateHashKeyDirection(raw); err != nil {
			return key, err
		}
		_, err := sqltype.BindIndexHash(index.KeyCollation(u, key), c)
		return key, err
	}
	_, err := sqltype.BindIndexOrdering(index.KeyCollation(u, key), c)
	return key, err
}

// Validate released system attribute identity without manufacturing an owned
// storage descriptor or comparator. Final system prohibition stays after all
// attributes, so a later ordinary missing column can still win.
func (e *Engine) validateSystemIndexKey(op *session.Operation, auth AuthorizationView, raw parser.IndexKey, method catalog.IndexMethod) error {
	id, name := sqltype.SystemAttributeType(raw.Column.Text)
	if raw.Collation != nil {
		_, err := e.lookupCollation(*raw.Collation, op)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); !ok || d.Code != "0A000" {
				return index.SemanticError(err)
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
			return sqlError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, catalog.ObjectNameText(*raw.Opclass), name))
		}
	} else if _, exists := method.DefaultClassID(id); !exists {
		return index.DefaultClassError(name, method)
	}
	return nil
}

func (e *Engine) bindIndexAttributes(op *session.Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation, s *parser.CreateIndex) (*catalog.IndexDefinition, []catalog.Column, []int32, map[string]int, error) {
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
			columns[i].Usage = index.KeyCollation(derived.Execution.ResultUsage(), key)
		} else {
			if catalog.IsSystemColumn(raw.Column.Text) {
				system = true
				if included {
					if err := index.ValidateIndexInclude(raw); err != nil {
						return fail(err)
					}
				} else if err := e.validateSystemIndexKey(op, auth, raw, d.Method); err != nil {
					return fail(err)
				}
				if !included && d.Method == catalog.HashMethod {
					if err := index.ValidateHashKeyDirection(raw); err != nil {
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
				if err := index.ValidateIndexInclude(raw); err != nil {
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
				columns[i].Usage = index.KeyCollation(columns[i].Usage, key)
			}
		}
		baseLabel := label
		for suffix := 1; ; suffix++ {
			if _, exists := lookup[label]; !exists {
				break
			}
			label = index.ColumnLabel(baseLabel, suffix)
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
