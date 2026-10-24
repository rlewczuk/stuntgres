package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var CitextPatternNames = [...]string{"texticlike", "texticnlike", "texticregexeq", "texticregexne"}
var citextPatternOperators = [...]string{"~", "~*", "!~", "!~*", "~~", "~~*", "!~~", "!~~*"}
var citextPatternKinds = [...]int{2, 2, 3, 3, 0, 0, 1, 1}
var citextPatternNegators = [...]int{2, 3, 0, 1, 6, 7, 4, 5}
var citextPatternRestrictions = [...]ids.OID{1814, 1815, 1820, 1823}
var citextPatternJoins = [...]ids.OID{1816, 1817, 1826, 1829}

func CitextPatternName(name string) bool {
	for _, candidate := range CitextPatternNames {
		if name == candidate {
			return true
		}
	}
	return false
}
func CitextPatternSignature(left, right sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(string(CitextSignature(left)) + "," + string(CitextSignature(right)))
}
func CitextOperatorSlot(slot int) bool {
	return slot >= CitextComparisonOperatorStart && slot < CitextConversionProcedureStart || slot >= CitextPatternOperatorStart && slot < CitextMemberCount
}
func citextPatternRight(id sqltype.ID, pair int) sqltype.ID {
	if pair == 1 {
		return sqltype.Text
	}
	return id
}
func (p *CitextProvider) stagePatterns(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	for kind, name := range CitextPatternNames {
		for pair := range 2 {
			revision, err := stage.Revision()
			if err != nil {
				return err
			}
			proc := &catalog.Procedure{ObjectAddress: ext.Members[CitextPatternProcedureStart+kind*2+pair], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: CitextPatternSignature(typ.ID(), citextPatternRight(typ.ID(), pair)), RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: ids.OID(sqltype.Bool), Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: name}, Implementation: p}
			if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
			}
			if err := stage.Create(proc); err != nil {
				return err
			}
		}
	}
	for pair := range 2 {
		for i, name := range citextPatternOperators {
			revision, err := stage.Revision()
			if err != nil {
				return err
			}
			kind := citextPatternKinds[i]
			op := &catalog.Operator{ObjectAddress: ext.Members[CitextPatternOperatorStart+pair*8+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Owner: owner, Left: ids.OID(typ.ID()), Right: ids.OID(citextPatternRight(typ.ID(), pair)), Result: ids.OID(sqltype.Bool), Function: ext.Members[CitextPatternProcedureStart+kind*2+pair].ObjectID, Negator: ext.Members[CitextPatternOperatorStart+pair*8+citextPatternNegators[i]].ObjectID, Restriction: citextPatternRestrictions[kind], Join: citextPatternJoins[kind]}
			if err := stage.Create(op); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateCitextPatternOperator(v catalog.Read, ext *catalog.Extension, base *catalog.Type, i int) error {
	if i < 0 || i >= 16 {
		return fmt.Errorf("stuntgres: invalid citext pattern slot")
	}
	pair, index := i/8, i%8
	kind := citextPatternKinds[index]
	op, ok := v.Operator(ext.Members[CitextPatternOperatorStart+i])
	if !ok || op.Address() != ext.Members[CitextPatternOperatorStart+i] || op.Namespace() != ext.TargetNamespace || op.Name() != citextPatternOperators[index] || op.Extension != ext.Address() || op.Left != ids.OID(base.SQLType.ID()) || op.Right != ids.OID(citextPatternRight(base.SQLType.ID(), pair)) || op.Result != ids.OID(sqltype.Bool) || op.Function != ext.Members[CitextPatternProcedureStart+kind*2+pair].ObjectID || op.Negator != ext.Members[CitextPatternOperatorStart+pair*8+citextPatternNegators[index]].ObjectID || op.Commutator != 0 || op.Hash || op.Merge || op.Restriction != citextPatternRestrictions[kind] || op.Join != citextPatternJoins[kind] {
		return fmt.Errorf("stuntgres: invalid citext pattern operator")
	}
	return ValidateProviderHeader(v, op, ids.OperatorClassID)
}
