package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

func CollectOperatorGroups(view catalog.Read) exec.OperatorGroups {
	groups := make(exec.OperatorGroups)
	for key, addresses := range view.State().OperatorNames().Buckets {
		for _, address := range addresses {
			d := view.MustOperator(address)
			groups[key.Name] = append(groups[key.Name], d)
		}
	}
	return groups
}

// Earlier namespaces mask only the same ordered operand signature.
func VisibleOperators(groups exec.OperatorGroups, path []ids.ObjectAddress, name string) []*catalog.Operator {
	var result []*catalog.Operator
	seen := make(map[analyze.OperatorSignature]bool)
	for _, ns := range path {
		for _, d := range groups[name] {
			if d.Namespace() != ns {
				continue
			}
			signature := analyze.OperatorSignature{Left: d.Left, Right: d.Right}
			if !seen[signature] {
				seen[signature] = true
				result = append(result, d)
			}
		}
	}
	return result
}
