package engine

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

const RestoreRetiredBoundary = "restore accepts only paired canonical retired constraint origins"

type restoredHistoricalFact struct {
	Ref                 ids.ObjectRef
	OID                 ids.OID
	Name                string
	Check               bool
	Attributes          []int32
	Expression          expr.Expr
	NotValid, NoInherit bool
	Local               bool
	Count               int
}

type retiredRepairRow struct {
	Parent             parser.ObjectName
	text, Column, Name string
	Attribute          int32
	Constraint         ids.OID
	Lifetime, Archive  uint64
	Check              bool
}

type retiredRepair struct {
	Relation                  parser.ObjectName
	text, Name                string
	Check, Local, BeforeLocal bool
	Before                    int
	Columns                   []string
	Rows                      []retiredRepairRow
	parents                   []string
}

type retiredSourceIdentity struct {
	Parent            ids.ObjectRef
	Archive, Lifetime uint64
	Constraint        ids.OID
}

type retiredSourceDescription struct {
	Column, Name string
	Attribute    int32
	Check        bool
}

type restoredHistoryAuthority struct {
	Facts       map[ids.OID]restoredHistoricalFact
	Archives    map[uint64]uint64
	Sources     map[retiredSourceIdentity]retiredSourceDescription
	NextArchive uint64
}

func ConstraintAttributeKeys(table *catalog.Relation, expression expr.Expr) []int32 {
	var keys []int32
	plan.WalkExpression(expression, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok && column.Origin.Address.ClassID == ids.RelationClassID && column.Origin.Address.ObjectID == table.Address().ObjectID && !slices.Contains(keys, column.Origin.Address.SubID) {
			keys = append(keys, column.Origin.Address.SubID)
		}
	})
	return keys
}

func CaptureHistoricalRelation(view catalog.Read, ref ids.ObjectRef, facts map[ids.OID]restoredHistoricalFact) {
	table, ok := view.Relation(ref.Address)
	if !ok || table.Inheritance == nil {
		return
	}
	namespace, ok := view.Namespace(table.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return
	}
	for _, c := range table.Constraints.NotNull {
		p := table.Inheritance.NotNull[c.Attribute]
		if len(p.Retired) != 0 {
			continue
		}
		if old, exists := facts[c.OID]; exists && old.Local == p.Local && old.Count == len(p.Origins) && old.NotValid == c.NotValid && old.NoInherit == c.NoInherit {
			continue
		}
		facts[c.OID] = restoredHistoricalFact{Ref: ref, OID: c.OID, Name: c.Name, Attributes: []int32{c.Attribute}, NotValid: c.NotValid, NoInherit: c.NoInherit, Local: p.Local, Count: len(p.Origins)}
	}
	for _, c := range table.Constraints.Check {
		p := table.Inheritance.Checks[c.Name]
		if len(p.Retired) != 0 {
			continue
		}
		if old, exists := facts[c.OID]; exists && old.Local == p.Local && old.Count == len(p.Origins) && old.NotValid == c.NotValid && old.NoInherit == c.NoInherit {
			continue
		}
		facts[c.OID] = restoredHistoricalFact{Ref: ref, OID: c.OID, Name: c.Name, Check: true, Attributes: ConstraintAttributeKeys(table, c.Expression), Expression: c.Expression, NotValid: c.NotValid, NoInherit: c.NoInherit, Local: p.Local, Count: len(p.Origins)}
	}
}

func retiredDeclarationNeeded(view catalog.Read, table *catalog.Relation, p catalog.InheritedConstraint, check, notValid bool) bool {
	if p.Local {
		return true
	}
	if len(p.Retired) == 0 {
		return false
	}
	if notValid {
		return len(p.Origins) == 0
	}
	// An invalid parent fact is emitted after data, and cannot provide this
	// captured valid enforcement during CREATE/valid-CHECK reconstruction.
	for _, origin := range p.Origins {
		parent, ok := view.Relation(origin.Parent)
		if !ok {
			continue
		}
		if check {
			for _, c := range parent.Constraints.Check {
				if c.OID == origin.Constraint && !c.NotValid {
					return false
				}
			}
		} else {
			for _, c := range parent.Constraints.NotNull {
				if c.OID == origin.Constraint && !c.NotValid {
					return false
				}
			}
		}
	}
	return true
}

func DumpNotNullDeclaration(view catalog.Read, table *catalog.Relation, attribute int32) bool {
	if table.Inheritance == nil {
		return true
	}
	for _, c := range table.Constraints.NotNull {
		if c.Attribute == attribute {
			return retiredDeclarationNeeded(view, table, table.Inheritance.NotNull[attribute], false, c.NotValid)
		}
	}
	return false
}

func DumpCheckDeclaration(view catalog.Read, table *catalog.Relation, c catalog.CheckConstraint) bool {
	return table.Inheritance == nil || retiredDeclarationNeeded(view, table, table.Inheritance.Checks[c.Name], true, c.NotValid)
}

