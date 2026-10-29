package control

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

type membershipReplayNode struct {
	descriptor catalog.Descriptor
	closure    []catalog.Descriptor
	sql        string
}

// Replay recursive roots before their companions, then reconcile each child's
// independent final membership. ADD root can temporarily attach an array that
// was later detached or foreign-attached in the source; that is not final state.
func (s *dumpScript) finalMembershipReplay() ([]string, error) {
	nodes := map[ids.ObjectAddress]*membershipReplayNode{}
	ensure := func(d catalog.Descriptor) (*membershipReplayNode, error) {
		if node := nodes[d.Address()]; node != nil {
			return node, nil
		}
		closure, err := catalog.ExtensionMemberClosure(s.CatalogView, d)
		if err != nil {
			return nil, err
		}
		sql, err := extension.MemberSQL(s.CatalogView, d)
		if err != nil {
			return nil, err
		}
		node := &membershipReplayNode{d, closure, sql}
		nodes[d.Address()] = node
		return node, nil
	}
	for _, d := range s.CatalogView.Descriptors() {
		if catalog.ExtensionMembership(d) == (ids.ObjectAddress{}) {
			continue
		}
		node, err := ensure(d)
		if err != nil {
			return nil, err
		}
		for _, companion := range node.closure {
			if _, err := ensure(companion); err != nil {
				return nil, err
			}
		}
	}
	ordered := make([]*membershipReplayNode, 0, len(nodes))
	for _, node := range nodes {
		ordered = append(ordered, node)
	}
	slices.SortFunc(ordered, func(a, b *membershipReplayNode) int {
		if len(a.closure) > len(b.closure) {
			return -1
		}
		if len(a.closure) < len(b.closure) {
			return 1
		}
		return strings.Compare(a.sql, b.sql)
	})
	current := map[ids.ObjectAddress]ids.ObjectAddress{}
	for address, extension := range s.preservedSeedMembership {
		current[address] = extension
	}
	var statements []string
	for _, node := range ordered {
		address := node.descriptor.Address()
		have, want := current[address], catalog.ExtensionMembership(node.descriptor)
		if have == want {
			continue
		}
		if have != (ids.ObjectAddress{}) {
			ext, ok := s.CatalogView.Extension(have)
			if !ok {
				return nil, extension.InvalidProviderDescriptor()
			}
			statements = append(statements, "ALTER EXTENSION "+catalog.QuoteIdentifier(ext.Name())+" DROP "+node.sql+";\n")
			for _, d := range node.closure {
				current[d.Address()] = ids.ObjectAddress{}
			}
		}
		if want != (ids.ObjectAddress{}) {
			ext, ok := s.CatalogView.Extension(want)
			if !ok {
				return nil, extension.InvalidProviderDescriptor()
			}
			statements = append(statements, "ALTER EXTENSION "+catalog.QuoteIdentifier(ext.Name())+" ADD "+node.sql+";\n")
			for _, d := range node.closure {
				current[d.Address()] = want
			}
		}
	}
	return statements, nil
}
