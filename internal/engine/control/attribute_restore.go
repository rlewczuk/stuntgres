package control

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
)

// Constructor manifests contain only fresh qualified SQL identities and physical
// positions derived from the parsed statement stream. They never carry authority.
type AttributeDropTarget struct{ Schema, Table string }
type AttributeMember struct {
	Target      AttributeDropTarget
	Number      int32
	Local       bool
	Inheritance int
}
type AttributeDropPair struct {
	Target       AttributeDropTarget
	Column       string
	Recursive    bool
	Participants []AttributeMember
}
type AttributeAddPair struct {
	Target       AttributeDropTarget
	Number       int32
	Participants []AttributeMember
}
type AttributeConstructorPairs struct {
	Creates     map[*parser.CreateTable]AttributeDropTarget
	Drops       map[*parser.AlterTable]AttributeDropPair
	Adds        map[*parser.AlterTable]AttributeAddPair
	Constraints map[*parser.AlterTable]AttributeDropTarget
	Detaches    map[*parser.AlterTable]AttributeDetachPair
	Teardowns   map[*parser.DropTable]AttributeDropTarget
}
type constructorColumn struct {
	Name                        string
	Local, Dropped, Placeholder bool
	Origins                     []AttributeDropTarget
}
type attributeConstructor struct {
	parents        []AttributeDropTarget
	columns        []constructorColumn
	closed, staged bool
}

func attributeTarget(name parser.ObjectName) (AttributeDropTarget, bool) {
	if len(name.Parts) != 2 {
		return AttributeDropTarget{}, false
	}
	return AttributeDropTarget{name.Parts[0].Text, name.Parts[1].Text}, true
}
func AttributePlaceholderName(name string) bool {
	if !strings.HasPrefix(name, AttributePlaceholderStem) {
		return false
	}
	hash, number, ok := strings.Cut(name[len(AttributePlaceholderStem):], "_")
	if !ok || len(hash) == 0 || len(hash) > 16 {
		return false
	}
	word, err := strconv.ParseUint(hash, 16, 64)
	if err != nil || strconv.FormatUint(word, 16) != hash {
		return false
	}
	number, collision, more := strings.Cut(number, "_")
	slot, err := strconv.ParseUint(number, 10, 31)
	if err != nil || slot == 0 || strconv.FormatUint(slot, 10) != number {
		return false
	}
	if more {
		n, err := strconv.ParseUint(collision, 10, 31)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != collision {
			return false
		}
	}
	return true
}
func inertAttributePlaceholder(column *parser.Column) bool {
	if column == nil || !AttributePlaceholderName(column.Name) || len(column.Constraints) != 0 || column.Collation != nil {
		return false
	}
	t := column.TypeName
	return (t.Name == "int4" || t.Name == "text") && t.Schema != nil && t.Schema.Text == "pg_catalog" && len(t.Modifiers) == 0 && len(t.ArrayBounds) == 0 && !t.Setof
}
func generatedAttributeDrop(s *parser.AlterTable) bool {
	if s == nil || s.IfExists || len(s.Name.Parts) != 2 || len(s.Actions) != 1 {
		return false
	}
	a := s.Actions[0]
	return a.Action == parser.DropColumn && AttributePlaceholderName(a.Column.Text) && !a.IfExists && !a.IfNotExists && a.Mode == parser.DropRestrict && a.Definition == nil && a.Identity == nil && a.Using == nil && a.Generation == nil && a.Check == nil && a.Unique == nil && a.ForeignKey == nil && a.Exclusion == nil && a.UsingIndex == nil && len(a.Options) == 0 && a.Constraint.Text == "" && a.Tablespace.Text == "" && len(a.Parent.Parts) == 0 && a.TypeName.Name == "" && a.Collation == nil && !a.NotValid && !a.NoInherit
}
func constructorLive(c *attributeConstructor, name string) int {
	for i, a := range c.columns {
		if !a.Dropped && a.Name == name {
			return i
		}
	}
	return -1
}
func constructorMember(target AttributeDropTarget, c *attributeConstructor, i int) AttributeMember {
	a := c.columns[i]
	return AttributeMember{target, int32(i + 1), a.Local, len(a.Origins)}
}
func constructorInherit(c *attributeConstructor, parent AttributeDropTarget, a constructorColumn) int {
	if i := constructorLive(c, a.Name); i >= 0 {
		if !slices.Contains(c.columns[i].Origins, parent) {
			c.columns[i].Origins = append(c.columns[i].Origins, parent)
		}
		return i
	}
	a.Local = false
	a.Origins = []AttributeDropTarget{parent}
	c.columns = append(c.columns, a)
	return len(c.columns) - 1
}

