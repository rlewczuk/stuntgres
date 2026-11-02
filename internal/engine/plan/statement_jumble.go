package plan

import "stuntgres/internal/engine/expr"

// Each concrete analyzed expression owns its static discriminator. The analyzed
// visitor streams selected plan fields and lexical operands separately. Wrappers
// deliberately override promoted kernel projections: a wrapper is one node, not
// a wrapper tag followed by a second embedded-family tag.
func (AggregateExpr) JumbleFields(s expr.StatementFields)  { s.JumbleText("plan.AggregateExpr") }
func (ArithmeticExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.ArithmeticExpr") }
func (ArrayCastElementExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.ArrayCastElementExpr")
}
func (ArrayCastExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.ArrayCastExpr") }
func (ArrayExpr) JumbleFields(s expr.StatementFields)     { s.JumbleText("plan.ArrayExpr") }
func (AssignmentValueExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.AssignmentValueExpr")
}
func (BitwiseExpr) JumbleFields(s expr.StatementFields)     { s.JumbleText("plan.BitwiseExpr") }
func (CachedOrderExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.CachedOrderExpr") }
func (CaseExpr) JumbleFields(s expr.StatementFields)        { s.JumbleText("plan.CaseExpr") }
func (CaseTestExpr) JumbleFields(s expr.StatementFields)    { s.JumbleText("plan.CaseTestExpr") }
func (CitextComparisonExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.CitextComparisonExpr")
}
func (CitextPatternExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.CitextPatternExpr") }
func (CoercionExpr) JumbleFields(s expr.StatementFields)      { s.JumbleText("plan.CoercionExpr") }
func (CollationExpr) JumbleFields(s expr.StatementFields)     { s.JumbleText("plan.CollationExpr") }
func (ColumnExpr) JumbleFields(s expr.StatementFields)        { s.JumbleText("plan.ColumnExpr") }
func (ComparisonExpr) JumbleFields(s expr.StatementFields)    { s.JumbleText("plan.ComparisonExpr") }
func (ConditionalExpr) JumbleFields(s expr.StatementFields)   { s.JumbleText("plan.ConditionalExpr") }
func (CubeComparisonExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.CubeComparisonExpr")
}
func (DatumCastExpr) JumbleFields(s expr.StatementFields)   { s.JumbleText("plan.DatumCastExpr") }
func (DistinctExpr) JumbleFields(s expr.StatementFields)    { s.JumbleText("plan.DistinctExpr") }
func (DomainValueExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.DomainValueExpr") }
func (EarthCheckExpr) JumbleFields(s expr.StatementFields)  { s.JumbleText("plan.EarthCheckExpr") }
func (ExtensionPathsExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.ExtensionPathsExpr")
}
func (FieldExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.FieldExpr") }
func (GinEnumCompareExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.GinEnumCompareExpr")
}
func (GroupExpr) JumbleFields(s expr.StatementFields)    { s.JumbleText("plan.GroupExpr") }
func (GroupingExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.GroupingExpr") }
func (HstoreComparisonExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.HstoreComparisonExpr")
}
func (InsertUsernameExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InsertUsernameExpr")
}
func (InstalledAmcheckExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledAmcheckExpr")
}
func (InstalledAutoincExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledAutoincExpr")
}
func (InstalledConnectbyExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledConnectbyExpr")
}
func (InstalledCryptoExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledCryptoExpr")
}
func (InstalledCubeExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.InstalledCubeExpr") }
func (InstalledCubeSpatialExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledCubeSpatialExpr")
}
func (InstalledDblinkBuildSQLDeleteExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledDblinkBuildSQLDeleteExpr")
}
func (InstalledDblinkBuildSQLInsertExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledDblinkBuildSQLInsertExpr")
}
func (InstalledDblinkBuildSQLUpdateExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledDblinkBuildSQLUpdateExpr")
}
func (InstalledDblinkGetPkeyExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledDblinkGetPkeyExpr")
}
func (InstalledEarthExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledEarthExpr")
}
func (InstalledEarthPointExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledEarthPointExpr")
}
func (InstalledLevenshteinExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledLevenshteinExpr")
}
func (InstalledMetaphoneExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledMetaphoneExpr")
}
func (InstalledPageinspectExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPageinspectExpr")
}
func (InstalledPgFreespaceExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgFreespaceExpr")
}
func (InstalledPgrowlocksExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgrowlocksExpr")
}
func (InstalledPgStatStatementsExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgStatStatementsExpr")
}
func (InstalledPgstattupleExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgstattupleExpr")
}
func (InstalledPgstattupleStatsExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgstattupleStatsExpr")
}
func (InstalledPgvisibilityExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgvisibilityExpr")
}
func (n InstalledPgsurgeryExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgsurgeryExpr")
	s.JumbleUint64(uint64(n.Slots[0]))
	s.JumbleUint64(uint64(n.Slots[1]))
}
func (InstalledPgpArmorHeadersExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgpArmorHeadersExpr")
}
func (InstalledSoundexExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledSoundexExpr")
}
func (InstalledSSLInfoExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledSSLInfoExpr")
}
func (InstalledTablefuncCrosstabExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledTablefuncCrosstabExpr")
}
func (InstalledTablefuncNormalExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledTablefuncNormalExpr")
}
func (InstalledTextExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.InstalledTextExpr") }
func (InstalledUUIDOsspExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledUUIDOsspExpr")
}
func (InstalledVerifyHeapamExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledVerifyHeapamExpr")
}
func (LikeExpr) JumbleFields(s expr.StatementFields)         { s.JumbleText("plan.LikeExpr") }
func (LiteralExpr) JumbleFields(s expr.StatementFields)      { s.JumbleText("plan.LiteralExpr") }
func (LogicalExpr) JumbleFields(s expr.StatementFields)      { s.JumbleText("plan.LogicalExpr") }
func (MergeActionExpr) JumbleFields(s expr.StatementFields)  { s.JumbleText("plan.MergeActionExpr") }
func (NullTestExpr) JumbleFields(s expr.StatementFields)     { s.JumbleText("plan.NullTestExpr") }
func (ParameterExpr) JumbleFields(s expr.StatementFields)    { s.JumbleText("plan.ParameterExpr") }
func (PLAssignmentExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.PLAssignmentExpr") }
func (PLAssignmentTargetExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.PLAssignmentTargetExpr")
}
func (PLIOCoercionExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.PLIOCoercionExpr") }
func (PLLocalExpr) JumbleFields(s expr.StatementFields)      { s.JumbleText("plan.PLLocalExpr") }
func (RangeConstructorExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.RangeConstructorExpr")
}
func (ReturningColumnExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.ReturningColumnExpr")
}
func (ReturningWholeRowExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.ReturningWholeRowExpr")
}
func (n RowComparisonExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.RowComparisonExpr")
	s.JumbleText(n.Operator)
}
func (RowExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.RowExpr") }
func (ScalarArrayComparisonExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.ScalarArrayComparisonExpr")
}
func (SelectedOperatorExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.SelectedOperatorExpr")
}
func (SeriesExpr) JumbleFields(s expr.StatementFields)     { s.JumbleText("plan.SeriesExpr") }
func (SQLRoutineExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.SQLRoutineExpr") }
func (SubqueryExpr) JumbleFields(s expr.StatementFields)   { s.JumbleText("plan.SubqueryExpr") }
func (SubscriptExpr) JumbleFields(s expr.StatementFields)  { s.JumbleText("plan.SubscriptExpr") }
func (SubscriptsExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.SubscriptsExpr") }
func (SystemAttributeExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.SystemAttributeExpr")
}
func (TruthTestExpr) JumbleFields(s expr.StatementFields)    { s.JumbleText("plan.TruthTestExpr") }
func (UnknownOutput) JumbleFields(s expr.StatementFields)    { s.JumbleText("plan.UnknownOutput") }
func (UnnestExpr) JumbleFields(s expr.StatementFields)       { s.JumbleText("plan.UnnestExpr") }
func (UpdateOutputExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.UpdateOutputExpr") }
func (ViewQualificationExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.ViewQualificationExpr")
}
func (*WindowExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("*plan.WindowExpr") }
