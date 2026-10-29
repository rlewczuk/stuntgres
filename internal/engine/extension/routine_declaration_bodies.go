package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// Closed analyzed REL_18_6 recipes. Syntax and display share these exact parts;
// installation members are metadata identities, not executable captures.
type declarationParts []proc.DeclarationPart
func declarationJoin(parts ...declarationParts) declarationParts {
 var result declarationParts
 for _,part:=range parts {result=append(result,part...)}
 return result
}
func declarationText(text string) declarationParts {return declarationParts{proc.DeclarationText(text)}}
func declarationArg(slot int) declarationParts {return declarationParts{proc.DeclarationArgument(slot)}}
func declarationBuiltin(name, signature string, args ...declarationParts) declarationParts {
 return declarationCall(proc.DeclarationFunction(name,signature),args...)
}
func declarationCall(name proc.DeclarationPart, args ...declarationParts) declarationParts {
 result:=declarationParts{name,proc.DeclarationText("(")}
 for i,arg:=range args {if i!=0 {result=append(result,proc.DeclarationText(", "))};result=append(result,arg...)}
 return append(result,proc.DeclarationText(")"))
}
func declarationCast(value declarationParts, typ proc.DeclarationPart) declarationParts {
 return declarationJoin(declarationText("("),value,declarationText(")::"),declarationParts{typ})
}
func declarationString(value string) declarationParts {
 return declarationParts{proc.DeclarationString(value),proc.DeclarationText("::"),proc.DeclarationTypeName(25,"text")}
}
func declarationBinary(left declarationParts, operator string, leftType,rightType uint32, right declarationParts) declarationParts {
 part:=proc.DeclarationOp(operator,leftType,rightType)
 // Released pg_operator identities, separate from executable operator support.
 if leftType==701 && rightType==701 {
  switch operator {case "*":part.OID=594;case "/":part.OID=593;case "<":part.OID=672;case ">":part.OID=674}
 } else if leftType==23 && rightType==23 && operator=="=" {part.OID=96
 } else if leftType==25 && rightType==25 && operator=="||" {part.OID=654}
 if part.OID==0 {panic("stuntgres: unknown installed declaration operator")}
 return declarationJoin(declarationText("("),left,declarationText(" "),declarationParts{part},declarationText(" "),right,declarationText(")"))
}
func mustRoutineDeclarationBody(parts declarationParts) sqltype.StructuralTree {
 body,err:=proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Return:true,Parts:parts})
 if err!=nil {panic(err)}
 return body
}

var citextRegexpDeclarationBodies = func() [10]sqltype.StructuralTree {
 var bodies [10]sqltype.StructuralTree
 names:=[5]string{"regexp_match","regexp_matches","regexp_replace","regexp_split_to_array","regexp_split_to_table"}
 textType:=proc.DeclarationTypeName(25,"text")
 for family,name:=range names {
  for flagged:=range 2 {
   args:=[]declarationParts{declarationCast(declarationArg(0),textType),declarationCast(declarationArg(1),textType)}
   flagSlot:=2; signature:="25,25,25"
   if name=="regexp_replace" {args=append(args,declarationArg(2));flagSlot=3;signature="25,25,25,25"}
   flag:=declarationString("i")
   if flagged!=0 {
    formal:=declarationArg(flagSlot)
    condition:=declarationBinary(declarationBuiltin("strpos","25,25",formal,declarationString("c")),"=",23,23,declarationText("0"))
    concat:=declarationBinary(formal,"||",25,25,declarationString("i"))
    flag=declarationJoin(declarationText("CASE WHEN "),condition,declarationText(" THEN "),concat,declarationText(" ELSE "),formal,declarationText(" END"))
   }
   args=append(args,flag)
   bodies[family*2+flagged]=mustRoutineDeclarationBody(declarationBuiltin(name,signature,args...))
  }
 }
 return bodies
}()