func (t *dumpRelation) EmitRetiredConstraints(view catalog.Read, checks bool, emit func(string) error, setOwner func(string) error) error {
	graph := t.Descriptor.Inheritance
	if graph == nil {
		return nil
	}
	qualified := catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name)
	emitFact := func(name string, keys []int32, p catalog.InheritedConstraint, check, notValid bool) error {
		if len(p.Retired) == 0 {
			return nil
		}
		if err := setOwner("postgres"); err != nil {
			return err
		}
		beforeLocal := retiredDeclarationNeeded(view, t.Descriptor, p, check, notValid)
		var rows, parents, keyQueries []string
		for _, attribute := range keys {
			slot := t.Descriptor.IndexForAttribute(attribute)
			if slot < 0 {
				return fmt.Errorf("stuntgres: missing historical constraint key")
			}
			keyQueries = append(keyQueries, "(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = "+dump.CommentLiteral(qualified)+"::regclass AND attname = "+dump.CommentLiteral(t.Columns[slot].Name)+")")
		}
		kind := "n"
		if check {
			kind = "c"
		}
		for _, history := range p.Retired {
			parent, ok := view.Relation(history.Parent.Address)
			if !ok {
				return fmt.Errorf("stuntgres: missing retired constraint parent")
			}
			parentName := catalog.QuoteIdentifier(view.Get(parent.Namespace()).Name()) + "." + catalog.QuoteIdentifier(parent.Name())
			rows = append(rows, "("+dump.CommentLiteral(parentName)+"::regclass, "+dump.CommentLiteral(history.Column)+", "+strconv.FormatInt(int64(history.Attribute), 10)+", "+dump.CommentLiteral(kind)+", "+dump.CommentLiteral(history.Name)+", "+dump.CommentLiteral(strconv.FormatUint(history.Archive, 10)+":"+strconv.FormatUint(history.Lifetime, 10))+", "+dump.CommentLiteral(strconv.FormatUint(uint64(history.Constraint), 10))+")")
			if !slices.Contains(parents, parentName) {
				parents = append(parents, parentName)
			}
		}
		sql := "UPDATE pg_catalog.pg_constraint SET conislocal = " + strconv.FormatBool(p.Local) + ", coninhcount = " + strconv.Itoa(len(p.Origins)) + " + (SELECT count(DISTINCT (retired.column1, retired.column6, retired.column7)) FROM (VALUES " + strings.Join(rows, ", ") + ") AS retired) WHERE conrelid = " + dump.CommentLiteral(qualified) + "::regclass AND conname = " + dump.CommentLiteral(name) + " AND contype = " + dump.CommentLiteral(kind) + " AND conkey = ARRAY[" + strings.Join(keyQueries, ", ") + "] AND conislocal = " + strconv.FormatBool(beforeLocal) + " AND coninhcount = " + strconv.Itoa(len(p.Origins))
		for _, parent := range parents {
			sql += " AND EXISTS (SELECT 1 FROM pg_catalog.pg_inherits WHERE inhrelid = " + dump.CommentLiteral(qualified) + "::regclass AND inhparent = " + dump.CommentLiteral(parent) + "::regclass)"
		}
		return emit(sql + ";\n")
	}
	if checks {
		for _, c := range t.Descriptor.Constraints.Check {
			if err := emitFact(c.Name, ConstraintAttributeKeys(t.Descriptor, c.Expression), graph.Checks[c.Name], true, c.NotValid); err != nil {
				return err
			}
		}
	} else {
		for _, c := range t.NotNullConstraints {
			if err := emitFact(c.Name, []int32{c.Attribute}, graph.NotNull[c.Attribute], false, c.NotValid); err != nil {
				return err
			}
		}
	}
	return nil
}

func RetiredConstraintEvents(view catalog.Read, events dump.Graph, tables []dumpRelation) {
	for i := range tables {
		table := tables[i].Descriptor
		graph := table.Inheritance
		if graph == nil {
			continue
		}
		has := false
		for _, p := range graph.NotNull {
			has = has || len(p.Retired) != 0
		}
		for _, p := range graph.Checks {
			has = has || len(p.Retired) != 0
		}
		if !has {
			continue
		}
		event := dump.Event{OID: table.Address().ObjectID, Phase: dump.RetiredConstraints}
		seen := make(map[ids.ObjectAddress]bool)
		var ready func(ids.ObjectAddress)
		ready = func(address ids.ObjectAddress) {
			if seen[address] {
				return
			}
			seen[address] = true
			events.Add(event, dump.Event{OID: address.ObjectID, Phase: dump.TableCompletion})
			if parent, ok := view.Relation(address); ok && parent.Inheritance != nil {
				for _, ancestor := range parent.Inheritance.Parents {
					ready(ancestor)
				}
			}
		}
		ready(table.Address())
	}
}
