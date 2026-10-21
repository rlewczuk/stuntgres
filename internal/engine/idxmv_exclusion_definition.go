package engine

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func validateExclusions(view catalog.Read, parent *catalog.Relation, names map[string]bool) error {
	for _, c := range parent.Constraints.Exclusion {
		index, ok := view.Relation(c.Index)
		if !ok || parent.RelationKind != catalog.TableRelation && parent.Partition == nil || c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || index.SQLIndex == nil || !index.SQLIndex.IsExclusion() || index.Name() != c.Name || index.SQLIndex.Constraint.OID != c.OID || index.SQLIndex.Parent != parent.Address() || len(c.Operators) != len(index.SQLIndex.Keys) || c.InitiallyDeferred && !c.Deferrable {
			return fmt.Errorf("stuntgres: invalid exclusion constraint identity")
		}
		d := index.SQLIndex
		if d.Constraint.Name != c.Name || d.Constraint.Index != c.Index || len(d.Exclusion) != len(c.Operators) || len(c.Operators) == 0 || &d.Exclusion[0] != &c.Operators[0] {
			return fmt.Errorf("stuntgres: invalid exclusion operator ownership")
		}
		names[c.Name] = true
	}
	return nil
}

func exclusionDiagnostic(ctx context.Context, parent, index *catalog.Relation, key, existing []sqltype.Value, schema string, output sqltype.OutputEnvironment, allowed, building bool, catalog catalog.Read) error {
	message := fmt.Sprintf(`conflicting key value violates exclusion constraint "%s"`, index.Name())
	if building {
		message = fmt.Sprintf(`could not create exclusion constraint "%s"`, index.Name())
	}
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23P01", Message: message, Schema: schema, Table: parent.Name(), Constraint: index.Name(), Detail: "Key conflicts with existing key."}
	if building {
		d.Detail = "Key conflicts exist."
	}
	if !allowed {
		return d
	}
	labels := make([]string, len(key))
	values := make([]string, len(key))
	prior := make([]string, len(key))
	for i, value := range key {
		attribute, node, err := indexCatalogKey(parent, index.SQLIndex.Keys[i])
		if err != nil {
			return err
		}
		if node != nil {
			// Index-key syntax groups a top-level operator without imposing the
			// fully parenthesized catalog renderer on casts inside functions.
			labels[i], err = (stored.Deparser{View: catalog, Relation: parent, Pretty: true, Output: output}).SQL(ctx, node, stored.DeparseParent{Precedence: 6})
		} else {
			labels[i], err = stored.DeparseAttribute(parent, attribute)
		}
		if err != nil {
			return err
		}
		values[i], err = value.Type().OutputWithEnvironment(ctx, value, output)
		if err != nil {
			return err
		}
		prior[i], err = existing[i].Type().OutputWithEnvironment(ctx, existing[i], output)
		if err != nil {
			return err
		}
	}
	middle := " conflicts with existing key "
	if building {
		middle = " conflicts with key "
	}
	label := "(" + strings.Join(labels, ", ") + ")"
	d.Detail = "Key " + label + "=(" + strings.Join(values, ", ") + ")" + middle + label + "=(" + strings.Join(prior, ", ") + ")."
	return d
}
