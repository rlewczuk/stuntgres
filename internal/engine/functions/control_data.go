package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ControlDataKind identifies a control-file inquiry, never physical state.
type ControlDataKind uint8

const (
	ControlCheckpoint ControlDataKind = iota
	ControlSystem
	ControlInit
	ControlRecovery
)

var controlDataNames = [...]string{"pg_control_checkpoint", "pg_control_system", "pg_control_init", "pg_control_recovery"}
var controlCheckpointLabels = [...]string{"checkpoint_lsn", "redo_lsn", "redo_wal_file", "timeline_id", "prev_timeline_id", "full_page_writes", "next_xid", "next_oid", "next_multixact_id", "next_multi_offset", "oldest_xid", "oldest_xid_dbid", "oldest_active_xid", "oldest_multi_xid", "oldest_multi_dbid", "oldest_commit_ts_xid", "newest_commit_ts_xid", "checkpoint_time"}
var controlCheckpointTypes = [...]sqltype.ID{sqltype.PgLSN, sqltype.PgLSN, sqltype.Text, sqltype.Int4, sqltype.Int4, sqltype.Bool, sqltype.Text, sqltype.Oid, sqltype.Xid, sqltype.Xid, sqltype.Xid, sqltype.Oid, sqltype.Xid, sqltype.Xid, sqltype.Oid, sqltype.Xid, sqltype.Xid, sqltype.Timestamptz}
var controlSystemLabels = [...]string{"pg_control_version", "catalog_version_no", "system_identifier", "pg_control_last_modified"}
var controlSystemTypes = [...]sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8, sqltype.Timestamptz}
var controlInitLabels = [...]string{"max_data_alignment", "database_block_size", "blocks_per_segment", "wal_block_size", "bytes_per_wal_segment", "max_identifier_length", "max_index_columns", "max_toast_chunk_size", "large_object_chunk_size", "float8_pass_by_value", "data_page_checksum_version", "default_char_signedness"}
var controlInitTypes = [...]sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Bool, sqltype.Int4, sqltype.Bool}
var controlRecoveryLabels = [...]string{"min_recovery_end_lsn", "min_recovery_end_timeline", "backup_start_lsn", "backup_end_lsn", "end_of_backup_record_required"}
var controlRecoveryTypes = [...]sqltype.ID{sqltype.PgLSN, sqltype.Int4, sqltype.PgLSN, sqltype.PgLSN, sqltype.Bool}

func (k ControlDataKind) Name() string {
	if int(k) >= len(controlDataNames) { return "" }
	return controlDataNames[k]
}
func (k ControlDataKind) members() ([]string, []sqltype.ID) {
	switch k {
	case ControlCheckpoint:
		return controlCheckpointLabels[:], controlCheckpointTypes[:]
	case ControlSystem:
		return controlSystemLabels[:], controlSystemTypes[:]
	case ControlInit:
		return controlInitLabels[:], controlInitTypes[:]
	case ControlRecovery:
		return controlRecoveryLabels[:], controlRecoveryTypes[:]
	}
	return nil, nil
}

// ControlData retains only the immutable typed recipe and routine provenance.
type ControlData struct {
	expr.Info
	Kind ControlDataKind
}

func bindControlData(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 0 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := ControlDataKind(len(controlDataNames))
	for i, candidate := range controlDataNames {
		if candidate == name {
			kind = ControlDataKind(i)
			break
		}
	}
	if !visible || int(kind) >= len(controlDataNames) || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	labels, types := kind.members()
	members := make([]sqltype.CompositeMember, len(labels))
	for i, label := range labels {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: label, Usage: b.Type(types[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile}), CatalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return ControlData{info, kind}, nil
}

func (n ControlData) Validate() error {
	if n.Kind.Name() == "" { return fmt.Errorf("stuntgres: invalid control data signature") }
	labels, types := n.Kind.members()
	d := n.Usage.Type
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != len(labels) || n.Usage != d.Usage() {
		return fmt.Errorf("stuntgres: invalid control data record")
	}
	for i, label := range labels {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != label || m.Usage.Type == nil || m.Usage.Type.ID() != types[i] || m.Usage != m.Usage.Type.Usage() {
			return fmt.Errorf("stuntgres: invalid control data member")
		}
	}
	return nil
}
func (n ControlData) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	if err := n.Validate(); err != nil { return sqltype.Value{}, err }
	return sqltype.Value{}, expr.ErrorAt("0A000", n.Kind.Name()+" physical control-file access is unsupported in embedded mode", parser.Position{})
}
func (n ControlData) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
