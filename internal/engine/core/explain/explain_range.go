package explain

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// These maps borrow real owners only during capture. Only document is published;
// it contains detached scalar/container values, never these traversal records.
type rangeCapture struct {
	host      Host
	document  map[string]any
	nextEntry int
	viewPath  map[ids.ObjectAddress]bool
}

type rangeScope struct {
	id      int
	value   map[string]any
	nodes   map[plan.Node]map[string]any
	sources map[int]map[string]any
}

type rangeEnvironment struct {
	auth             access.View
	invoker          authorization.PrincipalRef
	leaves           []exec.Leaf
	queries          map[rangeQueryKey]*rangeScope
	frames           map[rangeCoordinate]*rangeScope
	producers        map[*plan.CTEDefinition]*rangeScope
	declarations     map[*plan.CTEDefinition]rangeDeclaration
	localDefinitions []*plan.CTEDefinition
	rewrites         view.RuleCTECommands
	demand           rangeDemandContext
	write            *rangeWrite
}

type rangeQueryKey struct {
	query  *plan.Query
	parent *rangeScope
}

type rangeWrite struct {
	binding   analyze.Binding
	statement parser.Statement
	layer     int
}

func (c *rangeCapture) environment(a access.View, leaves []exec.Leaf, invoker authorization.PrincipalRef, inherited *rangeEnvironment) *rangeEnvironment {
	env := &rangeEnvironment{auth: a, invoker: invoker, leaves: leaves, queries: make(map[rangeQueryKey]*rangeScope), frames: make(map[rangeCoordinate]*rangeScope), demand: rangeDemandContext{executableRead: true}}
	if inherited == nil {
		env.producers = make(map[*plan.CTEDefinition]*rangeScope)
		env.declarations = make(map[*plan.CTEDefinition]rangeDeclaration)
	} else {
		env.producers, env.declarations, env.rewrites = inherited.producers, inherited.declarations, inherited.rewrites
	}
	return env
}

func (c *rangeCapture) scope(kind string, parent *rangeScope, owner map[string]any) *rangeScope {
	scopes := c.document["Scopes"].([]any)
	id := len(scopes) + 1
	s := &rangeScope{id: id, value: map[string]any{"Scope ID": id, "Kind": kind, "Entries": []any{}}, nodes: make(map[plan.Node]map[string]any), sources: make(map[int]map[string]any)}
	if parent != nil {
		s.value["Parent Scope ID"] = parent.id
	}
	if owner != nil {
		s.value["Owner Entry ID"] = owner["Entry ID"]
	}
	c.document["Scopes"] = append(scopes, s.value)
	return s
}

func (c *rangeCapture) entry(s *rangeScope, kind, name string, columns []catalog.Column, aliases []string) map[string]any {
	c.nextEntry++
	labels := make([]string, len(columns))
	for i, column := range columns {
		labels[i] = column.Name
		if i < len(aliases) {
			labels[i] = aliases[i]
		}
	}
	e := map[string]any{"Entry ID": c.nextEntry, "Kind": kind, "Columns": labels}
	if name != "" {
		e["Range Name"] = name
	}
	s.value["Entries"] = append(s.value["Entries"].([]any), e)
	return e
}

func (c *rangeCapture) relation(e map[string]any, a access.View, ref ids.ObjectRef, role string) error {
	relation, ok := a.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	namespace, ok := a.Catalog.Namespace(relation.Namespace())
	if !ok {
		return ids.ErrCatalogReplaced
	}
	e["Relation OID"], e["Schema"], e["Relation Name"], e["Relation Kind"] = uint32(ref.Address.ObjectID), namespace.Name(), relation.Name(), pgcatalog.ClassKind(a.Catalog, relation)
	e["Role"], e["Required Permissions"] = role, []any{}
	return nil
}

