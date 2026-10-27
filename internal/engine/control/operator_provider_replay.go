package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

// operatorProviderReplayStatements relocates installed recipe operators after
// their extension and final operand type identities exist. Installation creates
// these operators; emitting CREATE would replace their manifest authority.
func (s *dumpScript) operatorProviderReplayStatements() ([]string, error) {
	var statements []string
	for _, event := range s.eventOrder {
		if event.Phase != dump.Declaration {
			continue
		}
		ext, ok := s.CatalogView.Extension(ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: event.OID})
		if !ok {
			continue
		}
		for _, address := range ext.Members {
			op, ok := s.CatalogView.Operator(address)
			if !ok || op.Namespace() == ext.TargetNamespace {
				continue
			}
			if !extension.HstoreRelocatableOperator(s.CatalogView, ext, address) {
				return nil, extension.InvalidProviderDescriptor()
			}
			original, ok := s.CatalogView.Namespace(ext.TargetNamespace)
			if !ok {
				return nil, extension.InvalidProviderDescriptor()
			}
			current, ok := s.CatalogView.Namespace(op.Namespace())
			if !ok {
				return nil, extension.InvalidProviderDescriptor()
			}
			left := "NONE"
			if op.Left != 0 {
				left = regobj.TypeSpelling(s.CatalogView, nil, op.Left)
			}
			right := regobj.TypeSpelling(s.CatalogView, nil, op.Right)
			statement := "ALTER OPERATOR " + catalog.QuoteIdentifier(original.Name()) + "." + op.Name() + "(" + left + ", " + right + ") SET SCHEMA " + catalog.QuoteIdentifier(current.Name()) + ";\n"
			statements = append(statements, statement)
		}
	}
	return statements, nil
}
