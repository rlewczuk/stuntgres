package catalog

import (
	"fmt"
	"slices"
	"strings"
)

// ExtensionVersionEdge records a shipped directed update script. An edge whose
// source cannot be installed is metadata only, never an executable promise.
type ExtensionVersionEdge struct{ From, To string }
type ExtensionVersions struct {
	Bases []string
	Edges []ExtensionVersionEdge
}

func (v ExtensionVersions) Detached() ExtensionVersions {
	return ExtensionVersions{Bases: slices.Clone(v.Bases), Edges: slices.Clone(v.Edges)}
}

// ExtensionNameDetail is the released filename-safety validation, in precedence
// order. The caller supplies the extension/version diagnostic family.
func ExtensionNameDetail(name string, version bool) string {
	prefix := "Extension names"
	if version {
		prefix = "Version names"
	}
	switch {
	case name == "":
		return prefix + " must not be empty."
	case strings.Contains(name, "--"):
		return prefix + ` must not contain "--".`
	case strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-"):
		return prefix + ` must not begin or end with "-".`
	case strings.ContainsAny(name, "/\\"):
		return prefix + " must not contain directory separator characters."
	}
	return ""
}

func (v ExtensionVersions) Vertices() []string {
	names := slices.Clone(v.Bases)
	for _, edge := range v.Edges {
		names = append(names, edge.From, edge.To)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func (v ExtensionVersions) hasVersion(name string) bool {
	if slices.Contains(v.Bases, name) {
		return true
	}
	for _, edge := range v.Edges {
		if edge.From == name || edge.To == name {
			return true
		}
	}
	return false
}

// Path finds a shortest directed script path, including both endpoints. Nil
// means unreachable. Declared edge order breaks ties internally; it does not
// establish an SQL row-order promise.
func (v ExtensionVersions) Path(from, to string) []string {
	if !v.hasVersion(from) {
		return nil
	}
	queue := []string{from}
	predecessor := map[string]string{from: ""}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		if current == to {
			path := []string{current}
			for current != from {
				current = predecessor[current]
				path = append(path, current)
			}
			slices.Reverse(path)
			return path
		}
		for _, edge := range v.Edges {
			if edge.From != current {
				continue
			}
			if _, seen := predecessor[edge.To]; !seen {
				predecessor[edge.To] = current
				queue = append(queue, edge.To)
			}
		}
	}
	return nil
}

// InstallPath chooses a real base plus the fewest update scripts; graph-only
// sources do not become installable simply by appearing in an edge.
func (v ExtensionVersions) InstallPath(target string) []string {
	var best []string
	bases := slices.Clone(v.Bases)
	slices.Sort(bases)
	for _, base := range bases {
		path := v.Path(base, target)
		if path != nil && (best == nil || len(path) < len(best)) {
			best = path
		}
	}
	return best
}
func (v ExtensionVersions) Installable() []string {
	var result []string
	for _, name := range v.Vertices() {
		if v.InstallPath(name) != nil {
			result = append(result, name)
		}
	}
	return result
}
func (v ExtensionVersions) Validate(defaultVersion string) error {
	if len(v.Bases) == 0 {
		return fmt.Errorf("stuntgres: extension has no base version")
	}
	seen := map[string]bool{}
	for _, base := range v.Bases {
		if ExtensionNameDetail(base, true) != "" || seen[base] {
			return fmt.Errorf("stuntgres: invalid or duplicate extension base version")
		}
		seen[base] = true
	}
	edges := map[ExtensionVersionEdge]bool{}
	for _, edge := range v.Edges {
		if ExtensionNameDetail(edge.From, true) != "" || ExtensionNameDetail(edge.To, true) != "" || edge.From == edge.To || edges[edge] {
			return fmt.Errorf("stuntgres: invalid or duplicate extension update edge")
		}
		edges[edge] = true
	}
	if ExtensionNameDetail(defaultVersion, true) != "" || v.InstallPath(defaultVersion) == nil {
		return fmt.Errorf("stuntgres: extension default is not installable")
	}
	return nil
}
