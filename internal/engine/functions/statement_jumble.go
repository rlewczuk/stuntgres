package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// FuncExpr.funcformat distinguishes SQL grammar calls from ordinary calls.
func (n Binary) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.Binary")
	s.JumbleBool(n.Syntax == parser.SQLFunctionSyntax)
}
func (n DatetimeExtract) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DatetimeExtract")
	s.JumbleBool(n.Syntax == parser.SQLFunctionSyntax)
}
func (n DatetimeOverlaps) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DatetimeOverlaps")
	s.JumbleBool(n.Syntax == parser.SQLFunctionSyntax)
}
func (n PosixSubstring) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.PosixSubstring")
	s.JumbleBool(n.Syntax == parser.SQLFunctionSyntax)
}
func (n SimilarSubstring) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.SimilarSubstring")
	s.JumbleBool(n.Syntax == parser.SQLFunctionSyntax)
}
func (n String) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.String")
	s.JumbleBool(n.Syntax != parser.OrdinaryStringSyntax)
}
func (n Identity) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.Identity")
	s.JumbleUint64(uint64(n.Kind))
	s.JumbleBool(n.OrdinaryCall)
}
func (n CurrentDateTime) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.CurrentDateTime")
	s.JumbleUint64(uint64(n.Kind))
	s.JumbleBool(n.HasPrecision)
	if n.HasPrecision {
		s.JumbleUint64(uint64(n.Precision))
	}
}
func (n Timezone) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.Timezone")
	s.JumbleBool(n.Local)
}
func (n XMLSerialize) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.XMLSerialize")
	s.JumbleBool(n.Indent == "indent")
}
func (n XMLDocumentPredicate) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.XMLDocumentPredicate")
	s.JumbleBool(n.Negated)
}
func (n XMLConstruct) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.XMLConstruct")
	// XmlExpr intentionally ignores XML element/attribute names, DOCUMENT/CONTENT
	// and output type/typmod. Keep the operation and the two argument-list shapes.
	s.JumbleUint64(uint64(n.kind))
	s.JumbleUint64(uint64(len(n.names)))
}
func (n JSONPredicate) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.JSONPredicate")
	kind := n.Kind
	if kind == "" {
		kind = "value"
	}
	s.JumbleText(kind)
	s.JumbleBool(n.Unique)
	s.JumbleBool(n.Not)
}

// JsonFormat retains format_type as well as encoding. An omitted input
// clause is DEFAULT, not explicit FORMAT JSON; output defaults are resolved
// by the expression family (REL_18_6 parse_expr.c).
func jumbleJSONFormat(s expr.StatementFields, format *parser.JSONFormat, fallback string) {
	encoding := ""
	if format != nil {
		fallback = "json"
		encoding = format.Encoding
	}
	s.JumbleText(fallback)
	s.JumbleText(encoding)
}
func (n SQLJSON) JumbleFields(s expr.StatementFields) { n.JumbleReturning(s, n.Usage) }

