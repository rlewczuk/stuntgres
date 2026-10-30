package catalog

import (
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func (d *TSDictionary) Seeded() bool {
	return d.english || d.Pinned()
}
func (d *TSTemplate) Seeded() bool {
	return d.snowball || d.Pinned()
}

// Pinned distinguishes fixed bootstrap catalog identities from allocated initdb
// Snowball seeds, which retain seed replay authority but ordinary dependencies.
func (d *TSDictionary) Pinned() bool {
	return d.Address() == (ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
}
func (d *TSTemplate) Pinned() bool {
	return d.Address() == (ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}) || d.Address() == (ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3730}) || d.Address() == (ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3733}) || d.Address() == (ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3742})
}

func InstallBuiltinDictionaries(state *State, owner authorization.PrincipalRef, runtime DictionaryRuntime) {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	template := &TSTemplate{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "simple", Init: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}, Lexize: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3726}}
	installSeedDescriptor(state, template)
	installSeedDescriptor(state, &TSTemplate{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3742}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "thesaurus", Init: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3740}, Lexize: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3741}})
	installSeedDescriptor(state, &TSTemplate{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3733}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "ispell", Init: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3731}, Lexize: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3732}})
	installSeedDescriptor(state, &TSDictionary{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "simple", Owner: owner, Template: template.Address(), Runtime: runtime})
	installSeedDescriptor(state, &TSTemplate{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3730}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "synonym", Init: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3728}, Lexize: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3729}})
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
