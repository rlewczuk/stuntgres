package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// Names are namespace-wide only when generated. Explicit names are relation-local.
func constraintNames(read CatalogRead, namespace ids.ObjectAddress, candidate *catalog.Relation, reader ...definitionReader) map[string]bool {
	names := make(map[string]bool)
	descriptors := func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address, descriptor := range read.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
	}
	if len(reader) != 0 && reader[0] != nil {
		descriptors = reader[0].descriptors()
	}
	for _, descriptor := range descriptors {
		if candidate != nil && descriptor.Address() == candidate.Address() {
			descriptor = candidate
		}
		if domain, ok := descriptor.(*typeDescriptor); ok && domain.ObjectNamespace == namespace {
			for _, constraint := range domain.DomainChecks {
				names[constraint.Name] = true
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Namespace() == namespace {
			for _, constraint := range relation.NotNullConstraints {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.CheckConstraints {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.UniqueConstraints {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.ExclusionConstraints {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.ForeignKeys {
				names[constraint.Name] = true
			}
		}
	}
	return names
}

// Explicit names are relation-local and span every admitted constraint kind.
// Keep this allocation-free lookup shared by NN additions and FK attachment.
func relationConstraintNameExists(d *catalog.Relation, name string) bool {
	for _, c := range d.NotNullConstraints {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.CheckConstraints {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.UniqueConstraints {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.ExclusionConstraints {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.ForeignKeys {
		if c.Name == name {
			return true
		}
	}
	return false
}

func generatedConstraintName(table, column, base string, used map[string]bool) string {
	for suffix := 0; ; suffix++ {
		label := base
		if suffix != 0 {
			label += strconv.Itoa(suffix)
		}
		left, right := len(table), len(column)
		separators := 1
		if column != "" {
			separators++
		}
		for left+right > 63-len(label)-separators {
			if left > right {
				left--
			} else {
				right--
			}
		}
		for left > 0 && left < len(table) && !utf8.RuneStart(table[left]) {
			left--
		}
		for right > 0 && right < len(column) && !utf8.RuneStart(column[right]) {
			right--
		}
		name := table[:left] + "_"
		if column != "" {
			name += column[:right] + "_"
		}
		name += label
		if !used[name] {
			return name
		}
	}
}

// LIKE contributes copied identities at its original table-element position.
type createLikeConstraint struct {
	Element int
	catalog.NotNullConstraint
}

// Column-local structure is validated as CREATE reaches each typed column,
// before later elements or any default-expression cooking.
type createColumnDeclaration struct {
	columnNotNull    bool
	columnName       string
	notNull          bool
	name             string
	defaultExpr      parser.Expr
	copiedDefault    bool
	identity         *parser.IdentityDefinition
	generation       *parser.GenerationDefinition
	copiedGeneration bool
	serial           bool
}

func bindCreateColumnDeclaration(column *parser.Column, table string) (createColumnDeclaration, error) {
	var declaration createColumnDeclaration
	declaration.serial = serialTypeName(column.TypeName) != ""
	seen := declaration.serial
	declaration.columnNotNull = declaration.serial
	for _, constraint := range column.Constraints {
		if constraint.Check != nil || constraint.ForeignKey != nil {
			continue
		}
		if constraint.Unique != nil {
			if !constraint.Unique.Primary {
				continue
			}
			// PK implies local NN, but its name belongs only to the index
			// constraint. An explicit NN name remains independent.
			constraint.NotNull = true
			constraint.Name = parser.Identifier{}
		}
		if constraint.Generation != nil {
			generation := constraint.Generation
			if generation.Mode != parser.IdentityAlways {
				return declaration, insertError("42601", "for a generated column, GENERATED ALWAYS must be specified", generation.ModePos)
			}
			conflict := ""
			if declaration.generation != nil {
				conflict = "multiple generation clauses"
			}
			if declaration.defaultExpr != nil || declaration.serial {
				conflict = "both default and generation expression"
			}
			if declaration.identity != nil {
				conflict = "both identity and generation expression"
			}
			if conflict != "" {
				return declaration, insertError("42601", fmt.Sprintf(`%s specified for column "%s" of table "%s"`, conflict, column.Name, table), constraint.Pos)
			}
			declaration.generation = generation
			continue
		}
		if constraint.Identity != nil {
			if declaration.generation != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both identity and generation expression specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.identity != nil {
				return declaration, insertError("42601", fmt.Sprintf(`multiple identity specifications for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.defaultExpr != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both default and identity specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			declaration.identity = constraint.Identity
			constraint.NotNull = true
		}
		if constraint.Default != nil {
			if declaration.generation != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both default and generation expression specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.identity != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both default and identity specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.defaultExpr != nil {
				return declaration, insertError("42601", fmt.Sprintf(`multiple default values specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			declaration.defaultExpr = constraint.Default
			continue
		}
		if seen && declaration.columnNotNull != constraint.NotNull {
			return declaration, insertError("42601", fmt.Sprintf(`conflicting NULL/NOT NULL declarations for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
		}
		name := constraint.Name.Text
		if constraint.NotNull && declaration.columnName != "" && name != "" && declaration.columnName != name {
			return declaration, sqlError("XX000", fmt.Sprintf(`conflicting not-null constraint names "%s" and "%s"`, declaration.columnName, name))
		}
		seen, declaration.columnNotNull = true, constraint.NotNull
		if constraint.NotNull && name != "" {
			declaration.columnName = name
		}
	}
	if declaration.serial {
		if declaration.defaultExpr != nil {
			return declaration, sqlError("42601", fmt.Sprintf(`multiple default values specified for column "%s" of table "%s"`, column.Name, table))
		}
		if declaration.identity != nil {
			return declaration, sqlError("42601", fmt.Sprintf(`both default and identity specified for column "%s" of table "%s"`, column.Name, table))
		}
	}
	return declaration, nil
}

func bindCreateColumnIndex(columns []ColumnDescriptor) (map[string]int, error) {
	index := make(map[string]int, len(columns))
	for i, c := range columns {
		if _, exists := index[c.Name]; exists {
			return nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, c.Name))
		}
		if isSystemColumn(c.Name) {
			return nil, sqlError("42701", fmt.Sprintf("column name %q conflicts with a system column name", c.Name))
		}
		index[c.Name] = i
	}
	return index, nil
}

func bindCreateNotNull(s *parser.CreateTable, columns []ColumnDescriptor, declarations []createColumnDeclaration, inherited []createLikeConstraint, uniques []createUniqueConstraint, index map[string]int) ([]catalog.NotNullConstraint, error) {
	table := s.Name.Parts[len(s.Name.Parts)-1].Text
	// Retain first occurrence order while merging later declarations' names.
	var constraints []catalog.NotNullConstraint
	merge := func(slot int, name string) error {
		old := &declarations[slot]
		if old.name != "" && name != "" && old.name != name {
			return sqlError("42601", fmt.Sprintf(`conflicting not-null constraint names "%s" and "%s"`, old.name, name))
		}
		if !old.notNull {
			constraints = append(constraints, catalog.NotNullConstraint{Attribute: int32(slot + 1)})
		}
		old.notNull = true
		if name != "" {
			old.name = name
		}
		return nil
	}
	inheritedIndex := 0
	for elementIndex, element := range s.Elements {
		if c := element.Column; c != nil {
			slot := index[c.Name]
			if declarations[slot].columnNotNull {
				if err := merge(slot, declarations[slot].columnName); err != nil {
					return nil, err
				}
			}
		} else if constraint := element.NotNull; constraint != nil {
			if isSystemColumn(constraint.Column.Text) {
				return nil, sqlError("0A000", fmt.Sprintf(`cannot add not-null constraint on system column "%s"`, constraint.Column.Text))
			}
			slot, exists := index[constraint.Column.Text]
			if !exists {
				return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, constraint.Column.Text, table))
			}
			if err := merge(slot, constraint.Name.Text); err != nil {
				return nil, err
			}
		} else {
			for inheritedIndex < len(inherited) && inherited[inheritedIndex].Element == elementIndex {
				constraint := inherited[inheritedIndex]
				if err := merge(int(constraint.Attribute-1), constraint.Name); err != nil {
					return nil, err
				}
				inheritedIndex++
			}
		}
	}
	// Table-form PK adds NN only after the original column/table/LIKE NN
	// merge. Explicit NULL in a column therefore does not conflict here.
	for _, constraint := range uniques {
		if !constraint.Primary {
			continue
		}
		for _, attribute := range constraint.Attributes {
			if err := merge(int(attribute-1), ""); err != nil {
				return nil, err
			}
		}
	}
	for i := range declarations {
		columns[i].Nullable = !declarations[i].notNull
	}
	for i := range constraints {
		constraints[i].Name = declarations[constraints[i].Attribute-1].name
	}
	return constraints, nil
}

func nameCreateNotNull(d *catalog.Relation, read CatalogRead) error {
	if len(d.NotNullConstraints) == 0 {
		return nil
	}
	used := constraintNames(read, d.Namespace(), nil)
	// A local false entry was generated; true entries were explicitly named.
	local := make(map[string]bool, len(d.NotNullConstraints))
	for i := range d.NotNullConstraints {
		constraint := &d.NotNullConstraints[i]
		if constraint.Name == "" {
			constraint.Name = generatedConstraintName(d.Name(), d.Columns[constraint.Attribute-1].Name, "not_null", used)
			local[constraint.Name] = false
		} else {
			if explicit, exists := local[constraint.Name]; exists {
				if explicit {
					return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, constraint.Name, d.Name()))
				}
				const index = "pg_constraint_conrelid_contypid_conname_index"
				diagnostic := sqlError("23505", `duplicate key value violates unique constraint "`+index+`"`).(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", d.Address().ObjectID, constraint.Name)
				diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = "pg_catalog", "pg_constraint", index
				return diagnostic
			}
			local[constraint.Name] = true
		}
		used[constraint.Name] = true
	}
	return nil
}