// JumbleReturning consumes the existing constructor-owned output conversion's
// resolved usage without retaining another descriptor across rebuild/remap.
func (n SQLJSON) JumbleReturning(s expr.StatementFields, returning sqltype.Usage) {
	s.JumbleText("functions.SQLJSON")
	s.JumbleText(n.Raw.Kind)
	s.JumbleBool(n.Raw.Absent)
	s.JumbleBool(n.Raw.Unique)
	s.JumbleUint64(uint64(returning.Type.ID()))
	s.JumbleUint64(uint64(returning.Typmod))
	format := "json"
	if returning.Type.ID() == sqltype.JSONB {
		format = "jsonb"
	}
	jumbleJSONFormat(s, n.Raw.Output, format)
}
func (n SQLJSONQuery) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.SQLJSONQuery")
	s.JumbleText(n.Raw.Kind)
	s.JumbleText(n.Raw.TableColumn)
	s.JumbleUint64(uint64(n.Usage.Type.ID()))
	s.JumbleUint64(uint64(n.Usage.Typmod))
	s.JumbleUint64(uint64(n.Usage.Collation.ID))
	jumbleJSONFormat(s, n.Raw.Input, "default")
	format := "default"
	if n.Raw.Returning != nil {
		format = "json"
		if n.Usage.Type.ID() == sqltype.JSONB {
			format = "jsonb"
		}
	}
	// JSON_VALUE overrides even an explicit RETURNING's output format.
	if n.Raw.Kind == "json_value" {
		jumbleJSONFormat(s, nil, "default")
	} else {
		jumbleJSONFormat(s, n.Raw.Output, format)
	}
	for _, binding := range n.Raw.Passing {
		s.JumbleText(binding.Name.Text)
	}
	// JSW_UNSPEC and explicit JSW_NONE are distinct selected fields.
	s.JumbleText(n.Raw.Wrapper)
	s.JumbleBool(n.Raw.Kind == "json_value" || n.Raw.Quotes == "omit")
	empty, onError := n.Raw.Empty.Kind, n.Raw.Error.Kind
	if n.Raw.Kind == "json_exists" {
		empty = ""
	} else if empty == "" {
		empty = "null"
	}
	if onError == "" {
		onError = "null"
		if n.Raw.Kind == "json_exists" {
			onError = "false"
		}
	}
	s.JumbleText(empty)
	s.JumbleText(onError)
	useJSON, useIO := false, false
	switch n.Raw.Kind {
	case "json_exists":
		useJSON = n.Usage.Type.ID() != sqltype.Bool
	case "json_query":
		useJSON = n.Usage.Type.ID() != sqltype.JSONB || n.Raw.Quotes == "omit"
	case "json_value":
		if n.Usage.Type.ID() != sqltype.Text {
			useJSON = n.Usage.Type.IsDomain() && n.Usage.Type.HasDomainConstraints()
			useIO = !useJSON
		}
	}
	s.JumbleBool(useIO)
	s.JumbleBool(useJSON)
}
func (n XMLTable) JumbleChildren(visit func(expr.Expr), _ expr.StatementFields) {
	// REL_18_6 TableFunc includes document, row filter and column paths, but
	// excludes namespaces, names, output codecs, defaults and nullability.
	base := len(n.Raw.Namespaces)
	visit(n.Args[base+1])
	visit(n.Args[base])
	for _, field := range n.Fields {
		if field.Path >= 0 {
			visit(n.Args[field.Path])
		} else {
			visit(nil)
		}
	}
}
func (n JSONTable) JumbleChildren(visit func(expr.Expr), _ expr.StatementFields) {
	// JSON_TABLE's nested plan, PASSING values and column value expressions are
	// explicitly ignored TableFunc fields, not a graph to hash by reflection.
	visit(n.Args[0])
	visit(n.Args[n.Level.Path])
}

func (n XMLConstruct) JumbleChildren(visit func(expr.Expr), fields expr.StatementFields) {
	count := len(n.children)
	if n.kind == parser.XMLRoot {
		count++
		if !n.version {
			count++
		}
	}
	fields.JumbleUint64(uint64(count))
	for _, child := range n.children {
		visit(child)
	}
	if n.kind == parser.XMLRoot {
		if !n.version {
			fields.JumbleConst(sqltype.Text)
		}
		fields.JumbleConst(sqltype.Int4)
	}
}

func (n XMLParse) JumbleChildren(visit func(expr.Expr), fields expr.StatementFields) {
	fields.JumbleUint64(2)
	visit(n.Child)
	fields.JumbleConst(sqltype.Bool)
}

