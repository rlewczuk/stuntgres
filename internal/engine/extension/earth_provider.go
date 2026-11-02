package extension

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type EarthdistanceProvider struct{ control catalog.ExtensionControl }

var StockEarthdistance = &EarthdistanceProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Relocatable: true, Requires: []string{"cube"}, Comment: new("calculate great-circle distances on the surface of the Earth")}}

func (p *EarthdistanceProvider) Control() catalog.ExtensionControl {
	return p.control.Detached()
}
func (*EarthdistanceProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}

func earthCube(v catalog.Read) (*catalog.Type, error) {
	a := v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"})
	ext, ok := v.Extension(a)
	if !ok || len(ext.Members) == 0 {
		return nil, fmt.Errorf("stuntgres: missing earth cube installation")
	}
	if err := StockCube.ValidateExtension(v, ext); err != nil {
		return nil, err
	}
	base, _ := v.Type(ext.Members[0])
	return base, nil
}

// EarthCheckHelper resolves the closed CHECK recipe's genuine cube declaration.
// The sealed base-type cohort, not installed names or current e-membership,
// selects its immutable role. Current declaration/principal validation remains.
func EarthCheckHelper(view catalog.Read, base *catalog.Type, kind sqltype.EarthCheck) (*catalog.Procedure, error) {
	if base == nil || !kind.Valid() || ValidateTypeOrigin(view, base) != nil {
		return nil, fmt.Errorf("stuntgres: invalid earth CHECK base")
	}
	installation, ok := catalog.OriginInstallation(base)
	if !ok || installation.Implementation != StockCube || base.Origin.Declaration != 0 {
		return nil, fmt.Errorf("stuntgres: missing earth CHECK cube installation")
	}
	var slot int
	var symbol string
	switch kind {
	case sqltype.EarthNotPoint:
		slot, symbol = cubeRoutineMember(cubePointRoutineIndex), "cube_is_point"
	case sqltype.EarthNot3D:
		slot, symbol = cubeRoutineMember(cubeDimensionRoutineIndex), "cube_dim"
	default:
		slot, symbol = cubeSpatialProcedureStart+int(sqltype.CubeDistance), sqltype.CubeDistance.Routine()
	}
	if slot >= len(installation.Members) {
		return nil, fmt.Errorf("stuntgres: missing earth CHECK helper role")
	}
	helper, ok := view.Procedure(installation.Members[slot])
	if !ok || !catalog.SameInstallation(helper, installation) {
		return nil, fmt.Errorf("stuntgres: missing earth CHECK helper")
	}
	actual, err := ProcedureSymbol(view, helper)
	if err != nil || actual != symbol {
		return nil, fmt.Errorf("stuntgres: invalid earth CHECK helper declaration")
	}
	return helper, nil
}

func (*EarthdistanceProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	read := stage.CurrentRead()
	cube, err := earthCube(read)
	if err != nil {
		return err
	}
	ns := read.Get(cube.Namespace()).Name()
	available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, "earth")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(stage.CurrentRead(), ext.TargetNamespace, "earth") {
		q := catalog.DependencyIdentifier(ns)
		context := "SQL statement \"CREATE DOMAIN earth AS public.cube\n  CONSTRAINT not_point CHECK(public.cube_is_point(VALUE))\n  CONSTRAINT not_3d CHECK(public.cube_dim(VALUE) <= 3)\n  CONSTRAINT on_surface CHECK(abs(public.cube_distance(VALUE, '(0)'::public.cube) /\n  earth() - '1'::float8) < '10e-7'::float8)\"\nextension script file \"earthdistance--1.1.sql\", near line 30"
		context = strings.ReplaceAll(context, "public.", q+".")
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "earth" already exists`, Context: context}
	}
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	arrayOID, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	declaration := catalog.DependencyIdentifier(read.Get(ext.TargetNamespace).Name()) + ".earth"
	typ, err := sqltype.Domain(sqltype.ID(oid), "earth", declaration, cube.SQLType.Usage(), true)
	if err != nil {
		return err
	}
	array := sqltype.LookupArray(sqltype.ID(arrayOID), stage.AssociatedArrayName(ext.TargetNamespace, "earth"), typ)
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	acl, synthetic := stage.CreationTypeACL(owner, ext.TargetNamespace)
	domain := &catalog.Type{SQLType: typ, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID()}
	if err := stage.EarthChecks(domain); err != nil {
		return err
	}
	// PostgreSQL's script explicitly owns both the domain and companion array.
	ext.Members = make([]ids.ObjectAddress, EarthMemberCount)
	ext.Members[0] = domain.Address()
	ext.Members[1] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: arrayOID}
	if err := stage.Create(domain); err != nil {
		return err
	}
	if err := stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}); err != nil {
		return err
	}
	return StockEarthdistance.stageRoutines(stage, ext, owner, typ)
}

func (p *EarthdistanceProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid earthdistance manifest") }
	if p != StockEarthdistance || !currentManifest(v, ext) || ext.Name() != "earthdistance" || ext.Version != "1.2" || len(ext.Members) != EarthMemberCount {
		return invalid()
	}
	cube, err := earthCube(v)
	if err != nil {
		return err
	}
	d, ok := v.Type(ext.Members[0])
	if !ok || !d.SQLType.IsDomain() || d.Extension != ext.Address() || d.Revision() == 0 {
		return invalid()
	}
	a, ok := v.Type(ext.Members[1])
	if !ok || a.Address().ObjectID != ids.OID(d.Array) || !a.SQLType.IsArray() || a.SQLType.Concrete() || a.SQLType.Element() != d.SQLType || a.Owner != d.Owner || a.Namespace() != d.Namespace() || a.Extension != ext.Address() {
		return invalid()
	}
	if err := validateEarthDomainChecks(d, cube.SQLType.Usage()); err != nil {
		return err
	}
	for slot := 2; slot < EarthMemberCount; slot++ {
		if slot == EarthPointOperatorSlot {
			continue
		}
		proc, ok := v.Procedure(ext.Members[slot])
		if !ok || proc.Implementation != p {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return p.validatePoint(v, ext)
}

// The surviving domain retains the released CHECK recipe independently of
// current extension membership and the originating extension's existence.
func validateEarthDomainChecks(d *catalog.Type, base sqltype.Usage) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid earthdistance domain recipe") }
	if d.SQLType.DomainBase() != base || !d.SQLType.HasDomainChecks() || d.SQLType.OwnDomainNotNull() || len(d.DomainChecks) != 3 {
		return invalid()
	}
	for i, name := range []string{"not_3d", "not_point", "on_surface"} {
		check := d.DomainChecks[i]
		recipe, ok := check.Expression.(interface {
			EarthCheckRecipe() (sqltype.EarthCheck, sqltype.Usage, bool)
		})
		if !ok {
			return invalid()
		}
		actualKind, actualBase, valid := recipe.EarthCheckRecipe()
		kind, ordinal := sqltype.EarthNot3D, 1
		if i == 1 {
			kind, ordinal = sqltype.EarthNotPoint, 0
		}
		if i == 2 {
			kind, ordinal = sqltype.EarthOnSurface, 2
		}
		if !valid || actualBase != base || actualKind != kind || check.Name != name || check.Ordinal != ordinal || check.NotValid || check.OID == 0 {
			return invalid()
		}
		for _, previous := range d.DomainChecks[:i] {
			if previous.OID == check.OID {
				return invalid()
			}
		}
	}
	return nil
}
