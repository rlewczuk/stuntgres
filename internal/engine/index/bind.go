package index

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// LookupClass resolves the current descriptor by namespace and exact method.
func LookupClass(h KeyHost, auth ddl.Authority, raw parser.ObjectName, methodOID ids.OID) (sqltype.IndexClass, error) {
	method, ok := h.AccessView(auth).Catalog.AccessMethod(methodOID)
	if !ok {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: unknown index method identity")
	}
	path, err := h.SearchNamespaces(auth, raw, ids.OperatorClassCatalogID)
	if err != nil {
		return sqltype.IndexClass{}, SemanticError(err)
	}
	view := h.AccessView(auth).Catalog
	for _, namespace := range path {
		if d, ok := view.LookupOperatorClass(namespace, methodOID, raw.Parts[len(raw.Parts)-1].Text); ok {
			if c, ok := view.IndexClass(d.Address().ObjectID); ok {
				return c, nil
			}
		}
	}
	return sqltype.IndexClass{}, sqlError("42704", fmt.Sprintf(`operator class "%s" does not exist for access method "%s"`, catalog.ObjectNameText(raw), method.Name()))
}

// DefaultClassDiagnostic presents a propagated exact ambiguity through the
// supplied executing authority. Successful lookup and unrelated errors do not
// capture another path or allocate a diagnostic.
func DefaultClassDiagnostic(h KeyHost, auth ddl.Authority, err error) error {
	if err == nil {
		return nil
	}
	var ambiguity *catalog.DefaultClassAmbiguity
	if !errors.As(err, &ambiguity) {
		return err
	}
	path, pathErr := auth.ReferencePath()
	if pathErr != nil {
		return pathErr
	}
	return catalog.RenderDefaultClassAmbiguity(err, catalog.TypeDiagnosticNamer(h.AccessView(auth).Catalog, path))
}

// BindKey binds one index key of type u: collation, operator class and the
// ordering options method allows.
func BindKey(h KeyHost, auth ddl.Authority, u sqltype.Usage, raw parser.IndexKey, methodOID ids.OID) (catalog.IndexKey, error) {
	key := catalog.IndexKey{Desc: raw.Desc, NullsFirst: raw.Nulls == parser.NullsFirst || raw.Nulls == parser.NullsDefault && raw.Desc}
	fact, ok := h.AccessView(auth).Catalog.AccessMethod(methodOID)
	if !ok {
		return key, fmt.Errorf("stuntgres: unknown index method identity")
	}
	method, indexKind := fact.IndexImplementation()
	if !indexKind {
		return key, sqlError("55000", fmt.Sprintf(`access method "%s" is not of type INDEX`, fact.Name()))
	}
	if raw.Collation != nil {
		id, err := h.LookupCollation(*raw.Collation)
		if err != nil {
			return key, SemanticError(err)
		}
		if u.Type.Usage().Collation.ID == 0 {
			return key, sqlError("42804", "collations are not supported by type "+u.Type.DiagnosticName())
		}
		key.Collation = id
		d := h.AccessView(auth).Catalog.MustCollation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)})
		key.CollationRef, err = d.Usage()
		if err != nil {
			return key, err
		}
	}
	// Preserve the existing unsupported-input boundary without manufacturing a
	// diagnostic-only class or consulting a static name/default fallback.
	typeName := u.Type.DiagnosticName()
	// User-defined type spelling follows the current private catalog and search
	// path, just like other catalog diagnostics; builtins retain SQL spellings.
	if u.Type.IsUserDefined() {
		view := h.AccessView(auth).Catalog
		if descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}); ok {
			path, err := h.SearchNamespaces(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: descriptor.Name()}}}, ids.TypeClassID)
			if err != nil {
				return key, err
			}
			typeName = catalog.TypeDiagnosticNameAt(view, nil, descriptor, path)
		}
	}
	base := u.Type.BaseType()
	if method == catalog.HashMethod && base.IsArray() || method == catalog.BtreeMethod && base.ID() == sqltype.NameArray {
		return key, sqlError("0A000", "index operator class is unsupported")
	}
	var c sqltype.IndexClass
	var exists bool
	if raw.Opclass != nil {
		var err error
		c, err = LookupClass(h, auth, *raw.Opclass, methodOID)
		if err != nil {
			return key, err
		}
		if !c.Accepts(u.Type) {
			return key, sqlError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, catalog.ObjectNameText(*raw.Opclass), typeName))
		}
		key.ExplicitClass = true
		exists = true
	} else {
		var err error
		c, exists, err = h.AccessView(auth).Catalog.DefaultIndexClass(methodOID, u.Type)
		if err != nil {
			return key, DefaultClassDiagnostic(h, auth, err)
		}
	}
	if !exists {
		return key, DefaultClassError(typeName, fact.Name())
	}
	var supportErr error
	c, supportErr = routine.BindClassSupport(h.AccessView(auth).Catalog, c)
	if supportErr != nil {
		return key, supportErr
	}
	key.Class = c
	if method == catalog.GistMethod {
		if err := ValidateUnorderedKeyDirection(raw, fact.Name()); err != nil {
			return key, err
		}
		return key, nil
	}
	if method == catalog.BrinMethod {
		if err := ValidateUnorderedKeyDirection(raw, fact.Name()); err != nil {
			return key, err
		}
		_, err := sqltype.BindBrinComparison(KeyCollation(u, key), c)
		return key, err
	}
	if method == catalog.HashMethod {
		if err := ValidateUnorderedKeyDirection(raw, fact.Name()); err != nil {
			return key, err
		}
		_, err := sqltype.BindIndexHash(KeyCollation(u, key), c)
		return key, err
	}
	_, err := sqltype.BindIndexOrdering(KeyCollation(u, key), c)
	return key, err
}

