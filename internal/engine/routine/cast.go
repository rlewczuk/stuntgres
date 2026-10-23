package routine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func castEndpoints(h DDLHost, a ddl.Authority, s, t parser.TypeName) (*catalog.Type, *catalog.Type, error) {
	x, err := h.ResolveRegtype(a, s)
	if err != nil {
		return nil, nil, err
	}
	y, err := h.ResolveRegtype(a, t)
	if err != nil {
		return nil, nil, err
	}
	v := h.AccessView(a).Catalog
	return v.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: x}), v.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: y}), nil
}
func castOwner(h DDLHost, a ddl.Authority, s, t *catalog.Type) error {
	if ddl.Check(h, a, access.Requirement{Object: ddl.ObjectRef(h, a, s.Address()), OwnerOnly: true}) == nil || ddl.Check(h, a, access.Requirement{Object: ddl.ObjectRef(h, a, t.Address()), OwnerOnly: true}) == nil {
		return nil
	}
	return castTypeError(h, a, s, t, "42501", "must be owner of type %s or type %s")
}

func castTypeError(h DDLHost, a ddl.Authority, s, t *catalog.Type, code, format string) error {
	path, err := a.ReferencePath()
	if err != nil {
		return err
	}
	v := h.AccessView(a).Catalog
	typeName := func(t *catalog.Type) string {
		if t.SQLType.IsUserDefined() {
			return catalog.TypeDiagnosticNameAt(v, t, path)
		}
		return t.SQLType.DiagnosticName()
	}
	return sqlError(code, fmt.Sprintf(format, typeName(s), typeName(t)))
}
func StageCreateCast(h DDLHost, a ddl.Authority, s *parser.CreateCast) error {
	source, target, err := castEndpoints(h, a, s.Source, s.Target)
	if err != nil {
		return err
	}
	x, y := source.SQLType, target.SQLType
	for i, t := range []*sqltype.Descriptor{x, y} {
		facts, _ := t.PGTypeMetadata()
		if facts.Kind == "p" {
			which := "source"
			if i == 1 {
				which = "target"
			}
			return sqlError("42809", fmt.Sprintf("%s data type %s is a pseudo-type", which, t.DiagnosticName()))
		}
	}
	if err = castOwner(h, a, source, target); err != nil {
		return err
	}
	for _, t := range []*catalog.Type{source, target} {
		if err = ddl.Check(h, a, access.Requirement{Object: ddl.ObjectRef(h, a, t.Address()), Privileges: authorization.UsagePrivilege}); err != nil {
			return err
		}
	}
	if x.IsDomain() || y.IsDomain() {
		which := "source"
		if !x.IsDomain() {
			which = "target"
		}
		h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42809", Message: "cast will be ignored because the " + which + " data type is a domain"})
	}
	d := &catalog.Cast{Source: x.ID(), Target: y.ID(), Method: s.Method, Context: s.Context}
	arity := 0
	v := h.AccessView(a).Catalog
	if s.Method == "f" {
		namespaces, e := h.SearchNamespaces(a, s.Function.Name, ids.ProcedureClassID)
		if e != nil {
			return e
		}
		var exact *catalog.ProcedureSignature
		var signature catalog.ProcedureSignature
		var canonical string
		if s.Function.SignatureSpecified {
			signature, canonical, _, err = utilityRoutineArguments(h, a, s.Function)
			if err != nil {
				return err
			}
			exact = &signature
		}
		name := s.Function.Name.Parts[len(s.Function.Name.Parts)-1].Text
		p, count := selectUtilityRoutine(v, namespaces, name, "routine", exact)
		display := catalog.ObjectNameText(s.Function.Name)
		if count == 0 {
			return sqlError("42883", "function "+display+"("+canonical+") does not exist")
		}
		if count > 1 {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42725", Message: fmt.Sprintf("function name \"%s\" is not unique", display), Hint: "Specify the argument list to select the function unambiguously."}
		}
		if p.RoutineKind == catalog.ProcedureRoutine {
			return sqlError("42809", display+"("+canonical+") is not a function")
		}
		var inputs []sqltype.ID
		if p.Signature != "" {
			for _, word := range strings.Split(string(p.Signature), ",") {
				id, e := strconv.ParseUint(word, 10, 32)
				if e != nil {
					return e
				}
				inputs = append(inputs, sqltype.ID(id))
			}
		}
		arity = len(inputs)
		if arity < 1 || arity > 3 {
			return sqlError("42P17", "cast function must take one to three arguments")
		}
		var ok bool
		d.InputCast, ok = catalog.ImplicitBinaryCast(v, x.ID(), inputs[0])
		if !ok {
			return sqlError("42P17", "argument of cast function must match or be binary-coercible from source data type")
		}
		if arity > 1 && inputs[1] != sqltype.Int4 {
			return sqlError("42P17", "second argument of cast function must be type integer")
		}
		if arity > 2 && inputs[2] != sqltype.Bool {
			return sqlError("42P17", "third argument of cast function must be type boolean")
		}
		d.ResultCast, ok = catalog.ImplicitBinaryCast(v, sqltype.ID(p.Result), y.ID())
		if !ok {
			return sqlError("42P17", "return data type of cast function must match or be binary-coercible to target data type")
		}
		if p.RoutineKind != catalog.FunctionRoutine {
			return sqlError("42P17", "cast function must be a normal function")
		}
		if p.Builtin != nil && p.Builtin.ReturnsSet {
			return sqlError("42P17", "cast function must not return a set")
		}
		d.Function = p.Address()
	} else if s.Method == "b" {
		av := h.AccessView(a)
		actor, e := av.Roles.Descriptor(av.State.CurrentUser)
		if e != nil {
			return e
		}
		if !actor.Superuser {
			return sqlError("42501", "must be superuser to create a cast WITHOUT FUNCTION")
		}
		f, g := x.AttributeFacts(), y.AttributeFacts()
		if f.Length != g.Length || f.ByValue != g.ByValue || f.Alignment != g.Alignment {
			return sqlError("42P17", "source and target data types are not physically compatible")
		}
		switch {
		case x.IsComposite() || y.IsComposite():
			return sqlError("42P17", "composite data types are not binary-compatible")
		case x.IsArray() || y.IsArray():
			return sqlError("42P17", "array data types are not binary-compatible")
		case sqltype.IsRangeInspectType(x) || sqltype.IsRangeInspectType(y):
			return sqlError("42P17", "range data types are not binary-compatible")
		case x.IsEnum() || y.IsEnum():
			return sqlError("42P17", "enum data types are not binary-compatible")
		case x.IsDomain() || y.IsDomain():
			return sqlError("42P17", "domain data types must not be marked binary-compatible")
		}
	}
	if x == y && arity < 2 {
		return sqlError("42P17", "source data type and target data type are the same")
	}
	if s.Method == "b" {
		if _, err = sqltype.PlanBinaryCoercion(x, y); err != nil {
			return err
		}
	}
	if _, ok := v.Cast(x.ID(), y.ID()); ok {
		return castTypeError(h, a, source, target, "42710", "cast from type %s to type %s already exists")
	}
	for i := range sqltype.CastMetadataCount() {
		f, _ := sqltype.CastMetadataAt(i)
		if f.Source == x.ID() && f.Target == y.ID() {
			return castTypeError(h, a, source, target, "42710", "cast from type %s to type %s already exists")
		}
	}
	d.OID, err = h.AllocateOID()
	if err != nil {
		return err
	}
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Create(d)
}
func StageDropCast(h DDLHost, a ddl.Authority, s *parser.DropCast) error {
	source, target, err := castEndpoints(h, a, s.Source, s.Target)
	if err != nil {
		var d *sqlstate.Error
		if s.IfExists && errors.As(err, &d) && (d.Code == "42704" || d.Code == "3F000") {
			ddl.Notice(h, "00000", d.Message+", skipping")
			return nil
		}
		return err
	}
	if err = castOwner(h, a, source, target); err != nil {
		return err
	}
	d, ok := h.AccessView(a).Catalog.Cast(source.SQLType.ID(), target.SQLType.ID())
	if !ok {
		for i := range sqltype.CastMetadataCount() {
			f, _ := sqltype.CastMetadataAt(i)
			if f.Source == source.SQLType.ID() && f.Target == target.SQLType.ID() {
				return castTypeError(h, a, source, target, "2BP01", "cannot drop cast from %s to %s because it is required by the database system")
			}
		}
		if s.IfExists {
			from, to := writtenRoutineTypeName(s.Source), writtenRoutineTypeName(s.Target)
			ddl.Notice(h, "00000", fmt.Sprintf("cast from type %s to type %s does not exist, skipping", from, to))
			return nil
		}
		return castTypeError(h, a, source, target, "42704", "cast from type %s to type %s does not exist")
	}
	if d.GeneratedRange != (ids.ObjectAddress{}) {
		if err := (catalog.CastProvider{}).Validate(h.AccessView(a).Catalog, d); err != nil {
			return err
		}
		cast, err := h.DependencyObject(a, d.Address())
		if err != nil {
			return err
		}
		function, err := h.DependencyObject(a, d.Function)
		if err != nil {
			return err
		}
		diagnostic := sqlError("2BP01", "cannot drop "+cast+" because "+function+" requires it").(*sqlstate.Error)
		diagnostic.Hint = "You can drop " + function + " instead."
		return diagnostic
	}
	h.DropTarget(d.Address())
	return h.RecordDependencyPath(a)
}