func (c *rangeCapture) permission(e map[string]any, a access.View, principal authorization.PrincipalRef, req analyze.Requirement) error {
	role, err := a.Roles.Descriptor(principal)
	if err != nil {
		return err
	}
	privilege := ""
	switch req.Privilege {
	case authorization.SelectPrivilege:
		privilege = "SELECT"
	case authorization.InsertPrivilege:
		privilege = "INSERT"
	case authorization.UpdatePrivilege:
		privilege = "UPDATE"
	case authorization.DeletePrivilege:
		privilege = "DELETE"
	default:
		return fmt.Errorf("stuntgres: invalid range permission demand")
	}
	columns := make([]string, len(req.Columns))
	for i, slot := range req.Columns {
		if slot < 0 || slot >= len(req.Relation.Columns) {
			return ids.ErrCatalogReplaced
		}
		columns[i] = req.Relation.Columns[slot].Name
	}
	demand := map[string]any{"Principal": role.Name, "Privilege": privilege, "Columns": columns, "Whole Relation": req.Whole()}
	e["Required Permissions"] = append(e["Required Permissions"].([]any), demand)
	return nil
}

func (r *Renderer) captureRanges(statement parser.Statement, bound Bound) error {
	if !r.options.RangeTable {
		return nil
	}
	c := rangeCapture{host: r.host, document: map[string]any{"Scopes": []any{}}, viewPath: make(map[ids.ObjectAddress]bool)}
	kind := "command"
	if _, ok := statement.(*parser.Select); ok {
		kind = "query"
	}
	root := c.scope(kind, nil, nil)
	if err := c.binding(root, r.auth.View, statement, bound.Binding, nil); err != nil {
		return err
	}
	if bound.Rules != nil {
		for _, command := range bound.Rules.Commands {
			s := c.scope("rule", root, nil)
			if err := c.binding(s, command.Auth.Access(), command.Syntax, command.Binding, nil); err != nil {
				return err
			}
		}
	}
	r.rangeTable = c.document
	return nil
}

func rangeTargetName(statement parser.Statement, relation *catalog.Relation) string {
	name := relation.Name()
	switch syntax := statement.(type) {
	case *parser.Insert:
		if syntax.Alias.Text != "" {
			name = syntax.Alias.Text
		}
	case *parser.Update:
		if syntax.Alias.Text != "" {
			name = syntax.Alias.Text
		}
	case *parser.Delete:
		if syntax.Alias.Text != "" {
			name = syntax.Alias.Text
		}
	case *parser.Merge:
		if syntax.Alias.Text != "" {
			name = syntax.Alias.Text
		}
	}
	return name
}

func (c *rangeCapture) binding(s *rangeScope, a access.View, statement parser.Statement, binding analyze.Binding, inherited *rangeEnvironment) error {
	logical := binding
	if binding.ViewWrite != nil {
		logical = view.ViewWriteOf(binding.ViewWrite).Logical
	}
	env := c.environment(a, logical.QuerySources, a.State.CurrentUser, inherited)
	if local := view.RuleCTEsOf(binding.Rule.CTEs); len(local) != 0 {
		if len(env.rewrites) == 0 {
			env.rewrites = local
		} else {
			env.rewrites = maps.Clone(env.rewrites)
			maps.Copy(env.rewrites, local)
		}
	}
	for _, definition := range logical.CTEs {
		if _, known := env.declarations[definition]; !known {
			env.declarations[definition] = rangeDeclaration{}
			env.localDefinitions = append(env.localDefinitions, definition)
		}
	}
	if logical.Relation != nil {
		c.registerFrame(s, env, 1)
		e := c.entry(s, "relation", rangeTargetName(statement, logical.Relation), logical.Relation.Columns, nil)
		if err := c.relation(e, a, logical.Ref, "target"); err != nil {
			return err
		}
		if err := analyze.BindingRequirements(logical, statement, func(req analyze.Requirement) error { return c.permission(e, a, a.State.CurrentUser, req) }); err != nil {
			return err
		}
		if binding.ViewWrite != nil {
			if err := c.writeLayer(s, e, env, binding, statement, 0); err != nil {
				return err
			}
		}
	}
	if logical.Query != nil {
		queryScope := s
		if _, selectStatement := statement.(*parser.Select); !selectStatement {
			// INSERT's SELECT producer owns a query namespace independently
			// of the command target/RETURNING namespace.
			queryScope = c.scope("query", s, nil)
		}
		env.queries[rangeQueryKey{logical.Query, s}] = queryScope
		if err := c.queryNodes(queryScope, env, logical.Query.Root); err != nil {
			return err
		}
	}
	// All command-local frames must be known before an expression enters a
	// child query. INSERT's producer above deliberately has its own frame map.
	if logical.Update != nil {
		c.registerFrames(s, env, logical.Update.From)
	}
	c.registerFrames(s, env, logical.Delete.Using)
	if logical.Merge != nil {
		c.registerFrames(s, env, logical.Merge.Source)
	}
	if logical.Update != nil {
		if err := c.node(s, env, logical.Update.From); err != nil {
			return err
		}
	}
	if err := c.node(s, env, logical.Delete.Using); err != nil {
		return err
	}
	if logical.Merge != nil {
		if err := c.node(s, env, logical.Merge.Source); err != nil {
			return err
		}
	}
	if err := c.bindingExpressions(s, env, logical); err != nil {
		return err
	}
	for _, definition := range env.localDefinitions {
		if _, err := c.producer(env, definition); err != nil {
			return err
		}
	}
	return nil
}

