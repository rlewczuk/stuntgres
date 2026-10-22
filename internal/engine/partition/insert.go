package partition

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type InsertGroup struct {
	Ref    ids.ObjectRef
	Rows   index.WriteBatch
	Unique *index.UniqueWriteBinding
}

// InsertState is the routing state of one INSERT or COPY into a partitioned
// or partition target, captured under Engine.mu. Authority is the captured
// view and Env its environment (Restore's trusted insertion keeps its own
// $user there). NamedRow, Destination and PhysicalRow describe the row being
// routed; Groups and Order collect the accepted rows per leaf.
type InsertState struct {
	Catalog     catalog.Read
	Authority   access.View
	Env         access.Environment
	Target      *catalog.Relation
	Destination *catalog.Relation
	Layouts     map[ids.ObjectAddress]*exec.InheritanceQuerySegment
	PhysicalRow []sqltype.Value
	NamedRow    []sqltype.Value
	Groups      map[ids.ObjectAddress]*InsertGroup
	Order       []*InsertGroup
}

// Route walks the partition tree from Target to the leaf that accepts row,
// failing with MissingPartition when a level has no matching child.
func (state *InsertState) Route(evaluation *exec.Context, row []sqltype.Value, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (*catalog.Relation, error) {
	destination := state.Target
	for destination.Partition != nil {
		var values [32]catalog.PartitionDatum
		key, err := RowKey(destination, state.RowFor(destination, row), evaluation, values[:0])
		if err != nil {
			return nil, err
		}
		var selected *catalog.Relation
		for _, address := range destination.Partition.Children {
			child := state.Catalog.MustRelation(address)
			if child.Membership.DefaultBound {
				continue
			}
			contains, err := Contains(evaluation.Context, destination.Partition.Keys, child.Membership, key, input)
			if err != nil {
				return nil, err
			}
			if contains {
				selected = child
				break
			}
		}
		if selected == nil && destination.Partition.DefaultChild != (ids.ObjectAddress{}) {
			selected = state.Catalog.MustRelation(destination.Partition.DefaultChild)
		}
		if selected == nil {
			return nil, state.MissingPartition(evaluation.Context, destination, key.Values, output)
		}
		destination = selected
	}
	return destination, nil
}

// MissingPartition is the 23514 routing failure under parent. The key values
// are disclosed only to an actor without RLS who may SELECT the table or
// every key column.
func (state *InsertState) MissingPartition(ctx context.Context, parent *catalog.Relation, key []catalog.PartitionDatum, output sqltype.OutputEnvironment) error {
	ref := state.Catalog.Owner().Domain.Ref(state.Catalog.Epoch(), parent.Address())
	err := sqlError("23514", fmt.Sprintf(`no partition of relation "%s" found for row`, parent.Name())).(*sqlstate.Error)
	err.Schema, err.Table = state.Catalog.Get(parent.Namespace()).Name(), parent.Name()
	// Routing/integrity errors remain real, but an actor subject to RLS must
	// not receive key values even when ordinary SELECT ACLs are present.
	applies, securityErr := access.RowSecurityApplies(state.Authority.Roles, state.Target, state.Authority.State.CurrentUser)
	if securityErr != nil {
		return securityErr
	}
	if applies {
		return err
	}
	rights, checkErr := state.Authority.Effective(state.Env, ref, false)
	if checkErr != nil {
		return checkErr
	}
	if rights&authorization.SelectPrivilege == 0 {
		for _, entry := range parent.Partition.Keys {
			if entry.Derived != nil {
				return err
			}
			column := ref
			column.Address.SubID = entry.Attribute
			rights, checkErr = state.Authority.Effective(state.Env, column, false)
			if checkErr != nil {
				return checkErr
			}
			if rights&authorization.SelectPrivilege == 0 {
				return err
			}
		}
	}
	var names, values strings.Builder
	renderer := stored.Deparser{View: state.Catalog, Relation: parent, Output: output}
	for i, entry := range parent.Partition.Keys {
		if i != 0 {
			names.WriteString(", ")
			values.WriteString(", ")
		}
		label := entry.Label
		if entry.Derived != nil {
			if renderer.Path == nil {
				path, pathErr := state.Env.ReferencePath()
				if pathErr != nil {
					return pathErr
				}
				renderer.Path = path
			}
			cast, coercion := entry.Derived.Definition.(plan.CoercionExpr)
			regTSCast := coercion && cast.Mode == sqltype.Explicit && sqltype.IsRegTS(entry.Usage.Type.BaseType().ID())
			keyRenderer := renderer
			if regTSCast {
				// Partition diagnostics group the whole pretty cast expression,
				// not its simple operand, as in PostgreSQL's partition key label.
				keyRenderer.Pretty = true
			}
			text, renderErr := keyRenderer.SQL(ctx, entry.Derived.Definition, stored.DeparseParent{})
			if renderErr != nil {
				return renderErr
			}
			if regTSCast {
				text = "(" + text + ")"
			}
			label = text
		} else {
			label = catalog.Identifier(label)
		}
		names.WriteString(label)
		if err := exec.WriteDiagnosticValue(ctx, &values, key[i].Value, output); err != nil {
			return err
		}
	}
	err.Detail = fmt.Sprintf("Partition key of the failing row contains (%s) = (%s).", names.String(), values.String())
	return err
}

// RowFor maps a row of Target to d's column layout.
func (state *InsertState) RowFor(d *catalog.Relation, row []sqltype.Value) []sqltype.Value {
	if segment := state.Layouts[d.Address()]; segment != nil {
		return segment.MapValues(row)
	}
	return row
}
