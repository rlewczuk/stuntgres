package routine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// OperatorDumped validates a collected operator and reports whether Dump
// writes it; seeded operators are part of every destination.
func OperatorDumped(view catalog.Read, d *catalog.Operator) (bool, error) {
	if err := catalog.Validate(OperatorProvider{}, view, d); err != nil {
		return false, err
	}
	return !OperatorSeeded(d) && d.Origin == nil, nil
}

// OperatorDumpDependencies adds each emitted operator's declaration event and
// the events of the objects it depends on. A shell some defined operator
// generates has no declaration of its own.
func OperatorDumpDependencies(r dump.Resolver, view catalog.Read, operators []*catalog.Operator, graph dump.Graph) error {
	for _, d := range operators {
		if d.Function == 0 && OperatorShellGenerator(view, d) != nil {
			continue
		}
		id := d.Address().ObjectID
		graph[dump.Event{OID: id}] = nil
		for _, dependency := range OperatorDependencyAddresses(view, d) {
			if dependency.ClassID == ids.NamespaceClassID {
				continue
			}
			predecessor, err := r.Predecessor(view, dependency, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				graph[dump.Event{OID: id}] = append(graph[dump.Event{OID: id}], dump.Event{OID: predecessor})
			}
		}
	}
	return nil
}

// OperatorDumpStatements renders each emitted operator's declaration into
// declarations and returns the deferred link statements of defined operators.
func OperatorDumpStatements(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, operators []*catalog.Operator, declarations map[ids.OID]dump.Statement) (map[ids.OID]string, error) {
	links := make(map[ids.OID]string)
	for _, d := range operators {
		if d.Function == 0 && OperatorShellGenerator(view, d) != nil {
			continue
		}
		declaration, err := OperatorDumpDeclaration(ctx, p, view, roles, d)
		if err != nil {
			return nil, err
		}
		declarations[d.Address().ObjectID] = declaration
		if d.Function != 0 {
			text, err := operatorDumpLinks(p, view, d)
			if err != nil {
				return nil, err
			}
			if text != "" {
				links[d.Address().ObjectID] = text
			}
		}
	}
	return links, nil
}

func operatorSQLName(view catalog.Read, d *catalog.Operator) string {
	return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + d.Name()
}
func operatorSQLSignature(p dump.Presenter, view catalog.Read, d *catalog.Operator) (string, error) {
	arguments := [2]string{"NONE", "NONE"}
	for i, id := range [...]ids.OID{d.Left, d.Right} {
		if id == 0 {
			continue
		}
		t, ok := view.LookupType(sqltype.ID(id))
		if !ok {
			return "", fmt.Errorf("stuntgres: missing operator signature type")
		}
		text, err := p.TypeDeclaration(view, nil, t.Usage())
		if err != nil {
			return "", err
		}
		arguments[i] = text
	}
	return operatorSQLName(view, d) + " (" + arguments[0] + ", " + arguments[1] + ")", nil
}
func operatorRoutineSQLName(p dump.Presenter, view catalog.Read, id ids.OID) (string, error) {
	identity, ok := p.RoutineIdentity(view, id)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing operator routine identity")
	}
	namespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: identity.Namespace})
	if namespace == nil {
		return "", fmt.Errorf("stuntgres: missing operator routine namespace")
	}
	return catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(identity.Name), nil
}

