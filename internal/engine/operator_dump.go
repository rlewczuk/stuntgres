package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func operatorSQLName(view catalog.Read, d *catalog.Operator) string {
	return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + d.Name()
}
func operatorSQLSignature(view catalog.Read, d *catalog.Operator) (string, error) {
	arguments := [2]string{"NONE", "NONE"}
	for i, id := range [...]ids.OID{d.Left, d.Right} {
		if id == 0 {
			continue
		}
		t, ok := view.LookupType(sqltype.ID(id))
		if !ok {
			return "", fmt.Errorf("stuntgres: missing operator signature type")
		}
		text, err := catalogTypeDeclaration(view, t.Usage())
		if err != nil {
			return "", err
		}
		arguments[i] = text
	}
	return operatorSQLName(view, d) + " (" + arguments[0] + ", " + arguments[1] + ")", nil
}
func operatorRoutineSQLName(view catalog.Read, id ids.OID) (string, error) {
	identity, ok := routineIdentityByOID(view, id)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing operator routine identity")
	}
	namespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: identity.Namespace})
	if namespace == nil {
		return "", fmt.Errorf("stuntgres: missing operator routine namespace")
	}
	return catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(identity.Name), nil
}
func operatorDumpDeclaration(ctx context.Context, view catalog.Read, roles authorization.Read, d *catalog.Operator) (dumpDomain, error) {
	if err := ctx.Err(); err != nil {
		return dumpDomain{}, err
	}
	if err := (OperatorProvider{}).Validate(view, d); err != nil {
		return dumpDomain{}, err
	}
	if d.Function == 0 {
		return orphanOperatorShellDump(ctx, view, roles, d)
	}
	return operatorDumpDefined(ctx, view, roles, d, false)
}

// Reciprocal links between defined custom operators are established only after
// both declarations. Shell targets must instead be created by a forward link.
func operatorDumpDeferredLink(view catalog.Read, source *catalog.Operator, kind int, id ids.OID) bool {
	peer, _ := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
	if peer == nil || peer.Function == 0 || OperatorSeeded(peer) {
		return false
	}
	reciprocal := peer.Commutator
	if kind == 1 {
		reciprocal = peer.Negator
	}
	return reciprocal == source.Address().ObjectID
}

func operatorDumpDefined(ctx context.Context, view catalog.Read, roles authorization.Read, d *catalog.Operator, keepForwardLinks bool) (dumpDomain, error) {
	options := make([]string, 0, 10)
	for i, id := range [...]ids.OID{d.Left, d.Right} {
		if id == 0 {
			continue
		}
		t, _ := view.LookupType(sqltype.ID(id))
		text, err := catalogTypeDeclaration(view, t.Usage())
		if err != nil {
			return dumpDomain{}, err
		}
		key := "RIGHTARG"
		if i == 0 {
			key = "LEFTARG"
		}
		options = append(options, key+" = "+text)
	}
	routine, err := operatorRoutineSQLName(view, d.Function)
	if err != nil {
		return dumpDomain{}, err
	}
	options = append(options, "FUNCTION = "+routine)
	for i, id := range [...]ids.OID{d.Commutator, d.Negator} {
		if id == 0 {
			continue
		}
		peer, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
		if !ok {
			return dumpDomain{}, fmt.Errorf("stuntgres: missing operator link")
		}
		if !keepForwardLinks && operatorDumpDeferredLink(view, d, i, id) {
			continue
		}
		key := "COMMUTATOR"
		if i == 1 {
			key = "NEGATOR"
		}
		options = append(options, key+" = OPERATOR("+operatorSQLName(view, peer)+")")
	}
	for i, id := range [...]ids.OID{d.Restriction, d.Join} {
		if id == 0 {
			continue
		}
		name, err := operatorRoutineSQLName(view, id)
		if err != nil {
			return dumpDomain{}, err
		}
		key := "RESTRICT"
		if i == 1 {
			key = "JOIN"
		}
		options = append(options, key+" = "+name)
	}
	if d.Hash {
		options = append(options, "HASHES")
	}
	if d.Merge {
		options = append(options, "MERGES")
	}
	text := "CREATE OPERATOR " + operatorSQLName(view, d) + " (" + strings.Join(options, ", ") + ");\n"
	owners := []*catalog.Operator{d}
	for _, id := range [...]ids.OID{d.Commutator, d.Negator} {
		peer, _ := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
		if peer != nil && peer.Function == 0 {
			owners = append(owners, peer)
		}
	}
	for _, owned := range owners {
		owner, err := roles.Descriptor(owned.Owner)
		if err != nil {
			return dumpDomain{}, err
		}
		signature, err := operatorSQLSignature(view, owned)
		if err != nil {
			return dumpDomain{}, err
		}
		text += "ALTER OPERATOR " + signature + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n"
	}
	// Create as bootstrap so independently owned forward shells can be completed
	// and definitions whose owners lack schema CREATE still replay faithfully.
	return dumpDomain{owner: "postgres", sql: text}, nil
}