func (c *rangeCapture) view(s *rangeScope, e map[string]any, env *rangeEnvironment, relation *catalog.Relation, mark plan.RowMark, demand rangeDemandContext) error {
	if c.viewPath[relation.Address()] {
		return fmt.Errorf("stuntgres: recursive stored EXPLAIN source")
	}
	c.viewPath[relation.Address()] = true
	defer delete(c.viewPath, relation.Address())
	definition := stored.ViewOf(relation)
	if definition == nil {
		return ids.ErrCatalogReplaced
	}
	a := env.auth
	a.State.CurrentUser = relation.Owner
	if definition.Invoker() {
		a.State.CurrentUser = env.invoker
	}
	leaves := make([]exec.Leaf, len(definition.Sources()))
	for i := range leaves {
		var err error
		leaves[i], err = view.SourceRequirementLeaf(a, relation, i, mark)
		if err != nil {
			return err
		}
	}
	child := c.scope("view", s, e)
	e["Child Scope ID"] = child.id
	next := c.environment(a, leaves, env.invoker, nil)
	next.demand = demand
	next.queries[rangeQueryKey{definition.Query(), child}] = child
	return c.queryNodes(child, next, definition.Query().Root)
}

func (c *rangeCapture) writeLayer(parent *rangeScope, owner map[string]any, env *rangeEnvironment, b analyze.Binding, statement parser.Statement, index int) error {
	w := view.ViewWriteOf(b.ViewWrite)
	if index >= len(w.Layers) {
		return nil
	}
	layer := w.Layers[index]
	relation, ok := env.auth.Catalog.Relation(layer.Ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	definition := stored.ViewOf(relation)
	a := env.auth
	a.State.CurrentUser = relation.Owner
	if definition.Invoker() {
		a.State.CurrentUser = env.invoker
	}
	leaves := make([]exec.Leaf, len(definition.Sources()))
	for i := range leaves {
		leaf, included, err := view.WriteSourceRequirementLeaf(a, b, relation, layer, i)
		if err != nil {
			return err
		}
		if !included {
			leaf, err = view.SourceRequirementLeaf(a, relation, i, plan.RowMark{})
			if err != nil {
				return err
			}
			leaf.AnalysisOnly, leaf.Mark = true, plan.RowMark{}
		}
		leaves[i] = leaf
	}
	s := c.scope("view", parent, owner)
	owner["Child Scope ID"] = s.id
	next := c.environment(a, leaves, env.invoker, nil)
	next.write = &rangeWrite{binding: b, statement: statement, layer: index}
	next.queries[rangeQueryKey{definition.Query(), s}] = s
	if err := c.queryNodes(s, next, definition.Query().Root); err != nil {
		return err
	}
	e := s.sources[layer.MainOccurrence]
	if e == nil {
		return fmt.Errorf("stuntgres: missing view write range owner")
	}
	e["Role"] = "target/source"
	delete(e, "Analysis Only") // The actual write target remains executable.
	source, ok := a.Catalog.Relation(layer.SourceRef.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	return c.permission(e, a, a.State.CurrentUser, view.WriteTargetRequirement(statement, layer, source))
}