// OperatorDumpDeclaration renders one operator as CREATE OPERATOR with its
// owner changes; an orphan shell is created through a temporary generator.
func OperatorDumpDeclaration(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, d *catalog.Operator) (dump.Statement, error) {
	if err := ctx.Err(); err != nil {
		return dump.Statement{}, err
	}
	if err := (OperatorProvider{}).Validate(view, d); err != nil {
		return dump.Statement{}, err
	}
	if d.Function == 0 {
		return orphanOperatorShellDump(ctx, p, view, roles, d)
	}
	return operatorDumpDefined(ctx, p, view, roles, d, false)
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

func operatorDumpDefined(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, d *catalog.Operator, keepForwardLinks bool) (dump.Statement, error) {
	options := make([]string, 0, 10)
	for i, id := range [...]ids.OID{d.Left, d.Right} {
		if id == 0 {
			continue
		}
		t, _ := view.LookupType(sqltype.ID(id))
		text, err := p.TypeDeclaration(view, nil, t.Usage())
		if err != nil {
			return dump.Statement{}, err
		}
		key := "RIGHTARG"
		if i == 0 {
			key = "LEFTARG"
		}
		options = append(options, key+" = "+text)
	}
	function, err := operatorRoutineSQLName(p, view, d.Function)
	if err != nil {
		return dump.Statement{}, err
	}
	options = append(options, "FUNCTION = "+function)
	for i, id := range [...]ids.OID{d.Commutator, d.Negator} {
		if id == 0 {
			continue
		}
		peer, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
		if !ok {
			return dump.Statement{}, fmt.Errorf("stuntgres: missing operator link")
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
		name, err := operatorRoutineSQLName(p, view, id)
		if err != nil {
			return dump.Statement{}, err
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
			return dump.Statement{}, err
		}
		signature, err := operatorSQLSignature(p, view, owned)
		if err != nil {
			return dump.Statement{}, err
		}
		text += "ALTER OPERATOR " + signature + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n"
	}
	// Create as bootstrap so independently owned forward shells can be completed
	// and definitions whose owners lack schema CREATE still replay faithfully.
	return dump.Statement{Owner: "postgres", SQL: text}, nil
}

// OperatorShellGenerator selects a deterministic extant forward reference for
// an unfilled shell, or nil when none exists.
func OperatorShellGenerator(view catalog.Read, shell *catalog.Operator) *catalog.Operator {
	var selected *catalog.Operator
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Operator)
		if ok && d.Function != 0 && (d.Commutator == shell.Address().ObjectID || d.Negator == shell.Address().ObjectID) && (selected == nil || d.Address().ObjectID < selected.Address().ObjectID) {
			selected = d
		}
	}
	return selected
}
func orphanOperatorShellDump(ctx context.Context, p dump.Presenter, view catalog.Read, roles authorization.Read, shell *catalog.Operator) (dump.Statement, error) {
	var implementation *catalog.Procedure
	negator := false
	for key, declaration := range OperatorRoutines() {
		comm := shell.Left != 0 && key.Left == shell.Right && key.Right == shell.Left
		neg := key.Left == shell.Left && key.Right == shell.Right && declaration.Result == ids.OID(sqltype.Bool)
		if !comm && !neg {
			continue
		}
		candidate, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID})
		if candidate != nil && (implementation == nil || candidate.Address().ObjectID < implementation.Address().ObjectID) {
			implementation = candidate
			negator = !comm
		}
	}
	if implementation == nil {
		return dump.Statement{}, fmt.Errorf("stuntgres: cannot serialize orphan operator shell %s: no compatible real implementation provider", operatorSQLName(view, shell))
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
	generator.Function, generator.Result = implementation.Address().ObjectID, implementation.Result
	generator.Commutator, generator.Negator = 0, 0
	if negator {
		generator.Negator = shell.Address().ObjectID
	} else {
		generator.Commutator = shell.Address().ObjectID
	}
	generator.Hash, generator.Merge = false, false
	generator.Restriction, generator.Join = 0, 0
	declaration, err := operatorDumpDefined(ctx, p, view, roles, &generator, true)
	if err != nil {
		return dump.Statement{}, err
	}
	// Emitted DDL creates a collision-free temporary operator using the genuine
	// selected routine, generates the original shell, then retires the helper.
	signature, err := operatorSQLSignature(p, view, &generator)
	if err != nil {
		return dump.Statement{}, err
	}
	declaration.SQL += "DROP OPERATOR " + signature + ";\n"
	return declaration, nil
}

// OperatorDumpEvents adds the deferred link events of defined operators.
// Link events use raw endpoint declarations. Only semantic consumers are
// redirected to completion; redirecting structural edges would invent cycles.
func OperatorDumpEvents(view catalog.Read, operators []*catalog.Operator, graph dump.Graph) {
	for _, source := range operators {
		if source.Function == 0 {
			continue
		}
		declaration := dump.Event{OID: source.Address().ObjectID}
		for kind, id := range [...]ids.OID{source.Commutator, source.Negator} {
			peer, _ := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
			if peer == nil || peer.Function == 0 || OperatorSeeded(peer) {
				continue
			}
			target := dump.Event{OID: id}
			if operatorDumpDeferredLink(view, source, kind, id) {
				event := dump.Event{OID: declaration.OID, Phase: dump.OperatorLinks}
				graph[event] = append(graph[event], declaration, target)
			} else {
				// Completing the target clears the transient reciprocal field
				// installed by this asymmetric CREATE.
				graph[target] = append(graph[target], declaration)
			}
		}
	}
}

// NormalizeOperatorDumpPredecessors redirects semantic dependencies on an
// operator declaration to its link completion and drops duplicate edges.
func NormalizeOperatorDumpPredecessors(view catalog.Read, graph dump.Graph) {
	for event, predecessors := range graph {
		_, operator := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: event.OID})
		structural := event.Phase == dump.OperatorLinks || operator && event.Phase == dump.Declaration
		unique := predecessors[:0]
		for _, predecessor := range predecessors {
			if !structural && predecessor.Phase == dump.Declaration {
				completion := dump.Event{OID: predecessor.OID, Phase: dump.OperatorLinks}
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

func operatorDumpLinks(p dump.Presenter, view catalog.Read, source *catalog.Operator) (string, error) {
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
	signature, err := operatorSQLSignature(p, view, source)
	if err != nil {
		return "", err
	}
	return "ALTER OPERATOR " + signature + " SET (" + strings.Join(options, ", ") + ");\n", nil
}
