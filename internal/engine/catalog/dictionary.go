package catalog

import (
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func (d *TSDictionary) Seeded() bool {
	return d.english || d.Address() == (ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
}
func (d *TSTemplate) Seeded() bool {
	return d.snowball || d.Address() == (ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727})
}

func InstallBuiltinDictionaries(state *State, owner authorization.PrincipalRef, runtime DictionaryRuntime) {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	template := &TSTemplate{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "simple", Init: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}, Lexize: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3726}}
	installSeedDescriptor(state, template)
	installSeedDescriptor(state, &TSDictionary{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "simple", Owner: owner, Template: template.Address(), Runtime: runtime})
}

// DictionaryOptionsText is the canonical ordered pg_ts_dict option representation.
func DictionaryOptionsText(options []DictionaryOption) string {
	var out strings.Builder
	for i, o := range options {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(DependencyIdentifier(o.Name))
		out.WriteString(" = ")
		if o.Numeric {
			out.WriteString(o.Value)
			continue
		}
		value := o.Value
		if strings.Contains(value, `\`) {
			out.WriteByte('E')
			value = strings.ReplaceAll(value, `\`, `\\`)
		}
		out.WriteByte('\'')
		out.WriteString(strings.ReplaceAll(value, "'", "''"))
		out.WriteByte('\'')
	}
	return out.String()
}
