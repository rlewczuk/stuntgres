package control

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// parkCubeNames releases real SQL keys without moving nominal identities out of
// their current namespace (in particular, without a pg_catalog round trip).
// Operand-bearing operators need no rename: a later cube has a different type.
func (s *dumpScript) parkCubeNames(item replayInstallation, baseline catalog.Read, reserved map[string]bool) (map[ids.ObjectAddress]string, error) {
	names := map[ids.ObjectAddress]string{}
	groups := map[string][]providerReplay{}
	serial := 0
	for _, d := range item.recipe.Declarations {
		if s.CatalogView.Get(d.Address()) == nil {
			continue
		}
		kind := ""
		signature := catalog.ProcedureSignature("")
		switch d := d.(type) {
		case *catalog.Procedure:
			kind, signature = "FUNCTION", d.Signature
		case *catalog.OperatorClass:
			kind = "OPERATOR CLASS"
		case *catalog.OperatorFamily:
			kind = "OPERATOR FAMILY"
		default:
			continue
		}
		identity, err := extension.OwnerIdentity(baseline, baseline.Get(d.Address()))
		if err != nil {
			return nil, err
		}
		if kind != "FUNCTION" {
			signature = catalog.ProcedureSignature(identity.Signature)
		}
		name := ""
		for {
			serial++
			name = "__stuntgres_park_" + strconv.Itoa(serial)
			if !reserved[name] {
				break
			}
		}
		reserved[name] = true
		names[d.Address()] = name
		groups[kind] = append(groups[kind], providerReplay{
			original:  providerKey{item.schema, d.Name(), signature},
			current:   providerKey{item.schema, name, signature},
			arguments: identity.Signature, objectKind: kind,
		})
	}
	for _, kind := range []string{"FUNCTION", "OPERATOR CLASS", "OPERATOR FAMILY"} {
		statements, err := planProviderReplay(groups[kind])
		if err != nil {
			return nil, err
		}
		s.installationStatements = append(s.installationStatements, statements...)
	}
	return names, nil
}
