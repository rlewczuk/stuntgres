package table

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DeparseInputs is what the pg_get_*def, pg_get_expr and
// pg_get_serial_sequence inquiries read: the statement's catalog snapshot
// and its presentation settings. The root builds it from the statement's
// inquiry snapshot.
type DeparseInputs struct {
	Catalog            catalog.Read
	Path               []ids.ObjectAddress
	Output             sqltype.OutputEnvironment
	NonstandardStrings bool
	// Database qualifies three-part pg_get_serial_sequence names.
	Database string
	// Temporary is the session's TEMP namespace; only index deparse reads it.
	Temporary func() ids.ObjectAddress
	// ResolveRelation resolves a TEXT relation name as a utility lookup.
	ResolveRelation func(parser.ObjectName) (ids.ObjectRef, error)
	// BuiltinViewDefinition renders explicitly owned default or pretty fragments
	// from the same captured authority as catalog sources. The bool selects pretty;
	// empty text means no definition is owned for the requested format.
	BuiltinViewDefinition func(context.Context, catalog.Read, *catalog.Relation, bool) (string, error)
}

// Deparse answers one deparse inquiry over in.
func Deparse(c *exec.Context, in DeparseInputs, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if signature == functions.DeparseSerialSequence {
		return serialSequence(c, in, values, result)
	}
	view := in.Catalog
	pretty := false
	if prettyIndex := signature.PrettyIndex(); prettyIndex >= 0 {
		var err error
		pretty, err = values[prettyIndex].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	renderer := stored.Deparser{View: view, Pretty: pretty, Path: in.Path,
		Output: in.Output, NonstandardStrings: in.NonstandardStrings}
	idx := 1
	if signature != functions.DeparseExpr && signature != functions.DeparseExprPretty {
		idx = 0
	}
	if signature >= functions.DeparseIndex {
		return deparseRelation(c, in, signature, values, result, pretty)
	}
	id, err := values[idx].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	var text string
	if signature == functions.DeparseConstraint || signature == functions.DeparseConstraintPretty {
		found := false
		for _, descriptor := range view.Descriptors() {
			if domain, ok := descriptor.(*catalog.Type); ok {
				if domain.DomainNotNull != nil && domain.DomainNotNull.OID == ids.OID(id) {
					found, text = true, "NOT NULL"
				}
				for _, def := range domain.DomainChecks {
					if def.OID != ids.OID(id) {
						continue
					}
					found = true
					rendered, err := renderer.SQL(c.Context, def.Expression, stored.DeparseParent{})
					if err != nil {
						return sqltype.Value{}, err
					}
					text = "CHECK (" + rendered + ")"
					if def.NotValid {
						text += " NOT VALID"
					}
				}
				continue
			}
			relation, ok := descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			for _, def := range relation.Constraints.NotNull {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				name, err := renderer.Attribute(relation, def.Attribute)
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "NOT NULL " + name
				if def.NoInherit {
					text += " NO INHERIT"
				}
				if def.NotValid {
					text += " NOT VALID"
				}
			}
			for _, def := range relation.Constraints.Check {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				renderer.Relation = relation
				rendered, err := renderer.SQL(c.Context, def.Expression, stored.DeparseParent{})
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "CHECK (" + rendered + ")"
				if def.NoInherit {
					text += " NO INHERIT"
				}
				if def.NotValid {
					text += " NOT VALID"
				}
			}
			for _, def := range relation.Constraints.Unique {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				text, err = renderer.UniqueConstraintSQL(relation, &def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.ForeignKeys {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				text, err = renderer.ForeignKeyConstraintSQL(relation, def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.Exclusion {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				idx, ok := view.Relation(def.Index)
				if !ok {
					return sqltype.Value{}, fmt.Errorf("stuntgres: missing exclusion backing index")
				}
				text, err = index.DefinitionSQL(c.Context, view, idx, index.Presentation{Exclusion: true, Pretty: pretty, Path: in.Path, Output: in.Output})
				if err != nil {
					return sqltype.Value{}, err
				}
			}
		}
		if !found {
			return sqltype.Null(result), nil
		}
	} else {
		carrier, err := values[0].Tree()
		if err != nil {
			return sqltype.Value{}, err
		}
		var relation *catalog.Relation
		if id != 0 {
			var ok bool
			relation, ok = view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)})
			if !ok {
				return sqltype.Null(result), nil
			}
		}
		renderer.Relation = relation
		switch tree := carrier.(type) {
		case plan.BoundTree:
			text, err = renderer.SQL(c.Context, tree.Expression, stored.DeparseParent{})
		case plan.ExpressionListTree:
			renderer.RoutineDeclaration = true
			var out strings.Builder
			for i := range tree.Len() {
				if i != 0 {
					out.WriteString(", ")
				}
				var part string
				part, err = renderer.SQL(c.Context, tree.At(i), stored.DeparseParent{})
				if err != nil {
					break
				}
				out.WriteString(part)
			}
			text = out.String()
		default:
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign structural tree")
		}
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(result, text), nil
}

func deparseRelation(c *exec.Context, in DeparseInputs, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor, pretty bool) (sqltype.Value, error) {
	view := in.Catalog
	var address ids.ObjectAddress
	if signature == functions.DeparseViewText || signature == functions.DeparseViewTextPretty {
		text, err := values[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		name, err := parser.ParseRegclassName(text)
		if err != nil || len(name.Parts) > 2 {
			return sqltype.Value{}, sqlError("42602", "invalid name syntax")
		}
		for i := range name.Parts {
			if name.Parts[i].Text == "" {
				return sqltype.Value{}, sqlError("42602", "invalid name syntax")
			}
			name.Parts[i].Pos = parser.Position{}
		}
		ref, err := in.ResolveRelation(name)
		if err != nil {
			return sqltype.Value{}, err
		}
		address = ref.Address
	} else {
		id, err := values[0].OID()
		if err != nil {
			return sqltype.Value{}, err
		}
		address = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)}
	}
	relation, ok := view.Relation(address)
	if !ok {
		return sqltype.Null(result), nil
	}
	var text string
	var err error
	if signature == functions.DeparseIndex || signature == functions.DeparseIndexColumn {
		if relation.SQLIndex == nil {
			return sqltype.Null(result), nil
		}
		var column int64
		if signature == functions.DeparseIndexColumn {
			column, err = values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		text, err = index.DefinitionSQL(c.Context, view, relation, index.Presentation{Column: column, Pretty: pretty, Path: in.Path, Temporary: in.Temporary(), Output: in.Output})
	} else {
		if relation.View == nil {
			if in.BuiltinViewDefinition == nil || signature != functions.DeparseViewOID && signature != functions.DeparseViewText && signature != functions.DeparseViewOIDPretty && signature != functions.DeparseViewTextPretty {
				return sqltype.Null(result), nil
			}
			text, err := in.BuiltinViewDefinition(c.Context, view, relation, pretty)
			if err != nil {
				return sqltype.Value{}, err
			}
			if text == "" {
				return sqltype.Null(result), nil
			}
			return sqltype.String(result, text+";"), nil
		}
		var wrap int64
		if signature == functions.DeparseViewWrap {
			pretty = true
			wrap, err = values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		text, err = stored.ViewOf(relation).SelectSQL(c.Context, view, stored.Presentation{Deparse: true, Pretty: pretty, Wrap: wrap, Path: in.Path, Columns: relation.Columns, Output: in.Output, NonstandardStrings: in.NonstandardStrings})
		text += ";"
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, text), nil
}

// serialSequence reads the statement's catalog, not defaults or generator
// state. AUTO (serial/manual) and INTERNAL (identity) share the same owned address.
func serialSequence(c *exec.Context, in DeparseInputs, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	view := in.Catalog
	table, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	column, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	name, err := parser.ParseRegclassName(table)
	if err != nil {
		return sqltype.Value{}, sqlError("42602", "invalid name syntax")
	}
	name, err = regobj.ClassQualifiedName(name, table, in.Database)
	if err != nil {
		return sqltype.Value{}, err
	}
	for i := range name.Parts {
		// These offsets belong to TEXT input, not the outer SQL statement.
		name.Parts[i].Pos = parser.Position{}
	}
	ref, err := in.ResolveRelation(name)
	if err != nil {
		return sqltype.Value{}, err
	}
	relation := view.MustRelation(ref.Address)
	owner := ref.Address
	for i, descriptor := range relation.Columns {
		if descriptor.Name == column {
			owner.SubID = relation.Attributes[i]
			break
		}
	}
	if owner.SubID == 0 {
		if catalog.IsSystemColumn(column) {
			switch relation.RelationKind {
			case catalog.TableRelation, catalog.SequenceRelation, catalog.CatalogRelation, catalog.IndexRelation:
				return sqltype.Null(result), nil
			}
		}
		return sqltype.Value{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, relation.Name()))
	}
	for _, descriptor := range view.Descriptors() {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		sequence, ok := descriptor.(*catalog.Relation)
		if !ok || sequence.RelationKind != catalog.SequenceRelation || sequence.Sequence == nil || sequence.Sequence.Owned != owner {
			continue
		}
		namespace, ok := view.Namespace(sequence.Namespace())
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing owned sequence namespace")
		}
		return sqltype.String(result, catalog.Identifier(namespace.Name())+"."+catalog.Identifier(sequence.Name())), nil
	}
	return sqltype.Null(result), nil
}