var citextStringDeclarationBodies = func() [4]sqltype.StructuralTree {
 textType:=proc.DeclarationTypeName(25,"text")
 a:=declarationCast(declarationArg(0),textType)
 b:=declarationCast(declarationArg(1),textType)
 c:=declarationArg(2)
 escaped:=declarationBuiltin("regexp_replace","25,25,25,25",b,declarationString("([^a-zA-Z_0-9])"),declarationString("\\\\\\1"),declarationString("g"))
 split:=declarationBuiltin("regexp_split_to_array","25,25,25",a,escaped,declarationString("i"))
 return [4]sqltype.StructuralTree{
  mustRoutineDeclarationBody(declarationBuiltin("strpos","25,25",declarationBuiltin("lower","25",a),declarationBuiltin("lower","25",b))),
  mustRoutineDeclarationBody(declarationBuiltin("regexp_replace","25,25,25,25",a,escaped,declarationCast(c,textType),declarationString("gi"))),
  mustRoutineDeclarationBody(declarationJoin(declarationText("("),split,declarationText(")["),c,declarationText("]"))),
  mustRoutineDeclarationBody(declarationBuiltin("translate","25,25,25",declarationBuiltin("translate","25,25,25",a,declarationBuiltin("lower","25",b),c),declarationBuiltin("upper","25",b),c)),
 }
}()

// Member identities, not schema substitutions, supply current display names.
// The installation schema is retained only in the canonical parsed syntax.
func earthDeclarationBody(operation sqltype.EarthOperation, cubeSchema string) (sqltype.StructuralTree,error) {
 member:=func(extension string,slot int,class ids.OID,name string,arguments ...uint32) proc.DeclarationPart {
  spelling:=name
  if extension=="cube" {spelling=catalog.DependencyIdentifier(cubeSchema)+"."+name}
  return proc.DeclarationMember(extension,slot,uint32(class),spelling,arguments...)
 }
 earth:=func() declarationParts {return declarationCall(member("",2,ids.ProcedureClassID,"earth"))}
 cubeType:=member("cube",0,ids.TypeClassID,"cube")
 double:=proc.DeclarationTypeName(701,"double precision")
 number:=func(value string) declarationParts {return declarationParts{proc.DeclarationString(value),proc.DeclarationText("::"),double}}
 a,b:=declarationArg(0),declarationArg(1)
 binary:=func(left declarationParts,op string,right declarationParts) declarationParts {return declarationBinary(left,op,701,701,right)}
 builtin:=func(name string,args ...declarationParts) declarationParts {signature:="701";if name=="pi" {signature=""};if name=="atan2" {signature="701,701"};return declarationBuiltin(name,signature,args...)}
 twice:=func() declarationParts {return binary(number("2"),"*",earth())}
 coord:=func(index string) declarationParts {return declarationCall(member("cube",26,ids.ProcedureClassID,"cube_ll_coord",0,23),declarationCast(a,cubeType),declarationText(index))}
 var parts declarationParts
 switch operation {
 case sqltype.EarthRadiusOperation: parts=number("6378168")
 case sqltype.EarthSecToGC,sqltype.EarthGCToSec:
  limit:=number("1");denominator:=twice();top:=binary(builtin("pi"),"*",earth());trig:="asin"
  if operation==sqltype.EarthGCToSec {limit=builtin("pi");denominator=earth();top=twice();trig="sin"}
  parts=declarationJoin(declarationText("CASE WHEN "),binary(a,"<",number("0")),declarationText(" THEN "),number("0"),declarationText(" WHEN "),binary(binary(a,"/",denominator),">",limit),declarationText(" THEN "),top,declarationText(" ELSE "),binary(twice(),"*",builtin(trig,binary(a,"/",twice()))),declarationText(" END"))
 case sqltype.EarthLL:
  lat,lon:=builtin("radians",a),builtin("radians",b)
  x:=binary(binary(earth(),"*",builtin("cos",lat)),"*",builtin("cos",lon))
  y:=binary(binary(earth(),"*",builtin("cos",lat)),"*",builtin("sin",lon))
  z:=binary(earth(),"*",builtin("sin",lat))
  cube:=declarationCall(member("cube",18,ids.ProcedureClassID,"cube",701),x)
  cube=declarationCall(member("cube",22,ids.ProcedureClassID,"cube",0,701),cube,y)
  cube=declarationCall(member("cube",22,ids.ProcedureClassID,"cube",0,701),cube,z)
  parts=declarationCast(cube,member("",0,ids.TypeClassID,"earth"))
 case sqltype.EarthLatitude:
  ratio:=func() declarationParts {return binary(coord("3"),"/",earth())}
  parts=declarationJoin(declarationText("CASE WHEN "),binary(ratio(),"<",number("-1")),declarationText(" THEN "),number("-90"),declarationText(" WHEN "),binary(ratio(),">",number("1")),declarationText(" THEN "),number("90"),declarationText(" ELSE "),builtin("degrees",builtin("asin",ratio())),declarationText(" END"))
 case sqltype.EarthLongitude: parts=builtin("degrees",builtin("atan2",coord("2"),coord("1")))
 }
 return proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Return:true,Parts:parts})
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
