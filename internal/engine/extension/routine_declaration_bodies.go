package extension

import (
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// REL_18_6 citext--1.7--1.8.sql replaces the quoted declarations with
// SQL-standard bodies. These immutable syntax owners grant no execution rights.
var citextRegexpDeclarationBodies = [...]sqltype.StructuralTree{
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_match( $1::text, $2::text, 'i' );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_match( $1::text, $2::text, CASE WHEN pg_catalog.strpos($3, 'c') = 0 THEN  $3 || 'i' ELSE $3 END );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_matches( $1::text, $2::text, 'i' );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_matches( $1::text, $2::text, CASE WHEN pg_catalog.strpos($3, 'c') = 0 THEN  $3 || 'i' ELSE $3 END );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_replace( $1::text, $2::text, $3, 'i');`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_replace( $1::text, $2::text, $3, CASE WHEN pg_catalog.strpos($4, 'c') = 0 THEN  $4 || 'i' ELSE $4 END);`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_split_to_array( $1::text, $2::text, 'i' );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_split_to_array( $1::text, $2::text, CASE WHEN pg_catalog.strpos($3, 'c') = 0 THEN  $3 || 'i' ELSE $3 END );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_split_to_table( $1::text, $2::text, 'i' );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_split_to_table( $1::text, $2::text, CASE WHEN pg_catalog.strpos($3, 'c') = 0 THEN  $3 || 'i' ELSE $3 END );`),
}

var citextStringDeclarationBodies = [...]sqltype.StructuralTree{
	mustRoutineDeclarationBody(`SELECT pg_catalog.strpos( pg_catalog.lower( $1::text ), pg_catalog.lower( $2::text ) );`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.regexp_replace( $1::text, pg_catalog.regexp_replace($2::text, '([^a-zA-Z_0-9])', E'\\\\\\1', 'g'), $3::text, 'gi' );`),
	mustRoutineDeclarationBody(`SELECT (pg_catalog.regexp_split_to_array( $1::text, pg_catalog.regexp_replace($2::text, '([^a-zA-Z_0-9])', E'\\\\\\1', 'g'), 'i'))[$3];`),
	mustRoutineDeclarationBody(`SELECT pg_catalog.translate( pg_catalog.translate( $1::text, pg_catalog.lower($2::text), $3), pg_catalog.upper($2::text), $3);`),
}

func mustRoutineDeclarationBody(source string) sqltype.StructuralTree {
	body, err := proc.NewRoutineDeclarationBody(source)
	if err != nil {
		panic(err)
	}
	return body
}

// The cube schema substitution is made once against the actual installed
// dependency, before the declaration is published; catalog reads never parse.
func earthDeclarationBody(op sqltype.EarthOperation, cubeSchema string) (sqltype.StructuralTree, error) {
	var source string
	switch op {
	case sqltype.EarthRadiusOperation:
		source = `SELECT '6378168'::float8;`
	case sqltype.EarthSecToGC:
		source = `SELECT CASE WHEN $1 < '0'::float8 THEN '0'::float8 WHEN $1 / ('2'::float8 * earth()) > '1'::float8 THEN pi() * earth() ELSE '2'::float8 * earth() * asin($1 / ('2'::float8 * earth())) END;`
	case sqltype.EarthGCToSec:
		source = `SELECT CASE WHEN $1 < '0'::float8 THEN '0'::float8 WHEN $1 / earth() > pi() THEN '2'::float8 * earth() ELSE '2'::float8 * earth() * sin($1 / ('2'::float8 * earth())) END;`
	case sqltype.EarthLL:
		source = `SELECT @cube@.cube(@cube@.cube(@cube@.cube(earth() * cos(radians($1)) * cos(radians($2))), earth() * cos(radians($1)) * sin(radians($2))), earth() * sin(radians($1)))::earth;`
	case sqltype.EarthLatitude:
		source = `SELECT CASE WHEN @cube@.cube_ll_coord($1, 3) / earth() < '-1'::float8 THEN '-90'::float8 WHEN @cube@.cube_ll_coord($1, 3) / earth() > '1'::float8 THEN '90'::float8 ELSE degrees(asin(@cube@.cube_ll_coord($1, 3) / earth())) END;`
	case sqltype.EarthLongitude:
		source = `SELECT degrees(atan2(@cube@.cube_ll_coord($1, 2), @cube@.cube_ll_coord($1, 1)));`
	}
	source = strings.ReplaceAll(source, "@cube@", catalog.DependencyIdentifier(cubeSchema))
	return proc.NewRoutineDeclarationBody(source)
}

// EarthInstallationCubePlacement reads the real producer's dependency spelling.
// The returned name is only a replay coordinate; the sealed domain's base type,
// not that name, identifies the prerequisite installation.
func EarthInstallationCubePlacement(recipe *catalog.Installation) (string, ids.ObjectAddress, error) {
	invalid := func() (string, ids.ObjectAddress, error) {
		return "", ids.ObjectAddress{}, InvalidProviderDescriptor()
	}
	if !recipe.Valid() || recipe.Provider != "earthdistance" || recipe.Implementation != StockEarthdistance || recipe.Version != "1.2" || len(recipe.Declarations) != EarthMemberCount {
		return invalid()
	}
	domain, ok := recipe.Declarations[0].(*catalog.Type)
	if !ok || domain.SQLType == nil || !domain.SQLType.IsDomain() || !domain.SQLType.DomainBase().Type.IsCube() {
		return invalid()
	}
	schema := ""
	for _, op := range []sqltype.EarthOperation{sqltype.EarthLL, sqltype.EarthLatitude, sqltype.EarthLongitude} {
		slot := 2 + int(op)
		p, ok := recipe.Declarations[slot].(*catalog.Procedure)
		if !ok || p.Address() != recipe.Members[slot] || p.Origin != nil || p.Implementation != StockEarthdistance || p.Name() != op.Name() || p.Signature != earthRoutineSignature(op, domain.SQLType.ID()) || p.Result != earthRoutineResult(op, domain.SQLType.ID()) {
			return invalid()
		}
		captured, err := proc.EarthDeclarationCubeSchema(p.DeclarationBody, op)
		if err != nil {
			return "", ids.ObjectAddress{}, err
		}
		if schema != "" && schema != captured {
			return invalid()
		}
		schema = captured
	}
	return schema, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domain.SQLType.DomainBase().Type.ID())}, nil
}