// Select a deterministic extant forward reference for an unfilled shell.
func operatorShellGenerator(view catalog.Read, shell *catalog.Operator) *catalog.Operator {
	var selected *catalog.Operator
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Operator)
		if ok && d.Function != 0 && (d.Commutator == shell.Address().ObjectID || d.Negator == shell.Address().ObjectID) && (selected == nil || d.Address().ObjectID < selected.Address().ObjectID) {
			selected = d
		}
	}
	return selected
}
func orphanOperatorShellDump(ctx context.Context, view catalog.Read, roles authorization.Read, shell *catalog.Operator) (dumpDomain, error) {
	var routine *catalog.Procedure
	negator := false
	for key, declaration := range OperatorRoutines() {
		comm := shell.Left != 0 && key.Left == shell.Right && key.Right == shell.Left
		neg := key.Left == shell.Left && key.Right == shell.Right && declaration.Result == ids.OID(sqltype.Bool)
		if !comm && !neg {
			continue
		}
		candidate, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID})
		if candidate != nil && (routine == nil || candidate.Address().ObjectID < routine.Address().ObjectID) {
			routine = candidate
			negator = !comm
		}
	}
	if routine == nil {
		return dumpDomain{}, fmt.Errorf("stuntgres: cannot serialize orphan operator shell %s: no compatible real implementation provider", operatorSQLName(view, shell))
	}
	name := "#~#"
	alphabet := "~!@#^&|`?"
	for suffix := 0; ; suffix++ {
		code := suffix
		symbol := "#~"
		for {
			symbol += string(alphabet[code%len(alphabet)])
			code /= len(alphabet)
			if code == 0 {
				break
			}
		}
		symbol += "#"
		left, right := shell.Right, shell.Left
		if negator {
			left, right = shell.Left, shell.Right
		}
		key := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectNamespace: shell.Namespace(), ObjectName: symbol, Left: left, Right: right}
		if _, exists := view.LookupName(catalog.DescriptorNameKey(&key)); !exists {
			name = symbol
			break
		}
	}
	generator := *shell
	generator.ObjectName = name
	generator.Left, generator.Right = shell.Right, shell.Left
	if negator {
		generator.Left, generator.Right = shell.Left, shell.Right
	}
	generator.Function, generator.Result = routine.Address().ObjectID, routine.Result
	generator.Commutator, generator.Negator = 0, 0
	if negator {
		generator.Negator = shell.Address().ObjectID
	} else {
		generator.Commutator = shell.Address().ObjectID
	}
	generator.Hash, generator.Merge = false, false
	generator.Restriction, generator.Join = 0, 0
	declaration, err := operatorDumpDefined(ctx, view, roles, &generator, true)
	if err != nil {
		return dumpDomain{}, err
	}
	// Emitted DDL creates a collision-free temporary operator using the genuine
	// selected routine, generates the original shell, then retires the helper.
	signature, err := operatorSQLSignature(view, &generator)
	if err != nil {
		return dumpDomain{}, err
	}
	declaration.sql += "DROP OPERATOR " + signature + ";\n"
	return declaration, nil
}

// Link events use raw endpoint declarations. Only semantic consumers are
// redirected to completion; redirecting structural edges would invent cycles.
func operatorDumpEvents(view catalog.Read, operators []*catalog.Operator, graph map[dumpEventKey][]dumpEventKey) {
	for _, source := range operators {
		if source.Function == 0 {
			continue
		}
		declaration := dumpEventKey{oid: source.Address().ObjectID}
		for kind, id := range [...]ids.OID{source.Commutator, source.Negator} {
			peer, _ := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
			if peer == nil || peer.Function == 0 || OperatorSeeded(peer) {
				continue
			}
			target := dumpEventKey{oid: id}
			if operatorDumpDeferredLink(view, source, kind, id) {
				event := dumpEventKey{oid: declaration.oid, phase: dumpOperatorLinks}
				graph[event] = append(graph[event], declaration, target)
			} else {
				// Completing the target clears the transient reciprocal field
				// installed by this asymmetric CREATE.
				graph[target] = append(graph[target], declaration)
			}
		}
	}
}

func normalizeOperatorDumpPredecessors(view catalog.Read, graph map[dumpEventKey][]dumpEventKey) {
	for event, predecessors := range graph {
		_, operator := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: event.oid})
		structural := event.phase == dumpOperatorLinks || operator && event.phase == dumpDeclaration
		unique := predecessors[:0]
		for _, predecessor := range predecessors {
			if !structural && predecessor.phase == dumpDeclaration {
				completion := dumpEventKey{oid: predecessor.oid, phase: dumpOperatorLinks}
				if _, exists := graph[completion]; exists {
					predecessor = completion
				}
			}
			if !slices.Contains(unique, predecessor) {
				unique = append(unique, predecessor)
			}
		}
		graph[event] = unique
	}
}

func operatorDumpLinks(view catalog.Read, source *catalog.Operator) (string, error) {
	var options []string
	for kind, id := range [...]ids.OID{source.Commutator, source.Negator} {
		if id == 0 || !operatorDumpDeferredLink(view, source, kind, id) {
			continue
		}
		peer := view.MustOperator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
		key := "COMMUTATOR"
		if kind == 1 {
			key = "NEGATOR"
		}
		options = append(options, key+" = OPERATOR("+operatorSQLName(view, peer)+")")
	}
	if len(options) == 0 {
		return "", nil
	}
	signature, err := operatorSQLSignature(view, source)
	if err != nil {
		return "", err
	}
	return "ALTER OPERATOR " + signature + " SET (" + strings.Join(options, ", ") + ");\n", nil
}
