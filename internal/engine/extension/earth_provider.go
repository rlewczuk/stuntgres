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

func (*EarthdistanceProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	cube, err := earthCube(stage.Read())
	if err != nil {
		return err
	}
	ns := stage.Read().Get(cube.Namespace()).Name()
	available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, "earth")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, "earth") {
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
	declaration := catalog.DependencyIdentifier(stage.Read().Get(ext.TargetNamespace).Name()) + ".earth"
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
	ext.Members = []ids.ObjectAddress{domain.Address(), {ClassID: ids.TypeClassID, ObjectID: arrayOID}}
	if err := stage.Create(domain); err != nil {
		return err
	}
	return stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true})
}

func (p *EarthdistanceProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid earthdistance manifest") }
	if p != StockEarthdistance || ext.Name() != "earthdistance" || ext.Version != "1.2" || len(ext.Members) != 2 {
		return invalid()
	}
	cube, err := earthCube(v)
	if err != nil {
		return err
	}
	d, ok := v.Type(ext.Members[0])
	if !ok || !d.SQLType.IsDomain() || d.SQLType.DomainBase() != cube.SQLType.Usage() || !d.SQLType.HasDomainChecks() || d.SQLType.OwnDomainNotNull() || len(d.DomainChecks) != 3 || d.Extension != ext.Address() || d.Revision() == 0 {
		return invalid()
	}
	a, ok := v.Type(ext.Members[1])
	if !ok || a.Address().ObjectID != ids.OID(d.Array) || !a.SQLType.IsArray() || a.SQLType.Concrete() || a.SQLType.Element() != d.SQLType || a.Owner != d.Owner || a.Namespace() != d.Namespace() || a.Extension != ext.Address() {
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
		if !valid || actualBase != cube.SQLType.Usage() || actualKind != kind || check.Name != name || check.Ordinal != ordinal || check.NotValid || check.OID == 0 {
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
