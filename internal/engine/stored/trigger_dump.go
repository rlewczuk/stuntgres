package stored

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// TriggerDumpGroups emits portable definitions, never invocation frames.
// Regenerable row clones are omitted; retained historical clones carry a native
// provenance annotation paired with their actual child CREATE.
func TriggerDumpGroups(ctx context.Context, read catalog.Read, roles authorization.Read) ([]dump.Group, error) {
	var groups []dump.Group
	var dependencies []dump.Group
	var modes []dump.Group
	for _, descriptor := range read.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || !relation.HasTriggers() {
			continue
		}
		if read.MustNamespace(relation.Namespace()).Temporary != (ids.SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(relation.Owner)
		if err != nil {
			return nil, err
		}
		table, err := ruleRelationSQL(read, ids.ObjectRef{Address: relation.Address()})
		if err != nil {
			return nil, err
		}
		for _, t := range TriggersOf(relation) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			p := t.Parts()
			modes = append(modes, dump.Group{Owner: owner.Name, Table: table, SQL: TriggerModeSQL(table, p.Name, t.Mode())})
			for _, address := range p.AutoExtensions {
				extension, ok := read.Extension(address)
				if !ok {
					return nil, ids.ErrCatalogReplaced
				}
				dependencies = append(dependencies, dump.Group{Owner: owner.Name, Table: table, SQL: "ALTER TRIGGER " + catalog.QuoteIdentifier(p.Name) + " ON " + table + " DEPENDS ON EXTENSION " + catalog.QuoteIdentifier(extension.Name()) + ";\n"})
			}
			var retained *TriggerCloneReplay
			if p.Parent.ObjectID != 0 {
				if relation.Membership == nil {
					return nil, fmt.Errorf("stuntgres: missing retained trigger clone membership")
				}
				parent, ok := read.Relation(relation.Membership.Parent)
				if !ok {
					return nil, ids.ErrCatalogReplaced
				}
				var source Trigger
				for _, candidate := range TriggersOf(parent) {
					if candidate.OID() == p.Parent.ObjectID {
						source = candidate
						break
					}
				}
				if source.OID() == 0 {
					return nil, ids.ErrCatalogReplaced
				}
				if source.Parts().Level == TriggerRow {
					continue
				}
				retained = &TriggerCloneReplay{
					ParentSchema: read.MustNamespace(parent.Namespace()).Name(), ParentTable: parent.Name(), ParentTrigger: source.Name(),
					ChildSchema: read.MustNamespace(relation.Namespace()).Name(), ChildTable: relation.Name(), ChildTrigger: p.Name,
				}
			}
			function, ok := read.Get(p.Procedure.Address).(*catalog.Procedure)
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			functionNS := read.MustNamespace(function.Namespace())
			if functionNS.Temporary != (ids.SessionToken{}) {
				return nil, fmt.Errorf("stuntgres: dump cannot serialize a trigger referencing a temporary function")
			}
			var sql strings.Builder
			timing, level := "BEFORE", "ROW"
			if p.Timing == TriggerAfter {
				timing = "AFTER"
			}
			if p.Level == TriggerStatement {
				level = "STATEMENT"
			}
			sql.WriteString("CREATE TRIGGER " + catalog.QuoteIdentifier(p.Name) + " " + timing + " ")
			var events []string
			if p.Events&TriggerInsert != 0 {
				events = append(events, "INSERT")
			}
			if p.Events&TriggerUpdate != 0 {
				event := "UPDATE"
				if len(p.UpdateColumns) != 0 {
					columns := make([]string, len(p.UpdateColumns))
					for i, a := range p.UpdateColumns {
						columns[i], err = DeparseAttribute(relation, a)
						if err != nil {
							return nil, err
						}
					}
					event += " OF " + strings.Join(columns, ",")
				}
				events = append(events, event)
			}
			if p.Events&TriggerDelete != 0 {
				events = append(events, "DELETE")
			}
			if p.Events&TriggerTruncate != 0 {
				events = append(events, "TRUNCATE")
			}
			sql.WriteString(strings.Join(events, " OR ") + " ON " + table + " FOR EACH " + level)
			if p.When != nil {
				renderer := Deparser{View: read, ShowImplicit: true}
				renderer.ColumnSQL = func(column plan.ColumnExpr) (string, error) {
					if column.Scope != analyze.RulePseudoScope {
						return "", fmt.Errorf("stuntgres: invalid trigger WHEN column scope")
					}
					address := column.Origin.Address
					address.SubID = 0
					target, ok := read.Relation(address)
					if !ok {
						return "", ids.ErrCatalogReplaced
					}
					name, err := DeparseAttribute(target, column.Origin.Address.SubID)
					if err != nil {
						return "", err
					}
					side := "OLD"
					if column.Frame == 2 {
						side = "NEW"
					}
					return side + "." + name, nil
				}
				text, err := renderer.SQL(ctx, p.When, DeparseParent{})
				if err != nil {
					return nil, err
				}
				sql.WriteString(" WHEN (" + text + ")")
			}
			args := make([]string, len(p.Arguments))
			for i, arg := range p.Arguments {
				args[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
			}
			sql.WriteString(" EXECUTE FUNCTION " + catalog.QuoteIdentifier(functionNS.Name()) + "." + catalog.QuoteIdentifier(function.Name()) + "(" + strings.Join(args, ",") + ");\n")
			text := sql.String()
			if retained != nil {
				retained.SQL = text
				comment, err := retained.Comment()
				if err != nil {
					return nil, err
				}
				text = comment + text
			}
			groups = append(groups, dump.Group{Owner: owner.Name, Table: table, SQL: text})
		}
	}
	slices.SortFunc(groups, func(a, b dump.Group) int {
		if a.Table != b.Table {
			return strings.Compare(a.Table, b.Table)
		}
		return strings.Compare(a.SQL, b.SQL)
	})
	// Derived clones exist only after every parent CREATE. Keep their local x
	// metadata in a final deterministic phase, independent of table-name order.
	slices.SortFunc(dependencies, func(a, b dump.Group) int {
		if a.Table != b.Table {
			return strings.Compare(a.Table, b.Table)
		}
		return strings.Compare(a.SQL, b.SQL)
	})
	groups = append(groups, dependencies...)
	slices.SortFunc(modes, func(a, b dump.Group) int {
		if a.Table != b.Table {
			return strings.Compare(a.Table, b.Table)
		}
		return strings.Compare(a.SQL, b.SQL)
	})
	groups = append(groups, modes...)
	return groups, nil
}
