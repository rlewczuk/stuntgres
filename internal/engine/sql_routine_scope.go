package engine

import (
	"maps"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// This is a native active-body safety bound, not PostgreSQL's byte-valued
// max_stack_depth setting. Mutual and direct calls borrow the same root owner.
const maxSQLRoutineDepth = 128

func (s *StatementScope) enterSQLRoutine() error {
	if s.activeSQLRoutineDepth >= maxSQLRoutineDepth {
		return sqlError("54001", "stack depth limit exceeded")
	}
	s.activeSQLRoutineDepth++
	return nil
}

func (s *StatementScope) leaveSQLRoutine() {
	s.activeSQLRoutineDepth--
}

// SQL routines use the existing rule-command activation convention: one actual
// StatementScope/WriteStage/Finish and private query/admission/presentation scratch.
// This deliberately excludes effects, constraint queues and completion programs.
// The live replacement reservation is root owner state, not lexical scratch;
// existing waits/providers alone release/reacquire it and Finish sees its real flag.
// It is balanced on every nested success/error/cancellation, never concurrent.
type sqlRoutineScratch struct {
	command                                                                                         ruleCommandScratch
	mutations                                                                                       []statementMutation
	activeMutation                                                                                  int
	cteInvocation                                                                                   *cteInvocation
	activeViews                                                                                     []ids.ObjectAddress
	inheritanceRelations                                                                            []queryLeaf
	inheritanceCatalog                                                                              catalog.Read
	authority                                                                                       *queryAuthority
	routineAuth                                                                                     AuthorizationView
	expected                                                                                        *StatementDescription
	preparedReuse                                                                                   *preparedAnalysis
	preparedInvalid                                                                                 bool
	preparedPath                                                                                    sequence.Environment
	preparedNamespace                                                                               uint64
	preparedPrivate                                                                                 *catalogChanges
	preparedEpoch                                                                                   ids.CatalogEpoch
	preparedChangedDuringAdmission, admissionWaited                                                 bool
	initialParameters                                                                               []sqltype.Usage
	scalar                                                                                          bool
	result                                                                                          sqltype.Usage
	resultPlan                                                                                      sqltype.CoercionPlan
	protocolBind                                                                                    *protocolPortal
	protocolResume, protocolSimple, protocolSimpleCreated, protocolSimpleStreaming, protocolCommand bool
	protocolSimplePortal                                                                            *protocolPortal
	input                                                                                           sqltype.InputEnvironment
	output                                                                                          sqltype.OutputEnvironment
	view, partitionView                                                                             *visibility.View
	ownWrites                                                                                       map[ids.ObjectAddress][]storage.RowVersion
	ownDeletes                                                                                      map[ids.ObjectAddress]*visibility.PrivateDeletes
	domainEvaluation                                                                                *EvalContext
	domainInputCatalog                                                                              catalog.Read
	foreignOwner                                                                                    *foreignScanOwner
	foreignCursor                                                                                   *sqlCursor
	pgRegex                                                                                         sqltype.PGRegexExecution
	searchPathResults                                                                               [2]sqltype.Value
	commentCatalog                                                                                  catalog.Read
	commentRoles                                                                                    authorization.Read
	deparseAuth                                                                                     AuthorizationView
	visibilityCatalog                                                                               catalog.Read
	visibilityDemand                                                                                bool
	visibilityRelationPath, visibilityOtherPath                                                     []ids.ObjectAddress
	conversionDefaults                                                                              map[[2]int]conversionDefault
	regprocOutput, regprocedureOutput                                                               map[uint32]string
	identity                                                                                        identitySnapshot
	currentSchemaResult                                                                             sqltype.Value
	deparsePath                                                                                     []ids.ObjectAddress
	operatorOutput                                                                                  map[operatorOutputKey]string
	operatorGroups                                                                                  operatorGroups
	collationPath                                                                                   []ids.ObjectAddress
	regTSOutput                                                                                     map[regTSOutputKey]string
}

func (s *StatementScope) sqlRoutineScratch() sqlRoutineScratch {
	return sqlRoutineScratch{
		command: s.ruleScratch(), mutations: s.mutations, activeMutation: s.activeMutation, cteInvocation: s.cteInvocation,
		activeViews: s.activeViews, inheritanceRelations: s.inheritanceRelations, inheritanceCatalog: s.inheritanceCatalog,
		authority: s.authority, routineAuth: s.routineAuth, expected: s.Expected,
		preparedReuse: s.preparedReuse, preparedInvalid: s.preparedInvalid, preparedPath: s.preparedPath, preparedNamespace: s.preparedNamespace,
		preparedPrivate: s.preparedPrivate, preparedEpoch: s.preparedEpoch, preparedChangedDuringAdmission: s.preparedChangedDuringAdmission, admissionWaited: s.admissionWaited,
		initialParameters: s.initialParameters, scalar: s.sqlRoutineScalar, input: s.input, output: s.output,
		result: s.sqlRoutineResult, resultPlan: s.sqlRoutineResultPlan,
		protocolBind: s.protocolBind, protocolResume: s.protocolResume, protocolSimple: s.protocolSimple, protocolSimpleCreated: s.protocolSimpleCreated, protocolSimpleStreaming: s.protocolSimpleStreaming, protocolCommand: s.protocolCommand, protocolSimplePortal: s.protocolSimplePortal,
		view: s.view, partitionView: s.partitionValidationView, ownWrites: s.ownWrites, ownDeletes: s.ownDeletes,
		domainEvaluation: s.domainEvaluation, domainInputCatalog: s.domainInputCatalog, foreignOwner: s.foreignOwner, foreignCursor: s.foreignCursor, pgRegex: s.pgRegex,
		searchPathResults: s.searchPathResults, commentCatalog: s.commentCatalog, commentRoles: s.commentRoles, deparseAuth: s.deparseAuth,
		visibilityCatalog: s.visibilityCatalog, visibilityDemand: s.visibilityDemand, visibilityRelationPath: s.visibilityRelationPath, visibilityOtherPath: s.visibilityOtherPath,
		conversionDefaults: s.conversionDefaults, regprocOutput: s.regprocOutput, regprocedureOutput: s.regprocedureOutput, identity: s.identity,
		currentSchemaResult: s.currentSchemaResult, deparsePath: s.deparsePath, operatorOutput: s.operatorOutput, operatorGroups: s.operatorGroups, collationPath: s.collationPath, regTSOutput: s.regTSOutput,
	}
}
func (s *StatementScope) installSQLRoutineScratch(c sqlRoutineScratch) {
	s.installRuleScratch(c.command)
	s.mutations, s.activeMutation, s.cteInvocation = c.mutations, c.activeMutation, c.cteInvocation
	s.activeViews, s.inheritanceRelations, s.inheritanceCatalog = c.activeViews, c.inheritanceRelations, c.inheritanceCatalog
	s.authority, s.routineAuth, s.Expected = c.authority, c.routineAuth, c.expected
	s.preparedReuse, s.preparedInvalid, s.preparedPath = c.preparedReuse, c.preparedInvalid, c.preparedPath
	s.preparedNamespace, s.preparedPrivate, s.preparedEpoch = c.preparedNamespace, c.preparedPrivate, c.preparedEpoch
	s.preparedChangedDuringAdmission, s.admissionWaited = c.preparedChangedDuringAdmission, c.admissionWaited
	s.initialParameters, s.sqlRoutineScalar = c.initialParameters, c.scalar
	s.sqlRoutineResult, s.sqlRoutineResultPlan = c.result, c.resultPlan
	s.protocolBind, s.protocolResume, s.protocolSimple, s.protocolSimpleCreated = c.protocolBind, c.protocolResume, c.protocolSimple, c.protocolSimpleCreated
	s.protocolSimpleStreaming, s.protocolCommand, s.protocolSimplePortal = c.protocolSimpleStreaming, c.protocolCommand, c.protocolSimplePortal
	s.input, s.output = c.input, c.output
	s.view, s.partitionValidationView = c.view, c.partitionView
	s.ownWrites, s.ownDeletes = c.ownWrites, c.ownDeletes
	s.domainEvaluation, s.domainInputCatalog = c.domainEvaluation, c.domainInputCatalog
	s.foreignOwner, s.foreignCursor = c.foreignOwner, c.foreignCursor
	s.pgRegex = c.pgRegex
	s.searchPathResults, s.commentCatalog, s.commentRoles, s.deparseAuth = c.searchPathResults, c.commentCatalog, c.commentRoles, c.deparseAuth
	s.visibilityCatalog, s.visibilityDemand = c.visibilityCatalog, c.visibilityDemand
	s.visibilityRelationPath, s.visibilityOtherPath = c.visibilityRelationPath, c.visibilityOtherPath
	s.conversionDefaults, s.regprocOutput, s.regprocedureOutput = c.conversionDefaults, c.regprocOutput, c.regprocedureOutput
	s.identity, s.currentSchemaResult, s.deparsePath = c.identity, c.currentSchemaResult, c.deparsePath
	s.operatorOutput, s.operatorGroups, s.collationPath, s.regTSOutput = c.operatorOutput, c.operatorGroups, c.collationPath, c.regTSOutput
}

func (s *StatementScope) activateSQLRoutine(inputs []sqltype.Usage) func(error) error {
	saved := s.sqlRoutineScratch()
	// Preserve the live operation's presentation/time policy, but create fresh
	// provider/capture/query state. The parameter frame itself is proc-owned.
	ownWrites := saved.ownWrites
	if len(s.stage.rows) != 0 {
		ownWrites = maps.Clone(ownWrites)
		if ownWrites == nil {
			ownWrites = make(map[ids.ObjectAddress][]storage.RowVersion, len(s.stage.rows))
		}
		for address, rows := range s.stage.rows {
			ownWrites[address] = mergePrivateRows(ownWrites[address], rows, true)
		}
	}
	ownDeletes := mergeDeletions(saved.ownDeletes, s.stage.deletes)
	s.installSQLRoutineScratch(sqlRoutineScratch{input: saved.input, output: saved.output, initialParameters: inputs, scalar: true, ownWrites: ownWrites, ownDeletes: ownDeletes})
	s.input.LockedDomainInput, s.input.DomainChecks = s, s
	return func(primary error) error {
		if s.foreignOwner != nil {
			primary = s.finishForeignOwner(s.foreignOwner, primary)
		}
		if s.view != nil && s.view != saved.view {
			s.view.Release()
		}
		if s.partitionValidationView != nil && s.partitionValidationView != saved.partitionView {
			s.partitionValidationView.Release()
		}
		if s.authority != nil && s.authority != saved.authority {
			s.authority.scope = nil
		}
		s.installSQLRoutineScratch(saved)
		return primary
	}
}