// Validate released system attribute identity without manufacturing an owned
// storage descriptor or comparator. Final system prohibition stays after all
// attributes, so a later ordinary missing column can still win.
func validateSystemKey(h KeyHost, auth ddl.Authority, raw parser.IndexKey, methodOID ids.OID) error {
	id, name := sqltype.SystemAttributeType(raw.Column.Text)
	if raw.Collation != nil {
		_, err := h.LookupCollation(*raw.Collation)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); !ok || d.Code != "0A000" {
				return SemanticError(err)
			}
		}
		return sqlError("42804", "collations are not supported by type "+name)
	}
	if raw.Opclass != nil {
		class, err := LookupClass(h, auth, *raw.Opclass, methodOID)
		if err != nil {
			return err
		}
		if !class.AcceptsID(id) {
			return sqlError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, catalog.ObjectNameText(*raw.Opclass), name))
		}
	} else {
		_, exists, err := h.AccessView(auth).Catalog.DefaultIndexClassID(methodOID, id)
		if err != nil {
			return DefaultClassDiagnostic(h, auth, err)
		}
		if !exists {
			fact, _ := h.AccessView(auth).Catalog.AccessMethod(methodOID)
			return DefaultClassError(name, fact.Name())
		}
	}
	return nil
}

// BindAttributes binds the key and INCLUDE columns of s on parent: the index
// definition without its predicate, the index columns, their attribute
// numbers and the name lookup.
func BindAttributes(h Host, auth ddl.Authority, input sqltype.InputEnvironment, parent *catalog.Relation, s *parser.CreateIndex) (*catalog.IndexDefinition, []catalog.Column, []int32, map[string]int, error) {
	method, ok := h.AccessView(auth).Catalog.LookupAccessMethod(s.Method)
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("stuntgres: unknown index method identity")
	}
	if _, indexKind := method.IndexImplementation(); !indexKind {
		return nil, nil, nil, nil, sqlError("55000", fmt.Sprintf(`access method "%s" is not of type INDEX`, method.Name()))
	}
	return bindAttributes(h, auth, input, parent, s, method)
}

func bindAttributes(h Host, auth ddl.Authority, input sqltype.InputEnvironment, parent *catalog.Relation, s *parser.CreateIndex, method *catalog.AccessMethod) (*catalog.IndexDefinition, []catalog.Column, []int32, map[string]int, error) {
	total := len(s.Keys) + len(s.Include)
	physical, _ := method.IndexImplementation()
	d := &catalog.IndexDefinition{Method: physical, MethodOID: method.Address().ObjectID, Parent: parent.Address(), Keys: make([]catalog.IndexKey, len(s.Keys)), Include: make([]int32, len(s.Include)), Ready: true, Valid: true, Live: true}
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
			derived, name, err := h.BindIndexExpression(auth, parent, raw.Expression, nil, input)
			if err != nil {
				return fail(err)
			}
			label = name
			key, err := BindKey(h, auth, derived.Execution.ResultUsage(), raw, d.MethodOID)
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
			columns[i].Usage = KeyCollation(derived.Execution.ResultUsage(), key)
		} else {
			if catalog.IsSystemColumn(raw.Column.Text) {
				system = true
				if included {
					if err := ValidateIndexInclude(raw); err != nil {
						return fail(err)
					}
				} else if err := validateSystemKey(h, auth, raw, d.MethodOID); err != nil {
					return fail(err)
				}
				if !included && (d.Method == catalog.HashMethod || d.Method == catalog.BrinMethod || d.Method == catalog.GistMethod) {
					if err := ValidateUnorderedKeyDirection(raw, method.Name()); err != nil {
						return fail(err)
					}
				}
				continue
			}
			slot, exists := parent.Index[raw.Column.Text]
			if !exists {
				return fail(sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, raw.Column.Text)))
			}
			if err := RejectVirtualUses(parent, []int{slot}); err != nil {
				return fail(err)
			}
			columns[i] = parent.Columns[slot]
			if included {
				if err := ValidateIndexInclude(raw); err != nil {
					return fail(err)
				}
				d.Include[i-len(s.Keys)] = parent.Attributes[slot]
			} else {
				key, err := BindKey(h, auth, columns[i].Usage, raw, d.MethodOID)
				if err != nil {
					return fail(err)
				}
				key.Attribute = parent.Attributes[slot]
				d.Keys[i] = key
				columns[i].Usage = KeyCollation(columns[i].Usage, key)
			}
		}
		baseLabel := label
		for suffix := 1; ; suffix++ {
			if _, exists := lookup[label]; !exists {
				break
			}
			label = ColumnLabel(baseLabel, suffix)
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