// PairAttributeConstructors simulates only literal declaration shape. Native
// binding/execution still validates types, dependencies, inheritance and effects.
// Data/final ACL consumption closes column-shape admission. Every terminal is
// paired once with an exact earlier inert constructor, never merely a prefix.
func PairAttributeConstructors(statements []parser.Statement) (AttributeConstructorPairs, error) {
	pairs := AttributeConstructorPairs{Creates: make(map[*parser.CreateTable]AttributeDropTarget), Drops: make(map[*parser.AlterTable]AttributeDropPair), Adds: make(map[*parser.AlterTable]AttributeAddPair), Constraints: make(map[*parser.AlterTable]AttributeDropTarget)}
	pairs.Detaches = make(map[*parser.AlterTable]AttributeDetachPair)
	pairs.Teardowns = make(map[*parser.DropTable]AttributeDropTarget)
	helperTargets := make(map[AttributeDropTarget]bool)
	for _, statement := range statements {
		if s, ok := statement.(*parser.DropTable); ok && len(s.Names) == 1 {
			if target, ok := attributeTarget(s.Names[0]); ok && AttributeHelperName(target.Table) {
				helperTargets[target] = true
			}
		}
	}
	for _, statement := range statements {
		if s, ok := statement.(*parser.CreateTable); ok && len(s.Elements) == 0 && s.PartitionBy == nil {
			if target, ok := attributeTarget(s.Name); ok && AttributeHelperName(target.Table) {
				helperTargets[target] = true
			}
		}
	}
	helperObligations := make(map[AttributeDropTarget]bool)
	constructors := make(map[AttributeDropTarget]*attributeConstructor)
	obligations := make(map[AttributeDropTarget]map[string]bool)
	var order []AttributeDropTarget
	var propagate func(AttributeDropTarget, constructorColumn, *[]AttributeMember, map[AttributeDropTarget]bool) error
	propagate = func(parent AttributeDropTarget, a constructorColumn, members *[]AttributeMember, seen map[AttributeDropTarget]bool) error {
		if seen[parent] {
			return nil
		}
		seen[parent] = true
		for _, target := range order {
			child := constructors[target]
			if child == nil || !slices.Contains(child.parents, parent) {
				continue
			}
			if child.closed {
				return fmt.Errorf("stuntgres: late staged attribute addition")
			}
			i := constructorInherit(child, parent, a)
			child.staged = true
			member := constructorMember(target, child, i)
			at := slices.IndexFunc(*members, func(m AttributeMember) bool { return m.Target == target })
			if at >= 0 {
				(*members)[at] = member
			} else {
				*members = append(*members, member)
			}
			if err := propagate(target, child.columns[i], members, seen); err != nil {
				return err
			}
		}
		return nil
	}
	var retire func(AttributeDropTarget, string, *[]AttributeMember) error
	retire = func(parent AttributeDropTarget, name string, members *[]AttributeMember) error {
		for _, target := range order {
			child := constructors[target]
			if child == nil {
				continue
			}
			i := constructorLive(child, name)
			if i < 0 {
				continue
			}
			a := &child.columns[i]
			origin := slices.Index(a.Origins, parent)
			if origin < 0 {
				continue
			}
			if child.closed || !a.Placeholder {
				return fmt.Errorf("stuntgres: incompatible generated attribute retirement closure")
			}
			if !slices.ContainsFunc(*members, func(m AttributeMember) bool { return m.Target == target }) {
				*members = append(*members, constructorMember(target, child, i))
			}
			a.Origins = slices.Delete(a.Origins, origin, origin+1)
			if !a.Local && len(a.Origins) == 0 {
				a.Dropped = true
				if err := retire(target, name, members); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, statement := range statements {
		switch s := statement.(type) {
		case *parser.CreateTable:
			target, ok := attributeTarget(s.Name)
			if !ok || s.IfNotExists || s.OfType != nil || s.PartitionBy != nil || s.PartitionOf != nil {
				continue
			}
			c := &attributeConstructor{}
			for _, name := range s.Parents {
				parent, ok := attributeTarget(name)
				if !ok {
					continue
				}
				c.parents = append(c.parents, parent)
				if p := constructors[parent]; p != nil {
					for _, a := range p.columns {
						if !a.Dropped {
							constructorInherit(c, parent, a)
						}
					}
					c.staged = c.staged || p.staged
				}
			}
			for _, element := range s.Elements {
				if element.Column == nil {
					continue
				}
				column := element.Column
				if i := constructorLive(c, column.Name); i >= 0 {
					c.columns[i].Local = true
					c.columns[i].Placeholder = inertAttributePlaceholder(column)
				} else {
					c.columns = append(c.columns, constructorColumn{Name: column.Name, Local: true, Placeholder: inertAttributePlaceholder(column)})
				}
			}
			if constructors[target] == nil {
				order = append(order, target)
			}
			constructors[target] = c
			pairs.Creates[s] = target
		case *parser.AlterTable:
			target, qualified := attributeTarget(s.Name)
			if !qualified {
				continue
			}
			c := constructors[target]
			if c == nil || s.IfExists || len(s.Actions) != 1 {
				continue
			}
			a := s.Actions[0]
			if pair, ok := pairAttributeHelperDetach(s, constructors, helperObligations); ok {
				pairs.Detaches[s] = pair
				c.parents = slices.DeleteFunc(c.parents, func(parent AttributeDropTarget) bool { return parent == pair.Helper })
				continue
			}
			if a.Action == parser.AddColumn && a.Definition != nil && !s.Only && !a.IfExists && !a.IfNotExists && !c.closed {
				if constructorLive(c, a.Definition.Name) >= 0 {
					continue
				}
				column := constructorColumn{Name: a.Definition.Name, Local: true, Placeholder: inertAttributePlaceholder(a.Definition)}
				c.columns = append(c.columns, column)
				c.staged = true
				pair := AttributeAddPair{Target: target, Number: int32(len(c.columns)), Participants: []AttributeMember{constructorMember(target, c, len(c.columns)-1)}}
				if err := propagate(target, column, &pair.Participants, make(map[AttributeDropTarget]bool)); err != nil {
					return pairs, err
				}
				pairs.Adds[s] = pair
				continue
			}
			if generatedAttributeDrop(s) && !c.closed {
				i := constructorLive(c, a.Column.Text)
				if i < 0 {
					continue
				}
				column := &c.columns[i]
				if !column.Placeholder || !column.Local || len(column.Origins) != 0 {
					continue
				}
				pair := AttributeDropPair{Target: target, Column: a.Column.Text, Recursive: !s.Only, Participants: []AttributeMember{constructorMember(target, c, i)}}
				if s.Only {
					shared := false
					for _, child := range constructors {
						if at := constructorLive(child, a.Column.Text); at >= 0 && slices.Contains(child.columns[at].Origins, target) {
							shared = true
							break
						}
					}
					if shared {
						continue
					}
				}
				if !s.Only {
					if err := retire(target, a.Column.Text, &pair.Participants); err != nil {
						return pairs, err
					}
					if len(pair.Participants) == 1 {
						continue
					}
				}
				if helperTargets[target] && len(pair.Participants) > 1 {
					helperObligations[target] = true
				}
				for _, member := range pair.Participants {
					if obligations[member.Target] == nil {
						obligations[member.Target] = make(map[string]bool)
					}
					obligations[member.Target][pair.Column] = true
				}
				column.Dropped = true
				pairs.Drops[s] = pair
				continue
			}
			if c.staged && !c.closed && (!s.Only && (a.Action == parser.AddNotNull || a.Action == parser.AddCheck || a.Action == parser.AddUnique || a.Action == parser.AddForeignKey) || s.Only && a.Action == parser.AddExclusion) {
				pairs.Constraints[s] = target
				continue
			}
			// A non-shape repair does not reopen a consumed constructor. Ordinary
			// admission owns valid owner/options/recipe commands independently.
		case *parser.AlterTableAttachPartition:
			// Supported partition layouts finish their ordinary history before
			// attachment. Membership never becomes an ordinary constructor edge.
			if child, ok := attributeTarget(s.Partition); ok {
				if c := constructors[child]; c != nil {
					c.closed = true
				}
			}
		case *parser.DropTable:
			if helper, ok := pairAttributeHelperTeardown(s, constructors, helperObligations); ok {
				pairs.Teardowns[s] = helper
				delete(helperObligations, helper)
				delete(constructors, helper)
			}
		case *parser.Insert:
			if target, ok := attributeTarget(s.Name); ok {
				if c := constructors[target]; c != nil {
					c.closed = true
				}
			}
		case *parser.Update:
			if repair, ok := ParseObjectACLRepair(s); ok && repair.Class == 1259 {
				name, err := parser.ParseRegclassName(repair.Identity)
				if err == nil {
					if target, ok := attributeTarget(name); ok {
						if c := constructors[target]; c != nil {
							c.closed = true
						}
					}
				}
			}
		case *parser.Select:
			for _, c := range constructors {
				c.closed = true
			}
		}
	}
	if len(helperObligations) != 0 {
		return pairs, fmt.Errorf("stuntgres: incomplete attribute helper teardown")
	}
	for target, names := range obligations {
		for name := range names {
			if c := constructors[target]; c != nil && constructorLive(c, name) >= 0 {
				return pairs, fmt.Errorf("stuntgres: incomplete generated attribute retirement")
			}
		}
	}
	return pairs, nil
}

// CheckAttributeDefinition reuses CREATE's scalar boundary for one paired ADD or
// named constraint. Pairing grants no broader expression or catalog capability.
func CheckAttributeDefinition(s *parser.AlterTable) error {
	if s == nil || s.IfExists || len(s.Name.Parts) != 2 || len(s.Actions) != 1 {
		return sqlError("0A000", "restore accepts only paired attribute constructors")
	}
	a := s.Actions[0]
	if a.IfExists || a.IfNotExists {
		return sqlError("0A000", "restore accepts only unconditional attribute constructors")
	}
	element := parser.TableElement{}
	switch a.Action {
	case parser.AddColumn:
		if s.Only || a.Definition == nil {
			return sqlError("0A000", "restore accepts only recursive paired column additions")
		}
		element.Column = a.Definition
	case parser.AddNotNull:
		if a.Constraint.Text == "" {
			return sqlError("0A000", "restore requires named staged constraints")
		}
	case parser.AddCheck:
		element.Check = a.Check
	case parser.AddUnique:
		element.Unique = a.Unique
	case parser.AddExclusion:
		element.Exclusion = a.Exclusion
	case parser.AddForeignKey:
		element.ForeignKey = a.ForeignKey
	default:
		return sqlError("0A000", "restore accepts only paired attribute constructors")
	}
	return CheckCreateTable(&parser.CreateTable{Name: s.Name, Elements: []parser.TableElement{element}})
}
