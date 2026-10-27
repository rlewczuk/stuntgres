package tablecmds

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type analyzeTarget struct {
	ref     ids.ObjectRef
	columns []parser.Identifier
	named   bool
	name    string
	group   int
}

func analyzeWarning(s *core.StatementScope, code, message string) {
	s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message})
}

// Resolve every relation name before starting work, but validate column names
// only at each target: PostgreSQL preserves earlier standalone target progress.
func analyzeTargets(e *core.Engine, s *core.StatementScope, raw *parser.Analyze) ([]analyzeTarget, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return nil, err
	}
	var targets []analyzeTarget
	if len(raw.Targets) == 0 {
		for _, descriptor := range auth.Catalog.Descriptors() {
			if relation, ok := descriptor.(*catalog.Relation); ok && (table.VacuumHeap(relation) || relation.Partition != nil) && !table.VacuumForeignTemp(auth.Catalog, s.Op.Session.SessionToken, relation) {
				allowed, err := table.VacuumAllowed(s.Op.Context(), auth.View, auth, relation)
				if err != nil {
					return nil, err
				}
				if !allowed {
					continue
				}
				targets = append(targets, analyzeTarget{ref: auth.Ref(relation.Address())})
			}
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].ref.Address.ObjectID < targets[j].ref.Address.ObjectID })
		return targets, nil
	}
	for occurrence, target := range raw.Targets {
		ref, err := resolveVacuumReference(e, auth, target.Name)
		if err != nil {
			return nil, err
		}
		targets = append(targets, analyzeTarget{ref: ref, columns: target.Columns, named: true, name: target.Name.Parts[len(target.Name.Parts)-1].Text, group: occurrence + 1})
		if target.Only {
			continue
		}
		relation, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		children, err := analyzeClosure(auth.Catalog, relation, s.Op.Session.SessionToken)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.Address() != ref.Address {
				targets = append(targets, analyzeTarget{ref: auth.Ref(child.Address()), columns: target.Columns, group: occurrence + 1})
			}
		}
	}
	return targets, nil
}

func analyzeClosure(read catalog.Read, root *catalog.Relation, token ids.SessionToken) ([]*catalog.Relation, error) {
	var result []*catalog.Relation
	seen := make(map[ids.ObjectAddress]bool)
	var visit func(*catalog.Relation) error
	visit = func(relation *catalog.Relation) error {
		if seen[relation.Address()] || table.VacuumForeignTemp(read, token, relation) {
			return nil
		}
		seen[relation.Address()] = true
		if relation.Foreign != nil {
			return nil
		}
		result = append(result, relation)
		children := catalog.InheritanceChildren(read, relation.Address())
		if relation.Partition != nil {
			for _, address := range relation.Partition.Children {
				child, ok := read.Relation(address)
				if !ok {
					return ids.ErrCatalogReplaced
				}
				children = append(children, child)
			}
		}
		for _, child := range children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	err := visit(root)
	return result, err
}

func ExecuteAnalyze(e *core.Engine, s *core.StatementScope, raw *parser.Analyze, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	options, err := table.NormalizeAnalyze(raw)
	if err != nil {
		return core.Result{}, err
	}
	targets, err := analyzeTargets(e, s, raw)
	if err != nil {
		return core.Result{}, err
	}
	skippedGroup := 0
	for _, target := range targets {
		if target.group != 0 && target.group == skippedGroup {
			continue
		}
		skipped := false
		checkpoint := e.Locks.Checkpoint(s.LockTicket)
		err = func() error {
			defer e.Locks.Restore(s.LockTicket, checkpoint)
			if err := vacuumAcquire(s, target.ref, lock.ShareUpdateExclusive, options.SkipLocked); err != nil {
				if err == lock.ErrUnavailable && options.SkipLocked {
					skipped = true
					if target.named {
						analyzeWarning(s, "55P03", fmt.Sprintf(`skipping analyze of "%s" --- lock not available`, target.name))
					}
					return nil
				}
				return err
			}
			err := analyzeRelation(e, s, target, options)
			if err == lock.ErrUnavailable && options.SkipLocked {
				skipped = true
				if target.named {
					analyzeWarning(s, "55P03", fmt.Sprintf(`skipping analyze of "%s" --- lock not available`, target.name))
				}
				return nil
			}
			return err
		}()
		if err != nil {
			return core.Result{}, err
		}
		if skipped && target.named {
			skippedGroup = target.group
		}
	}
	s.CompleteUtility()
	return core.Result{}, nil
}
