package extension

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// The closed released SQL body records genuine member and pinned builtin
// identities. Execution uses the captured compact layout, not name resolution.
var pgFreespaceBody = func() sqltype.StructuralTree {
	bigint := proc.DeclarationTypeName(20, "bigint")
	cast := func(parts declarationParts) declarationParts { return declarationCast(parts, bigint) }
	block := declarationParts{proc.DeclarationIdentifierName("blkno")}
	scalar := declarationCall(proc.DeclarationMember("", 0, uint32(ids.ProcedureClassID), "pg_freespace", 2205, 20), declarationArg(0), block)
	size := declarationBuiltin("pg_relation_size", "2205", declarationArg(0))
	setting := cast(declarationBuiltin("current_setting", "25", declarationString("block_size")))
	// Released int8div/int8mi operator OIDs are declaration identities.
	divide := proc.DeclarationOp("/", 20, 20)
	divide.OID = 687
	quotient := declarationJoin(size, declarationText(" "), declarationParts{divide}, declarationText(" "), setting)
	subtract := proc.DeclarationOp("-", 20, 20)
	subtract.OID = 685
	end := declarationJoin(declarationText("("), quotient, declarationText(") "), declarationParts{subtract}, declarationText(" "), cast(declarationText("1")))
	series := declarationBuiltin("generate_series", "20,20", cast(declarationText("0")), end)
	parts := declarationJoin(declarationText("SELECT "), block, declarationText(", "), scalar, declarationText("\n    FROM "), series, declarationText(" blkno(blkno)"))
	body, err := proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Parts: parts, Source: "SELECT blkno, pg_freespace($1, blkno) FROM generate_series(0::bigint, pg_relation_size($1) / current_setting('block_size')::bigint - 1::bigint) AS blkno"})
	if err != nil {
		panic(err)
	}
	return body
}()
