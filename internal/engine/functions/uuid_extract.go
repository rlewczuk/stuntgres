package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type UUIDExtractKind uint8

const (
	UUIDExtractTimestamp UUIDExtractKind = iota + 1
	UUIDExtractVersion
)

func UUIDExtractFunction(name string) UUIDExtractKind {
	switch name {
	case "uuid_extract_timestamp":
		return UUIDExtractTimestamp
	case "uuid_extract_version":
		return UUIDExtractVersion
	}
	return 0
}

func (k UUIDExtractKind) Name() string {
	switch k {
	case UUIDExtractTimestamp:
		return "uuid_extract_timestamp"
	case UUIDExtractVersion:
		return "uuid_extract_version"
	}
	return ""
}

func (k UUIDExtractKind) Result() sqltype.ID {
	if k == UUIDExtractTimestamp {
		return sqltype.Timestamptz
	}
	if k == UUIDExtractVersion {
		return sqltype.Int2
	}
	return 0
}

// UUIDExtract is a strict immutable unary scalar. UUID parsing and timestamp
// presentation remain owned by sqltype and the session's ordinary adapters.
type UUIDExtract struct {
	expr.Info
	Kind UUIDExtractKind
	Arg  expr.Expr
}

func NewUUIDExtract(info expr.Info, kind UUIDExtractKind, arg expr.Expr) (UUIDExtract, error) {
	e := UUIDExtract{Info: info, Kind: kind, Arg: arg}
	return e, e.Validate()
}

func (e UUIDExtract) Validate() error {
	if e.Kind.Name() == "" || any(e.Arg) == nil || !e.Usage.Valid() || e.Usage.Type.ID() != e.Kind.Result() || e.Usage != e.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid stored UUID extraction provider")
	}
	u := e.Arg.ResultUsage()
	if !u.Valid() || u.Type.ID() != sqltype.UUID || u != u.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid stored UUID extraction argument")
	}
	return nil
}

func (e UUIDExtract) Reconstruct(info expr.Info, arg expr.Expr) (UUIDExtract, error) {
	return NewUUIDExtract(info, e.Kind, arg)
}

// UUIDExtract implements expr.Composite.
var _ expr.Composite = UUIDExtract{}

func (e UUIDExtract) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e UUIDExtract) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("UUIDExtract", children, 1); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children[0]))
}

func (e UUIDExtract) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Arg) {
		return sqltype.Null(e.Usage.Type), nil
	}
	v, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if v.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	u, err := v.UUID()
	if err != nil {
		return sqltype.Value{}, err
	}
	if u[8]&0xc0 != 0x80 {
		return sqltype.Null(e.Usage.Type), nil
	}
	version := u[6] >> 4
	if e.Kind == UUIDExtractVersion {
		return sqltype.Integer(e.Usage.Type, int64(version))
	}
	switch version {
	case 1:
		ticks := uint64(u[0])<<24 | uint64(u[1])<<16 | uint64(u[2])<<8 | uint64(u[3]) | uint64(u[4])<<40 | uint64(u[5])<<32 | uint64(u[6]&0x0f)<<56 | uint64(u[7])<<48
		// Divide unsigned Gregorian ticks before subtracting the PostgreSQL epoch.
		return sqltype.TimestampValue(e.Usage.Type, int64(ticks/10)-13165977600000000)
	case 7:
		ms := uint64(u[0])<<40 | uint64(u[1])<<32 | uint64(u[2])<<24 | uint64(u[3])<<16 | uint64(u[4])<<8 | uint64(u[5])
		return sqltype.TimestampValue(e.Usage.Type, int64(ms*1000)-946684800000000)
	default:
		return sqltype.Null(e.Usage.Type), nil
	}
}

func UUIDExtractIntrinsicRule() IntrinsicPropertyRule { return ExactImmutable }