// Ordinary function nodes project their concrete family here; their selected
// routine identities and lexical operands are streamed by the analyzed visitor.
// Private callable carriers participate just like exported expression families.
func (AccessInquiry) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.AccessInquiry") }
func (ACLValue) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.ACLValue") }
func (Advisory) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.Advisory") }
func (Int4Larger) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.Int4Larger") }
func (Amcheck) JumbleFields(s expr.StatementFields)        { s.JumbleText("functions.Amcheck") }
func (ArrayInspect) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.ArrayInspect") }
func (ArrayRandom) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.ArrayRandom") }
func (ArraySearch) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.ArraySearch") }
func (ArrayTransform) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.ArrayTransform") }
func (AttributeStatistics) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.AttributeStatistics")
}
func (BlockingInquiry) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.BlockingInquiry")
}
func (CastRoutine) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.CastRoutine") }
func (CatalogUserName) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.CatalogUserName")
}
func (CatalogForeignKeys) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.CatalogForeignKeys")
}
func (Circle) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.Circle") }
func (ColumnCompression) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.ColumnCompression")
}
func (GinCleanPendingList) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.GinCleanPendingList")
}
func (CommentInquiry) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.CommentInquiry") }
func (commitTimestampExpression) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.commitTimestampExpression")
}
func (Connectby) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.Connectby") }
func (ControlData) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.ControlData") }
func (Cube) JumbleFields(s expr.StatementFields)           { s.JumbleText("functions.Cube") }
func (CubeSpatial) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.CubeSpatial") }
func (CurrentSchemas) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.CurrentSchemas") }
func (DatabaseFilesInquiry) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DatabaseFilesInquiry")
}
func (DatetimeAgeFinite) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DatetimeAgeFinite")
}
func (DatetimeBin) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.DatetimeBin") }
func (DatetimeFormatting) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DatetimeFormatting")
}
func (DatetimeJustify) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DatetimeJustify")
}
func (DatetimeMake) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.DatetimeMake") }
func (DatetimeSleep) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.DatetimeSleep") }
func (DatetimeTrunc) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.DatetimeTrunc") }
func (DatetimeZoneCalendar) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DatetimeZoneCalendar")
}
func (DblinkBuildSQLDelete) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DblinkBuildSQLDelete")
}
func (DblinkBuildSQLInsert) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DblinkBuildSQLInsert")
}
func (DblinkBuildSQLUpdate) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DblinkBuildSQLUpdate")
}
func (DblinkGetPkey) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.DblinkGetPkey") }
func (Deparse) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.Deparse") }
func (DictionaryLexize) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DictionaryLexize")
}
func (EarthCoordinate) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.EarthCoordinate")
}
func (EarthPoint) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.EarthPoint") }
func (EncodingNameCall) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.EncodingNameCall")
}
func (Keywords) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.Keywords") }
func (Enum) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.Enum") }
func (EventTriggerDDLCommands) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.EventTriggerDDLCommands")
}
func (EventTriggerDroppedObjects) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.EventTriggerDroppedObjects")
}
func (EventTriggerRewrite) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.EventTriggerRewrite")
}
func (HstoreArray) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.HstoreArray") }
func (HstoreConstruct) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.HstoreConstruct")
}
func (HstoreJSON) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.HstoreJSON") }
func (HstoreRecord) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.HstoreRecord") }
func (LtreeArray) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.LtreeArray") }
func (LtreeHierarchy) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.LtreeHierarchy") }
func (LtreeLquery) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.LtreeLquery") }
func (LtreeLtxtquery) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.LtreeLtxtquery") }
func (LtreePath) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.LtreePath") }
func (IntbigIO) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.IntbigIO") }
func (PgTrgm) JumbleFields(s expr.StatementFields)         { s.JumbleText("functions.PgTrgm") }
func (Posix) JumbleFields(s expr.StatementFields)          { s.JumbleText("functions.Posix") }
func (Regexp) JumbleFields(s expr.StatementFields)         { s.JumbleText("functions.Regexp") }
func (RegexpSet) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.RegexpSet") }
func (Similar) JumbleFields(s expr.StatementFields)        { s.JumbleText("functions.Similar") }
func (SimilarEscape) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.SimilarEscape") }
func (LikeEscape) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.LikeEscape") }
func (XMLParse) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.XMLParse") }
func (XMLTextCall) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.XMLTextCall") }
func (XMLExists) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.XMLExists") }
func (XMLValidator) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.XMLValidator") }
func (XMLXPath) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.XMLXPath") }
func (Format) JumbleFields(s expr.StatementFields)         { s.JumbleText("functions.Format") }
func (FTS) JumbleFields(s expr.StatementFields)            { s.JumbleText("functions.FTS") }
func (FTSConversion) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.FTSConversion") }
func (FTSCurrentConfig) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.FTSCurrentConfig")
}
func (FTSInspection) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.FTSInspection") }
func (FTSParser) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.FTSParser") }
func (FTSQuery) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.FTSQuery") }
func (FTSVector) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.FTSVector") }
func (GenericFileList) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.GenericFileList")
}
func (GenericFileRead) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.GenericFileRead")
}
func (GeometryConversion) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.GeometryConversion")
}
func (GeometryMeasure) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.GeometryMeasure")
}
func (GeometryRelation) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.GeometryRelation")
}
func (HstoreRows) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.HstoreRows") }
func (IndexProperty) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.IndexProperty") }
func (IntegerIndexSupport) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.IntegerIndexSupport")
}
func (Macaddr8Compare) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.Macaddr8Compare")
}
func (InputValidity) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.InputValidity") }
func (InstalledText) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.InstalledText") }
func (IntaggEnumeration) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.IntaggEnumeration")
}
func (IsnValidation) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.IsnValidation") }
func (JSONCreate) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.JSONCreate") }
func (JSONExtract) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.JSONExtract") }
func (JSONKeys) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.JSONKeys") }
func (JSONEach) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.JSONEach") }
func (JSONElements) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.JSONElements") }
func (JSONBElements) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.JSONBElements") }
func (JSONRecord) JumbleFields(s expr.StatementFields)     { s.JumbleText("functions.JSONRecord") }
func (JSONStripNulls) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.JSONStripNulls") }
func (JSONTable) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.JSONTable") }
func (JSONBPathQueryArray) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.JSONBPathQueryArray")
}
func (JSONBPathExists) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.JSONBPathExists")
}
func (JSONBPathMatch) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.JSONBPathMatch") }
func (JSONBPathQueryFirst) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.JSONBPathQueryFirst")
}
func (JSONBPathQuery) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.JSONBPathQuery") }
func (JSONBRecord) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.JSONBRecord") }
func (JSONBSet) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.JSONBSet") }
func (LargeObjectCall) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.LargeObjectCall")
}
func (Levenshtein) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.Levenshtein") }
func (LineEquality) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.LineEquality") }
func (LogicalDecoding) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.LogicalDecoding")
}
func (MACUnary) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.MACUnary") }
func (MACADDR8Comparison) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.MACADDR8Comparison")
}
func (Math) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.Math") }
func (Metaphone) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.Metaphone") }
func (NamespaceInquiry) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.NamespaceInquiry")
}
func (NetworkFormat) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.NetworkFormat") }
func (NetworkSubnet) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.NetworkSubnet") }
func (ListeningChannels) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.ListeningChannels")
}
func (NotificationQueueUsage) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.NotificationQueueUsage")
}
func (NullCount) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.NullCount") }
func (NumericFormatting) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.NumericFormatting")
}
func (ObjectACL) JumbleFields(s expr.StatementFields)      { s.JumbleText("functions.ObjectACL") }
func (ObjectAddress) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.ObjectAddress") }
func (OptionsToTable) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.OptionsToTable") }
func (Pageinspect) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.Pageinspect") }
func (PartitionRoot) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.PartitionRoot") }
func (PartitionTree) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.PartitionTree") }
func (PartitionAncestors) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.PartitionAncestors")
}
func (snapshotExpression) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.snapshotExpression")
}
func (Pgcrypto) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.Pgcrypto") }
func (PgpArmorHeaders) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.PgpArmorHeaders")
}
func (PgFreespace) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.PgFreespace") }
func (Pgrowlocks) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.Pgrowlocks") }
func (PgstattupleStats) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.PgstattupleStats")
}
func (PgStatStatements) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.PgStatStatements")
}
func (Pgstattuple) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.Pgstattuple") }
func (Pgvisibility) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.Pgvisibility") }
func (n Pgsurgery) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.Pgsurgery")
	s.JumbleUint64(uint64(n.Slots[0]))
	s.JumbleUint64(uint64(n.Slots[1]))
}
func (Point) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.Point") }
func (Polygon) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.Polygon") }
func (publicationInfo) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.publicationInfo")
}
func (Random) JumbleFields(s expr.StatementFields)        { s.JumbleText("functions.Random") }
func (RangeInspect) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.RangeInspect") }
func (RangeOperator) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.RangeOperator") }
func (RecoveryControl) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.RecoveryControl")
}
func (RecoveryInfo) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.RecoveryInfo") }
func (WALResourceManagers) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.WALResourceManagers")
}
func (RegLookup) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.RegLookup") }
func (SizeInquiry) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.SizeInquiry") }
func (RelationStatistics) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.RelationStatistics")
}
func (ReplicationMessages) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.ReplicationMessages")
}
func (ReplicationOrigin) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.ReplicationOrigin")
}
func (ReplicationSlot) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.ReplicationSlot")
}
func (RuntimeCapability) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.RuntimeCapability")
}
func (LoadedModules) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.LoadedModules") }
func (CurrentLogfile) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.CurrentLogfile") }
func (SequenceCall) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.SequenceCall") }
func (ServerSignal) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.ServerSignal") }
func (SessionActivity) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.SessionActivity")
}
func (SessionEndpoint) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.SessionEndpoint")
}
func (SessionSignal) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.SessionSignal") }
func (Setting) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.Setting") }
func (Soundex) JumbleFields(s expr.StatementFields)       { s.JumbleText("functions.Soundex") }
func (sqlJSONDocument) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.sqlJSONDocument")
}
func (SSLInfo) JumbleFields(s expr.StatementFields)        { s.JumbleText("functions.SSLInfo") }
func (StatisticsIO) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.StatisticsIO") }
func (StatsSnapshot) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.StatsSnapshot") }
func (StringVariadic) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.StringVariadic") }
func (TablefuncCrosstab) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.TablefuncCrosstab")
}
func (TablefuncNormal) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.TablefuncNormal")
}
func (TablespaceLocation) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.TablespaceLocation")
}
func (TablespaceDatabases) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.TablespaceDatabases")
}
func (Tcn) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.Tcn") }
func (TextPatternComparison) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.TextPatternComparison")
}
func (transactionIdentityExpression) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.transactionIdentityExpression")
}
func (TransactionTime) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.TransactionTime")
}
func (TriggerBuiltin) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.TriggerBuiltin") }
func (TriggerDepth) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.TriggerDepth") }
func (TypeInquiry) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.TypeInquiry") }
func (UUIDExtract) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.UUIDExtract") }
func (UUIDGeneration) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.UUIDGeneration") }
func (VerifyHeapam) JumbleFields(s expr.StatementFields)   { s.JumbleText("functions.VerifyHeapam") }
func (VersionInformation) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.VersionInformation")
}
func (VisibilityInquiry) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.VisibilityInquiry")
}
func (WALPosition) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.WALPosition") }
func (WALSummary) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.WALSummary") }
func (XMLMapping) JumbleFields(s expr.StatementFields)  { s.JumbleText("functions.XMLMapping") }
func (XMLTable) JumbleFields(s expr.StatementFields)    { s.JumbleText("functions.XMLTable") }
