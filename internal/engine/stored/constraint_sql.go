package stored

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (r Deparser) ForeignKeyConstraintSQL(child *catalog.Relation, f catalog.ForeignKeyConstraint) (string, error) {
	return r.ForeignKeyConstraintSQLWithPrimary(child, f, false)
}

// Implicit primary references preserve the captured provider when its deferred
// construction follows another UNIQUE over the same attributes.
func (r Deparser) ForeignKeyConstraintSQLWithPrimary(child *catalog.Relation, f catalog.ForeignKeyConstraint, primary bool) (string, error) {
	view, path := r.View, r.Path
	parent, ok := view.Relation(f.Target)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing foreign-key deparse target")
	}
	local := make([]string, len(f.Attributes))
	var referenced []string
	if !primary {
		referenced = make([]string, len(f.Referenced))
	}
	for i, a := range f.Attributes {
		var err error
		local[i], err = r.Attribute(child, a)
		if err != nil {
			return "", err
		}
		if !primary {
			referenced[i], err = r.Attribute(parent, f.Referenced[i])
			if err != nil {
				return "", err
			}
		}
	}
	name := r.Identifier(parent.Name())
	visible := false
	for _, namespace := range path {
		if a, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: parent.Name()}); ok {
			visible = a == parent.Address()
			break
		}
	}
	if !visible {
		name = r.Identifier(view.Get(parent.Namespace()).Name()) + "." + name
	}
	text := "FOREIGN KEY (" + strings.Join(local, ", ") + ") REFERENCES " + name
	if !primary {
		text += "(" + strings.Join(referenced, ", ") + ")"
	}
	if f.Full {
		text += " MATCH FULL"
	}
	for _, direction := range []struct {
		name   string
		action parser.ForeignKeyAction
	}{{"UPDATE", f.UpdateAction}, {"DELETE", f.DeleteAction}} {
		if direction.action == parser.ForeignKeyNoAction {
			continue
		}
		action, err := foreignKeyActionSQL(direction.action)
		if err != nil {
			return "", err
		}
		text += " ON " + direction.name + " " + action
		if direction.name == "DELETE" && len(f.DeleteAttributes) != 0 {
			labels := make([]string, len(f.DeleteAttributes))
			for i, a := range f.DeleteAttributes {
				labels[i], err = DeparseAttribute(child, a)
				if err != nil {
					return "", err
				}
			}
			text += " (" + strings.Join(labels, ", ") + ")"
		}
	}
	if f.NotValid {
		text += " NOT VALID"
	}
	return text, nil
}

func foreignKeyActionSQL(action parser.ForeignKeyAction) (string, error) {
	switch action {
	case parser.ForeignKeyNoAction:
		return "NO ACTION", nil
	case parser.ForeignKeyRestrict:
		return "RESTRICT", nil
	case parser.ForeignKeyCascade:
		return "CASCADE", nil
	case parser.ForeignKeySetNull:
		return "SET NULL", nil
	case parser.ForeignKeySetDefault:
		return "SET DEFAULT", nil
	default:
		return "", fmt.Errorf("stuntgres: invalid foreign-key action")
	}
}

func (r Deparser) UniqueConstraintSQL(parent *catalog.Relation, c *catalog.UniqueConstraint) (string, error) {
	index, ok := r.View.Relation(c.Index)
	if !ok || index.SQLIndex == nil {
		return "", fmt.Errorf("stuntgres: missing unique deparse index")
	}
	labels := make([]string, len(c.Attributes)+len(index.SQLIndex.Include))
	for i, attribute := range c.Attributes {
		label, err := r.Attribute(parent, attribute)
		if err != nil {
			return "", err
		}
		labels[i] = label
	}
	prefix := "UNIQUE"
	if c.Primary {
		prefix = "PRIMARY KEY"
	} else if c.NullsNotDistinct {
		prefix += " NULLS NOT DISTINCT"
	}
	text := prefix + " (" + strings.Join(labels[:len(c.Attributes)], ", ") + ")"
	if len(index.SQLIndex.Include) != 0 {
		for i, attribute := range index.SQLIndex.Include {
			label, err := r.Attribute(parent, attribute)
			if err != nil {
				return "", err
			}
			labels[len(c.Attributes)+i] = label
		}
		text += " INCLUDE (" + strings.Join(labels[len(c.Attributes):], ", ") + ")"
	}
	return text, nil
}
