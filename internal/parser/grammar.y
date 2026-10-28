%{
package parser
%}
%union {
 tok lexeme
 stmt Statement
 explainOptions []ExplainOption
 explainOption ExplainOption
 dictionaryOptions []DictionaryOption
 dictionaryOption DictionaryOption
 alterDictionary *AlterDictionary
 configurationOptions []ConfigurationOption
 configurationOption ConfigurationOption
 alterConfiguration *AlterConfiguration
 statements []Statement
 rule *CreateRule
 trigger *CreateTrigger
 triggerEvent TriggerEvent
 triggerEvents []TriggerEvent
 eventFilters []EventTriggerFilter
 eventFilter EventTriggerFilter
 foreignDDL *ForeignDDL
 foreignOptions []ForeignOption
 databaseOptions []DatabaseOption
 databaseOption DatabaseOption
 foreignOption ForeignOption
 foreignElements foreignElements
 foreignActions []ForeignTableAction
 foreignTableAction *ForeignTableAction
 foreignText *string
 insertStmt *Insert
 copyStmt *Copy
 copyOption *CopyOption
 copyOptions []CopyOption
 copyWhere copyWhereClause
 insertConflict *InsertConflict
 col *Column
 columns []Column
 elements []TableElement
 element *TableElement
 likeOptions []LikeOption
 likeOption LikeOption
 objectNames []ObjectName
 dropMode DropMode
 typ TypeName
 jsonFormat *JSONFormat
 jsonOptions *JSONAggregateOptions
 sqljson *SQLJSONExpr
 sqljsonQuery *SQLJSONQueryExpr
 sqljsonPassing []SQLJSONPassing
 sqljsonBehavior SQLJSONBehavior
 jsonTable *JSONTable
 jsonTableColumns []JSONTableColumn
 jsonTableColumn *JSONTableColumn
 types []TypeName
 grantRoutine GrantRoutineTarget
 grantRoutines []GrantRoutineTarget
 grantRoutineArgument *GrantRoutineArgument
 grantRoutineArguments []GrantRoutineArgument
 routineParameter *RoutineParameter
 routineParameters []RoutineParameter
 routineResult *routineResult
 routineAS *routineAS
 routineAttributes []RoutineAttribute
 routineAttribute RoutineAttribute
 createLanguage *CreateLanguage
 plBlock *PLBlock
 plDeclarations []PLDeclaration
 plDeclaration *PLDeclaration
 plStatements []PLStatement
 plStatement *PLStatement
 plHandlers []PLHandler
 plHandler PLHandler
 plConditions []PLCondition
 plCondition PLCondition
 plType *PLType
 plIdentifier Identifier
 plExpression PLExpression
 plExpressions []PLExpression
 plElse plElse
 plArms []PLArm
 plArm PLArm
 plCursor *PLCursorStatement
 plQuery plCursorQuery
 plQueryLoop *PLQueryLoop
 plForeach *PLForeach
 plIntegerFor *PLIntegerFor
 plDynamic *PLDynamicStatement
 plMotion *PLCursorMotion
 plActual *PLCursorActual
 plActuals []PLCursorActual
 plRaise *PLRaise
 plRaiseOption *PLRaiseOption
 plRaiseOptions []PLRaiseOption
 plDiagnostics *PLDiagnostics
 plDiagnostic PLDiagnosticAssignment
 plDiagnosticAssignments []PLDiagnosticAssignment
 constraints []ColumnConstraint
 constraint *ColumnConstraint
 notNull *NotNullConstraint
 check *CheckConstraint
 unique *UniqueConstraint
 foreignKey *ForeignKeyConstraint
 foreignAction foreignActionClause
 usingIndex *UsingIndexConstraint
 exclusion *ExclusionConstraint
 exclusionElements []ExclusionElement
 exclusionElement *ExclusionElement
 alterTarget alterTarget
 lockTargets []LockTarget
 lockMode TableLockMode
 alterActions []AlterTableAction
 alterAction *AlterTableAction
 dropTarget dropTarget
 targets []InsertTarget
 target InsertTarget
 subscripts []ArraySubscript
 steps []TargetStep
 step TargetStep
 subscript ArraySubscript
 assignments []UpdateAssignment
 assignment *UpdateAssignment
 names []string
 rows [][]Expr
 row []Expr
 value Value
 expr Expr
 groupedPrimary groupedPrimary
 mutationQualifier mutationQualifier
 call FunctionExpr
 predicate PredicateKind
 selectStmt *Select
 ctes []CommonTableExpression
 cte *CommonTableExpression
 cteHint CTEMaterialization
 cteClause cteClause
 cycle *CycleClause
 search *SearchClause
 parenthesizedQuery parenthesizedQuery
 insertSource *InsertSource
 mergeArms []MergeArm
 mergeArm *MergeArm
 valuesQuery *ValuesQuery
 order *Order
 orderKey OrderKey
 orderKeys []OrderKey
 nulls NullPlacement
 locking RowLockClause
 lockings []RowLockClause
 lockWait RowLockWait
 bounds *QueryBounds
 tail queryTail
 desc bool
 objectName ObjectName
 collation *ObjectName
 settingValues settingList
 settingValue settingAtom
 roleOptions *RoleOptions
 roleSpec RoleSpec
 roleSpecs []RoleSpec
 membershipOptions []MembershipOption
 membershipOption MembershipOption
 grantor *RoleSpec
 grantPrivileges []GrantPrivilege
 grantPrivilege GrantPrivilege
 grantGrantees []GrantGrantee
 grantGrantee GrantGrantee
 objectGrant *ObjectGrant
 defaultPrivileges *AlterDefaultPrivileges
 defaultOptions []DefaultPrivilegesOption
 defaultOption DefaultPrivilegesOption
 settingName *settingName
 tables []*CreateTable
 table *CreateTable
 queryTable *CreateQueryTable
 into *ObjectName
 modes []TransactionMode
 mode TransactionMode
 isolation TransactionIsolation
 sequenceOptions []SequenceOption
 sequenceOption *SequenceOption
 sequence *CreateSequence
 alterSequence *AlterSequence
 integer int64
 frameExclusion FrameExclusion
 joinKind JoinKind
 persistence string
 onCommit onCommitClause
 identity *IdentityDefinition
 generation *GenerationDefinition
 identityMode IdentityMode
 override InsertOverride
 caseArms []CaseArm
 caseArm CaseArm
 fromItem FromItem
 functionTable *FunctionTable
 functionTables []FunctionExpr
 identifiers []Identifier
 functionColumnDefinitions []FunctionColumnDefinition
 identifier *Identifier
 viewOptions []ViewOption
 viewOption ViewOption
 zoneSet *Set
 returning *Returning
 returningAliases []ReturningAlias
 returningAlias ReturningAlias
 indexKeys []IndexKey
 indexKey *IndexKey
 createIndex *CreateIndex
 alterView *AlterView
 enumLabels []EnumLabel
 alterEnum *AlterEnum
 rangeOptions []RangeOption
 rangeOption *RangeOption
 alterIndex *AlterIndex
 indexOptions []IndexOption
 indexOption *IndexOption
 indexValue Value
 windowSpec *WindowSpec
 windowOver *windowOverClause
 windows []WindowDefinition
 window *WindowDefinition
 windowFrame *WindowFrame
 frameBound FrameBound
 groupClause *GroupClause
 groupItems []GroupItem
 groupItem GroupItem
 cursorFetch *FetchCursor
 createCollation *CreateCollation
 alterCollation *AlterCollation
 alterConversion *AlterConversion
 collationOptions []CollationOption
 collationOption CollationOption
 partitionKey *PartitionKey
 partitionBound *PartitionBound
 partitionDatum PartitionDatum
 partitionData []PartitionDatum
 extensionOptions []ExtensionOption
 extensionOption ExtensionOption
 operatorOption *OperatorOption
 operatorOptions []OperatorOption
 operatorSignature *OperatorSignature
 operatorSignatures []OperatorSignature
 alterOperator *AlterOperator
 familyItem *OperatorFamilyItem
 familyItems []OperatorFamilyItem
 alterFamily *AlterOperatorFamily
 createClass *CreateOperatorClass
 alterClass *AlterOperatorClass
 vacuumOptions []VacuumOption
 vacuumOption *VacuumOption
 vacuumTargets []VacuumTarget
 vacuumTarget VacuumTarget
}
%token <tok> IDENT NUMBER STRING BITSTRING PARAM
/* Parser-private numeric continuation for DATE literals, never a keyword. */
%token <tok> DATE_MODIFIER_NUMBER
/* Private bounded replay entry for destination-context INSERT cells. */
%token <tok> INSERT_SCALAR_VALUES
%token <tok> TYPE_INPUT
%token <tok> PL_ENTRY PL_RETURN PL_CONSTANT PL_ALIAS PL_TYPE_COPY PL_ROW_COPY PL_LABEL_OPEN PL_LABEL_CLOSE PL_ASSIGN PL_EXCEPTION PL_RAISE PL_SQLSTATE PL_ELSIF
%token <tok> PL_GET PL_CURRENT PL_STACKED PL_DIAGNOSTICS
%token <tok> PL_TARGETS_ENTRY
%token <tok> PL_THEN PL_WHEN PL_WHEN_COMMA
%token <tok> PL_ASSERT PL_RAISE_LEVEL PL_MESSAGE_COMMA PL_RAISE_USING
%token <tok> TRIGGER EVENT
%token <tok> PL_OPEN PL_LOOP PL_WHILE PL_NEXT PL_PRIOR PL_LAST PL_EXIT PL_CONTINUE
%token <tok> PL_FOREACH PL_SLICE
%token <tok> PL_INTEGER_FOR PL_REVERSE PL_BY
%token <tok> PL_EXECUTE PL_RETURN_QUERY PL_DYNAMIC_INTO PL_DYNAMIC_USING PL_DYNAMIC_COMMA PL_STRICT
%token <tok> SETOF_TYPE
%token <tok> DOT_DOT
%token <tok> LESS_EQUAL GREATER_EQUAL NOT_EQUAL EQUALS_GREATER
%token <tok> SYMBOLIC_OPERATOR
%token <tok> OPERATOR QUALIFIED_OPERATOR
%token <tok> OPCLASS
%token <tok> TYPECAST
%token <tok> '+' '-' '*' '/' '%' '^' '[' ']' ':' '(' ')' '=' '<' '>' ',' '.'
%token <tok> CREATE TABLE INSERT UPDATE INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE OR
%token <tok> DOMAIN TYPE
%token <tok> FUNCTION PROCEDURE ROUTINE AGGREGATE OUT_ARG INOUT_ARG RETURNS_CLAUSE
%token <tok> ROUTINE_CALLED ROUTINE_COST ROUTINE_SUPPORT ROUTINE_PARALLEL ROUTINE_SECURITY ROUTINE_EXTERNAL
%token <tok> RETURN_BODY ATOMIC_BODY
%token <tok> CALL
%token <tok> PARAMETER
%token <tok> LANGUAGE LARGE OBJECT DATA WRAPPER
%token <tok> COPY EXPLAIN
%token <tok> TRUNCATE CONTINUE
%token <tok> ARRAY
%token <tok> HAVING
%token <tok> FILTER WITHIN
%token <tok> JSON_ARRAYAGG JSON_OBJECTAGG JSON_FORMAT JSON_FORMAT_TYPE JSON_ENCODING JSON_ABSENT JSON_NULL JSON_KEYS JSON_WITH JSON_WITHOUT
%token <tok> SQLJSON_ARRAY SQLJSON_OBJECT SQLJSON_PARSE SQLJSON_SCALAR SQLJSON_SERIALIZE JSON_PREDICATE JSON_KIND
%token <tok> SQLJSON_QUERY_FUNC JSON_PASSING JSON_WRAPPER JSON_CONDITIONAL JSON_UNCONDITIONAL JSON_QUOTES JSON_KEEP JSON_OMIT JSON_BEHAVIOR JSON_EMPTY JSON_ERROR JSON_SCALAR_STRING
%token <tok> JSON_TABLE_FUNC JSON_COLUMNS JSON_NESTED JSON_PATH
%token <tok> OVER WINDOW PARTITION FRAME_UNIT FRAME_EDGE FRAME_START
%token <tok> UNIQUE PRIMARY
%token <tok> FOREIGN REFERENCES MATCH PERIOD INITIALLY
%token <tok> ENFORCED
%token <tok> NOT_DEFERRABLE_LA NOT_IN_LA
%token <tok> FOR
%token <tok> LIMIT OFFSET FETCH
%token <tok> DECLARE CURSOR CLOSE SCROLL HOLD FORWARD BACKWARD ABSOLUTE RELATIVE MOVE
%token <tok> OF NOWAIT SKIP LOCKED UNION INTERSECT EXCEPT
%token <tok> CROSS INNER JOIN NATURAL USING VIEW MATERIALIZED LEFT RIGHT FULL OUTER
%token <tok> ORDINALITY
%token <tok> VIEW_CHECK_WITH
%token <tok> DELETE
%token <tok> MERGE MATCHED SOURCE TARGET DO NOTHING
%token <tok> CONFLICT ON_CONFLICT_LA
%token <tok> RETURNING
%token <tok> BETWEEN SYMMETRIC ASYMMETRIC
%token <tok> INTEGER BIGINT TEXT BOOLEAN KW_U KW_C KW_T KW_R AS CAST NULLS_LA
%token <tok> DOUBLE_TYPE PRECISION_TYPE CHARACTER_TYPE BIT_TYPE VARYING_TYPE
%token <tok> GROUPING SETS ROLLUP CUBE
%token <tok> MERGE_ACTION
%token <tok> TIMESTAMP WITHOUT TIME ZONE
%token <tok> AT
%token <tok> INTERVAL HOUR MINUTE SECOND DAY MONTH YEAR
%token <tok> SET SHOW RESET ALL SESSION TO DEFAULT LOCAL ON
%token <tok> NAMES
%token <tok> SELECTED_FUNC VARIADIC
%token <tok> STRING_FUNC NORMALIZE NORMALIZED TRIM NFC NFD NFKC NFKD LEADING TRAILING BOTH
%token <tok> CASE WHEN THEN ELSE COALESCE NULLIF GREATEST LEAST
%token <tok> IF EXISTS
%token <tok> ANY SOME ROW
%token <tok> ROLE USER GROUP DROP WITH AUTHORIZATION SESSION_USER CURRENT_USER CURRENT_ROLE
%token <tok> RECURSIVE XML
%token <tok> GRANT REVOKE ADMIN INHERIT OPTION GRANTED
%token <tok> CURRENT_CATALOG SYSTEM_USER IDENTITY_FUNC IDENTITY_KEYWORD_FUNC
%token <tok> CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP LOCALTIME LOCALTIMESTAMP
%token <tok> CURRENT_DATETIME_FUNC CURRENT_DATETIME_KEYWORD_FUNC
%token <tok> LOCK ACCESS EXCLUSIVE MODE IN
%token <tok> LATERAL
%token <tok> LIKE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CASCADE RESTRICT CURRENT_SCHEMA
%token <tok> ILIKE ESCAPE SIMILAR
%token <tok> COMMENT COLUMN IS
%token <tok> COLLATE
%token <tok> COLLATION
%token <tok> CONVERSION
%token <tok> STATISTICS
%token <tok> REFRESH VERSION
%token <tok> EXTENSION DATABASE
%token <tok> ISNULL NOTNULL DISTINCT UNKNOWN
%token <tok> CONSTRAINT ADD INHERITS
%token <tok> CHECK
%token <tok> CHECK_FUNC
%token <tok> TX_TIME_FUNC
%token <tok> INDEX_SUBQUERY_OPEN
%token <tok> GENERATION_OVER GENERATION_PARTITION
%token <tok> OVERLAY POSITION SUBSTRING PLACING
%token <tok> EXTRACT
%token <tok> OVERLAPS
%token <tok> BEGIN START COMMIT END ROLLBACK ABORT WORK TRANSACTION AND NO CHAIN
%token <tok> ISOLATION LEVEL READ ONLY DEFERRABLE SAVEPOINT RELEASE PREPARE PREPARED
%token <tok> EXECUTE DEALLOCATE DISCARD
%token <tok> COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE
%token <tok> SEQUENCE INCREMENT MINVALUE MAXVALUE CACHE CYCLE OWNED RESTART TEMP UNLOGGED LOGGED SEQUENCE_FUNC
%token <tok> SEARCH BREADTH DEPTH FIRST
%token <tok> REASSIGN
%token <tok> GENERATED ALWAYS IDENTITY OVERRIDING SYSTEM VALUE
%token <tok> ROLE_PASSWORD ROLE_CONNECTION ROLE_VALID ROLE_ENCRYPTED ROLE_UNENCRYPTED
%token <tok> INDEX REINDEX
%token <tok> VACUUM FREEZE VERBOSE ANALYZE
%token <tok> CONCURRENTLY
%token <tok> INCLUDE TABLESPACE
%token <tok> EXCLUDE
%token <tok> POLICY
%token <tok> RULE ALSO INSTEAD NOTIFY LISTEN UNLISTEN LOAD
%token <tok> FDW_SERVER FDW_MAPPING FDW_HANDLER FDW_VALIDATOR FDW_IMPORT
%token <tok> LANGUAGE_TRUSTED LANGUAGE_PROCEDURAL LANGUAGE_INLINE
%token <tok> TS_CONFIGURATION
/* Contextual command continuations remain below arithmetic precedence. */
%nonassoc NAME_LIST
%nonassoc INTO KW_R ANALYZE UNIQUE PRIMARY CONSTRAINT COLUMN ALL DEFAULT ON USER GROUP HAVING IN AND SAVEPOINT
/* Predicate continuations reduce a completed cast type before consuming IS,
 * ISNULL or NOTNULL. Keep their shared nonassociativity below comparisons. */
%nonassoc IS ISNULL NOTNULL
%nonassoc BETWEEN NOT NOT_IN_LA OVERLAPS
/* ESCAPE shifts after a complete pattern, but cannot become a cast type tail. */
%nonassoc ESCAPE
/* The qualified opener participates as lookahead as well as through %prec:
 * otherwise a completed scalar cannot resolve whether to shift its infix use. */
%left SYMBOLIC_OPERATOR OPERATOR QUALIFIED_OPERATOR
%left '+' '-'
%left '*' '/' '%'
%left '^'
%left AT
%right UNARY
/* KW_T, LIKE and WITH also start suffixes. Shift an adjacent type tail before
 * completing a single type name: known excluded families use their first type
 * location, while arbitrary tails are syntax errors at the second token. */
/* Sequence options and an identity constraint terminate a generic type name,
 * rather than extending an arbitrary two-word type spelling. */
%nonassoc START NO INCREMENT MINVALUE MAXVALUE CACHE CYCLE OWNED RESTART GENERATED
/* SQL-regex continuation terminates a postfix-cast type, rather than becoming
 * an arbitrary second type-name word. Preserve SIMILAR's keyword identity. */
%nonassoc SIMILAR
/* Frame units/edges terminate postfix type annotations in window keys/bounds. */
%nonassoc FRAME_UNIT FRAME_EDGE FRAME_START
/* Attribute dependency modes terminate a complete type, including generic
 * names such as smallint, rather than extending a two-word type spelling. */
%nonassoc CASCADE RESTRICT
%nonassoc TYPE_NAME
/* Retain contextual type-tail shifts instead of relying on yacc defaults. */
%nonassoc INDEX CONCURRENTLY
/* Postfix casts put projection labels in the follow set of type names.
 * Preserve declaration grammar's deliberate type-tail shift rather than
 * leaving those decisions as implicit shift/reduce conflicts. Interval field
 * keywords also shift qualifier tails after INTERVAL and INTERVAL STRING. */
/* REINDEX previously used KW_U: keep that same type-tail shift when promoting
 * it to a utility token. INDEX/CONCURRENTLY retain their existing decisions. */
/* These membership tokens were IDENT before promotion. Preserve IDENT's
 * existing type-tail decision, exactly as for REINDEX above. */
/* CONVERSION formerly used KW_U; preserve its type-tail decisions too. */
/* RECURSIVE/XML likewise retain KW_U's contextual identifier/type-tail precedence. */
/* LISTEN/UNLISTEN/LOAD also retain their former KW_U type-tail precedence. */
%nonassoc IDENT DOMAIN TYPE INCLUDE TABLESPACE SELECTED_FUNC UNKNOWN COPY INSERT UPDATE DELETE VALUES BY KW_U TS_CONFIGURATION RECURSIVE XML KW_C CONVERSION STATISTICS FDW_SERVER FDW_MAPPING FDW_HANDLER FDW_VALIDATOR FDW_IMPORT LANGUAGE_TRUSTED LANGUAGE_PROCEDURAL LANGUAGE_INLINE ZONE SET SHOW RESET SESSION LOCAL IF EXISTS ROLE DROP AUTHORIZATION LOCK ACCESS EXCLUSIVE MODE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CURRENT_SCHEMA COMMENT ADD INHERITS BEGIN COMMIT ROLLBACK ABORT WORK TRANSACTION CHAIN ISOLATION LEVEL READ RELEASE PREPARE PREPARED COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE SEQUENCE TEMP UNLOGGED LOGGED SEQUENCE_FUNC ALWAYS IDENTITY OVERRIDING SYSTEM VALUE YEAR MONTH DAY HOUR MINUTE SECOND REINDEX VACUUM MATERIALIZED ADMIN INHERIT OPTION GRANTED REVOKE REFRESH VERSION DISCARD LISTEN UNLISTEN LOAD '.'
%nonassoc EXPLAIN
/* These words previously used KW_U. Preserve its type-tail and COPY-WITH
 * decisions rather than adding implicit zero-precedence conflicts. */
%nonassoc RETURN_BODY ATOMIC_BODY
%nonassoc EXCLUDE
/* Preserve the existing generic type-tail shift for contextual call terminals. */
%nonassoc CHECK_FUNC TX_TIME_FUNC EXTRACT OVERLAY POSITION SUBSTRING ROW
%nonassoc GENERATION_OVER
%nonassoc COALESCE NULLIF GREATEST LEAST CASE WHEN THEN ELSE
%nonassoc IDENTITY_FUNC IDENTITY_KEYWORD_FUNC CURRENT_DATETIME_FUNC CURRENT_DATETIME_KEYWORD_FUNC
%nonassoc STRING_FUNC NORMALIZE NORMALIZED TRIM NFC NFD NFKC NFKD
%nonassoc '('
%nonassoc KW_T FREEZE VERBOSE CROSS INNER JOIN NATURAL LEFT RIGHT FULL OUTER LIKE ILIKE WITH WITHOUT
%nonassoc '='
%left COLLATE
%left TYPECAST
%nonassoc '['
/* Parenthesized targets enter the scalar layer once, sharing its postfixes. */
%nonassoc ')'
%nonassoc FILTER WITHIN
%nonassoc OVER
%type <windowSpec> window_spec
%type <windowOver> over_clause
%type <windows> windows window_definitions
%type <window> window_definition
%type <identifier> window_parent
%type <row> window_partition
%type <orderKeys> window_order
%type <tok> window_name ordinary_call_keyword
%type <windowFrame> window_frame
%type <frameBound> frame_bound
%type <frameExclusion> window_exclusion
%type <stmt> statement create create_table drop_table alter_drop_column alter_default_single alter_rename_single alter_schema_single insert select set show reset create_role alter_role drop_role session_authorization role_selection lock_table create_schema alter_schema drop_schema transaction_control
%type <stmt> explain explainable
%type <explainOptions> explain_option_list explain_legacy
%type <explainOption> explain_option
%type <tok> explain_option_name explain_word
%type <expr> explain_option_value explain_optional_value
%type <plDynamic> pl_dynamic pl_dynamic_command
%type <tok> pl_dynamic_head pl_dynamic_using pl_dynamic_comma pl_dynamic_into
%type <plExpressions> pl_dynamic_values
%type <desc> pl_dynamic_strict
%type <stmt> foreign_statement
%type <foreignDDL> foreign_wrapper_head foreign_wrapper_definition foreign_server_head foreign_server_definition foreign_mapping_head foreign_table_head foreign_import_head foreign_alter_wrapper_head foreign_alter_wrapper_prefix foreign_alter_wrapper
%type <foreignOptions> foreign_options foreign_option_list foreign_alter_options foreign_alter_option_list
%type <foreignOptions> foreign_optional_alter_options
%type <foreignOption> foreign_option foreign_alter_option
%type <tok> wrapper_word
%type <foreignElements> foreign_elements foreign_element foreign_optional_elements
%type <foreignActions> foreign_table_actions
%type <foreignTableAction> foreign_table_action alter_column_add
%type <foreignText> foreign_version
%type <objectNames> foreign_names
%type <stmt> truncate_table
%type <tok> truncate_identity
%type <stmt> create_policy alter_policy drop_policy
%type <stmt> create_rule alter_rule drop_rule rule_action
%type <stmt> create_trigger alter_trigger drop_trigger
%type <stmt> create_event_trigger alter_event_trigger drop_event_trigger
%type <eventFilters> event_filters event_filter_clauses
%type <eventFilter> event_filter
%type <names> event_tag_list
%type <trigger> trigger_head
%type <triggerEvent> trigger_event
%type <triggerEvents> trigger_events
%type <tok> trigger_timing trigger_function trigger_each trigger_statement
%type <desc> trigger_row
%type <expr> trigger_when
%type <names> trigger_arguments trigger_argument_list
%type <tok> trigger_argument
%type <rule> rule_head
%type <tok> rule_event
%type <expr> rule_condition
%type <desc> rule_instead
%type <statements> rule_actions rule_action_list
%type <tok> policy_command policy_option
%type <desc> policy_permissive
%type <grantGrantees> policy_roles
%type <expr> policy_using policy_check
%type <stmt> copy
%type <copyStmt> copy_target copy_endpoint
%type <tok> copy_direction
%type <copyOptions> copy_options copy_option_list copy_legacy_options
%type <copyOption> copy_option copy_option_value copy_legacy_option
%type <tok> copy_option_string
%type <identifiers> copy_option_strings
%type <tok> copy_option_name
%type <copyWhere> copy_where
%type <stmt> declare_cursor fetch_cursor close_cursor
%type <cursorFetch> cursor_direction
%type <integer> cursor_scroll cursor_integer
%type <desc> cursor_hold
%type <tok> cursor_from cursor_word cursor_command
%type <stmt> select_query
%type <selectStmt> query_select query_set query_intersection select_core select_operand select_projection
%type <ctes> cte_list
%type <cteClause> with_clause
%type <cte> cte_definition
%type <cteHint> cte_materialization
%type <cycle> cycle_clause
%type <search> search_clause
%type <identifiers> cte_keys
%type <expr> cycle_constant
%type <queryTable> query_table_target
%type <stmt> create_query_table
%type <stmt> refresh_materialized_view
%type <selectStmt> query_table_body
%type <desc> query_table_data
%type <tok> data_word
%type <into> into_clause
%type <parenthesizedQuery> select_with_parens
%type <desc> set_modifier
%type <stmt> create_domain drop_domain alter_domain create_enum create_composite alter_enum
%type <check> domain_alter_check
%type <tok> validate_word
%type <stmt> alter_composite drop_type alter_table_of
%type <alterActions> composite_actions
%type <alterAction> composite_action
%type <tok> attribute_word options_word
%type <elements> typed_options typed_elements
%type <element> typed_element
%type <stmt> create_collation alter_collation drop_collation
%type <stmt> create_conversion alter_conversion drop_conversion
%type <stmt> create_statistics drop_statistics alter_statistics_target
%type <integer> statistics_target
%type <identifiers> statistics_kinds
%type <tok> statistics_create_head statistics_alter_head
%type <stmt> create_dictionary alter_dictionary drop_dictionary
%type <tok> dictionary_word
%type <dictionaryOptions> dictionary_options
%type <dictionaryOption> dictionary_option
%type <alterDictionary> dictionary_alter_head
%type <stmt> create_configuration alter_configuration drop_configuration
%type <configurationOptions> configuration_options
%type <configurationOption> configuration_option
%type <alterConfiguration> configuration_alter_head
%type <identifiers> configuration_tokens
%type <stmt> create_function create_aggregate drop_routine alter_routine
%type <stmt> create_language
%type <createLanguage> language_head language_flags
%type <objectName> language_inline language_validator
%type <routineAS> routine_as
%type <routineAttributes> function_attributes procedure_attributes alter_routine_attributes
%type <routineAttribute> function_attribute procedure_attribute routine_setting
%type <tok> function_attribute_word
%type <stmt> procedure_head
%type <tok> procedure_rows_word procedure_input_word procedure_leakproof_word routine_current_word
%type <tok> routine_security_word
%type <stmt> create_cast drop_cast
%type <stmt> do_statement
%type <plBlock> pl_block
%type <plDeclarations> pl_declarations pl_declaration_list
%type <plDeclaration> pl_declaration
%type <plStatements> pl_statements
%type <plStatement> pl_statement
%type <plDiagnostics> pl_diagnostics_head
%type <plDiagnostic> pl_diagnostic_assignment
%type <plDiagnosticAssignments> pl_diagnostic_assignments
%type <tok> pl_diagnostic_item pl_diagnostic_equal
%type <plHandlers> pl_exceptions pl_handlers
%type <plHandler> pl_handler
%type <plConditions> pl_conditions
%type <plCondition> pl_condition
%type <plType> pl_type
%type <plIdentifier> pl_label pl_end_label
%type <desc> pl_constant
%type <tok> pl_not_null
%type <expr> pl_target
%type <plExpression> pl_default pl_return_value pl_expression pl_if_condition pl_search pl_transfer_when pl_transfer_expression
%type <plExpressions> pl_when_items pl_case_condition
%type <plElse> pl_else
%type <plArms> pl_elsif_arms pl_case_arms
%type <plArm> pl_case_arm
%type <tok> pl_name pl_declaration_name
%type <tok> pl_if_head pl_elsif_head pl_when_head pl_case_head pl_when_separator
%type <tok> pl_transfer_kind pl_transfer_when_head
%type <tok> pl_while_head
%type <plCursor> pl_open_tail pl_cursor_actuals
%type <plMotion> pl_cursor_motion
%type <plActual> pl_cursor_actual
%type <plActuals> pl_cursor_actual_list
%type <plDeclarations> pl_cursor_arguments pl_cursor_argument_list
%type <plDeclaration> pl_cursor_argument
%type <plQuery> pl_cursor_query
%type <plQueryLoop> pl_query_loop_source
%type <plForeach> pl_foreach_slice
%type <plForeach> pl_foreach_targets
%type <plIntegerFor> pl_integer_targets pl_integer_lower pl_integer_upper pl_integer_step pl_integer_range
%type <plExpression> pl_integer_operand
%type <tok> pl_integer_in pl_integer_reverse
%type <expr> pl_foreach_target
%type <tok> pl_foreach_array
%type <stmt> pl_query_loop_statement
%type <stmt> embedded_sql_statement pl_static_statement
%type <tok> pl_cursor_for pl_cursor_from
%type <row> pl_cursor_targets
%type <plExpressions> pl_using pl_using_values
%type <plRaise> pl_raise_body
%type <plRaiseOptions> pl_raise_options pl_raise_option_list
%type <plRaiseOption> pl_raise_option pl_raise_option_name
%type <plExpressions> pl_raise_arguments
%type <plExpression> pl_assert_message
%type <tok> pl_message_comma pl_raise_using pl_option_assign pl_assert_head
%type <tok> cast_context
%type <tok> returns_word
%type <routineParameters> function_arguments optional_function_arguments function_declaration_argument_list table_function_columns
%type <routineParameter> function_declaration_argument table_function_column
%type <routineResult> function_result
%type <grantRoutineArguments> aggregate_arguments
%type <statements> standard_routine_body routine_body_list
%type <stmt> routine_body_statement
%type <stmt> nontransaction_statement transaction_common
%type <tok> routine_return_head routine_atomic_head
%type <alterConversion> conversion_alter_head
%type <stmt> create_operator alter_operator drop_operator
%type <objectName> operator_name
%type <tok> operator_option_schema_keyword
%type <operatorOption> operator_option
%type <operatorOptions> operator_options
%type <operatorSignature> operator_signature
%type <operatorSignatures> operator_signatures
%type <alterOperator> operator_alter_head
%type <stmt> create_operator_family alter_operator_family drop_operator_family
%type <tok> family_word search_word family_operator_word
%type <stmt> create_access_method drop_access_method
%type <tok> method_word access_method_kind
%type <alterFamily> operator_family_head
%type <familyItem> family_add_item family_operator_item family_function_item family_drop_item
%type <familyItems> family_add_items family_drop_items
%type <types> family_signature
%type <stmt> create_operator_class alter_operator_class drop_operator_class
%type <createClass> operator_class_head
%type <alterClass> operator_class_alter_head
%type <tok> class_word storage_word
%type <familyItem> class_item
%type <familyItems> class_items
%type <createCollation> collation_create_head
%type <alterCollation> collation_alter_head
%type <collationOptions> collation_options
%type <collationOption> collation_option
%type <tok> collation_name_word
%type <stmt> create_extension drop_extension alter_extension_update
%type <stmt> create_tablespace
%type <stmt> create_database
%type <databaseOptions> database_options database_option_list
%type <databaseOption> database_option database_option_value
%type <tok> database_option_name
%type <desc> database_option_equals
%type <grantor> tablespace_owner
%type <tok> tablespace_location
%type <indexOptions> tablespace_options
%type <extensionOptions> extension_options extension_option_list extension_update_options
%type <extensionOption> extension_option extension_update_option
%type <tok> extension_version
%type <enumLabels> enum_labels enum_label_list
%type <columns> composite_members composite_member_list
%type <col> composite_member
%type <tok> enum_word range_word generic_word_without_frame generic_word_base_without_vacuum_frame
%type <rangeOptions> range_options
%type <rangeOption> range_option
%type <tok> enum_placement_word
%type <alterEnum> enum_placement
%type <tok> check_function_head
%type <tok> extract_field
%type <alterActions> alter_actions
%type <alterAction> alter_selected_action ordinary_alter_action alter_constraint
%type <tok> alter_type_keyword
%type <collation> column_collation
%type <expr> alter_using
%type <stmt> delete
%type <tok> delete_alias update_alias from_alias insert_alias
%type <returning> returning
%type <returningAliases> returning_aliases returning_options
%type <returningAlias> returning_option
%type <stmt> create_view alter_view drop_view
%type <alterView> view_alter_head materialized_alter_head
%type <tok> replace_word depends_word
%type <createIndex> index_head index_definition index_policy index_storage index_tablespace create_index
%type <tok> index_method index_direction index_null_policy
%type <indexKeys> index_keys index_include
%type <indexKey> index_key index_atom index_class
%type <insertStmt> insert_base
%type <insertConflict> insert_conflict conflict_target
%type <tok> conflict_open
%type <expr> conflict_predicate
%type <partitionKey> partition_by
%type <partitionBound> partition_bound hash_bound_options hash_bound_option
%type <partitionDatum> partition_datum list_partition_datum
%type <partitionData> partition_data list_partition_data
%type <indexKeys> partition_keys
%type <indexKey> partition_key
%type <elements> partition_options partition_elements
%type <element> partition_element
%type <viewOptions> index_options index_option_list
%type <viewOption> index_option
%type <tok> index_option_value
%type <stmt> alter_index drop_index reindex
%type <alterIndex> alter_index_head
%type <indexOptions> maintenance_index_option_list reindex_options reindex_option_list
%type <indexOption> maintenance_index_option reindex_option
%type <tok> index_option_name reindex_kind maintenance_concurrent
%type <indexValue> maintenance_index_option_value
%type <stmt> vacuum analyze
%type <vacuumOptions> vacuum_options vacuum_option_list vacuum_legacy
%type <vacuumOptions> analyze_options
%type <vacuumOption> vacuum_option
%type <vacuumTargets> vacuum_targets vacuum_target_list
%type <vacuumTarget> vacuum_target
%type <tok> vacuum_full vacuum_freeze vacuum_verbose vacuum_analyze
%type <desc> vacuum_star
%type <identifiers> view_columns view_names
%type <viewOptions> view_options view_option_list view_check_option
%type <viewOption> view_option
%type <tok> view_option_value
%type <mutationQualifier> mutation_where
%type <expr> scalar_projection
%type <fromItem> optional_from
%type <row> row_fields
%type <expr> overlaps_row
%type <tok> quantifier
%type <valuesQuery> query_values
%type <rows> query_rows
%type <rows> insert_scalar_rows
%type <row> insert_scalar_row insert_scalar_cells
%type <expr> insert_scalar_cell
%type <row> query_row query_cells
%type <expr> query_cell
%type <stmt> update
%type <assignments> update_assignments update_row_assignments
%type <assignment> update_assignment update_row_assignment
%type <col> column
%type <elements> elements optional_elements
%type <element> element
%type <check> check_constraint
%type <unique> unique_constraint column_unique primary_constraint column_primary
%type <foreignKey> foreign_key foreign_reference foreign_body foreign_definition foreign_actions
%type <identifiers> foreign_columns foreign_columns_optional foreign_set_columns
%type <tok> foreign_match foreign_match_optional
%type <foreignAction> foreign_action foreign_update foreign_delete
%type <tok> primary_head primary_modifier
%type <usingIndex> using_index_constraint
%type <identifiers> primary_columns
%type <exclusion> exclusion_head exclusion_definition exclusion_storage exclusion_tablespace exclusion_predicate exclusion_constraint
%type <exclusionElements> exclusion_elements
%type <exclusionElement> exclusion_element
%type <objectName> exclusion_operator
%type <stmt> set_constraints
%type <desc> unique_nulls
%type <likeOptions> like_options
%type <likeOption> like_option
%type <tok> like_option_name
%type <objectNames> relation_names table_parents
%type <objectName> qualified_operator
%type <tok> operator_symbol
%type <dropMode> drop_mode
%type <typ> type temporal_type generic_precision generic_modifier_atom generic_modifiers type_modifier_head date_literal_precision
%type <typ> declared_type declared_type_base declaration_type_base declaration_invalid_type declared_array_bounds
%type <typ> routine_argument_type
%type <tok> bit_modifier_operator bit_modifier_symbol
%type <typ> interval_type interval_fields interval_second_fields interval_type_qualifier
%type <constraints> nullability column_constraints domain_constraints domain_column_constraints
%type <constraint> column_constraint
%type <notNull> table_not_null create_not_null
%type <tok> nn_modifier nn_inherit
%type <tok> column_suffix
%type <tok> name non_between_name setting_word setting_word_without_between role_name role_option_word type_name bare_label col_label generic_word generic_word_without_exists generic_word_base suffix nulls_keyword nulls_position
%type <tok> generic_statement_word generic_word_base_without_vacuum
%type <tok> generic_create_word
%type <tok> predicate_suffix where_predicate_suffix
%type <tok> role_selection_target role_selection_assignment
%type <tok> cast_function
%type <expr> string_function
%type <expr> check_function_call
%type <tok> string_word normalization_form normalized_form trim_direction
%type <row> trim_arguments trim_list
%type <settingName> setting_name set_name local_setting_name
%type <zoneSet> zone_setting
%type <integer> interval_precision
%type <tok> interval_qualifier interval_field
%type <typ> qualified_call_head
%type <targets> insert_targets
%type <target> insert_target
%type <subscripts> array_subscripts
%type <subscript> array_subscript
%type <steps> target_steps
%type <step> target_step
%type <expr> field_value relation_star
%type <expr> array_bracket array_member column_reference
%type <row> array_members array_member_list
%type <objectName> relation_name
%type <fromItem> from_list joined_table from_atom from_base update_from delete_using
%type <functionTable> function_table function_table_base
%type <call> function_table_call rows_function_call rows_function_base
%type <functionTables> function_table_calls
%type <identifiers> alias_columns using_columns
%type <functionColumnDefinitions> function_column_definition_tail
%type <identifier> using_alias
%type <joinKind> inner_join
%type <desc> optional_outer
%type <alterTarget> alter_target rename_target
%type <lockTargets> lock_targets
%type <lockMode> lock_mode optional_lock_mode
%type <desc> optional_lock_nowait
%type <desc> optional_column
%type <dropTarget> drop_target
%type <tok> alter_action
%type <names> names
%type <row> values call_args function_values sequence_args sequence_values
%type <groupClause> group
%type <groupItems> group_keys producer_units
%type <groupItem> group_item
%type <expr> having
%type <value> value
%type <expr> scalar scalar_atom operand target_base target_between target_between_predicate target_comparison target default_target where target_not target_and target_or projection_target function function_argument
%type <row> statistics_elements
%type <expr> statistics_element
%type <expr> create_default_target
%type <groupedPrimary> grouped_primary
%type <expr> aggregate_call aggregate_within aggregate_application
%type <tok> json_arrayagg_head json_objectagg_head
%type <expr> json_object_key
%type <jsonFormat> json_format
%type <jsonOptions> json_null_policy json_object_null_policy json_unique_policy json_returning
%type <typ> json_return_type
%type <tok> sqljson_array_head sqljson_object_head sqljson_parse_head sqljson_scalar_head sqljson_serialize_head json_predicate_kind json_keys
%type <desc> json_unique
%type <sqljson> sqljson_members sqljson_member sqljson_values sqljson_value sqljson_optional_values
%type <expr> sqljson_constructor
%type <expr> sqljson_query
%type <tok> sqljson_query_head sqljson_wrapper sqljson_quotes sqljson_wrapper_kind sqljson_scalar_string
%type <sqljsonPassing> sqljson_passing sqljson_bindings
%type <sqljsonBehavior> sqljson_behavior
%type <sqljsonQuery> sqljson_behaviors
%type <jsonTable> json_table
%type <jsonTableColumns> json_table_columns json_table_column_list
%type <jsonTableColumn> json_table_column
%type <tok> json_table_head json_columns_head
%type <identifier> json_path_name
%type <expr> json_column_path
%type <tok> like_operator
%type <expr> target_escape
%type <expr> conditional conditional_call case_test case_else conditional_value
%type <caseArms> case_arms
%type <caseArm> case_arm
%type <row> conditional_args count_values sqljson_legacy_values
%type <tok> conditional_word
%type <tok> comparison_operator
%type <call> function_args function_argument_list ordinary_named_arguments ordinary_mixed_arguments
%type <call> transaction_time_head transaction_time_args transaction_time_base transaction_time_filtered transaction_time_call
%type <predicate> truth_kind
%type <selectStmt> projection projection_item
%type <tok> insert_head on_keyword
%type <insertSource> insert_input
%type <selectStmt> insert_select
%type <order> order
%type <bounds> bounds nonempty_bounds limit_clause offset_clause
%type <tail> query_tail
%type <expr> limit_value fetch_count
%type <tok> fetch_direction count_rows
%type <desc> optional_count_rows
%type <locking> locking_clause
%type <locking> lock_strength
%type <lockings> locking_list
%type <objectNames> lock_of row_lock_names
%type <objectName> row_lock_target
%type <lockWait> lock_wait
%type <orderKeys> order_keys aggregate_order
%type <orderKey> order_key
%type <nulls> nulls
%type <lockings> locking
%type <desc> direction
%type <settingValues> setting_values
%type <settingValue> setting_value
%type <desc> role_kind
%type <roleOptions> role_options
%type <integer> role_limit
%type <tok> alter_role_target
%type <stmt> role_setting_action
%type <tok> role_alias current_setting_word
%type <identifier> role_setting_database
%type <tables> schema_tables
%type <table> schema_table
%type <tok> schema_owner
%type <desc> drop_behavior
%type <stmt> comment
%type <objectName> comment_column_name
%type <value> comment_text
%type <tok> comment_excluded_kind comment_excluded_word
%type <tok> transaction_start commit_word rollback_start characteristics_word
%type <modes> transaction_modes
%type <mode> transaction_mode
%type <isolation> isolation_mode
%type <desc> opt_chain
%type <tok> transaction_name
%type <stmt> create_sequence alter_sequence drop_sequence
%type <sequence> sequence_create_head
%type <alterSequence> sequence_alter_head
%type <sequenceOptions> sequence_options
%type <sequenceOption> sequence_option
%type <integer> sequence_integer
%type <persistence> sequence_persistence
%type <onCommit> table_on_commit
%type <tok> table_preserve table_rows
%type <tok> sequence_name_word
%type <identity> identity_definition identity_alter_options
%type <identityMode> identity_mode
%type <generation> generation_definition
%type <tok> generation_kind generation_expression_word
%type <sequenceOptions> identity_options identity_option_list
%type <sequenceOption> identity_option identity_set_option
%type <override> insert_override
%type <stmt> merge
%type <stmt> cte_body with_mutation with_rule_mutation
%type <mergeArms> merge_arms
%type <mergeArm> merge_arm merge_insert
%type <expr> merge_condition
%type <tok> merge_target_class
%type <stmt> role_membership
%type <stmt> alter_default_privileges
%type <defaultPrivileges> default_privileges_action
%type <defaultOptions> default_privileges_options
%type <defaultOption> default_privileges_option
%type <tok> default_privileges_class
%type <stmt> alter_owner reassign_owned drop_owned
%type <tok> ownership_name_word
%type <roleSpec> membership_role_spec membership_role_name
%type <roleSpecs> membership_role_specs membership_role_names
%type <roleSpecs> tablespace_owners
%type <membershipOptions> membership_options membership_option_list
%type <membershipOption> membership_option
%type <grantor> membership_grantor
%type <tok> membership_revoke_option
%type <tok> membership_keyword
%type <desc> membership_option_value
%type <grantPrivileges> grant_privileges
%type <grantPrivilege> grant_privilege
%type <tok> grant_privilege_word
%type <identifiers> grant_columns grant_column_list grant_schema_list
%type <grantGrantee> grant_grantee grant_role_spec
%type <grantGrantee> foreign_role foreign_alter_role
%type <grantGrantees> grant_grantees
%type <objectGrant> grant_targets
%type <desc> grant_option
%type <objectNames> grant_relations
%type <objectNames> grant_simple_names grant_parameter_names
%type <objectNames> grant_large_objects
%type <tok> grant_object_number
%type <grantRoutines> grant_routines
%type <grantRoutine> grant_routine
%type <grantRoutineArgument> grant_routine_argument
%type <grantRoutineArguments> grant_routine_arguments grant_routine_argument_list
%type <grantRoutines> aggregate_utility_targets
%type <grantRoutine> aggregate_utility_target
%type <grantRoutineArguments> aggregate_utility_arguments
%type <tok> grant_routine_kind grant_routine_mode grant_parameter_name routine_name_word
%type <tok> grant_external_name_word
%type <typ> standalone_type
%type <stmt> prepare_sql execute_sql deallocate_sql preparable discard
%type <stmt> notify listen unlisten load
%type <stmt> alter_system
%type <types> prepare_types prepare_type_list
%type <row> execute_arguments execute_argument_list
%%
script: separators optional_statements
 | TYPE_INPUT standalone_type { sqllex.(*lexer).typeInputResult = standaloneType($2) }
 | TYPE_INPUT SETOF_TYPE standalone_type { typ:=standaloneType($3);typ.Setof=true;sqllex.(*lexer).typeInputResult=typ }
 | PL_ENTRY pl_block pl_final_semicolon { sqllex.(*lexer).plResult = $2 }
 | PL_TARGETS_ENTRY pl_cursor_targets { sqllex.(*lexer).plTargetsResult = $2 } ;
standalone_type: type { $$=$1 }
 | standalone_type '.' col_label { $$=appendStandalonePart($1,$3) }
 | standalone_type '(' { if len($1.Parts)<3 {sqllex.(*lexer).syntax($2);return 1};sqllex.(*lexer).characterGeneric=true;sqllex.(*lexer).genericModifier=true;sqllex.(*lexer).multipleModifiers=true;sqllex.(*lexer).bitModifier=true } generic_modifiers ')' { $$=$1;$$.Modifiers=$4.Modifiers }
 | standalone_type '[' ']' { $$=$1;$$.ArrayBounds=append($$.ArrayBounds,0) }
 | standalone_type '[' NUMBER ']' { n,ok:=sequenceInteger(sqllex,$3,"");if !ok{return 1};$$=$1;$$.ArrayBounds=append($$.ArrayBounds,int32(n)) }
 | standalone_type ARRAY { $$=$1;$$.ArrayBounds=append($$.ArrayBounds,0) }
 | standalone_type ARRAY '[' NUMBER ']' { n,ok:=sequenceInteger(sqllex,$4,"");if !ok{return 1};$$=$1;$$.ArrayBounds=append($$.ArrayBounds,int32(n)) } ;
separators: /* empty */ | separators ';' ;
/* Left recursion (PostgreSQL's stmtmulti) bounds the parse stack by one statement
   instead of growing three cells per statement until the end of the script. */
optional_statements: /* empty */ | statement_list | statement_list ';' separators ;
statement_list: accepted | statement_list ';' separators accepted ;
accepted: statement { sqllex.(*lexer).insertStatement=false; if $1 != nil { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } } ;
statement: nontransaction_statement | transaction_control ;
nontransaction_statement: embedded_sql_statement | select | declare_cursor | fetch_cursor | close_cursor | execute_sql
 | explain
 | generic_statement_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | DROP %prec NAME_LIST { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 } ;
embedded_sql_statement: create | create_sequence | alter_sequence | drop_sequence | alter_index | drop_index | reindex | alter_view | drop_view | drop_table | alter_drop_column | insert | merge | update | delete | set | show | reset | create_role | alter_role | drop_role | session_authorization | role_selection | role_membership | lock_table | create_schema | alter_schema | drop_schema | comment
 | with_mutation
 | do_statement
 | CALL relation_name '(' function_args ')' {call:=$4;call.Name=$2;call.Pos=$2.Parts[0].Pos;call.ProcedureCall=true;$$=&Call{Function:call,Pos:$1.pos}}
 | vacuum
 | analyze
 | truncate_table
 | alter_default_privileges
 | alter_owner | reassign_owned | drop_owned
 | copy
 | INSERT_SCALAR_VALUES insert_scalar_rows { $$=&Insert{Rows:$2} }
 | drop_domain
 | alter_domain
 | set_constraints
 | prepare_sql | deallocate_sql | discard
 | create_extension | drop_extension | alter_extension_update
 | create_tablespace
 | create_database
 | alter_collation | drop_collation | alter_enum
 | alter_conversion | drop_conversion
 | drop_statistics
 | alter_dictionary | drop_dictionary
 | alter_configuration | drop_configuration
 | alter_operator | drop_operator
 | drop_routine | alter_routine
 | create_cast | drop_cast
 | alter_operator_family | drop_operator_family
 | alter_operator_class | drop_operator_class
 | drop_access_method
 | refresh_materialized_view
 | create_policy | alter_policy | drop_policy
 | create_rule | alter_rule | drop_rule
 | create_trigger | alter_trigger | drop_trigger
 | create_event_trigger | alter_event_trigger | drop_event_trigger
 | alter_composite | drop_type | alter_table_of
 | foreign_statement
 | notify | listen | unlisten | load
 | alter_system
 ;
explain: EXPLAIN explainable { $$=&Explain{Statement:$2,Pos:$1.pos} }
 | EXPLAIN explain_legacy explainable { $$=&Explain{Statement:$3,Options:$2,Pos:$1.pos} }
 | EXPLAIN '(' explain_option_list ')' explainable { $$=&Explain{Statement:$5,Options:$3,Pos:$1.pos} } ;
explainable: select | insert | update | delete | merge | with_mutation
 | declare_cursor | execute_sql | create_query_table ;
explain_legacy: ANALYZE { $$=[]ExplainOption{{Name:"analyze",Pos:$1.pos}} }
 | VERBOSE { $$=[]ExplainOption{{Name:"verbose",Pos:$1.pos}} }
 | ANALYZE VERBOSE { $$=[]ExplainOption{{Name:"analyze",Pos:$1.pos},{Name:"verbose",Pos:$2.pos}} } ;
explain_option_list: explain_option { $$=[]ExplainOption{$1} }
 | explain_option_list ',' explain_option { $$=append($1,$3) } ;
explain_option: explain_option_name explain_optional_value { $$=ExplainOption{Name:$1.text,Value:$2,Pos:$1.pos};if $1.keyword!=nil && $1.keyword.token==ANALYZE {$$.Name="analyze"} } ;
explain_optional_value: /* empty */ { $$=nil } | explain_option_value ;
explain_option_name: explain_word | ANALYZE ;
explain_word: name | KW_T | AUTHORIZATION | COLLATION | CONCURRENTLY
 | CROSS | CURRENT_SCHEMA | FREEZE | FULL | ILIKE | INNER | IS | ISNULL
 | JOIN | LEFT | LIKE | NATURAL | NOTNULL | OUTER | OVERLAPS | RIGHT
 | SIMILAR | VERBOSE ;
explain_option_value: explain_word { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}}} }
 | ON { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}}} }
 | STRING { $$=LiteralExpr{Value:Value{Kind:StringValue,Text:$1.text,Pos:$1.pos}} }
 | NUMBER { $$=LiteralExpr{Value:numberValue($1,"")} }
 | '+' NUMBER { v:=numberValue($2,"");v.Pos=$1.pos;$$=LiteralExpr{Value:v} }
 | '-' NUMBER { v:=numberValue($2,"-");v.Pos=$1.pos;$$=LiteralExpr{Value:v} }
 | TRUE { $$=LiteralExpr{Value:Value{Kind:BooleanValue,Boolean:true,Pos:$1.pos}} }
 | FALSE { $$=LiteralExpr{Value:Value{Kind:BooleanValue,Pos:$1.pos}} } ;
load: LOAD STRING { $$=&Load{Filename:$2.text,Pos:$1.pos} } ;
create_database: CREATE DATABASE name database_options {
 $$=&CreateDatabaseStmt{Name:$3.text,Options:$4}
} ;
database_options: database_option_list {$$=$1}
 | WITH database_option_list {$$=$2} ;
database_option_list: /* empty */ {$$=nil}
 | database_option_list database_option {$$=append($1,$2)} ;
database_option_name: name {$$=$1}
 | name LIMIT {if $1.text!="connection" || $1.keyword==nil {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.text="connection_limit"} ;
database_option_equals: /* empty */ {$$=false}
 | '=' {$$=true} ;
database_option: database_option_name database_option_equals database_option_value {
 $$=$3;$$.Name=$1.text;$$.Position=$1.pos.Character
} ;
database_option_value: name {$$=DatabaseOption{Value:$1.text}}
 | STRING {$$=DatabaseOption{Value:$1.text}}
 | DEFAULT {$$=DatabaseOption{Default:true}}
 | TRUE {$$=DatabaseOption{Value:$1.text}}
 | FALSE {$$=DatabaseOption{Value:$1.text}}
 | NUMBER {$$=DatabaseOption{Value:$1.text,Numeric:true}}
 | '-' NUMBER {$$=DatabaseOption{Value:"-"+$2.text,Numeric:true}}
 | '+' NUMBER {$$=DatabaseOption{Value:"+"+$2.text,Numeric:true}} ;
create_tablespace: CREATE TABLESPACE name tablespace_owner tablespace_location STRING tablespace_options {
 $$=&CreateTablespace{Name:Identifier{Text:$3.text,Pos:$3.pos},Owner:$4,Location:$6.text,LocationPos:$6.pos,Options:$7,Pos:$1.pos}
} ;
tablespace_owner: /* empty */ {$$=nil}
 | OWNER membership_role_spec {owner:=$2;$$=&owner} ;
tablespace_location: KW_U {if $1.text!="location" {sqllex.(*lexer).syntax($1);return 1}} ;
tablespace_options: /* empty */ {$$=nil}
 | WITH '(' maintenance_index_option_list ')' {$$=$3} ;
trigger_head: CREATE TRIGGER name trigger_timing trigger_events ON relation_name {
 $$=&CreateTrigger{Name:Identifier{Text:$3.text,Pos:$3.pos},Timing:$4.text,Events:$5,Table:$7,Pos:$1.pos}
} | CREATE OR replace_word TRIGGER name trigger_timing trigger_events ON relation_name {
 $$=&CreateTrigger{Name:Identifier{Text:$5.text,Pos:$5.pos},Timing:$6.text,Events:$7,Table:$9,Replace:true,Pos:$1.pos}
} ;
create_trigger: trigger_head trigger_row trigger_when EXECUTE trigger_function relation_name '(' trigger_arguments ')' {
 $1.Row=$2;$1.When=$3;$1.Function=$6;$1.Arguments=$8;$$=$1
} ;
trigger_timing: name {
 if $1.keyword==nil || ($1.text!="before" && $1.text!="after") {sqllex.(*lexer).syntax($1);return 1};$$=$1
} | INSTEAD OF {$$=$1} ;
trigger_events: trigger_event {$$=[]TriggerEvent{$1}}
 | trigger_events OR trigger_event {$$=append($1,$3)} ;
trigger_event: INSERT {$$=TriggerEvent{Name:"insert"}}
 | UPDATE {$$=TriggerEvent{Name:"update"}}
 | UPDATE OF view_names {$$=TriggerEvent{Name:"update",Columns:$3}}
 | DELETE {$$=TriggerEvent{Name:"delete"}}
 | TRUNCATE {$$=TriggerEvent{Name:"truncate"}} ;
trigger_row: /* empty */ {$$=false}
 | FOR ROW {$$=true}
 | FOR trigger_each ROW {$$=true}
 | FOR trigger_statement {$$=false}
 | FOR trigger_each trigger_statement {$$=false} ;
trigger_each: KW_U {if $1.text!="each" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
trigger_statement: KW_U {if $1.text!="statement" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
trigger_when: /* empty */ {$$=nil} | WHEN '(' target_or ')' {$$=$3} ;
trigger_function: FUNCTION | PROCEDURE ;
trigger_arguments: /* empty */ {$$=nil} | trigger_argument_list ;
trigger_argument_list: trigger_argument {$$=[]string{$1.text}}
 | trigger_argument_list ',' trigger_argument {$$=append($1,$3.text)} ;
trigger_argument: name | STRING | NUMBER ;
alter_trigger: ALTER TRIGGER name ON relation_name RENAME TO name {
 $$=&AlterTrigger{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Action:"rename",Target:Identifier{Text:$8.text,Pos:$8.pos},Pos:$1.pos}
} | ALTER TRIGGER name ON relation_name depends_word ON EXTENSION name {
 $$=&AlterTrigger{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Action:"depends",Target:Identifier{Text:$9.text,Pos:$9.pos},Pos:$1.pos}
} | ALTER TRIGGER name ON relation_name NO depends_word ON EXTENSION name {
 $$=&AlterTrigger{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Action:"no_depends",Target:Identifier{Text:$10.text,Pos:$10.pos},Pos:$1.pos}
} ;
drop_trigger: DROP TRIGGER name ON relation_name drop_mode {
 $$=&DropTrigger{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Mode:$6,Pos:$1.pos}
} | DROP TRIGGER IF EXISTS name ON relation_name drop_mode {
 $$=&DropTrigger{Name:Identifier{Text:$5.text,Pos:$5.pos},Table:$7,IfExists:true,Mode:$8,Pos:$1.pos}
} ;
create_event_trigger: CREATE EVENT TRIGGER name ON name event_filters EXECUTE trigger_function relation_name '(' ')' {
 $$=&CreateEventTrigger{Name:Identifier{Text:$4.text,Pos:$4.pos},Event:Identifier{Text:$6.text,Pos:$6.pos},Filters:$7,Function:$10,Pos:$1.pos}
} ;
event_filters: /* empty */ {$$=nil} | WHEN event_filter_clauses {$$=$2} ;
event_filter_clauses: event_filter {$$=[]EventTriggerFilter{$1}}
 | event_filter_clauses AND event_filter {$$=append($1,$3)} ;
event_filter: name IN '(' event_tag_list ')' {$$=EventTriggerFilter{Name:Identifier{Text:$1.text,Pos:$1.pos},Values:$4}} ;
event_tag_list: STRING {$$=[]string{$1.text}}
 | event_tag_list ',' STRING {$$=append($1,$3.text)} ;
alter_event_trigger: ALTER EVENT TRIGGER name name {
 if $5.keyword==nil || ($5.text!="enable" && $5.text!="disable") {sqllex.(*lexer).syntax($5);return 1}
 $$=&AlterEventTrigger{Name:Identifier{Text:$4.text,Pos:$4.pos},Enabled:$5.text=="enable",Pos:$1.pos}
} ;
drop_event_trigger: DROP EVENT TRIGGER name drop_mode {$$=&DropEventTrigger{Name:Identifier{Text:$4.text,Pos:$4.pos},Mode:$5,Pos:$1.pos}}
 | DROP EVENT TRIGGER IF EXISTS name drop_mode {$$=&DropEventTrigger{Name:Identifier{Text:$6.text,Pos:$6.pos},IfExists:true,Mode:$7,Pos:$1.pos}} ;
rule_head: CREATE RULE name AS ON rule_event TO relation_name {
 sqllex.(*lexer).ruleDefinition=true
 $$=&CreateRule{Name:Identifier{Text:$3.text,Pos:$3.pos},Event:$6.text,Table:$8,Pos:$1.pos}
} | CREATE OR replace_word RULE name AS ON rule_event TO relation_name {
 sqllex.(*lexer).ruleDefinition=true
 $$=&CreateRule{Name:Identifier{Text:$5.text,Pos:$5.pos},Event:$8.text,Table:$10,Replace:true,Pos:$1.pos}
} ;
create_rule: rule_head rule_condition DO rule_instead rule_actions {
 $1.Condition=$2;$1.Instead=$4;$1.Actions=$5;$$=$1
 sqllex.(*lexer).ruleDefinition=false
} ;
rule_event: SELECT | INSERT | UPDATE | DELETE ;
rule_condition: /* empty */ {$$=nil} | WHERE target_or {$$=$2} ;
rule_instead: /* empty */ {$$=false} | ALSO {$$=false} | INSTEAD {$$=true} ;
rule_actions: NOTHING {$$=nil} | rule_action {$$=[]Statement{$1}}
 | '(' rule_action_list ')' {$$=$2} | '(' rule_action_list ';' ')' {$$=$2} ;
rule_action_list: rule_action {$$=[]Statement{$1}} | rule_action_list ';' rule_action {$$=append($1,$3)} ;
rule_action: select | insert | update | delete | with_rule_mutation | notify ;
notify: NOTIFY name {$$=&Notify{Channel:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos}}
 | NOTIFY name ',' STRING {$$=&Notify{Channel:Identifier{Text:$2.text,Pos:$2.pos},Payload:$4.text,Pos:$1.pos}} ;
listen: LISTEN name {$$=&Listen{Channel:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos}} ;
unlisten: UNLISTEN name {$$=&Unlisten{Channel:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos}}
 | UNLISTEN '*' {$$=&Unlisten{All:true,Pos:$1.pos}} ;
alter_rule: ALTER RULE name ON relation_name RENAME TO name {
 $$=&AlterRule{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Rename:Identifier{Text:$8.text,Pos:$8.pos},Pos:$1.pos}
} ;
drop_rule: DROP RULE name ON relation_name drop_mode {
 $$=&DropRule{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Mode:$6,Pos:$1.pos}
} | DROP RULE IF EXISTS name ON relation_name drop_mode {
 $$=&DropRule{Name:Identifier{Text:$5.text,Pos:$5.pos},Table:$7,IfExists:true,Mode:$8,Pos:$1.pos}
} ;
create_policy: CREATE POLICY name ON relation_name policy_permissive policy_command policy_roles policy_using policy_check {
 $$=&CreatePolicy{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Permissive:$6,Command:$7.text,Roles:$8,Using:$9,Check:$10,Pos:$1.pos}
} ;
policy_option: name ;
policy_permissive: /* empty */ {$$=true}
 | AS policy_option {
 if $2.text!="permissive" && $2.text!="restrictive" {
 sqllex.(*lexer).policyOptionError($2)
 return 1
 };$$=$2.text=="permissive"
} ;
policy_command: /* empty */ {$$=lexeme{text:"all"}}
 | FOR ALL {$$=$2} | FOR SELECT {$$=$2} | FOR INSERT {$$=$2}
 | FOR UPDATE {$$=$2} | FOR DELETE {$$=$2} ;
policy_roles: /* empty */ {$$=nil} | TO grant_grantees {
 for _,role:=range $2 {if role.Role.Name=="none" {sqllex.(*lexer).failAt("42939","role name \"none\" is reserved",role.Role.Pos.Offset);return 1}}
 $$=$2
} ;
policy_using: /* empty */ {$$=nil}
 | USING '(' {sqllex.(*lexer).policyExpression=true} target_or ')' {sqllex.(*lexer).policyExpression=false;$$=$4} ;
policy_check: /* empty */ {$$=nil}
 | WITH CHECK '(' {sqllex.(*lexer).policyExpression=true} target_or ')' {sqllex.(*lexer).policyExpression=false;$$=$5} ;
alter_policy: ALTER POLICY name ON relation_name policy_roles policy_using policy_check {
 $$=&AlterPolicy{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Roles:$6,Using:$7,Check:$8,Pos:$1.pos}
} | ALTER POLICY name ON relation_name RENAME TO name {
 target:=Identifier{Text:$8.text,Pos:$8.pos};$$=&AlterPolicy{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Rename:&target,Pos:$1.pos}
} | ALTER POLICY IF EXISTS name ON relation_name RENAME TO name {
 target:=Identifier{Text:$10.text,Pos:$10.pos};$$=&AlterPolicy{Name:Identifier{Text:$5.text,Pos:$5.pos},Table:$7,Rename:&target,IfExists:true,Pos:$1.pos}
} ;
drop_policy: DROP POLICY name ON relation_name drop_mode {
 $$=&DropPolicy{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Mode:$6,Pos:$1.pos}
} | DROP POLICY IF EXISTS name ON relation_name drop_mode {
 $$=&DropPolicy{Name:Identifier{Text:$5.text,Pos:$5.pos},Table:$7,IfExists:true,Mode:$8,Pos:$1.pos}
} ;
copy: COPY copy_target copy_direction copy_endpoint copy_options copy_where {
 $$=$2;$2.Pos=$1.pos;$2.From=$3.text=="from";$2.Endpoint=$4.Endpoint;$2.Filename=$4.Filename;$2.Options=$5;$2.Where=$6.expression
 if !$2.From && $6.expression!=nil {sqllex.(*lexer).failAt("42601","WHERE clause not allowed with COPY TO",$6.pos.Offset);return 1}
 if $2.Query!=nil && $2.From {sqllex.(*lexer).syntax($3);return 1}
 if ($2.From && $2.Endpoint==CopyStdout)||(!$2.From && $2.Endpoint==CopyStdin) {sqllex.(*lexer).syntax($1);return 1}
} ;
copy_target: relation_name view_columns { $$=&Copy{Name:$1,Columns:$2} }
 | '(' query_select ')' { $$=&Copy{Query:$2} } ;
copy_direction: FROM { $$=$1 } | TO { $$=$1 } ;
copy_endpoint: generic_word {
 switch $1.text {case "stdin":$$=&Copy{Endpoint:CopyStdin};case "stdout":$$=&Copy{Endpoint:CopyStdout};default:sqllex.(*lexer).syntax($1);return 1}
 }
 | STRING { $$=&Copy{Endpoint:CopyFile,Filename:$1.text} }
 | generic_word STRING {if $1.text!="program" {sqllex.(*lexer).syntax($1);return 1};$$=&Copy{Endpoint:CopyProgram,Filename:$2.text}} ;
copy_options: /* empty */ { $$=nil }
 | WITH '(' copy_option_list ')' { $$=$3 }
 | '(' copy_option_list ')' { $$=$2 }
 | copy_legacy_options { $$=$1 }
 | WITH copy_legacy_options { $$=$2 } ;
copy_option_list: copy_option { $$=[]CopyOption{*$1} }
 | copy_option_list ',' copy_option { $$=append($1,*$3) } ;
copy_option_name: generic_word | NULL | DEFAULT ;
copy_option: copy_option_name { $$=&CopyOption{Name:$1.text,Pos:$1.pos} }
 | copy_option_name copy_option_value { $$=$2;$$.Name=$1.text;$$.Pos=$1.pos} ;
copy_option_string: STRING | generic_word | TEXT | TRUE | FALSE ;
copy_option_strings: copy_option_string { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} }
 | copy_option_strings ',' copy_option_string { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
copy_option_value: copy_option_string { $$=&CopyOption{Argument:CopyArgumentScalar,HasValue:true,Value:$1.text} }
 | NUMBER { $$=&CopyOption{Argument:CopyArgumentScalar,HasValue:true,Value:$1.text,Numeric:true} }
 | '+' NUMBER { $$=&CopyOption{Argument:CopyArgumentScalar,HasValue:true,Value:"+"+$2.text,Numeric:true} }
 | '-' NUMBER { $$=&CopyOption{Argument:CopyArgumentScalar,HasValue:true,Value:"-"+$2.text,Numeric:true} }
 | DEFAULT { $$=&CopyOption{Argument:CopyArgumentScalar,HasValue:true,Value:"default"} }
 | '*' { $$=&CopyOption{Argument:CopyArgumentStar,HasValue:true,Value:"*"} }
 | '(' copy_option_strings ')' { $$=&CopyOption{Argument:CopyArgumentColumns,HasValue:true,Columns:$2} } ;
copy_legacy_options: copy_legacy_option { $$=[]CopyOption{*$1} }
 | copy_legacy_options copy_legacy_option { $$=append($1,*$2) } ;
copy_legacy_option: generic_word STRING { $$=&CopyOption{Argument:CopyArgumentScalar,Name:$1.text,Pos:$1.pos,HasValue:true,Value:$2.text} }
 | NULL STRING { $$=&CopyOption{Argument:CopyArgumentScalar,Name:"null",Pos:$1.pos,HasValue:true,Value:$2.text} }
 | generic_word {if $1.text!="header" {sqllex.(*lexer).syntax($1);return 1};$$=&CopyOption{Name:"header",Pos:$1.pos}} ;
copy_where: /* empty */ { $$=copyWhereClause{} } | WHERE target_or { $$=copyWhereClause{expression:$2,pos:$1.pos} } ;
prepare_sql: PREPARE name prepare_types AS preparable { $$=&PrepareSQL{Name:Identifier{Text:$2.text,Pos:$2.pos},Types:$3,Body:$5,Source:sqllex.(*lexer).input,Pos:$1.pos} } ;
prepare_types: /* empty */ { $$=nil } | '(' prepare_type_list ')' { $$=$2 } ;
prepare_type_list: type { $$=[]TypeName{$1} } | prepare_type_list ',' type { $$=append($1,$3) } ;
preparable: select | insert | update | delete | merge ;
execute_sql: EXECUTE name execute_arguments { $$=&ExecuteSQL{Name:Identifier{Text:$2.text,Pos:$2.pos},Arguments:$3,Pos:$1.pos} } ;
execute_arguments: /* empty */ { $$=nil } | '(' execute_argument_list ')' { $$=$2 } ;
execute_argument_list: scalar_projection { $$=[]Expr{$1} } | execute_argument_list ',' scalar_projection { $$=append($1,$3) } ;
deallocate_sql: DEALLOCATE name { $$=&DeallocateSQL{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | DEALLOCATE ALL { $$=&DeallocateSQL{All:true,Pos:$1.pos} }
 | DEALLOCATE PREPARE name { $$=&DeallocateSQL{Name:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | DEALLOCATE PREPARE ALL { $$=&DeallocateSQL{All:true,Pos:$1.pos} } ;
discard: DISCARD ALL { $$=&Discard{Category:DiscardAll,Pos:$1.pos} }
 | DISCARD TEMP { $$=&Discard{Category:DiscardTemp,Pos:$1.pos} }
 | DISCARD KW_U {
   category:=DiscardPlans
   if $2.keyword==nil {sqllex.(*lexer).syntax($2);return 1}
   switch $2.text {case "plans":case "sequences":category=DiscardSequences;default:sqllex.(*lexer).syntax($2);return 1}
   $$=&Discard{Category:category,Pos:$1.pos}
 } ;
declare_cursor: DECLARE name cursor_scroll CURSOR cursor_hold FOR query_select { $$=&DeclareCursor{Name:Identifier{Text:$2.text,Pos:$2.pos},Scroll:CursorScroll($3),Query:$7,Hold:$5,Source:sqllex.(*lexer).input,Pos:$1.pos} } ;
cursor_scroll: /* empty */ { $$=int64(CursorScrollDefault) }
 | NO SCROLL { $$=int64(CursorNoScroll) }
 | SCROLL { $$=int64(CursorScrollEnabled) } ;
cursor_hold: /* empty */ { $$=false }
 | WITHOUT HOLD { $$=false }
 | WITH HOLD { $$=true } ;
cursor_from: FROM | IN ;
cursor_command: FETCH | MOVE ;
fetch_cursor: cursor_command cursor_direction cursor_from name { $2.Name=Identifier{Text:$4.text,Pos:$4.pos};$2.Pos=$1.pos;$2.Move=$1.text=="move";$$=$2 }
 | cursor_command cursor_direction name { $2.Name=Identifier{Text:$3.text,Pos:$3.pos};$2.Pos=$1.pos;$2.Move=$1.text=="move";$$=$2 } ;
cursor_integer: NUMBER { n,ok:=cursorInteger(sqllex,$1,"");if !ok{return 1};$$=n }
 | '+' NUMBER { n,ok:=cursorInteger(sqllex,$2,"+");if !ok{return 1};$$=n }
 | '-' NUMBER { n,ok:=cursorInteger(sqllex,$2,"-");if !ok{return 1};$$=n } ;
cursor_direction: /* empty */ { $$=&FetchCursor{Count:1} }
 | cursor_integer { $$=&FetchCursor{Count:$1} }
 | ALL { $$=&FetchCursor{All:true} }
 | FORWARD { $$=&FetchCursor{Count:1} }
 | FORWARD cursor_integer { $$=&FetchCursor{Count:$2} }
 | FORWARD ALL { $$=&FetchCursor{All:true} }
 | BACKWARD { $$=&FetchCursor{Count:1,Direction:CursorBackward} }
 | BACKWARD cursor_integer { $$=&FetchCursor{Count:$2,Direction:CursorBackward} }
 | BACKWARD ALL { $$=&FetchCursor{All:true,Direction:CursorBackward} }
 | ABSOLUTE cursor_integer { $$=&FetchCursor{Count:$2,Direction:CursorAbsolute} }
 | RELATIVE cursor_integer { $$=&FetchCursor{Count:$2,Direction:CursorRelative} }
 | FIRST { $$=&FetchCursor{Count:1,Direction:CursorAbsolute} }
 | KW_U { direction,count:=CursorForward,int64(1);switch $1.text {case "next":case "prior":direction=CursorBackward;case "last":direction=CursorAbsolute;count=-1;default:sqllex.(*lexer).syntax($1);return 1};$$=&FetchCursor{Count:count,Direction:direction} } ;
close_cursor: CLOSE name { $$=&CloseCursor{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | CLOSE ALL { $$=&CloseCursor{All:true,Pos:$1.pos} } ;
cursor_word: DECLARE | CURSOR | CLOSE | SCROLL | HOLD | FORWARD | BACKWARD | ABSOLUTE | RELATIVE | MOVE ;
sequence_persistence: /* empty */ { $$="logged" } | TEMP { $$="temporary" } | UNLOGGED { $$="unlogged" } ;
sequence_create_head: CREATE sequence_persistence SEQUENCE relation_name { $$=&CreateSequence{Name:$4,Persistence:$2,Pos:$1.pos} }
 | CREATE sequence_persistence SEQUENCE IF NOT EXISTS relation_name { $$=&CreateSequence{Name:$7,Persistence:$2,IfNotExists:true,Pos:$1.pos} } ;
create_sequence: sequence_create_head sequence_options { $1.Options=$2; $$=$1 } ;
sequence_alter_head: ALTER SEQUENCE relation_name { $$=&AlterSequence{Name:$3,Pos:$1.pos} }
 | ALTER SEQUENCE IF EXISTS relation_name { $$=&AlterSequence{Name:$5,IfExists:true,Pos:$1.pos} } ;
alter_sequence: sequence_alter_head sequence_options { if len($2)==0{sqllex.(*lexer).failAt("42601","syntax error at end of input",len(sqllex.(*lexer).input));return 1};$1.Options=$2; $$=$1 }
 | sequence_alter_head RENAME TO name { $1.Action="rename"; $1.Target=Identifier{Text:$4.text,Pos:$4.pos}; $$=$1 }
 | sequence_alter_head OWNER TO schema_owner { $1.Action="owner"; $1.Target=Identifier{Text:$4.text,Pos:$4.pos}; $1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved; $$=$1 }
 | sequence_alter_head SET SCHEMA name { $1.Action="schema"; $1.Target=Identifier{Text:$4.text,Pos:$4.pos}; $$=$1 }
 | sequence_alter_head SET LOGGED { $1.Action="logged"; $$=$1 }
 | sequence_alter_head SET UNLOGGED { $1.Action="unlogged"; $$=$1 } ;
drop_sequence: DROP SEQUENCE relation_names drop_mode { $$=&DropSequence{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP SEQUENCE IF EXISTS relation_names drop_mode { $$=&DropSequence{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
sequence_options: /* empty */ { $$=nil } | sequence_options sequence_option { $$=append($1,*$2) } ;
sequence_integer: NUMBER { n,ok:=sequenceInteger(sqllex,$1,"");if !ok{return 1};$$=n }
 | '+' NUMBER { n,ok:=sequenceInteger(sqllex,$2,"+");if !ok{return 1};$$=n }
 | '-' NUMBER { n,ok:=sequenceInteger(sqllex,$2,"-");if !ok{return 1};$$=n } ;
sequence_option: AS type { $$=&SequenceOption{Name:"as",Type:$2,Pos:$1.pos} }
 | INCREMENT sequence_integer { $$=&SequenceOption{Name:"increment",Value:$2,Pos:$1.pos} }
 | INCREMENT BY sequence_integer { $$=&SequenceOption{Name:"increment",Value:$3,Pos:$1.pos} }
 | MINVALUE sequence_integer { $$=&SequenceOption{Name:"min",Value:$2,Pos:$1.pos} }
 | NO MINVALUE { $$=&SequenceOption{Name:"min",Type:TypeName{Name:"default"},Pos:$1.pos} }
 | MAXVALUE sequence_integer { $$=&SequenceOption{Name:"max",Value:$2,Pos:$1.pos} }
 | NO MAXVALUE { $$=&SequenceOption{Name:"max",Type:TypeName{Name:"default"},Pos:$1.pos} }
 | START sequence_integer { $$=&SequenceOption{Name:"start",Value:$2,Pos:$1.pos} }
 | START WITH sequence_integer { $$=&SequenceOption{Name:"start",Value:$3,Pos:$1.pos} }
 | CACHE sequence_integer { $$=&SequenceOption{Name:"cache",Value:$2,Pos:$1.pos} }
 | CYCLE { $$=&SequenceOption{Name:"cycle",Value:1,Pos:$1.pos} }
 | NO CYCLE { $$=&SequenceOption{Name:"cycle",Pos:$1.pos} }
 | RESTART { $$=&SequenceOption{Name:"restart",Type:TypeName{Name:"default"},Pos:$1.pos} }
 | RESTART sequence_integer { $$=&SequenceOption{Name:"restart",Value:$2,Pos:$1.pos} }
 | RESTART WITH sequence_integer { $$=&SequenceOption{Name:"restart",Value:$3,Pos:$1.pos} }
 | OWNED BY comment_column_name { $$=&SequenceOption{Name:"owned",Owned:$3,Pos:$1.pos} } ;
identity_mode: ALWAYS { $$=IdentityAlways } | BY DEFAULT { $$=IdentityByDefault } ;
identity_definition: GENERATED identity_mode AS IDENTITY identity_options { $$=&IdentityDefinition{Mode:$2,Options:$5,Pos:$1.pos} } ;
generation_definition: GENERATED identity_mode AS '(' scalar_projection ')' generation_kind { expression:=$5;if restriction:=sqllex.(*lexer).generationCompletedRestriction;restriction!=nil {expression=ForbiddenGenerationExpr{*restriction}};$$=&GenerationDefinition{Expression:expression,Mode:$2,Kind:$7.text,Pos:$1.pos,ModePos:sqllex.(*lexer).generationModePos} } ;
generation_kind: /* empty */ { $$=lexeme{} } | KW_U { if $1.text!="stored" && $1.text!="virtual" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
generation_expression_word: KW_U { if $1.text!="expression" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
identity_options: /* empty */ { $$=nil } | '(' identity_option_list ')' { $$=$2 } ;
identity_option_list: /* empty */ { $$=nil } | identity_option_list identity_option { $$=append($1,*$2) } ;
identity_option: sequence_option { $$=$1 } | SEQUENCE name relation_name { if $2.text!="name" {sqllex.(*lexer).syntax($2);return 1}; $$=&SequenceOption{Name:"name",Owned:$3,Pos:$1.pos} }
 | LOGGED { $$=&SequenceOption{Name:"logged",Pos:$1.pos} }
 | UNLOGGED { $$=&SequenceOption{Name:"unlogged",Pos:$1.pos} } ;
identity_set_option: sequence_option { $$=$1 }
 | LOGGED { $$=&SequenceOption{Name:"logged",Pos:$1.pos} }
 | UNLOGGED { $$=&SequenceOption{Name:"unlogged",Pos:$1.pos} } ;
identity_alter_options: SET GENERATED identity_mode { $$=&IdentityDefinition{Mode:$3} }
 | SET identity_set_option { if $2.Name=="as" || $2.Name=="owned" || $2.Name=="restart" {name:=$2.Name;if name=="owned"{name="owned_by"};sqllex.(*lexer).failAt("42601","sequence option \""+name+"\" not supported here",$2.Pos.Offset);return 1};$$=&IdentityDefinition{Options:[]SequenceOption{*$2}} }
 | RESTART { $$=&IdentityDefinition{Options:[]SequenceOption{{Name:"restart",Type:TypeName{Name:"default"},Pos:$1.pos}}} }
 | RESTART sequence_integer { $$=&IdentityDefinition{Options:[]SequenceOption{{Name:"restart",Value:$2,Pos:$1.pos}}} }
 | RESTART WITH sequence_integer { $$=&IdentityDefinition{Options:[]SequenceOption{{Name:"restart",Value:$3,Pos:$1.pos}}} }
 | identity_alter_options SET GENERATED identity_mode { $$=$1; if $$.Mode!=IdentityNone {$$.ConflictingModes=true};$$.Mode=$4 }
 | identity_alter_options SET identity_set_option { if $3.Name=="as" || $3.Name=="owned" || $3.Name=="restart" {name:=$3.Name;if name=="owned"{name="owned_by"};sqllex.(*lexer).failAt("42601","sequence option \""+name+"\" not supported here",$3.Pos.Offset);return 1};$$=$1;$$.Options=append($$.Options,*$3) }
 | identity_alter_options RESTART { $$=$1;$$.Options=append($$.Options,SequenceOption{Name:"restart",Type:TypeName{Name:"default"},Pos:$2.pos}) }
 | identity_alter_options RESTART sequence_integer { $$=$1;$$.Options=append($$.Options,SequenceOption{Name:"restart",Value:$3,Pos:$2.pos}) }
 | identity_alter_options RESTART WITH sequence_integer { $$=$1;$$.Options=append($$.Options,SequenceOption{Name:"restart",Value:$4,Pos:$2.pos}) } ;
comment: COMMENT ON TABLE relation_name IS comment_text { $$=&Comment{Kind:CommentTable,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON FOREIGN TABLE relation_name IS comment_text { $$=&Comment{Kind:CommentForeignTable,Name:$5,Text:$7.Text,Null:$7.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON COLUMN comment_column_name IS comment_text { $$=&Comment{Kind:CommentColumn,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON SCHEMA name IS comment_text { $$=&Comment{Kind:CommentSchema,Name:ObjectName{Parts:[]Identifier{{Text:$4.text,Pos:$4.pos}}},Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON ROLE name IS comment_text { $$=&Comment{Kind:CommentRole,Name:ObjectName{Parts:[]Identifier{{Text:$4.text,Pos:$4.pos}}},Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON SEQUENCE relation_name IS comment_text { $$=&Comment{Kind:CommentSequence,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON comment_excluded_kind relation_name IS comment_text { sqllex.(*lexer).deferUnsupported($3,"COMMENT object kind is unsupported"); $$=nil } ;
comment_column_name: name { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}} }
 | name '.' col_label { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}} }
 | name '.' col_label '.' col_label { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}} } ;
comment_text: STRING { $$=Value{Kind:StringValue,Text:$1.text,Pos:$1.pos} }
 | NULL { $$=Value{Kind:NullValue,Pos:$1.pos} } ;
comment_excluded_word: IDENT | KW_U | XML | KW_T | KW_R | UNIQUE | VIEW | INDEX | TYPE | DATABASE | EXTENSION ;
comment_excluded_kind: comment_excluded_word { if $1.keyword==nil || !unsupportedCommentKinds[$1.text] { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
transaction_control: transaction_common | commit_word opt_transaction opt_chain { $$=&TransactionControl{Kind:KindCommit,Chain:$3,Pos:$1.pos} } ;
transaction_common: transaction_start { $$=&TransactionControl{Kind:KindStart,Pos:$1.pos} }
 | transaction_start transaction_modes { $$=&TransactionControl{Kind:KindStart,Pos:$1.pos,Modes:$2} }
 | SET TRANSACTION transaction_modes { $$=&TransactionCharacteristics{Pos:$1.pos,Modes:$3} }
 | SET SESSION characteristics_word AS TRANSACTION transaction_modes { $$=&TransactionCharacteristics{Session:true,Pos:$1.pos,Modes:$6} }
 | rollback_start opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$2,Pos:$1.pos} }
 | ABORT opt_transaction opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$3,Pos:$1.pos} }
 | SAVEPOINT name { $$=&SavepointControl{Kind:CreateSavepoint,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | RELEASE name { $$=&SavepointControl{Kind:ReleaseSavepoint,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | RELEASE SAVEPOINT name { $$=&SavepointControl{Kind:ReleaseSavepoint,Name:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | rollback_start TO name { $$=&SavepointControl{Kind:RollbackSavepoint,Name:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | rollback_start TO SAVEPOINT name { $$=&SavepointControl{Kind:RollbackSavepoint,Name:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos} }
 | PREPARE TRANSACTION STRING { $$=&TransactionControl{Kind:KindPrepareTransaction,GID:$3.text,Pos:$1.pos} }
 | COMMIT PREPARED STRING { $$=&TransactionControl{Kind:KindCommitPrepared,GID:$3.text,Pos:$1.pos} }
 | ROLLBACK PREPARED STRING { $$=&TransactionControl{Kind:KindRollbackPrepared,GID:$3.text,Pos:$1.pos} } ;
transaction_start: BEGIN opt_transaction { $$=$1 } | START TRANSACTION { $$=$1 } ;
commit_word: COMMIT | END ;
/* Share the entire ROLLBACK [WORK|TRANSACTION] prefix before choosing an
 * executable end or rollback-to. Never shift optional words into TO-only
 * syntax while reducing a competing bare ROLLBACK end introducer. */
rollback_start: ROLLBACK opt_transaction { $$=$1 } ;
opt_transaction: /* empty */ | WORK | TRANSACTION ;
opt_chain: /* empty */ { $$=false } | AND CHAIN { $$=true } | AND NO CHAIN { $$=false } ;
characteristics_word: KW_U { if $1.text!="characteristics" { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
transaction_modes: transaction_mode { $$=[]TransactionMode{$1} }
 | transaction_modes ',' transaction_mode { $$=append($1,$3) }
 | transaction_modes transaction_mode { $$=append($1,$2) } ;
transaction_mode: ISOLATION LEVEL isolation_mode { $$=TransactionMode{Isolation:$3} }
 | READ ONLY { $$=TransactionMode{AccessSet:true,ReadOnly:true} }
 | READ mode_write { $$=TransactionMode{AccessSet:true} }
 | DEFERRABLE { $$=TransactionMode{DeferrableSet:true,Deferrable:true} }
 | NOT_DEFERRABLE_LA DEFERRABLE { $$=TransactionMode{DeferrableSet:true} } ;
isolation_mode: READ COMMITTED { $$=IsolationReadCommitted }
 | READ UNCOMMITTED { $$=IsolationReadUncommitted }
 | REPEATABLE READ { $$=IsolationRepeatableRead }
 | SERIALIZABLE { $$=IsolationSerializable } ;
mode_write: KW_U { if $1.text!="write" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_table: LOCK optional_table lock_targets optional_lock_mode optional_lock_nowait { $$=&LockTable{Targets:$3,Mode:$4,Pos:$1.pos,Nowait:$5} } ;
truncate_table: TRUNCATE optional_table lock_targets truncate_identity drop_mode { $$=&Truncate{Targets:$3,Mode:$5,Pos:$1.pos,RestartIdentity:$4.text=="restart"} } ;
truncate_identity: /* empty */ { $$=lexeme{} }
 | CONTINUE IDENTITY { $$=$1 }
 | RESTART IDENTITY { $$=$1 } ;
optional_table: /* empty */ | TABLE ;
lock_targets: alter_target { $$=[]LockTarget{{Name:$1.Name,Only:$1.Only}} }
 | lock_targets ',' alter_target { $$=append($1,LockTarget{Name:$3.Name,Only:$3.Only}) } ;
optional_lock_mode: /* empty */ { $$=LockAccessExclusive }
 | IN lock_mode MODE { $$=$2 } ;
lock_mode: ACCESS lock_share { $$=LockAccessShare }
 | ROW lock_share { $$=LockRowShare }
 | ROW EXCLUSIVE { $$=LockRowExclusive }
 | lock_share UPDATE EXCLUSIVE { $$=LockShareUpdateExclusive }
 | lock_share { $$=LockShare }
 | lock_share ROW EXCLUSIVE { $$=LockShareRowExclusive }
 | EXCLUSIVE { $$=LockExclusive }
 | ACCESS EXCLUSIVE { $$=LockAccessExclusive } ;
optional_lock_nowait: /* empty */ { $$=false } | lock_nowait { $$=true } ;
lock_share: KW_U { if $1.text!="share" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_nowait: NOWAIT ;
set_constraints: SET KW_U relation_names KW_U {if $2.text!="constraints" || ($4.text!="deferred" && $4.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};$$=&SetConstraints{Names:$3,Deferred:$4.text=="deferred",Pos:$1.pos}}
 | SET KW_U ALL KW_U {if $2.text!="constraints" || ($4.text!="deferred" && $4.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};$$=&SetConstraints{All:true,Deferred:$4.text=="deferred",Pos:$1.pos}} ;
set: SET set_name setting_assignment setting_values { $$=parsedSet($2.identifier(),$4,$1.pos) }
 | SET XML OPTION name { if $4.keyword==nil || ($4.text!="document" && $4.text!="content") {sqllex.(*lexer).syntax($4);return 1};$$=&Set{Name:Identifier{Text:"xmloption",Pos:$2.pos},Values:[]string{$4.text},Pos:$1.pos} }
 | SET SESSION XML OPTION name { if $5.keyword==nil || ($5.text!="document" && $5.text!="content") {sqllex.(*lexer).syntax($5);return 1};$$=&Set{Name:Identifier{Text:"xmloption",Pos:$3.pos},Values:[]string{$5.text},Pos:$1.pos} }
 | SET LOCAL XML OPTION name { if $5.keyword==nil || ($5.text!="document" && $5.text!="content") {sqllex.(*lexer).syntax($5);return 1};$$=&Set{Name:Identifier{Text:"xmloption",Pos:$3.pos},Values:[]string{$5.text},Local:true,Pos:$1.pos} }
 | SET set_name setting_assignment DEFAULT { $$=&Set{Name:$2.identifier(),Default:true,Pos:$1.pos} }
 | SET SESSION set_name setting_assignment setting_values { $$=parsedSet($3.identifier(),$5,$1.pos) }
 | SET SESSION set_name setting_assignment DEFAULT { $$=&Set{Name:$3.identifier(),Default:true,Pos:$1.pos} }
 /* LOCAL as a parameter name is distinct from transaction-local scope. */
 | SET LOCAL setting_assignment setting_values { $$=parsedSet(Identifier{Text:$2.text,Pos:$2.pos},$4,$1.pos) }
 | SET LOCAL setting_assignment DEFAULT { $$=&Set{Name:Identifier{Text:$2.text,Pos:$2.pos},Default:true,Pos:$1.pos} }
 | SET SESSION LOCAL setting_assignment setting_values { $$=parsedSet(Identifier{Text:$3.text,Pos:$3.pos},$5,$1.pos) }
 | SET SESSION LOCAL setting_assignment DEFAULT { $$=&Set{Name:Identifier{Text:$3.text,Pos:$3.pos},Default:true,Pos:$1.pos} }
 | SET LOCAL local_setting_name setting_assignment setting_values { s:=parsedSet($3.identifier(),$5,$1.pos);s.Local=true;$$=s }
 | SET LOCAL local_setting_name setting_assignment DEFAULT { $$=&Set{Name:$3.identifier(),Default:true,Local:true,Pos:$1.pos} }
 | SET NAMES setting_value { $$=parsedSet(Identifier{Text:"client_encoding",Pos:$2.pos},settingList{values:[]string{$3.text},stringValues:[]bool{$3.isString}},$1.pos) }
 | SET NAMES DEFAULT { $$=&Set{Name:Identifier{Text:"client_encoding",Pos:$2.pos},Default:true,Pos:$1.pos} }
 | SET SESSION NAMES setting_value { $$=parsedSet(Identifier{Text:"client_encoding",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{$4.isString}},$1.pos) }
 | SET SESSION NAMES DEFAULT { $$=&Set{Name:Identifier{Text:"client_encoding",Pos:$3.pos},Default:true,Pos:$1.pos} }
 | SET LOCAL NAMES setting_value { s:=parsedSet(Identifier{Text:"client_encoding",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{$4.isString}},$1.pos);s.Local=true;$$=s }
 | SET LOCAL NAMES DEFAULT { $$=&Set{Name:Identifier{Text:"client_encoding",Pos:$3.pos},Default:true,Local:true,Pos:$1.pos} }
 | SET SCHEMA STRING { $$=parsedSet(Identifier{Text:"search_path",Pos:$2.pos},settingList{values:[]string{$3.text},stringValues:[]bool{true}},$1.pos) }
 | SET SESSION SCHEMA STRING { $$=parsedSet(Identifier{Text:"search_path",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{true}},$1.pos) }
 | SET TIME ZONE zone_setting { $$=$4; $4.Name=Identifier{Text:"timezone",Pos:$2.pos};$4.Pos=$1.pos }
 | SET SESSION TIME ZONE zone_setting { $$=$5; $5.Name=Identifier{Text:"timezone",Pos:$3.pos};$5.Pos=$1.pos }
 | SET LOCAL TIME ZONE zone_setting { $$=$5; $5.Name=Identifier{Text:"timezone",Pos:$3.pos};$5.Pos=$1.pos;$5.Local=true }
 | SET LOCAL SCHEMA STRING { s:=parsedSet(Identifier{Text:"search_path",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{true}},$1.pos);s.Local=true;$$=s } ;
setting_assignment: TO | '=' ;
alter_system: ALTER SYSTEM SET setting_name setting_assignment external_setting_values { $$=&AlterSystem{Action:"SET",Name:$4.identifier(),Pos:$1.pos} }
 | ALTER SYSTEM SET setting_name setting_assignment DEFAULT { $$=&AlterSystem{Action:"SET",Name:$4.identifier(),Default:true,Pos:$1.pos} }
 | ALTER SYSTEM RESET setting_name { $$=&AlterSystem{Action:"RESET",Name:$4.identifier(),Pos:$1.pos} }
 | ALTER SYSTEM RESET ALL { $$=&AlterSystem{Action:"RESET ALL",Pos:$1.pos} } ;
/* External values consume the lexer's numeric syntax without session conversion
 * or allocation of a discarded list. Execution refuses before value validation. */
external_setting_values: external_setting_value | external_setting_values ',' external_setting_value ;
external_setting_value: STRING | name | TRUE | FALSE | ON | NUMBER | '+' NUMBER | '-' NUMBER ;
setting_values: setting_value { $$=settingList{values:[]string{$1.text},stringValues:[]bool{$1.isString}} } | setting_values ',' setting_value { $$=$1;$$.values=append($$.values,$3.text);$$.stringValues=append($$.stringValues,$3.isString) } ;
setting_value: STRING { $$=settingAtom{text:$1.text,isString:true} } | name { $$=settingAtom{text:$1.text,isString:true} }
 | TRUE { $$=settingAtom{text:"true",isString:true} } | FALSE { $$=settingAtom{text:"false",isString:true} }
 | ON { $$=settingAtom{text:$1.text,isString:true} }
 | NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$1,false); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$2,false); if !ok { return 1 } }
 | '-' NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$2,true); if !ok { return 1 } } ;
zone_setting: STRING { $$=&Set{Values:[]string{$1.text}} }
 | NUMBER { $$=&Set{Values:[]string{$1.text}} }
 | '+' NUMBER { $$=&Set{Values:[]string{$2.text}} }
 | '-' NUMBER { $$=&Set{Values:[]string{"-"+$2.text}} }
 | DEFAULT { $$=&Set{Default:true} }
 | LOCAL { $$=&Set{Default:true} }
 | INTERVAL interval_precision STRING interval_qualifier { $$=&Set{Interval:&TimeZoneInterval{Text:$3.text,Precision:int32($2),Qualifier:$4.text,Pos:$1.pos}} } ;
interval_precision: /* empty */ { $$=-1 }
 | '(' NUMBER ')' { var ok bool;$$,ok=timezoneIntervalPrecision(sqllex,$2);if !ok{return 1} } ;
interval_field: HOUR | MINUTE | SECOND | DAY | MONTH | YEAR ;
interval_qualifier: /* empty */ { $$=lexeme{} }
 | interval_field { if $1.text!="hour" && $1.text!="minute" {sqllex.(*lexer).failAt("42601","time zone interval must be HOUR or HOUR TO MINUTE",$1.pos.Offset);return 1};$$=$1 }
 | interval_field TO interval_field { if $1.text!="hour" || $3.text!="minute" {sqllex.(*lexer).failAt("42601","time zone interval must be HOUR or HOUR TO MINUTE",$1.pos.Offset);return 1};$$=$1;$$.text="hour to minute" } ;
show: SHOW setting_name { $$=&Show{Name:$2.identifier(),Pos:$1.pos} }
 | SHOW TIME ZONE { $$=&Show{Name:Identifier{Text:"timezone",Pos:$2.pos},Pos:$1.pos} }
 | SHOW TRANSACTION ISOLATION LEVEL { $$=&Show{Name:Identifier{Text:"transaction_isolation",Pos:$2.pos},Pos:$1.pos} }
 | SHOW ALL { $$=&Show{All:true,Pos:$1.pos} } ;
reset: RESET set_name { $$=&Reset{Name:$2.identifier(),Pos:$1.pos} }
 | RESET TIME ZONE { $$=&Reset{Name:Identifier{Text:"timezone",Pos:$2.pos},Pos:$1.pos} }
 | RESET LOCAL { $$=&Reset{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | RESET ALL { $$=&Reset{All:true,Pos:$1.pos} } ;
setting_name: name { $$=&settingName{first:$1} }
 | setting_name '.' name { $$=$1; if $$.parts==nil { $$.parts=[]string{$$.first.text,$3.text} } else { $$.parts=append($$.parts,$3.text) } } ;
set_name: setting_word { $$=&settingName{first:$1} }
 | LOCAL '.' name { $$=&settingName{first:$1,parts:[]string{$1.text,$3.text}} }
 | ROLE '.' name { $$=&settingName{first:$1,parts:[]string{$1.text,$3.text}} }
 | set_name '.' name { $$=$1; if $$.parts==nil { $$.parts=[]string{$$.first.text,$3.text} } else { $$.parts=append($$.parts,$3.text) } } ;
/* An undotted ROLE after SET LOCAL belongs only to the excluded role command,
 * never to the setting-name reduction. Dotted ROLE and ordinary LOCAL remain
 * parameter names, and ROLE stays name-eligible everywhere else. */
local_setting_name: set_name { $$=$1 }
 | LOCAL { $$=&settingName{first:$1} } ;
create_table: CREATE TABLE relation_name '(' optional_elements ')' table_parents partition_by table_on_commit { $$ = &CreateTable{Name:$3, Elements:$5, Parents:$7, Pos:$1.pos,Persistence:"logged",PartitionBy:$8,OnCommit:$9.action,OnCommitPos:$9.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name '(' optional_elements ')' table_parents partition_by table_on_commit { $$ = &CreateTable{Name:$6, Elements:$8, Parents:$10, Pos:$1.pos,IfNotExists:true,Persistence:"logged",PartitionBy:$11,OnCommit:$12.action,OnCommitPos:$12.pos} }
 | CREATE TEMP TABLE relation_name '(' optional_elements ')' table_parents partition_by table_on_commit { $$ = &CreateTable{Name:$4, Elements:$6, Parents:$8, Pos:$1.pos,Persistence:"temporary",PartitionBy:$9,OnCommit:$10.action,OnCommitPos:$10.pos} }
 | CREATE TEMP TABLE IF NOT EXISTS relation_name '(' optional_elements ')' table_parents partition_by table_on_commit { $$ = &CreateTable{Name:$7, Elements:$9, Parents:$11, Pos:$1.pos,IfNotExists:true,Persistence:"temporary",PartitionBy:$12,OnCommit:$13.action,OnCommitPos:$13.pos} }
 | CREATE TABLE relation_name PARTITION OF relation_name partition_options partition_bound partition_by table_on_commit { $$=&CreateTable{Name:$3,Elements:$7,Pos:$1.pos,Persistence:"logged",PartitionOf:&PartitionParent{Name:$6,Bound:*$8,Pos:$4.pos},PartitionBy:$9,OnCommit:$10.action,OnCommitPos:$10.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name PARTITION OF relation_name partition_options partition_bound partition_by table_on_commit { $$=&CreateTable{Name:$6,Elements:$10,Pos:$1.pos,IfNotExists:true,Persistence:"logged",PartitionOf:&PartitionParent{Name:$9,Bound:*$11,Pos:$7.pos},PartitionBy:$12,OnCommit:$13.action,OnCommitPos:$13.pos} }
 | CREATE TEMP TABLE relation_name PARTITION OF relation_name partition_options partition_bound partition_by table_on_commit { $$=&CreateTable{Name:$4,Elements:$8,Pos:$1.pos,Persistence:"temporary",PartitionOf:&PartitionParent{Name:$7,Bound:*$9,Pos:$5.pos},PartitionBy:$10,OnCommit:$11.action,OnCommitPos:$11.pos} }
 | CREATE TABLE relation_name OF relation_name typed_options table_on_commit {source:=$5;$$=&CreateTable{Name:$3,OfType:&source,Elements:$6,Pos:$1.pos,Persistence:"logged",OnCommit:$7.action,OnCommitPos:$7.pos}}
 | CREATE TABLE IF NOT EXISTS relation_name OF relation_name typed_options table_on_commit {source:=$8;$$=&CreateTable{Name:$6,OfType:&source,Elements:$9,Pos:$1.pos,IfNotExists:true,Persistence:"logged",OnCommit:$10.action,OnCommitPos:$10.pos}}
 | CREATE TEMP TABLE relation_name OF relation_name typed_options table_on_commit {source:=$6;$$=&CreateTable{Name:$4,OfType:&source,Elements:$7,Pos:$1.pos,Persistence:"temporary",OnCommit:$8.action,OnCommitPos:$8.pos}}
 | CREATE TEMP TABLE IF NOT EXISTS relation_name OF relation_name typed_options table_on_commit {source:=$9;$$=&CreateTable{Name:$7,OfType:&source,Elements:$10,Pos:$1.pos,IfNotExists:true,Persistence:"temporary",OnCommit:$11.action,OnCommitPos:$11.pos}}
 | CREATE TEMP TABLE IF NOT EXISTS relation_name PARTITION OF relation_name partition_options partition_bound partition_by table_on_commit { $$=&CreateTable{Name:$7,Elements:$11,Pos:$1.pos,IfNotExists:true,Persistence:"temporary",PartitionOf:&PartitionParent{Name:$10,Bound:*$12,Pos:$8.pos},PartitionBy:$13,OnCommit:$14.action,OnCommitPos:$14.pos} } ;
table_parents: /* empty */ { $$=nil } | INHERITS '(' relation_names ')' { $$=$3 } ;
table_on_commit: /* empty */ { $$=onCommitClause{} }
 | ON COMMIT DROP { $$=onCommitClause{action:OnCommitDrop,pos:$1.pos} }
 | ON COMMIT DELETE table_rows { $$=onCommitClause{action:OnCommitDelete,pos:$1.pos} }
 | ON COMMIT table_preserve table_rows { $$=onCommitClause{action:OnCommitPreserve,pos:$1.pos} } ;
table_preserve: KW_U { if $1.text!="preserve" {sqllex.(*lexer).syntax($1);return 1}; $$=$1 } ;
table_rows: FRAME_UNIT { if $1.text!="rows" {sqllex.(*lexer).syntax($1);return 1}; $$=$1 } ;
partition_by: /* empty */ {$$=nil}
 | PARTITION BY FRAME_UNIT '(' partition_keys ')' {
 if $3.text!="range" {sqllex.(*lexer).deferUnsupported($3,"partition strategy is unsupported")}
 $$=&PartitionKey{Keys:$5,Pos:$1.pos}
 }
 | PARTITION BY IDENT '(' partition_keys ')' {
 strategy:=PartitionHash
 switch $3.text {
 case "hash":
 case "list": strategy=PartitionList
 default: sqllex.(*lexer).syntax($3);return 1
 }
 $$=&PartitionKey{Strategy:strategy,Keys:$5,Pos:$1.pos}
 } ;
partition_keys: partition_key {$$=[]IndexKey{*$1}}
 | partition_keys ',' partition_key {$$=append($1,*$3)} ;
partition_key: index_atom column_collation index_class {$$=$1;$$.Collation=$2;$$.Opclass=$3.Opclass;$$.Options=$3.Options} ;
partition_bound: DEFAULT {$$=&PartitionBound{Default:true,Pos:$1.pos}}
 | FOR VALUES FROM '(' partition_data ')' TO '(' partition_data ')' {$$=&PartitionBound{Lower:$5,Upper:$9,Pos:$3.pos}}
 | FOR VALUES IN '(' list_partition_data ')' {$$=&PartitionBound{Strategy:PartitionList,Values:$5,Pos:$3.pos}}
 | FOR VALUES WITH '(' hash_bound_options ')' {
 $$=$5;$$.Strategy=PartitionHash;$$.Pos=$3.pos
 if $$.ModulusPos.Character==0 {sqllex.(*lexer).failAt("42601","modulus for hash partition must be specified",$3.pos.Offset);return 1}
 if $$.RemainderPos.Character==0 {sqllex.(*lexer).failAt("42601","remainder for hash partition must be specified",$3.pos.Offset);return 1}
 } ;
hash_bound_options: hash_bound_option {$$=$1}
 | hash_bound_options ',' hash_bound_option {
 $$=$1
 if $3.ModulusPos.Character!=0 {
  if $$.ModulusPos.Character!=0 {sqllex.(*lexer).failAt("42710","modulus for hash partition provided more than once",$3.ModulusPos.Offset);return 1}
  $$.Modulus=$3.Modulus;$$.ModulusPos=$3.ModulusPos
 } else {
  if $$.RemainderPos.Character!=0 {sqllex.(*lexer).failAt("42710","remainder for hash partition provided more than once",$3.RemainderPos.Offset);return 1}
  $$.Remainder=$3.Remainder;$$.RemainderPos=$3.RemainderPos
 }
 } ;
hash_bound_option: name NUMBER {
 bound,ok:=sqllex.(*lexer).hashBoundOption($1,$2);$$=&bound
 if !ok{return 1}
 } ;
list_partition_data: list_partition_datum {$$=[]PartitionDatum{$1}}
 | list_partition_data ',' list_partition_datum {$$=append($1,$3)} ;
list_partition_datum: target_or {$$=PartitionDatum{Expression:$1,Pos:ExpressionStart($1)}} ;
partition_data: partition_datum {$$=[]PartitionDatum{$1}}
 | partition_data ',' partition_datum {$$=append($1,$3)} ;
/* MINVALUE/MAXVALUE already reduce as unreserved column names. Classify their
 * single-name raw expression here, rather than competing with that reduction. */
partition_datum: target_or {
 $$=PartitionDatum{Expression:$1,Pos:$1.SourcePosition()}
 if column,ok:=$1.(ColumnExpr);ok && len(column.Name.Parts)==1 {
  switch column.Name.Parts[0].Text {
  case "minvalue": $$=PartitionDatum{Kind:-1,Pos:$1.SourcePosition()}
  case "maxvalue": $$=PartitionDatum{Kind:1,Pos:$1.SourcePosition()}
  }
 }
 } ;
partition_options: /* empty */ {$$=nil}
 | '(' partition_elements ')' {$$=$2} ;
partition_elements: partition_element {$$=[]TableElement{*$1}}
 | partition_elements ',' partition_element {$$=append($1,*$3)} ;
partition_element: name column_constraints {c:=Column{Name:$1.text,Constraints:$2,Pos:$1.pos};$$=&TableElement{Column:&c,Pos:$1.pos}}
 | check_constraint {$$=&TableElement{Check:$1,Pos:$1.Pos}}
 | unique_constraint {$$=&TableElement{Unique:$1,Pos:$1.Pos}}
 | primary_constraint {$$=&TableElement{Unique:$1,Pos:$1.Pos}}
 | exclusion_constraint {$$=&TableElement{Exclusion:$1,Pos:$1.Pos}}
 | partition_element ENFORCED {if !sqllex.(*lexer).elementEnforcement($1,$2,false){return 1};$$=$1}
 | partition_element NOT ENFORCED {if !sqllex.(*lexer).elementEnforcement($1,$2,true){return 1};$$=$1}
 | partition_element NOT_DEFERRABLE_LA DEFERRABLE {if !sqllex.(*lexer).elementForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | partition_element DEFERRABLE {if !sqllex.(*lexer).elementForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | partition_element INITIALLY name {if !sqllex.(*lexer).elementForeignProperty($1,$2,$3){return 1};$$=$1} ;
typed_options: /* empty */ {$$=nil} | '(' typed_elements ')' {$$=$2} ;
typed_elements: typed_element {$$=[]TableElement{*$1}}
 | typed_elements ',' typed_element {$$=append($1,*$3)} ;
typed_element: name {c:=Column{Name:$1.text,Pos:$1.pos};$$=&TableElement{Column:&c,Pos:$1.pos}}
 | name column_constraints {c:=Column{Name:$1.text,Pos:$1.pos,Constraints:$2};$$=&TableElement{Column:&c,Pos:$1.pos}}
 | name WITH options_word nullability {c:=Column{Name:$1.text,Pos:$1.pos,Constraints:$4};$$=&TableElement{Column:&c,Pos:$1.pos}}
 | check_constraint {$$=&TableElement{Check:$1,Pos:$1.Pos}}
 | unique_constraint {$$=&TableElement{Unique:$1,Pos:$1.Pos}}
 | primary_constraint {$$=&TableElement{Unique:$1,Pos:$1.Pos}}
 | foreign_key {$$=&TableElement{ForeignKey:$1,Pos:$1.Pos}}
 | exclusion_constraint {$$=&TableElement{Exclusion:$1,Pos:$1.Pos}}
 | table_not_null {$$=&TableElement{NotNull:$1,Pos:$1.Pos}}
 | typed_element ENFORCED {if !sqllex.(*lexer).elementEnforcement($1,$2,false){return 1};$$=$1}
 | typed_element NOT ENFORCED {if !sqllex.(*lexer).elementEnforcement($1,$2,true){return 1};$$=$1}
 | typed_element NOT_DEFERRABLE_LA DEFERRABLE {if !sqllex.(*lexer).elementForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | typed_element DEFERRABLE {if !sqllex.(*lexer).elementForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | typed_element INITIALLY name {if !sqllex.(*lexer).elementForeignProperty($1,$2,$3){return 1};$$=$1} ;
options_word: KW_U {if $1.text!="options" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
query_table_target: CREATE TABLE relation_name {$$=&CreateQueryTable{Name:$3,Pos:$1.pos}}
 | CREATE TABLE relation_name '(' view_names ')' {$$=&CreateQueryTable{Name:$3,Columns:$5,Pos:$1.pos}}
 | CREATE TABLE IF NOT EXISTS relation_name {$$=&CreateQueryTable{Name:$6,Pos:$1.pos,IfNotExists:true}}
 | CREATE TABLE IF NOT EXISTS relation_name '(' view_names ')' {$$=&CreateQueryTable{Name:$6,Columns:$8,Pos:$1.pos,IfNotExists:true}}
 | CREATE MATERIALIZED VIEW relation_name {$$=&CreateQueryTable{Name:$4,Pos:$1.pos,Form:CreateMaterializedView}}
 | CREATE MATERIALIZED VIEW relation_name '(' view_names ')' {$$=&CreateQueryTable{Name:$4,Columns:$6,Pos:$1.pos,Form:CreateMaterializedView}}
 | CREATE MATERIALIZED VIEW IF NOT EXISTS relation_name {$$=&CreateQueryTable{Name:$7,Pos:$1.pos,IfNotExists:true,Form:CreateMaterializedView}}
 | CREATE MATERIALIZED VIEW IF NOT EXISTS relation_name '(' view_names ')' {$$=&CreateQueryTable{Name:$7,Columns:$9,Pos:$1.pos,IfNotExists:true,Form:CreateMaterializedView}}
 | CREATE TEMP TABLE relation_name {sqllex.(*lexer).deferUnsupported($2,"CREATE syntax is unsupported");$$=&CreateQueryTable{Name:$4,Pos:$1.pos}}
 | CREATE TEMP TABLE relation_name '(' view_names ')' {sqllex.(*lexer).deferUnsupported($2,"CREATE syntax is unsupported");$$=&CreateQueryTable{Name:$4,Columns:$6,Pos:$1.pos}}
 | CREATE TEMP TABLE IF NOT EXISTS relation_name {sqllex.(*lexer).deferUnsupported($2,"CREATE syntax is unsupported");$$=&CreateQueryTable{Name:$7,Pos:$1.pos,IfNotExists:true}}
 | CREATE TEMP TABLE IF NOT EXISTS relation_name '(' view_names ')' {sqllex.(*lexer).deferUnsupported($2,"CREATE syntax is unsupported");$$=&CreateQueryTable{Name:$7,Columns:$9,Pos:$1.pos,IfNotExists:true}} ;
create_query_table: query_table_target query_table_options AS query_table_body query_table_data {$1.Query=$4;$1.NoData=$5;$$=$1} ;
query_table_body: query_select {$$=$1}
 | TABLE relation_name {sqllex.(*lexer).deferUnsupported($1,"TABLE queries are unsupported");$$=nil}
 | EXECUTE name {sqllex.(*lexer).deferUnsupported($1,"CREATE TABLE AS EXECUTE is unsupported");$$=nil}
 | EXECUTE name '(' call_args ')' {sqllex.(*lexer).deferUnsupported($1,"CREATE TABLE AS EXECUTE is unsupported");$$=nil} ;
query_table_data: /* empty */ {$$=false} | WITH data_word {$$=false} | WITH NO data_word {$$=true} ;
refresh_materialized_view: REFRESH MATERIALIZED VIEW maintenance_concurrent relation_name query_table_data {$$=&RefreshMaterializedView{Name:$5,NoData:$6,Concurrent:$4.pos,Pos:$1.pos}} ;
data_word: DATA {$$=$1} ;
query_table_options: /* empty */
 | query_table_options USING name {sqllex.(*lexer).deferUnsupported($2,"table access methods are unsupported")}
 | query_table_options WITH '(' view_option_list ')' {sqllex.(*lexer).deferUnsupported($2,"table storage parameters are unsupported")}
 | query_table_options TABLESPACE name {sqllex.(*lexer).deferUnsupported($2,"table tablespaces are unsupported")}
 | query_table_options ON COMMIT KW_U FRAME_UNIT {if $4.text!="preserve" || $5.text!="rows" {sqllex.(*lexer).syntax($4);return 1};sqllex.(*lexer).deferUnsupported($2,"ON COMMIT is unsupported")}
 | query_table_options ON COMMIT DELETE FRAME_UNIT {if $5.text!="rows" {sqllex.(*lexer).syntax($5);return 1};sqllex.(*lexer).deferUnsupported($2,"ON COMMIT is unsupported")}
 | query_table_options ON COMMIT DROP {sqllex.(*lexer).deferUnsupported($2,"ON COMMIT is unsupported")} ;
/* WITH before a check option is contextual, leaving query suffix diagnostics
 * and ordinary unreserved LOCAL/CASCADED identifiers unchanged. */
view_as: AS {sqllex.(*lexer).viewQuery=true} ;
create_view: CREATE VIEW relation_name view_columns view_options view_as select view_check_option { query,ok:=$7.(*Select); if !ok {sqllex.(*lexer).failAt("0A000","view body requires an ordinary SELECT",$1.pos.Offset);return 1};$$=&CreateView{Name:$3,Columns:$4,Options:append($5,$8...),Query:query,Pos:$1.pos} }
 | CREATE OR replace_word VIEW relation_name view_columns view_options view_as select view_check_option { query,ok:=$9.(*Select); if !ok {sqllex.(*lexer).failAt("0A000","view body requires an ordinary SELECT",$1.pos.Offset);return 1};$$=&CreateView{Name:$5,Columns:$6,Options:append($7,$10...),Query:query,Replace:true,Pos:$1.pos} }
 | CREATE TEMP VIEW relation_name view_columns view_options view_as select view_check_option { query,ok:=$8.(*Select); if !ok {sqllex.(*lexer).failAt("0A000","view body requires an ordinary SELECT",$1.pos.Offset);return 1};$$=&CreateView{Name:$4,Columns:$5,Options:append($6,$9...),Query:query,Persistence:"temporary",Pos:$1.pos} }
 | CREATE OR replace_word TEMP VIEW relation_name view_columns view_options view_as select view_check_option { query,ok:=$10.(*Select); if !ok {sqllex.(*lexer).failAt("0A000","view body requires an ordinary SELECT",$1.pos.Offset);return 1};$$=&CreateView{Name:$6,Columns:$7,Options:append($8,$11...),Query:query,Replace:true,Persistence:"temporary",Pos:$1.pos} }
 | CREATE RECURSIVE VIEW relation_name '(' view_names ')' view_options view_as select view_check_option { query,ok:=sqllex.(*lexer).recursiveViewQuery($4,$6,$10,$11,$1.pos);if !ok {return 1};$$=&CreateView{Name:$4,Columns:$6,Options:$8,Query:query,Pos:$1.pos} }
 | CREATE OR replace_word RECURSIVE VIEW relation_name '(' view_names ')' view_options view_as select view_check_option { query,ok:=sqllex.(*lexer).recursiveViewQuery($6,$8,$12,$13,$1.pos);if !ok {return 1};$$=&CreateView{Name:$6,Columns:$8,Options:$10,Query:query,Replace:true,Pos:$1.pos} }
 | CREATE TEMP RECURSIVE VIEW relation_name '(' view_names ')' view_options view_as select view_check_option { query,ok:=sqllex.(*lexer).recursiveViewQuery($5,$7,$11,$12,$1.pos);if !ok {return 1};$$=&CreateView{Name:$5,Columns:$7,Options:$9,Query:query,Persistence:"temporary",Pos:$1.pos} }
 | CREATE OR replace_word TEMP RECURSIVE VIEW relation_name '(' view_names ')' view_options view_as select view_check_option { query,ok:=sqllex.(*lexer).recursiveViewQuery($7,$9,$13,$14,$1.pos);if !ok {return 1};$$=&CreateView{Name:$7,Columns:$9,Options:$11,Query:query,Replace:true,Persistence:"temporary",Pos:$1.pos} } ;
view_check_option: /* empty */ {$$=nil}
 | VIEW_CHECK_WITH CHECK OPTION {$$=[]ViewOption{{Name:Identifier{Text:"check_option",Pos:$1.pos},Value:"cascaded",HasValue:true}}}
 | VIEW_CHECK_WITH name CHECK OPTION {if $2.keyword==nil || ($2.text!="local" && $2.text!="cascaded") {sqllex.(*lexer).syntax($2);return 1};$$=[]ViewOption{{Name:Identifier{Text:"check_option",Pos:$1.pos},Value:$2.text,HasValue:true}}} ;
replace_word: name { if $1.text!="replace" || $1.keyword==nil {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
view_alter_head: ALTER VIEW relation_name {$$=&AlterView{Name:$3,Pos:$1.pos}}
 | ALTER VIEW IF EXISTS relation_name {$$=&AlterView{Name:$5,IfExists:true,Pos:$1.pos}} ;
alter_view: view_alter_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | view_alter_head RENAME optional_column name TO name {$1.Action="column";$1.Column=Identifier{Text:$4.text,Pos:$4.pos};$1.Target=Identifier{Text:$6.text,Pos:$6.pos};$$=$1}
 | view_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1}
 | view_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | view_alter_head SET '(' view_option_list ')' {$1.Action="set";$1.Options=$4;$$=$1}
 | view_alter_head RESET '(' view_option_list ')' {$1.Action="reset";$1.Options=$4;$$=$1}
 | view_alter_head ALTER optional_column name SET DEFAULT target_or {sqllex.(*lexer).failAt("0A000","ALTER VIEW column defaults are unsupported",$2.pos.Offset);return 1}
 | view_alter_head ALTER optional_column name DROP DEFAULT {sqllex.(*lexer).failAt("0A000","ALTER VIEW column defaults are unsupported",$2.pos.Offset);return 1}
 | materialized_alter_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | materialized_alter_head RENAME optional_column name TO name {$1.Action="column";$1.Column=Identifier{Text:$4.text,Pos:$4.pos};$1.Target=Identifier{Text:$6.text,Pos:$6.pos};$$=$1}
 | materialized_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1}
 | materialized_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | materialized_alter_head depends_word ON EXTENSION name {if $1.IfExists {sqllex.(*lexer).syntax($2);return 1};$1.Action="depends";$1.Target=Identifier{Text:$5.text,Pos:$5.pos};$$=$1}
 | materialized_alter_head NO depends_word ON EXTENSION name {if $1.IfExists {sqllex.(*lexer).syntax($3);return 1};$1.Action="no_depends";$1.Target=Identifier{Text:$6.text,Pos:$6.pos};$$=$1} ;
materialized_alter_head: ALTER MATERIALIZED VIEW relation_name {$$=&AlterView{Name:$4,Materialized:true,Pos:$1.pos}}
 | ALTER MATERIALIZED VIEW IF EXISTS relation_name {$$=&AlterView{Name:$6,Materialized:true,IfExists:true,Pos:$1.pos}} ;
depends_word: KW_U {if $1.text!="depends" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
drop_view: DROP VIEW relation_names drop_mode {$$=&DropView{Names:$3,Mode:$4,Pos:$1.pos}}
 | DROP VIEW IF EXISTS relation_names drop_mode {$$=&DropView{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos}}
 | DROP MATERIALIZED VIEW relation_names drop_mode {$$=&DropView{Names:$4,Mode:$5,Pos:$1.pos,Materialized:true}}
 | DROP MATERIALIZED VIEW IF EXISTS relation_names drop_mode {$$=&DropView{Names:$6,IfExists:true,Mode:$7,Pos:$1.pos,Materialized:true}} ;
view_columns: /* empty */ { $$=nil } | '(' view_names ')' { $$=$2 } ;
view_names: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} } | view_names ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
view_options: /* empty */ { $$=nil } | WITH '(' view_option_list ')' { $$=$3 } ;
view_option_list: view_option { $$=[]ViewOption{$1} } | view_option_list ',' view_option { $$=append($1,$3) } ;
view_option: name { $$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:"true"} } | name '=' view_option_value { $$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text,HasValue:true} } ;
view_option_value: name | STRING | NUMBER | TRUE | FALSE | ON ;
create: create_table { $$=$1 }
 | create_query_table { $$=$1 }
 | create_view { $$=$1 }
 | create_index { $$=$1 }
 | create_domain { $$=$1 }
 | create_collation { $$=$1 }
 | create_conversion { $$=$1 }
 | create_statistics { $$=$1 }
 | alter_statistics_target { $$=$1 }
 | create_dictionary { $$=$1 }
 | create_configuration { $$=$1 }
 | create_function { $$=$1 }
 | create_language { $$=$1 }
 | create_aggregate { $$=$1 }
 | create_operator { $$=$1 }
 | create_operator_family { $$=$1 }
 | create_operator_class { $$=$1 }
 | create_access_method { $$=$1 }
 | create_enum { $$=$1 }
 | create_composite { $$=$1 }
 | CREATE UNLOGGED TABLE { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 }
 | CREATE generic_create_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
/* Use a distinct unreserved head token, like DOMAIN, so generic-word
 * reductions cannot steal another CREATE declaration's continuation. */
create_enum: CREATE TYPE relation_name AS enum_word '(' enum_labels ')' {
 $$=&CreateEnum{Name:$3,Labels:$7,Pos:$1.pos}
}
 | CREATE TYPE relation_name AS range_word '(' range_options ')' {
 $$=&CreateRange{Name:$3,Options:$7,Pos:$1.pos}
} ;
enum_word: generic_word_without_frame {
 if $1.keyword==nil || $1.text!="enum" {sqllex.(*lexer).failAt("0A000","CREATE TYPE syntax is unsupported",$1.pos.Offset);return 1}
 $$=$1
} ;
range_word: FRAME_UNIT {
 if $1.text!="range" {sqllex.(*lexer).failAt("0A000","CREATE TYPE syntax is unsupported",$1.pos.Offset);return 1}
 $$=$1
} ;
range_options: range_option {$$=[]RangeOption{*$1}}
 | range_options ',' range_option {$$=append($1,*$3)} ;
range_option: col_label '=' type {$$=&RangeOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3}} ;
enum_labels: /* empty */ {$$=nil} | enum_label_list {$$=$1} ;
enum_label_list: STRING {$$=[]EnumLabel{{Text:$1.text,Pos:$1.pos}}}
 | enum_label_list ',' STRING {$$=append($1,EnumLabel{Text:$3.text,Pos:$3.pos})} ;
alter_enum: ALTER TYPE relation_name ADD VALUE STRING enum_placement {
 $7.Name=$3;$7.Label=EnumLabel{Text:$6.text,Pos:$6.pos};$7.Pos=$1.pos;$$=$7
}
 | ALTER TYPE relation_name ADD VALUE IF NOT EXISTS STRING enum_placement {
 $10.Name=$3;$10.Label=EnumLabel{Text:$9.text,Pos:$9.pos};$10.IfNotExists=true;$10.Pos=$1.pos;$$=$10
}
 | ALTER TYPE relation_name RENAME VALUE STRING TO STRING {
 $$=&AlterEnum{Name:$3,Old:EnumLabel{Text:$6.text,Pos:$6.pos},Label:EnumLabel{Text:$8.text,Pos:$8.pos},Rename:true,Pos:$1.pos}
} ;
enum_placement: /* empty */ {$$=&AlterEnum{}}
 | enum_placement_word STRING {
 $$=&AlterEnum{Neighbor:EnumLabel{Text:$2.text,Pos:$2.pos},Before:$1.text=="before"}
} ;
enum_placement_word: generic_word {
 if $1.keyword==nil || $1.text!="before" && $1.text!="after" {sqllex.(*lexer).syntax($1);return 1}
 $$=$1
} ;
create_composite: CREATE TYPE relation_name AS '(' composite_members ')' { $$=&CreateComposite{Name:$3,Members:$6,Pos:$1.pos} } ;
composite_members: /* empty */ {$$=nil} | composite_member_list {$$=$1} ;
composite_member_list: composite_member {$$=[]Column{*$1}}
 | composite_member_list ',' composite_member {$$=append($1,*$3)} ;
composite_member: name type column_collation {$$=&Column{Name:$1.text,TypeName:$2,Collation:$3,Pos:$1.pos}} ;
create_domain: CREATE DOMAIN relation_name domain_as type column_collation domain_constraints { $$=&CreateDomain{Name:$3,Base:$5,Collation:$6,Constraints:$7,Pos:$1.pos} } ;
domain_as: /* empty */ | AS ;
domain_constraints: /* empty */ {$$=nil}
 | domain_column_constraints {$$=$1}
 | '(' {sqllex.(*lexer).syntax($1);return 1}
 | column_suffix {sqllex.(*lexer).capability($1,unsupportedSuffixes,"domain constraints are unsupported");return 1} ;
domain_column_constraints: column_constraint { $$=[]ColumnConstraint{*$1} }
 | domain_column_constraints column_constraint { $$=appendColumnConstraint($1,*$2) }
 | domain_column_constraints NO nn_inherit {if $1[len($1)-1].Check!=nil {$1[len($1)-1].Check.NoInherit=true} else {sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported")};$$=$1} ;
drop_domain: DROP DOMAIN relation_names drop_mode { $$=&DropDomain{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP DOMAIN IF EXISTS relation_names drop_mode { $$=&DropDomain{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
alter_domain: ALTER DOMAIN relation_name ADD domain_alter_check {$$=&AlterDomain{Name:$3,Action:DomainAddCheck,Check:$5,Constraint:$5.Name,Pos:$1.pos}}
 | ALTER DOMAIN relation_name DROP CONSTRAINT drop_target drop_mode {$$=&AlterDomain{Name:$3,Action:DomainDropCheck,Constraint:$6.Name,IfExists:$6.IfExists,Mode:$7,Pos:$1.pos}}
 | ALTER DOMAIN relation_name validate_word CONSTRAINT name {$$=&AlterDomain{Name:$3,Action:DomainValidateCheck,Constraint:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER DOMAIN relation_name RENAME TO name {$$=&AlterDomain{Name:$3,Action:DomainRename,Target:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER DOMAIN relation_name SET SCHEMA name {$$=&AlterDomain{Name:$3,Action:DomainSetSchema,Target:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER DOMAIN relation_name SET DEFAULT target_or {$$=&AlterDomain{Name:$3,Action:DomainSetDefault,Default:$6,Pos:$1.pos}}
 | ALTER DOMAIN relation_name DROP DEFAULT {$$=&AlterDomain{Name:$3,Action:DomainDropDefault,Pos:$1.pos}}
 | ALTER DOMAIN relation_name SET NOT NULL {$$=&AlterDomain{Name:$3,Action:DomainSetNotNull,Pos:$1.pos}}
 | ALTER DOMAIN relation_name DROP NOT NULL {$$=&AlterDomain{Name:$3,Action:DomainDropNotNull,Pos:$1.pos}} ;
domain_alter_check: check_constraint {$$=$1}
 | domain_alter_check NOT nn_modifier {if $3.text=="enforced" {sqllex.(*lexer).deferUnsupported($2,"CHECK constraints cannot be marked NOT ENFORCED")} else {$1.NotValid=true};$$=$1}
 | domain_alter_check ENFORCED {sqllex.(*lexer).deferUnsupported($2,"CHECK constraints cannot be marked ENFORCED");$$=$1}
 | domain_alter_check NO nn_inherit {$1.NoInherit=true;$$=$1} ;
validate_word: KW_U {if $1.keyword==nil || $1.text!="validate" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
drop_type: DROP TYPE relation_names drop_mode {$$=&DropType{Names:$3,Mode:$4,Pos:$1.pos}}
 | DROP TYPE IF EXISTS relation_names drop_mode {$$=&DropType{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos}} ;
alter_composite: ALTER TYPE relation_name composite_actions {$$=&AlterComposite{Name:$3,Actions:$4,Pos:$1.pos}}
 | ALTER TYPE relation_name RENAME attribute_word name TO name drop_mode {target:=Identifier{Text:$8.text,Pos:$8.pos};$$=&AlterComposite{Name:$3,Column:Identifier{Text:$6.text,Pos:$6.pos},Rename:&target,Mode:$9,Pos:$1.pos}}
 | ALTER TYPE relation_name RENAME TO name {$$=&AlterType{Name:$3,Target:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER TYPE relation_name SET SCHEMA name {$$=&AlterType{Name:$3,Target:Identifier{Text:$6.text,Pos:$6.pos},SetSchema:true,Pos:$1.pos}} ;
attribute_word: KW_U {if $1.text!="attribute" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
composite_actions: composite_action {$$=[]AlterTableAction{*$1}}
 | composite_actions ',' composite_action {$$=append($1,*$3)} ;
composite_action: ADD attribute_word composite_member drop_mode {c:=$3;$$=&AlterTableAction{Action:AddColumn,Column:Identifier{Text:c.Name,Pos:c.Pos},Definition:c,Mode:$4,Pos:$1.pos}}
 | DROP attribute_word drop_target drop_mode {$$=&AlterTableAction{Action:DropColumn,Column:$3.Name,IfExists:$3.IfExists,Mode:$4,Pos:$1.pos}}
 | ALTER attribute_word name alter_type_keyword type column_collation drop_mode {$$=&AlterTableAction{Action:AlterColumnType,Column:Identifier{Text:$3.text,Pos:$3.pos},TypeName:$5,Collation:$6,Mode:$7,Pos:$1.pos}} ;
alter_table_of: ALTER TABLE alter_target OF relation_name {source:=$5;$$=&AlterTableOf{Name:$3.Name,Only:$3.Only,Type:&source,Pos:$1.pos}}
 | ALTER TABLE alter_target NOT OF {$$=&AlterTableOf{Name:$3.Name,Only:$3.Only,Pos:$1.pos}} ;
collation_create_head: CREATE COLLATION relation_name {$$=&CreateCollation{Name:$3,Pos:$1.pos}}
 | CREATE COLLATION IF NOT EXISTS relation_name {$$=&CreateCollation{Name:$6,IfNotExists:true,Pos:$1.pos}} ;
create_collation: collation_create_head FROM relation_name {source:=$3;$1.From=&source;$$=$1}
 | collation_create_head '(' collation_options ')' {$1.Options=$3;$$=$1} ;
collation_options: collation_option {$$=[]CollationOption{$1}}
 | collation_options ',' collation_option {$$=append($1,$3)} ;
collation_option: col_label '=' relation_name {value:=$3.Parts[0].Text;for _,p:=range $3.Parts[1:]{value+="."+p.Text};source:=$3;$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:value,Source:&source}}
 | col_label '=' STRING {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text}}
 | col_label '=' NUMBER {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text,Numeric:true}}
 | col_label '=' '-' NUMBER {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:"-"+$4.text,Numeric:true}}
 | col_label '=' '+' NUMBER {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$4.text,Numeric:true}}
 | col_label '=' TRUE {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text}}
 | col_label '=' FALSE {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text}}
 | col_label '=' NULL {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text}}
 | col_label '=' ON {$$=CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text}} ;
collation_alter_head: ALTER COLLATION relation_name {$$=&AlterCollation{Name:$3,Pos:$1.pos}} ;
alter_collation: collation_alter_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | collation_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | collation_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1}
 | collation_alter_head REFRESH VERSION {$1.Action="refresh";$$=$1} ;
/* Dedicated clause terminals avoid SET SCHEMA competing with two arbitrary
 * names. Their released unreserved identifier legality remains unchanged. */
collation_name_word: REFRESH | VERSION | EXTENSION | DATABASE ;
drop_collation: DROP COLLATION relation_names drop_mode {$$=&DropCollation{Names:$3,Mode:$4,Pos:$1.pos}}
 | DROP COLLATION IF EXISTS relation_names drop_mode {$$=&DropCollation{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos}} ;
create_conversion: CREATE CONVERSION relation_name FOR STRING TO STRING FROM relation_name {$$=&CreateConversion{Name:$3,Source:$5.text,Destination:$7.text,Function:$9,Pos:$1.pos}}
 | CREATE DEFAULT CONVERSION relation_name FOR STRING TO STRING FROM relation_name {$$=&CreateConversion{Name:$4,Source:$6.text,Destination:$8.text,Function:$10,Default:true,Pos:$1.pos}} ;
conversion_alter_head: ALTER CONVERSION relation_name {$$=&AlterConversion{Name:$3,Pos:$1.pos}} ;
alter_conversion: conversion_alter_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | conversion_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | conversion_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1} ;
drop_conversion: DROP CONVERSION relation_names drop_mode {$$=&DropConversion{Names:$3,Mode:$4,Pos:$1.pos}}
 | DROP CONVERSION IF EXISTS relation_names drop_mode {$$=&DropConversion{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos}}
 | DROP DEFAULT {sqllex.(*lexer).syntax($2);return 1} ;
statistics_create_head: CREATE STATISTICS {$$=$1} ;
statistics_kinds: /* empty */ {$$=nil}
 | '(' grant_column_list ')' {$$=$2} ;
statistics_elements: statistics_element {$$=[]Expr{$1}}
 | statistics_elements ',' statistics_element {$$=append($1,$3)} ;
statistics_element: column_reference {$$=$1}
 | function {$$=$1}
 | conditional_call {$$=$1}
 | string_function {$$=$1}
 | sqljson_query {$$=$1}
 | check_function_head function_args ')' {call,ok:=selectedScalarCall(sqllex,$1,$2);if !ok{return 1};$$=call}
 | name '(' function_args ')' {$$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos}}
 | name '.' col_label '(' function_args ')' {$$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos}}
 | '(' scalar_projection ')' {$$=$2} ;
create_statistics: statistics_create_head relation_name statistics_kinds ON statistics_elements FROM relation_name {$$=&CreateStatistics{Name:$2,Kinds:$3,Elements:$5,Relation:$7,Pos:$1.pos}}
 | statistics_create_head statistics_kinds ON statistics_elements FROM relation_name {$$=&CreateStatistics{Kinds:$2,Elements:$4,Relation:$6,Pos:$1.pos}}
 | statistics_create_head IF NOT EXISTS relation_name statistics_kinds ON statistics_elements FROM relation_name {$$=&CreateStatistics{Name:$5,Kinds:$6,Elements:$8,Relation:$10,IfNotExists:true,Pos:$1.pos}} ;
drop_statistics: DROP STATISTICS relation_names drop_mode {$$=&DropStatistics{Names:$3,Mode:$4,Pos:$1.pos}}
 | DROP STATISTICS IF EXISTS relation_names drop_mode {$$=&DropStatistics{Names:$5,Mode:$6,IfExists:true,Pos:$1.pos}} ;
statistics_alter_head: ALTER STATISTICS {$$=$1} ;
statistics_target: cursor_integer {$$=$1} | DEFAULT {$$=-1} ;
alter_statistics_target: statistics_alter_head relation_name SET STATISTICS statistics_target {$$=&AlterStatisticsTarget{Name:$2,Target:$5,Pos:$1.pos}}
 | statistics_alter_head IF EXISTS relation_name SET STATISTICS statistics_target {$$=&AlterStatisticsTarget{Name:$4,Target:$7,IfExists:true,Pos:$1.pos}} ;
create_aggregate: CREATE AGGREGATE relation_name '(' aggregate_arguments ')' '(' operator_options ')' {args,ok:=sqllex.(*lexer).aggregateUtilityArguments($5,nil);if !ok{return 1};$$=&CreateAggregate{Name:$3,Arguments:args,Options:$8,Pos:$1.pos}}
 | CREATE AGGREGATE relation_name '(' ORDER BY grant_routine_argument_list ')' '(' operator_options ')' {args,ok:=sqllex.(*lexer).aggregateUtilityArguments(nil,$7);if !ok{return 1};$$=&CreateAggregate{Name:$3,Arguments:args,Ordered:true,Options:$10,Pos:$1.pos}}
 | CREATE AGGREGATE relation_name '(' grant_routine_argument_list ORDER BY grant_routine_argument_list ')' '(' operator_options ')' {args,ok:=sqllex.(*lexer).aggregateUtilityArguments($5,$8);if !ok{return 1};$$=&CreateAggregate{Name:$3,Arguments:args,Ordered:true,DirectArgs:len($5),Options:$11,Pos:$1.pos}} ;
dictionary_word: KW_U {if $1.text!="dictionary" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
dictionary_options: dictionary_option {$$=[]DictionaryOption{$1}}
 | dictionary_options ',' dictionary_option {$$=append($1,$3)} ;
dictionary_option: collation_option {$$=DictionaryOption{CollationOption:$1,Supplied:true}}
 | col_label {$$=DictionaryOption{CollationOption:CollationOption{Name:Identifier{Text:$1.text,Pos:$1.pos}}}} ;
create_dictionary: CREATE TEXT SEARCH dictionary_word relation_name '(' dictionary_options ')' {$$=&CreateDictionary{Name:$5,Options:$7,Pos:$1.pos}} ;
dictionary_alter_head: ALTER TEXT SEARCH dictionary_word relation_name {$$=&AlterDictionary{Name:$5,Pos:$1.pos}} ;
alter_dictionary: dictionary_alter_head '(' dictionary_options ')' {$1.Options=$3;$1.Action="options";$$=$1}
 | dictionary_alter_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | dictionary_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | dictionary_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1} ;
drop_dictionary: DROP TEXT SEARCH dictionary_word relation_names drop_mode {$$=&DropDictionary{Names:$5,Mode:$6,Pos:$1.pos}}
 | DROP TEXT SEARCH dictionary_word IF EXISTS relation_names drop_mode {$$=&DropDictionary{Names:$7,IfExists:true,Mode:$8,Pos:$1.pos}} ;
configuration_options: configuration_option {$$=[]ConfigurationOption{$1}}
 | configuration_options ',' configuration_option {$$=append($1,$3)} ;
configuration_option: dictionary_option {$$=configurationOption($1)} ;
create_configuration: CREATE TEXT SEARCH TS_CONFIGURATION relation_name '(' configuration_options ')' {$$=&CreateConfiguration{Name:$5,Options:$7,Pos:$1.pos}} ;
configuration_alter_head: ALTER TEXT SEARCH TS_CONFIGURATION relation_name {$$=&AlterConfiguration{Name:$5,Pos:$1.pos}} ;
configuration_tokens: name {$$=[]Identifier{{Text:$1.text,Pos:$1.pos}}}
 | configuration_tokens ',' name {$$=append($1,Identifier{Text:$3.text,Pos:$3.pos})} ;
alter_configuration: configuration_alter_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | configuration_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | configuration_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1}
 | configuration_alter_head ADD FDW_MAPPING FOR configuration_tokens WITH relation_names {$1.Action="mapping";$1.MappingAction=ConfigurationAddMapping;$1.Tokens=$5;$1.Dictionaries=$7;$$=$1}
 | configuration_alter_head ALTER FDW_MAPPING FOR configuration_tokens WITH relation_names {$1.Action="mapping";$1.MappingAction=ConfigurationAlterMapping;$1.Tokens=$5;$1.Dictionaries=$7;$$=$1}
 | configuration_alter_head ALTER FDW_MAPPING replace_word relation_name WITH relation_name {$1.Action="mapping";$1.MappingAction=ConfigurationReplaceMapping;$1.OldDictionary=$5;$1.Dictionaries=[]ObjectName{$7};$$=$1}
 | configuration_alter_head ALTER FDW_MAPPING FOR configuration_tokens replace_word relation_name WITH relation_name {$1.Action="mapping";$1.MappingAction=ConfigurationReplaceMapping;$1.Tokens=$5;$1.OldDictionary=$7;$1.Dictionaries=[]ObjectName{$9};$$=$1}
 | configuration_alter_head DROP FDW_MAPPING FOR configuration_tokens {$1.Action="mapping";$1.MappingAction=ConfigurationDropMapping;$1.Tokens=$5;$$=$1}
 | configuration_alter_head DROP FDW_MAPPING IF EXISTS FOR configuration_tokens {$1.Action="mapping";$1.MappingAction=ConfigurationDropMapping;$1.Tokens=$7;$1.IfExists=true;$$=$1} ;
drop_configuration: DROP TEXT SEARCH TS_CONFIGURATION relation_names drop_mode {$$=&DropConfiguration{Names:$5,Mode:$6,Pos:$1.pos}}
 | DROP TEXT SEARCH TS_CONFIGURATION IF EXISTS relation_names drop_mode {$$=&DropConfiguration{Names:$7,IfExists:true,Mode:$8,Pos:$1.pos}} ;
aggregate_arguments: '*' {$$=nil} | grant_routine_arguments {$$=$1} ;
create_function: CREATE FUNCTION relation_name '(' function_arguments ')' function_result function_attributes LANGUAGE name function_attributes AS routine_as function_attributes {node:=&CreateFunction{Name:$3,Arguments:$5,Result:$7.Type,ResultSpecified:$7.Specified,TableResult:$7.Table,Language:Identifier{Text:$10.text,Pos:$10.pos},Body:$13.Body.text,BodyPos:routineBodyPosition($13.Body),Symbol:$13.Symbol.text,SymbolSpecified:$13.HasSymbol,SymbolPos:$13.Symbol.pos,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$8,$11,$14){return 1};$$=node}
 | CREATE FUNCTION relation_name '(' function_arguments ')' function_result function_attributes AS routine_as function_attributes LANGUAGE name function_attributes {node:=&CreateFunction{Name:$3,Arguments:$5,Result:$7.Type,ResultSpecified:$7.Specified,TableResult:$7.Table,Language:Identifier{Text:$13.text,Pos:$13.pos},Body:$10.Body.text,BodyPos:routineBodyPosition($10.Body),Symbol:$10.Symbol.text,SymbolSpecified:$10.HasSymbol,SymbolPos:$10.Symbol.pos,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$8,$11,$14){return 1};$$=node}
 | CREATE FUNCTION relation_name '(' function_arguments ')' function_result function_attributes standard_routine_body {node:=&CreateFunction{Name:$3,Arguments:$5,Result:$7.Type,ResultSpecified:$7.Specified,TableResult:$7.Table,Language:Identifier{Text:"sql"},Standard:$9,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$8){return 1};$$=node}
 | CREATE FUNCTION relation_name '(' function_arguments ')' function_result function_attributes LANGUAGE name function_attributes standard_routine_body {node:=&CreateFunction{Name:$3,Arguments:$5,Result:$7.Type,ResultSpecified:$7.Specified,TableResult:$7.Table,Language:Identifier{Text:$10.text,Pos:$10.pos},Standard:$12,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$8,$11){return 1};$$=node}
 | procedure_head procedure_attributes LANGUAGE name procedure_attributes AS routine_as procedure_attributes {node:=$1.(*CreateFunction);node.Language=Identifier{Text:$4.text,Pos:$4.pos};node.Body=$7.Body.text;node.BodyPos=routineBodyPosition($7.Body);node.Symbol=$7.Symbol.text;node.SymbolSpecified=$7.HasSymbol;node.SymbolPos=$7.Symbol.pos;node.setProcedureAttributes($2,$5,$8);$$=node}
 | procedure_head procedure_attributes AS routine_as procedure_attributes LANGUAGE name procedure_attributes {node:=$1.(*CreateFunction);node.Language=Identifier{Text:$7.text,Pos:$7.pos};node.Body=$4.Body.text;node.BodyPos=routineBodyPosition($4.Body);node.Symbol=$4.Symbol.text;node.SymbolSpecified=$4.HasSymbol;node.SymbolPos=$4.Symbol.pos;node.setProcedureAttributes($2,$5,$8);$$=node}
 | procedure_head procedure_attributes standard_routine_body {node:=$1.(*CreateFunction);node.Language=Identifier{Text:"sql"};node.Standard=$3;node.setProcedureAttributes($2);$$=node}
 | procedure_head procedure_attributes LANGUAGE name procedure_attributes standard_routine_body {node:=$1.(*CreateFunction);node.Language=Identifier{Text:$4.text,Pos:$4.pos};node.Standard=$6;node.setProcedureAttributes($2,$5);$$=node}
 | CREATE OR replace_word FUNCTION relation_name '(' function_arguments ')' function_result function_attributes LANGUAGE name function_attributes AS routine_as function_attributes {node:=&CreateFunction{Name:$5,Arguments:$7,Result:$9.Type,ResultSpecified:$9.Specified,TableResult:$9.Table,Language:Identifier{Text:$12.text,Pos:$12.pos},Body:$15.Body.text,BodyPos:routineBodyPosition($15.Body),Symbol:$15.Symbol.text,SymbolSpecified:$15.HasSymbol,SymbolPos:$15.Symbol.pos,Replace:true,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$10,$13,$16){return 1};$$=node}
 | CREATE OR replace_word FUNCTION relation_name '(' function_arguments ')' function_result function_attributes AS routine_as function_attributes LANGUAGE name function_attributes {node:=&CreateFunction{Name:$5,Arguments:$7,Result:$9.Type,ResultSpecified:$9.Specified,TableResult:$9.Table,Language:Identifier{Text:$15.text,Pos:$15.pos},Body:$12.Body.text,BodyPos:routineBodyPosition($12.Body),Symbol:$12.Symbol.text,SymbolSpecified:$12.HasSymbol,SymbolPos:$12.Symbol.pos,Replace:true,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$10,$13,$16){return 1};$$=node}
 | CREATE OR replace_word FUNCTION relation_name '(' function_arguments ')' function_result function_attributes standard_routine_body {node:=&CreateFunction{Name:$5,Arguments:$7,Result:$9.Type,ResultSpecified:$9.Specified,TableResult:$9.Table,Language:Identifier{Text:"sql"},Standard:$11,Replace:true,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$10){return 1};$$=node}
 | CREATE OR replace_word FUNCTION relation_name '(' function_arguments ')' function_result function_attributes LANGUAGE name function_attributes standard_routine_body {node:=&CreateFunction{Name:$5,Arguments:$7,Result:$9.Type,ResultSpecified:$9.Specified,TableResult:$9.Table,Language:Identifier{Text:$12.text,Pos:$12.pos},Standard:$14,Replace:true,Pos:$1.pos};if !sqllex.(*lexer).functionAttributes(node,$10,$13){return 1};$$=node} ;
procedure_head: CREATE PROCEDURE relation_name '(' function_arguments ')' {$$=&CreateFunction{Name:$3,Arguments:$5,Procedure:true,Pos:$1.pos}}
 | CREATE OR replace_word PROCEDURE relation_name '(' function_arguments ')' {$$=&CreateFunction{Name:$5,Arguments:$7,Procedure:true,Replace:true,Pos:$1.pos}} ;
procedure_attributes: /* empty */ {$$=nil}
 | procedure_attributes procedure_attribute {$$=append($1,$2)} ;
procedure_attribute: function_attribute {$$=$1}
 | WINDOW {$$=RoutineAttribute{Kind:"window",Pos:$1.pos}}
 | NOT procedure_leakproof_word {$$=RoutineAttribute{Kind:"leakproof",Value:"false",Pos:$1.pos}}
 | ROUTINE_SUPPORT relation_name {$$=RoutineAttribute{Kind:"support",Pos:$1.pos}} ;
procedure_rows_word: FRAME_UNIT {if $1.text!="rows" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
procedure_input_word: KW_U {if $1.keyword==nil || $1.text!="input" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
procedure_leakproof_word: KW_U {if $1.keyword==nil || $1.text!="leakproof" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
routine_as: STRING {$$=&routineAS{Body:$1}}
 | STRING ',' STRING {$$=&routineAS{Body:$1,Symbol:$3,HasSymbol:true}} ;
function_attributes: /* empty */ {$$=nil}
 | function_attributes function_attribute {$$=append($1,$2)} ;
function_attribute_word: KW_U {if $1.keyword==nil||($1.text!="strict"&&$1.text!="volatile"&&$1.text!="stable"&&$1.text!="immutable"&&$1.text!="leakproof") {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
function_attribute: function_attribute_word {$$=RoutineAttribute{Kind:$1.text,Pos:$1.pos}}
 | ROUTINE_CALLED ON NULL procedure_input_word {$$=RoutineAttribute{Kind:"strict",Value:"false",Pos:$1.pos}}
 | RETURNS_CLAUSE NULL ON NULL procedure_input_word {$$=RoutineAttribute{Kind:"strict",Value:"true",Pos:$1.pos}}
 | ROUTINE_COST grant_object_number {$$=RoutineAttribute{Kind:"cost",Value:$2.text,Pos:$1.pos}}
 | procedure_rows_word grant_object_number {$$=RoutineAttribute{Kind:"rows",Value:$2.text,Pos:$1.pos}}
 | ROUTINE_PARALLEL name {$$=RoutineAttribute{Kind:"parallel",Value:$2.text,Pos:$1.pos}}
 | ROUTINE_SECURITY routine_security_word {$$=RoutineAttribute{Kind:"security",Value:$2.text,Pos:$1.pos}}
 | ROUTINE_EXTERNAL ROUTINE_SECURITY routine_security_word {$$=RoutineAttribute{Kind:"security",Value:$3.text,Pos:$1.pos}}
 | routine_setting {$$=$1} ;
routine_setting: SET setting_name setting_assignment setting_values {$$=RoutineAttribute{Kind:"set",Setting:parsedSet($2.identifier(),$4,$1.pos),Pos:$1.pos}}
 | SET setting_name setting_assignment DEFAULT {$$=RoutineAttribute{Kind:"set",Setting:&Set{Name:$2.identifier(),Default:true,Pos:$1.pos},Pos:$1.pos}}
 | SET setting_name FROM routine_current_word {$$=RoutineAttribute{Kind:"set",Setting:&Set{Name:$2.identifier(),FromCurrent:true,Pos:$1.pos},Pos:$1.pos}} ;
routine_current_word: KW_U {if $1.keyword==nil||$1.text!="current" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
routine_security_word: KW_U {if $1.keyword==nil||($1.text!="definer"&&$1.text!="invoker") {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
alter_routine_attributes: function_attribute {$$=[]RoutineAttribute{$1}}
 | RESET setting_name {$$=[]RoutineAttribute{{Kind:"set",Setting:&Set{Name:$2.identifier(),Default:true,Pos:$1.pos},Pos:$1.pos}}}
 | RESET ALL {$$=[]RoutineAttribute{{Kind:"resetall",Pos:$1.pos}}}
 | alter_routine_attributes function_attribute {$$=append($1,$2)}
 | alter_routine_attributes RESET setting_name {$$=append($1,RoutineAttribute{Kind:"set",Setting:&Set{Name:$3.identifier(),Default:true,Pos:$2.pos},Pos:$2.pos})}
 | alter_routine_attributes RESET ALL {$$=append($1,RoutineAttribute{Kind:"resetall",Pos:$2.pos})} ;
language_flags: LANGUAGE_TRUSTED {$$=&CreateLanguage{Trusted:true}}
 | LANGUAGE_PROCEDURAL {$$=&CreateLanguage{Procedural:true}}
 | LANGUAGE_TRUSTED LANGUAGE_PROCEDURAL {$$=&CreateLanguage{Trusted:true,Procedural:true}} ;
language_head: CREATE LANGUAGE name {$$=&CreateLanguage{Name:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos}}
 | CREATE language_flags LANGUAGE name {$$=$2;$$.Name=Identifier{Text:$4.text,Pos:$4.pos};$$.Pos=$1.pos}
 | CREATE OR replace_word LANGUAGE name {$$=&CreateLanguage{Name:Identifier{Text:$5.text,Pos:$5.pos},Replace:true,Pos:$1.pos}}
 | CREATE OR replace_word language_flags LANGUAGE name {$$=$4;$$.Name=Identifier{Text:$6.text,Pos:$6.pos};$$.Replace=true;$$.Pos=$1.pos} ;
create_language: language_head FDW_HANDLER relation_name language_inline language_validator {$$=$1;$1.Handler=$3;$1.Inline=$4;$1.Validator=$5} ;
language_inline: /* empty */ {$$=ObjectName{}} | LANGUAGE_INLINE relation_name {$$=$2} ;
language_validator: /* empty */ {$$=ObjectName{}} | FDW_VALIDATOR relation_name {$$=$2} ;
do_statement: DO STRING {$$=&Do{Body:$2.text,BodyPos:routineBodyPosition($2),Language:Identifier{Text:"plpgsql"},Pos:$1.pos}}
 | DO LANGUAGE name STRING {$$=&Do{Body:$4.text,BodyPos:routineBodyPosition($4),Language:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos}}
 | DO STRING LANGUAGE name {$$=&Do{Body:$2.text,BodyPos:routineBodyPosition($2),Language:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos}} ;
pl_final_semicolon: /* empty */ | ';' ;
pl_label: /* empty */ {$$=Identifier{}}
 | PL_LABEL_OPEN pl_name PL_LABEL_CLOSE {$$=Identifier{Text:$2.text,Pos:$2.pos}} ;
pl_end_label: /* empty */ {$$=Identifier{}}
 | pl_name {$$=Identifier{Text:$1.text,Pos:$1.pos}} ;
pl_block: pl_label pl_declarations BEGIN pl_statements pl_exceptions END pl_end_label {$$=sqllex.(*lexer).plBlock($1,$2,$4,$7,$3.pos);$$.Handlers=$5;if sqllex.(*lexer).err!=nil{return 1}} ;
pl_exceptions: /* empty */ {$$=nil} | PL_EXCEPTION pl_handlers {$$=$2} ;
pl_handlers: pl_handler {$$=[]PLHandler{$1}} | pl_handlers pl_handler {$$=append($1,$2)} ;
pl_handler: WHEN pl_conditions THEN pl_statements {$$=PLHandler{Conditions:$2,Statements:$4,Pos:$1.pos}} ;
pl_conditions: pl_condition {$$=[]PLCondition{$1}} | pl_conditions OR pl_condition {$$=append($1,$3)} ;
pl_condition: pl_name {$$=PLCondition{Name:Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos}}
 | PL_SQLSTATE STRING {$$=PLCondition{Code:$2.text,Pos:$2.pos};if !validPLSQLState($2.text){sqllex.(*lexer).failAt("42601","invalid SQLSTATE code at or near \""+$2.raw+"\"",$2.pos.Offset);return 1}} ;
pl_declarations: /* empty */ {$$=nil}
 | DECLARE pl_declaration_list {$$=$2} ;
pl_declaration_list: /* empty */ {$$=nil}
 | pl_declaration_list pl_declaration ';' {$$=append($1,*$2)} ;
pl_declaration: pl_declaration_name pl_constant pl_type column_collation pl_not_null pl_default {$$=&PLDeclaration{Name:Identifier{Text:$1.text,Pos:$1.pos},Constant:$2,Type:*$3,Collation:$4,NotNull:$5.text!="",NotNullPos:$5.pos,Default:$6.Expression,DefaultStart:$6.Start,DefaultEnd:$6.End,DefaultDelimiter:$6.Delimiter}}
 | pl_declaration_name PL_ALIAS FOR pl_target {$$=&PLDeclaration{Name:Identifier{Text:$1.text,Pos:$1.pos},Alias:$4}}
 | pl_declaration_name cursor_scroll CURSOR pl_cursor_arguments pl_cursor_for pl_cursor_query {query:=sqllex.(*lexer).plBoundCursorQuery($6,$5);$$=&PLDeclaration{Name:Identifier{Text:$1.text,Pos:$1.pos},Cursor:&PLCursorDeclaration{Scroll:CursorScroll($2),Arguments:$4,Query:query.Query,QueryStart:query.Start,QueryEnd:query.End,QueryDelimiter:query.Delimiter}}} ;
pl_constant: /* empty */ {$$=false} | PL_CONSTANT {$$=true} ;
pl_not_null: /* empty */ {$$=lexeme{}} | NOT NULL {$$=$1} ;
pl_type: declared_type {$$=&PLType{Type:$1}}
 | declared_type '%' PL_TYPE_COPY {$$=cell(plCopiedType($1,false))}
 | declared_type '.' name '%' PL_TYPE_COPY {$$=cell(plCopiedType($1,false));$$.Copy.Parts=append($$.Copy.Parts,Identifier{Text:$3.text,Pos:$3.pos})}
 | declared_type '%' PL_ROW_COPY {$$=cell(plCopiedType($1,true))} ;
pl_default: /* empty */ {$$=PLExpression{}} | DEFAULT pl_expression {$$=sqllex.(*lexer).plBoundExpression($2,$1)} | PL_ASSIGN pl_expression {$$=sqllex.(*lexer).plBoundExpression($2,$1)} | '=' pl_expression {$$=sqllex.(*lexer).plBoundExpression($2,$1)} ;
pl_statements: /* empty */ {sqllex.(*lexer).plStatementBoundaryPending=true;$$=nil}
 | pl_statements pl_statement ';' {l:=sqllex.(*lexer);if command:=$2.SQL;command!=nil{command.finishSQL(l.input,$<tok>3.pos)};if dynamic:=$2.Dynamic;dynamic!=nil{dynamic.finishDynamic($<tok>3.pos)};if diagnostics:=$2.Diagnostics;diagnostics!=nil{diagnostics.finishSource(l.input,$<tok>3.pos)};if loop:=$2.IntegerFor;loop!=nil{l.plCheckEndLabel(loop.Body.Label,loop.Body.EndLabel);if l.err!=nil{return 1}};l.plStatementBoundaryPending=true;$$=append($1,*$2)} ;
pl_statement: pl_block {$$=&PLStatement{Block:$1,Pos:$1.Pos}}
 | pl_static_statement {l:=sqllex.(*lexer);$$=cell(l.plSQLStatement($1));if l.err!=nil{return 1}}
 | pl_dynamic {$$=&PLStatement{Dynamic:$1,Pos:$1.Start}}
 | COMMIT opt_chain {$$=&PLStatement{Transaction:plTransaction(KindCommit,$2,$1.pos),Pos:$1.pos}}
 | ROLLBACK opt_chain {$$=&PLStatement{Transaction:plTransaction(KindRollback,$2,$1.pos),Pos:$1.pos}}
 | PL_RETURN_QUERY pl_dynamic {$2.ReturnQuery=true;$$=&PLStatement{Dynamic:$2,Pos:$1.pos}}
 | pl_diagnostics_head pl_diagnostic_assignments {l:=sqllex.(*lexer);$1.Assignments=$2;if !l.finishPLDiagnostics($1){return 1};$$=&PLStatement{Diagnostics:$1,Pos:$1.Pos}}
 | PL_RAISE {$$=&PLStatement{Rethrow:true,Pos:$1.pos}}
 | PL_RAISE pl_raise_body {$$=&PLStatement{Raise:$2,Pos:$1.pos}}
 | PL_RAISE PL_RAISE_LEVEL pl_raise_body {$3.Level=plRaiseLevel($2);$$=&PLStatement{Raise:$3,Pos:$1.pos}}
 | pl_assert_head pl_expression pl_assert_message {$$=&PLStatement{Assert:&PLAssert{Condition:sqllex.(*lexer).plBoundExpression($2,$1),Message:$3},Pos:$1.pos}}
 | pl_target PL_ASSIGN pl_expression {value:=sqllex.(*lexer).plBoundExpression($3,$2);$$=&PLStatement{Target:$1,Expression:value.Expression,ExpressionStart:value.Start,ExpressionEnd:value.End,ExpressionDelimiter:value.Delimiter,Pos:$1.SourcePosition()}}
 | pl_target '=' pl_expression {value:=sqllex.(*lexer).plBoundExpression($3,$2);$$=&PLStatement{Target:$1,Expression:value.Expression,ExpressionStart:value.Start,ExpressionEnd:value.End,ExpressionDelimiter:value.Delimiter,Pos:$1.SourcePosition()}}
 | pl_target {sqllex.(*lexer).plBareTarget($1);return 1}
 | PL_RETURN pl_return_value {value:=sqllex.(*lexer).plBoundExpression($2,$1);$$=&PLStatement{Return:true,Expression:value.Expression,ExpressionStart:value.Start,ExpressionEnd:value.End,ExpressionDelimiter:value.Delimiter,Pos:$1.pos}}
 | pl_if_head pl_if_condition pl_statements pl_elsif_arms pl_else END IF {condition:=sqllex.(*lexer).plBoundExpression($2,$1);arms:=append([]PLArm{{Conditions:[]PLExpression{condition},Statements:$3}},$4...);$$=&PLStatement{If:&PLConditional{Arms:arms,HasElse:$5.HasElse,Else:$5.Else},Pos:$1.pos}}
 | PL_OPEN pl_target pl_open_tail {$3.Action=PLOpenCursor;$3.Variable=$2;$$=&PLStatement{Cursor:$3,Pos:$1.pos}}
 | FETCH pl_cursor_motion pl_cursor_from pl_target INTO pl_cursor_targets {cursor:=sqllex.(*lexer).plCursorTargets(&PLCursorStatement{Action:PLFetchCursor,Variable:$4,Motion:*$2,Targets:$6},$5);$$=&PLStatement{Cursor:cursor,Pos:$1.pos}}
 | MOVE pl_cursor_motion pl_cursor_from pl_target {$$=&PLStatement{Cursor:&PLCursorStatement{Action:PLMoveCursor,Variable:$4,Motion:*$2},Pos:$1.pos}}
 | CLOSE pl_target {$$=&PLStatement{Cursor:&PLCursorStatement{Action:PLCloseCursor,Variable:$2},Pos:$1.pos}}
 | pl_label PL_INTEGER_FOR pl_integer_targets pl_integer_range pl_statements END PL_LOOP pl_end_label {$4.Targets=$3.Targets;$4.TargetCommas=$3.TargetCommas;at:=$4.Upper.Delimiter;if $4.Step.Expression!=nil{at=$4.Step.Delimiter};$4.Body=&PLBlock{Label:$1,EndLabel:$8,IsLoop:true,Statements:$5,Pos:at};$$=&PLStatement{IntegerFor:$4,Pos:$2.pos}}
 | pl_label FOR pl_target IN pl_target pl_cursor_actuals PL_LOOP pl_statements END PL_LOOP pl_end_label {l:=sqllex.(*lexer);name,ok:=$3.(ColumnExpr);if !ok||len(name.Name.Parts)!=1{l.failAt("42601","invalid cursor FOR loop variable",$3.SourcePosition().Offset);return 1};$6.Action=PLForCursor;$6.Variable=$5;$6.LoopName=name.Name.Parts[0];$6.Loop=l.plLoop($1,$8,$11,$7.pos);$$=&PLStatement{Cursor:$6,Pos:$2.pos};if l.err!=nil{return 1}}
 | pl_label FOR pl_target IN pl_query_loop_source PL_LOOP pl_statements END PL_LOOP pl_end_label {l:=sqllex.(*lexer);$5.Target=$3;$5.Start=l.plSourceStart($5.End,$5.Delimiter,$4);$5.Body=l.plLoop($1,$7,$10,$6.pos);$$=&PLStatement{QueryLoop:$5,Pos:$2.pos};if l.err!=nil{return 1}}
 | pl_label PL_FOREACH pl_foreach_targets pl_foreach_slice IN pl_foreach_array pl_expression PL_LOOP pl_statements END PL_LOOP pl_end_label {l:=sqllex.(*lexer);loop:=$4;loop.Targets=$3.Targets;loop.TargetCommas=$3.TargetCommas;loop.Source=l.plBoundExpression($7,$6);loop.Body=l.plLoop($1,$9,$12,$8.pos);l.plAfterForeach=true;$$=&PLStatement{Foreach:loop,Pos:$2.pos};if l.err!=nil{return 1}}
 | pl_label PL_LOOP pl_statements END PL_LOOP pl_end_label {$$=&PLStatement{Loop:sqllex.(*lexer).plLoop($1,$3,$6,$2.pos),Pos:$2.pos};if sqllex.(*lexer).err!=nil{return 1}}
 | pl_label pl_while_head pl_integer_operand PL_LOOP pl_statements END PL_LOOP pl_end_label {l:=sqllex.(*lexer);condition:=l.plDelimitedExpression($3,$2,$4);$$=&PLStatement{While:&PLWhile{Condition:condition,Body:l.plLoop($1,$5,$8,$4.pos)},Pos:$2.pos};if l.err!=nil{return 1}}
 | pl_transfer_kind pl_end_label pl_transfer_when {value:=$3;$$=&PLStatement{Transfer:&PLTransfer{Continue:$1.text=="continue",Label:$2,Alias:value.Alias},Expression:value.Expression,ExpressionStart:value.Start,ExpressionEnd:value.End,ExpressionDelimiter:value.Delimiter,Pos:$1.pos}}
 | pl_case_head pl_search pl_case_arms pl_else END CASE {l:=sqllex.(*lexer);search:=l.plBoundExpression($2,$1);if search.Expression==nil {for _,arm:=range $3 {if len(arm.Conditions)!=1 {l.failAt("42601","syntax error at or near \",\"",arm.Conditions[0].Delimiter.Offset);return 1}}};$$=&PLStatement{Case:&PLConditional{Search:search,Arms:$3,HasElse:$4.HasElse,Else:$4.Else},Pos:$1.pos}} ;
pl_diagnostics_head: PL_GET PL_DIAGNOSTICS {$$=&PLDiagnostics{Pos:$1.pos}}
 | PL_GET PL_CURRENT PL_DIAGNOSTICS {$$=&PLDiagnostics{Pos:$1.pos}}
 | PL_GET PL_STACKED PL_DIAGNOSTICS {$$=&PLDiagnostics{Stacked:true,Pos:$1.pos}} ;
pl_diagnostic_assignments: pl_diagnostic_assignment {$$=[]PLDiagnosticAssignment{$1}}
 | pl_diagnostic_assignments ',' pl_diagnostic_assignment {$$=append($1,$3)} ;
pl_diagnostic_equal: '=' {$$=$<tok>1} | PL_ASSIGN ;
pl_diagnostic_assignment: pl_target pl_diagnostic_equal pl_diagnostic_item {l:=sqllex.(*lexer);item,ok:=l.plDiagnosticItem($3);if !ok{return 1};$$=PLDiagnosticAssignment{Target:$1,Item:item,ItemPos:$3.pos}} ;
pl_diagnostic_item: col_label | ';' {$$=$<tok>1} ;
pl_return_value: /* empty */ {$$=PLExpression{}} | pl_expression {$$=$1} ;
pl_dynamic_head: PL_EXECUTE {sqllex.(*lexer).plBeginExpression(plDynamicCommandBoundary);$$=$1} ;
pl_dynamic_command: pl_dynamic_head pl_expression {$$=&PLDynamicStatement{Start:$1.pos,Command:sqllex.(*lexer).plBoundExpression($2,$1)}} ;
pl_dynamic: pl_dynamic_command {$$=$1}
 | pl_dynamic pl_dynamic_using pl_dynamic_values {$$=sqllex.(*lexer).plDynamicUsing($1,$2,$3);if sqllex.(*lexer).err!=nil{return 1}} 
 | pl_dynamic pl_dynamic_into pl_dynamic_strict pl_foreach_targets {$$=sqllex.(*lexer).plDynamicInto($1,$2,$4,$3);if sqllex.(*lexer).err!=nil{return 1}} ;
pl_dynamic_using: USING {sqllex.(*lexer).plBeginExpression(plDynamicUsingBoundary);$$=$1}
 | PL_DYNAMIC_USING {sqllex.(*lexer).plBeginExpression(plDynamicUsingBoundary);$$=$1} ;
pl_dynamic_into: INTO {$$=$1} | PL_DYNAMIC_INTO {$$=$1} ;
pl_dynamic_strict: /* empty */ {$$=false} | PL_STRICT {$$=true} ;
pl_dynamic_comma: PL_DYNAMIC_COMMA {sqllex.(*lexer).plBeginExpression(plDynamicUsingBoundary);$$=$1} ;
pl_dynamic_values: pl_expression {$$=[]PLExpression{$1}}
 | pl_dynamic_values pl_dynamic_comma pl_expression {$$=append($1,sqllex.(*lexer).plBoundExpression($3,$2))} ;
pl_transfer_kind: PL_EXIT {$$=$1} | PL_CONTINUE {$$=$1} ;
pl_transfer_when: /* empty */ {$$=PLExpression{}}
 | pl_transfer_when_head pl_transfer_expression {$$=sqllex.(*lexer).plBoundExpression($2,$1)} ;
pl_transfer_when_head: WHEN {sqllex.(*lexer).plBeginExpression(plSemicolonBoundary);$$=$1} ;
pl_while_head: PL_WHILE {sqllex.(*lexer).plBeginExpression(plLoopBoundary);$$=$1} ;
pl_transfer_expression: pl_expression_start projection_item {l:=sqllex.(*lexer);l.plExpression=false;$$=l.plParsedExpression($2.Targets[0]);$$.Alias=$2.Labels[0]} ;
pl_expression: pl_expression_start scalar_projection {l:=sqllex.(*lexer);l.plExpression=false;$$=l.plParsedExpression($2)} ;
pl_expression_start: /* empty */ {sqllex.(*lexer).plExpression=true} ;
pl_raise_body: STRING pl_raise_arguments pl_raise_options {$$=&PLRaise{Form:PLRaiseFormat,Format:$1.text,Arguments:$2,Options:$3}}
 | pl_condition pl_raise_options {form:=PLRaiseCondition;if $1.Code!=""{form=PLRaiseSQLState};$$=&PLRaise{Form:form,Condition:$1,Options:$2}}
 | pl_raise_using pl_raise_option_list {$$=&PLRaise{Form:PLRaiseUsing,Options:$2}} ;
pl_raise_arguments: /* empty */ {$$=nil}
 | pl_raise_arguments pl_message_comma pl_expression {$$=append($1,sqllex.(*lexer).plBoundExpression($3,$2))} ;
pl_message_comma: ',' {sqllex.(*lexer).plBeginExpression(plMessageBoundary);$$=$1}
 | PL_MESSAGE_COMMA {sqllex.(*lexer).plBeginExpression(plMessageBoundary);$$=$1} ;
pl_raise_using: USING {$$=$1} | PL_RAISE_USING {$$=$1} ;
pl_raise_options: /* empty */ {$$=nil} | pl_raise_using pl_raise_option_list {$$=$2} ;
pl_raise_option_list: pl_raise_option {$$=[]PLRaiseOption{*$1}}
 | pl_raise_option_list PL_MESSAGE_COMMA pl_raise_option {$$=append($1,*$3)} ;
pl_raise_option_name: col_label {$$=cell(sqllex.(*lexer).plRaiseOption($1));if sqllex.(*lexer).err!=nil{return 1}} ;
pl_option_assign: '=' {sqllex.(*lexer).plBeginExpression(plOptionBoundary);$$=$1}
 | PL_ASSIGN {sqllex.(*lexer).plBeginExpression(plOptionBoundary);$$=$1} ;
pl_raise_option: pl_raise_option_name pl_option_assign pl_expression {$1.Value=sqllex.(*lexer).plBoundExpression($3,$2);$$=$1} ;
pl_assert_head: PL_ASSERT {sqllex.(*lexer).plBeginExpression(plMessageBoundary);$$=$1} ;
pl_assert_message: /* empty */ {$$=PLExpression{}}
 | pl_message_comma pl_expression {$$=sqllex.(*lexer).plBoundExpression($2,$1)} ;
pl_if_condition: pl_expression PL_THEN {sqllex.(*lexer).plThen=false;$$=$1} ;
pl_if_head: IF {sqllex.(*lexer).plBeginExpression(plThenBoundary);$$=$1} ;
pl_elsif_head: PL_ELSIF {sqllex.(*lexer).plBeginExpression(plThenBoundary);$$=$1} ;
pl_when_head: WHEN {sqllex.(*lexer).plBeginExpression(plWhenItemBoundary);$$=$1}
 | PL_WHEN {sqllex.(*lexer).plBeginExpression(plWhenItemBoundary);$$=$1} ;
pl_case_head: CASE {sqllex.(*lexer).plBeginExpression(plWhenBoundary);$$=$1} ;
pl_when_separator: PL_WHEN_COMMA {sqllex.(*lexer).plBeginExpression(plWhenItemBoundary);$$=$1} ;
pl_search: /* empty */ {$$=PLExpression{}} | pl_expression {$$=$1} ;
pl_elsif_arms: /* empty */ {$$=nil}
 | pl_elsif_arms pl_elsif_head pl_if_condition pl_statements {condition:=sqllex.(*lexer).plBoundExpression($3,$2);$$=append($1,PLArm{Conditions:[]PLExpression{condition},Statements:$4})} ;
pl_else: /* empty */ {$$=plElse{}}
 | ELSE pl_statements {$$=plElse{HasElse:true,Else:$2}} ;
pl_case_arms: pl_case_arm {$$=[]PLArm{$1}}
 | pl_case_arms pl_case_arm {$$=append($1,$2)} ;
pl_case_arm: pl_case_condition pl_statements {$$=PLArm{Conditions:$1,Statements:$2}} ;
pl_case_condition: pl_when_head pl_when_items PL_THEN {l:=sqllex.(*lexer);l.plThen=false;conditions:=$2;conditions[0]=l.plBoundExpression(conditions[0],$1);$$=conditions} ;
pl_when_items: pl_expression {$$=[]PLExpression{$1}}
 | pl_when_items pl_when_separator pl_expression {$$=append($1,sqllex.(*lexer).plBoundExpression($3,$2))} ;
pl_target: pl_name {$$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos}}
 | PARAM {$$=ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos}}
 | pl_target '.' col_label {$$=FieldExpr{Base:$1,Field:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.SourcePosition()}}
 | pl_target array_subscript {if base,ok:=$1.(SubscriptExpr);ok {base.Subscripts=append(base.Subscripts,$2);$$=base} else {$$=SubscriptExpr{Base:$1,Subscripts:[]ArraySubscript{$2},Pos:$1.SourcePosition()}}} ;
pl_name: IDENT | SELECTED_FUNC | KW_U | XML | KW_C | KW_T | RETURN_BODY | PL_SLICE | FIRST | SECOND
 | ROUTINE_CALLED | ROUTINE_COST | ROUTINE_SUPPORT | ROUTINE_PARALLEL | ROUTINE_SECURITY | ROUTINE_EXTERNAL ;
pl_foreach_target: pl_name {$$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos}}
 | PARAM {$$=ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos}}
 | pl_foreach_target '.' col_label {$$=FieldExpr{Base:$1,Field:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.SourcePosition()}} ;
pl_foreach_targets: pl_foreach_target {$$=&PLForeach{Targets:[]Expr{$1}}}
 | pl_foreach_targets ',' pl_foreach_target {$$=$1;$$.Targets=append($$.Targets,$3);$$.TargetCommas=append($$.TargetCommas,$2.pos)} ;
pl_declaration_name: pl_name | PL_ELSIF ;
pl_cursor_arguments: /* empty */ {$$=nil}
 | '(' pl_cursor_argument_list ')' {$$=$2} ;
pl_cursor_argument_list: pl_cursor_argument {$$=[]PLDeclaration{*$1}}
 | pl_cursor_argument_list ',' pl_cursor_argument {$$=append($1,*$3)} ;
pl_cursor_argument: pl_name pl_type {$$=&PLDeclaration{Name:Identifier{Text:$1.text,Pos:$1.pos},Type:*$2}} ;
pl_cursor_for: FOR | IS ;
pl_cursor_query: pl_expression_start query_select {l:=sqllex.(*lexer);l.plExpression=false;$$=plCursorQuery{Query:$2,End:l.plPriorTokenEnd,Delimiter:l.current.pos}} ;
pl_query_loop_source: pl_query_loop_start pl_query_loop_statement {l:=sqllex.(*lexer);l.plExpression=false;$$=&PLQueryLoop{Query:$2,End:l.plPriorTokenEnd,Delimiter:l.current.pos}} ;
pl_query_loop_statement: query_select {$$=$1} | insert {$$=$1} | update {$$=$1} | delete {$$=$1} | merge {$$=$1} | with_mutation {$$=$1} ;
pl_static_statement: embedded_sql_statement {$$=$1} | query_select {$$=$1} | SET TRANSACTION transaction_modes {$$=&TransactionCharacteristics{Pos:$1.pos,Modes:$3}} ;
pl_query_loop_start: /* empty */ {sqllex.(*lexer).plBeginExpression(plLoopBoundary)} ;
pl_integer_targets: pl_target {$$=&PLIntegerFor{Targets:[]Expr{$1}}}
 | pl_integer_targets ',' pl_target {$$=$1;$$.Targets=append($$.Targets,$3);$$.TargetCommas=append($$.TargetCommas,$2.pos)} ;
pl_integer_in: IN {sqllex.(*lexer).plBeginExpression(plRangeLowerBoundary);$$=$1} ;
pl_integer_reverse: /* empty */ {$$=lexeme{}} | PL_REVERSE {$$=$1} ;
pl_integer_operand: pl_expression_start projection_item {sqllex.(*lexer).plExpression=false;$$=PLExpression{Expression:$2.Targets[0],Alias:$2.Labels[0]}} ;
pl_integer_lower: pl_integer_in pl_integer_reverse pl_integer_operand DOT_DOT {l:=sqllex.(*lexer);predecessor:=$1;if $2.pos.Character!=0{predecessor=$2};$$=&PLIntegerFor{Lower:l.plDelimitedExpression($3,predecessor,$4),Reverse:$2.pos.Character!=0,ReversePos:$2.pos};l.plBeginExpression(plRangeUpperBoundary)} ;
pl_integer_upper: pl_integer_lower pl_integer_operand {$1.Upper=$2;$$=$1} ;
pl_integer_step: pl_integer_upper PL_BY {l:=sqllex.(*lexer);$1.Upper=l.plDelimitedExpression($1.Upper,lexeme{pos:$1.Lower.Delimiter},$2);l.plBeginExpression(plLoopBoundary);$$=$1} ;
pl_integer_range: pl_integer_upper PL_LOOP {$1.Upper=sqllex.(*lexer).plDelimitedExpression($1.Upper,lexeme{pos:$1.Lower.Delimiter},$2);$$=$1}
 | pl_integer_step pl_integer_operand PL_LOOP {$1.Step=sqllex.(*lexer).plDelimitedExpression($2,lexeme{pos:$1.Upper.Delimiter},$3);$$=$1} ;
pl_foreach_slice: /* empty */ {$$=&PLForeach{}}
 | PL_SLICE NUMBER {$$=sqllex.(*lexer).plForeachSlice($1,$2);if sqllex.(*lexer).err!=nil{return 1}} ;
pl_foreach_array: ARRAY {l:=sqllex.(*lexer);l.plForeachSource=$1.pos;l.plBeginExpression(plLoopBoundary);$$=$1} ;
pl_open_tail: pl_cursor_actuals {$$=$1}
 | cursor_scroll FOR pl_cursor_query {query:=sqllex.(*lexer).plBoundCursorQuery($3,$2);$$=&PLCursorStatement{Scroll:CursorScroll($1),Query:query.Query,QueryStart:query.Start,QueryEnd:query.End,QueryDelimiter:query.Delimiter}}
 | cursor_scroll FOR EXECUTE pl_expression pl_using {command:=sqllex.(*lexer).plBoundExpression($4,$3);$$=&PLCursorStatement{Scroll:CursorScroll($1),Command:command.Expression,CommandStart:command.Start,CommandEnd:command.End,CommandDelimiter:command.Delimiter,Using:$5}} ;
pl_cursor_actuals: /* empty */ {$$=&PLCursorStatement{ArgumentsStart:sqllex.(*lexer).current.pos}}
 | '(' pl_cursor_actual_list ')' {$2[0]=sqllex.(*lexer).plBoundCursorActual($2[0],$1);$$=&PLCursorStatement{Arguments:$2,HasArguments:true,ArgumentsStart:$1.pos,ArgumentsEnd:$3.pos}} ;
pl_cursor_actual_list: pl_cursor_actual {$$=[]PLCursorActual{*$1}}
 | pl_cursor_actual_list ',' pl_cursor_actual {*$3=sqllex.(*lexer).plBoundCursorActual(*$3,$2);$3.Separator=$2.pos;$$=append($1,*$3)} ;
pl_cursor_actual: pl_expression {$$=cell(sqllex.(*lexer).plCursorActual($1))}
 | pl_expression EQUALS_GREATER pl_expression {$$=cell(sqllex.(*lexer).plNamedCursorActual($1,$3,$2));if sqllex.(*lexer).err!=nil{return 1}}
 | pl_expression PL_ASSIGN pl_expression {$$=cell(sqllex.(*lexer).plNamedCursorActual($1,$3,$2));if sqllex.(*lexer).err!=nil{return 1}} ;
pl_using: /* empty */ {$$=nil} | USING pl_using_values {$2[0]=sqllex.(*lexer).plBoundExpression($2[0],$1);$$=$2} ;
pl_using_values: pl_expression {$$=[]PLExpression{$1}} | pl_using_values ',' pl_expression {$$=append($1,sqllex.(*lexer).plBoundExpression($3,$2))} ;
pl_cursor_targets: pl_target {$$=[]Expr{$1}} | pl_cursor_targets ',' pl_target {$$=append($1,$3)} ;
pl_cursor_from: /* empty */ {$$=lexeme{}} | FROM | IN ;
pl_cursor_motion: /* empty */ {$$=&PLCursorMotion{}}
 | PL_NEXT {$$=&PLCursorMotion{}}
 | PL_PRIOR {$$=&PLCursorMotion{Direction:CursorBackward}}
 | FIRST {$$=cell(sqllex.(*lexer).plFixedCursorMotion(1,$1))}
 | PL_LAST {$$=cell(sqllex.(*lexer).plFixedCursorMotion(-1,$1))}
 | ABSOLUTE pl_expression {$$=cell(sqllex.(*lexer).plCursorMotion(CursorAbsolute,$2,false,$1))}
 | RELATIVE pl_expression {$$=cell(sqllex.(*lexer).plCursorMotion(CursorRelative,$2,false,$1))}
 | FORWARD {$$=&PLCursorMotion{}}
 | BACKWARD {$$=&PLCursorMotion{Direction:CursorBackward}}
 | FORWARD pl_expression {$$=cell(sqllex.(*lexer).plCursorMotion(CursorForward,$2,true,$1))}
 | BACKWARD pl_expression {$$=cell(sqllex.(*lexer).plCursorMotion(CursorBackward,$2,true,$1))}
 | ALL {$$=&PLCursorMotion{All:true,Counted:true}}
 | FORWARD ALL {$$=&PLCursorMotion{All:true,Counted:true}}
 | BACKWARD ALL {$$=&PLCursorMotion{Direction:CursorBackward,All:true,Counted:true}} ;
routine_return_head: RETURN_BODY {sqllex.(*lexer).routineBodyDepth++;$$=$1} ;
routine_atomic_head: BEGIN ATOMIC_BODY {sqllex.(*lexer).routineBodyDepth++;$$=$1} ;
standard_routine_body: routine_return_head target {sqllex.(*lexer).routineBodyDepth--;$$=[]Statement{&Select{Targets:[]Expr{$2},Pos:$1.pos}}}
 | routine_atomic_head routine_body_list END {sqllex.(*lexer).routineBodyDepth--;$$=$2} ;
routine_body_list: /* empty */ {$$=make([]Statement,0)}
 | routine_body_list ';' {$$=$1}
 | routine_body_list routine_body_statement ';' {$$=append($1,$2)} ;
routine_body_statement: nontransaction_statement {$$=$1}
 | transaction_common {$$=$1}
 | COMMIT opt_transaction opt_chain {$$=&TransactionControl{Kind:KindCommit,Chain:$3,Pos:$1.pos}}
 | routine_return_head target {sqllex.(*lexer).routineBodyDepth--;$$=&Select{Targets:[]Expr{$2},Pos:$1.pos}} ;
function_arguments: routine_arguments_start optional_function_arguments {$$=$2} ;
routine_arguments_start: /* empty */ {l:=sqllex.(*lexer);l.routineFormalDepth=l.jsonParenDepth;l.routineFormalDefault=false} ;
optional_function_arguments: /* empty */ {$$=nil} | function_declaration_argument_list {$$=$1} ;
function_declaration_argument_list: function_declaration_argument {$$=[]RoutineParameter{*$1}}
 | function_declaration_argument_list ',' function_declaration_argument {$$=append($1,*$3)} ;
function_declaration_argument: grant_routine_argument {$$=&RoutineParameter{GrantRoutineArgument:*$1}}
 | grant_routine_argument DEFAULT target {$$=&RoutineParameter{GrantRoutineArgument:*$1,Default:$3}}
 | grant_routine_argument '=' target {$$=&RoutineParameter{GrantRoutineArgument:*$1,Default:$3}} ;
function_result: /* empty */ {$$=&routineResult{}}
 | returns_word declared_type {$$=&routineResult{Type:$2,Specified:true}}
 | returns_word SETOF_TYPE declared_type {typ:=$3;typ.Setof=true;$$=&routineResult{Type:typ,Specified:true}}
 | returns_word TABLE '(' table_function_columns ')' {$$=&routineResult{Type:TypeName{Name:"record",Setof:true,Pos:$2.pos},Specified:true,Table:$4}} ;
table_function_columns: table_function_column {$$=[]RoutineParameter{*$1}}
 | table_function_columns ',' table_function_column {$$=append($1,*$3)} ;
table_function_column: grant_parameter_name declared_type {$$=&RoutineParameter{GrantRoutineArgument:GrantRoutineArgument{Mode:"table",Name:Identifier{Text:$1.text,Pos:$1.pos},Type:$2,Pos:$1.pos}}} ;
returns_word: RETURNS_CLAUSE {sqllex.(*lexer).routineResultType=true;$$=$1} ;
create_operator: CREATE OPERATOR operator_name '(' operator_options ')' {$$=&CreateOperator{Name:$3,Options:$5,Pos:$1.pos}} ;
operator_name: operator_symbol {$$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}}}
 | name '.' operator_symbol {$$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}}} ;
operator_options: operator_option {$$=[]OperatorOption{*$1}} | operator_options ',' operator_option {$$=append($1,*$3)} ;
operator_option: col_label '=' type {option:=operatorTypeOption($1,$3);$$=&option}
 | col_label '=' SETOF_TYPE type {option:=operatorTypeOption($1,$4);$$=&option;$$.Type.Setof=true;$$.Kind=OperatorOptionTypeArgument;$$.ValuePos=$3.pos}
 | col_label '=' QUALIFIED_OPERATOR '(' name '.' operator_symbol ')' {$$=&OperatorOption{Name:$1.text,HasValue:true,Kind:OperatorOptionNameArgument,Reference:ObjectName{Parts:[]Identifier{{Text:$5.text,Pos:$5.pos},{Text:$7.text,Pos:$7.pos}}},Pos:$1.pos,ValuePos:$3.pos}}
 | col_label '=' operator_symbol {$$=&OperatorOption{Name:$1.text,HasValue:true,Kind:OperatorOptionNameArgument,Reference:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos,ValuePos:$3.pos}}
 | col_label '=' type_name '.' operator_symbol {$$=&OperatorOption{Name:$1.text,HasValue:true,Kind:OperatorOptionNameArgument,Reference:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}},Pos:$1.pos,ValuePos:$3.pos}}
 | col_label '=' operator_option_schema_keyword '.' operator_symbol {$$=&OperatorOption{Name:$1.text,HasValue:true,Kind:OperatorOptionNameArgument,Reference:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}},Pos:$1.pos,ValuePos:$3.pos}}
 | col_label '=' STRING {$$=&OperatorOption{Name:$1.text,HasValue:true,Kind:OperatorOptionStringArgument,Value:$3.text,Pos:$1.pos,ValuePos:$3.pos}}
 | col_label '=' NUMBER {option:=operatorNumericOption($1,$3,"",$3.pos);$$=&option}
 | col_label '=' '+' NUMBER {option:=operatorNumericOption($1,$4,"+",$3.pos);$$=&option}
 | col_label '=' '-' NUMBER {option:=operatorNumericOption($1,$4,"-",$3.pos);$$=&option}
 | col_label {$$=&OperatorOption{Name:$1.text,Pos:$1.pos}} ;
/* Share type_name before '.' with type/function option values. The remaining
 * ColumnName keywords are disjoint from type_name and retain schema legality
 * without the competing name reduction at every identifier token. */
operator_option_schema_keyword: AT | BIGINT | BOOLEAN | INOUT_ARG | INTEGER | INTERVAL
 | JSON_ARRAYAGG | JSON_OBJECTAGG | OUT_ARG | REASSIGN
 | ROLE_CONNECTION | ROLE_ENCRYPTED | ROLE_PASSWORD | ROLE_UNENCRYPTED | ROLE_VALID
 | SQLJSON_ARRAY | SQLJSON_OBJECT | SQLJSON_SCALAR | SQLJSON_SERIALIZE
 | TEXT | TIME | TIMESTAMP | VIEW ;
operator_signature: operator_name '(' type ',' type ')' {$$=&OperatorSignature{Name:$1,Left:$3,Right:$5,Pos:$1.Parts[0].Pos}} ;
operator_signatures: operator_signature {$$=[]OperatorSignature{*$1}} | operator_signatures ',' operator_signature {$$=append($1,*$3)} ;
operator_alter_head: ALTER OPERATOR operator_signature {$$=&AlterOperator{Signature:*$3,Pos:$1.pos}} ;
alter_operator: operator_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1}
 | operator_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | operator_alter_head SET '(' operator_options ')' {$1.Action="options";$1.Options=$4;$$=$1} ;
drop_operator: DROP OPERATOR operator_signatures drop_mode {$$=&DropOperator{Signatures:$3,Mode:$4,Pos:$1.pos}}
 | DROP OPERATOR IF EXISTS operator_signatures drop_mode {$$=&DropOperator{Signatures:$5,IfExists:true,Mode:$6,Pos:$1.pos}} ;
drop_routine: DROP grant_routine_kind grant_routines drop_mode {$$=&DropRoutine{Kind:$2.text,Targets:dropRoutineTargets($3),Mode:$4,Pos:$1.pos}}
 | DROP grant_routine_kind IF EXISTS grant_routines drop_mode {$$=&DropRoutine{Kind:$2.text,Targets:dropRoutineTargets($5),IfExists:true,Mode:$6,Pos:$1.pos}}
 | DROP AGGREGATE aggregate_utility_targets drop_mode {$$=&DropRoutine{Kind:"aggregate",Targets:dropRoutineTargets($3),Mode:$4,Pos:$1.pos}}
 | DROP AGGREGATE IF EXISTS aggregate_utility_targets drop_mode {$$=&DropRoutine{Kind:"aggregate",Targets:dropRoutineTargets($5),IfExists:true,Mode:$6,Pos:$1.pos}} ;
alter_routine: ALTER grant_routine_kind grant_routine RENAME TO name {$$=&AlterRoutine{Kind:$2.text,Routine:routineUtilityTarget($3),Action:"rename",Target:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER grant_routine_kind grant_routine SET SCHEMA name {$$=&AlterRoutine{Kind:$2.text,Routine:routineUtilityTarget($3),Action:"schema",Target:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER grant_routine_kind grant_routine alter_routine_attributes {$$=&AlterRoutine{Kind:$2.text,Routine:routineUtilityTarget($3),Action:"attributes",Attributes:$4,Pos:$1.pos}}
 | ALTER grant_routine_kind grant_routine OWNER TO schema_owner {$$=&AlterRoutine{Kind:$2.text,Routine:routineUtilityTarget($3),Action:"owner",Target:Identifier{Text:$6.text,Pos:$6.pos},OwnerIdentity:$6.keyword!=nil&&$6.keyword.category==Reserved,Pos:$1.pos}}
 | ALTER AGGREGATE aggregate_utility_target RENAME TO name {$$=&AlterRoutine{Kind:"aggregate",Routine:routineUtilityTarget($3),Action:"rename",Target:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER AGGREGATE aggregate_utility_target SET SCHEMA name {$$=&AlterRoutine{Kind:"aggregate",Routine:routineUtilityTarget($3),Action:"schema",Target:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER AGGREGATE aggregate_utility_target OWNER TO schema_owner {$$=&AlterRoutine{Kind:"aggregate",Routine:routineUtilityTarget($3),Action:"owner",Target:Identifier{Text:$6.text,Pos:$6.pos},OwnerIdentity:$6.keyword!=nil&&$6.keyword.category==Reserved,Pos:$1.pos}} ;
method_word: KW_U {if $1.text!="method" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
access_method_kind: INDEX {$$=$1} | TABLE {$$=$1} ;
create_access_method: CREATE ACCESS method_word name TYPE access_method_kind FDW_HANDLER relation_name {$$=&CreateAccessMethod{Name:Identifier{Text:$4.text,Pos:$4.pos},MethodKind:$6.text,Handler:$8,Pos:$1.pos}} ;
drop_access_method: DROP ACCESS method_word name drop_mode {$$=&DropAccessMethod{Name:Identifier{Text:$4.text,Pos:$4.pos},Mode:$5,Pos:$1.pos}}
 | DROP ACCESS method_word IF EXISTS name drop_mode {$$=&DropAccessMethod{Name:Identifier{Text:$6.text,Pos:$6.pos},IfExists:true,Mode:$7,Pos:$1.pos}} ;
create_cast: CREATE CAST '(' declared_type AS declared_type ')' WITH FUNCTION grant_routine cast_context {
 $$=&CreateCast{Source:standaloneType($4),Target:standaloneType($6),Function:routineUtilityTarget($10),Method:"f",Context:$11.text,Pos:$1.pos}
} | CREATE CAST '(' declared_type AS declared_type ')' WITH INOUT_ARG cast_context {
 $$=&CreateCast{Source:standaloneType($4),Target:standaloneType($6),Method:"i",Context:$10.text,Pos:$1.pos}
} | CREATE CAST '(' declared_type AS declared_type ')' WITHOUT FUNCTION cast_context {
 $$=&CreateCast{Source:standaloneType($4),Target:standaloneType($6),Method:"b",Context:$10.text,Pos:$1.pos}
} ;
cast_context: /* empty */ {$$=lexeme{text:"e"}}
 | AS KW_U {
 if $2.text=="assignment" {$$=lexeme{text:"a"}} else if $2.text=="implicit" {$$=lexeme{text:"i"}} else {sqllex.(*lexer).syntax($2);return 1}
} ;
drop_cast: DROP CAST '(' declared_type AS declared_type ')' drop_mode {
 $$=&DropCast{Source:standaloneType($4),Target:standaloneType($6),Mode:$8,Pos:$1.pos}
} | DROP CAST IF EXISTS '(' declared_type AS declared_type ')' drop_mode {
 $$=&DropCast{Source:standaloneType($6),Target:standaloneType($8),Mode:$10,IfExists:true,Pos:$1.pos}
} ;
family_word: KW_U {if $1.text!="family" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
search_word: name {if $1.text!="search" || $1.keyword==nil {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
create_operator_family: CREATE OPERATOR family_word relation_name USING name {$$=&CreateOperatorFamily{Name:$4,Method:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}} ;
operator_family_head: ALTER OPERATOR family_word relation_name USING name {$$=&AlterOperatorFamily{Name:$4,Method:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}} ;
alter_operator_family: operator_family_head ADD family_add_items {$1.Action="add";$1.Items=$3;$$=$1}
 | operator_family_head DROP family_drop_items {$1.Action="drop";$1.Items=$3;$$=$1}
 | operator_family_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | operator_family_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1}
 | operator_family_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1} ;
drop_operator_family: DROP OPERATOR family_word relation_name USING name drop_mode {$$=&DropOperatorFamily{Name:$4,Method:Identifier{Text:$6.text,Pos:$6.pos},Mode:$7,Pos:$1.pos}}
 | DROP OPERATOR family_word IF EXISTS relation_name USING name drop_mode {$$=&DropOperatorFamily{Name:$6,Method:Identifier{Text:$8.text,Pos:$8.pos},IfExists:true,Mode:$9,Pos:$1.pos}} ;
family_add_items: family_add_item {$$=[]OperatorFamilyItem{*$1}} | family_add_items ',' family_add_item {$$=append($1,*$3)} ;
family_add_item: family_operator_item {$$=$1}
 | family_operator_item FOR search_word {$$=$1;$$.Purpose="s"}
 | family_operator_item FOR ORDER BY relation_name {$$=$1;$$.Purpose="o";$$.SortFamily=$5}
 | family_function_item {$$=$1} ;
/* OPERATOR is unreserved: the lexer promotes it after CREATE/ALTER/DROP,
 * but ADD and subsequent comma-separated members retain its KW_U token. */
family_operator_word: OPERATOR {$$=$1}
 | KW_U {if $1.text!="operator" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
family_operator_item: family_operator_word NUMBER operator_name {$$=&OperatorFamilyItem{Number:$2.text,Name:$3,Purpose:"s",Pos:$1.pos}}
 | family_operator_word NUMBER operator_name '(' prepare_type_list ')' {$$=&OperatorFamilyItem{Number:$2.text,Name:$3,Arguments:$5,Signature:true,Purpose:"s",Pos:$1.pos}} ;
family_function_item: FUNCTION NUMBER relation_name family_signature {$$=&OperatorFamilyItem{Function:true,Number:$2.text,Name:$3,Arguments:$4,Signature:true,Pos:$1.pos}}
 | FUNCTION NUMBER relation_name {$$=&OperatorFamilyItem{Function:true,Number:$2.text,Name:$3,Pos:$1.pos}}
 | FUNCTION NUMBER '(' prepare_type_list ')' relation_name family_signature {$$=&OperatorFamilyItem{Function:true,Number:$2.text,Association:$4,Name:$6,Arguments:$7,Signature:true,Pos:$1.pos}} ;
family_signature: '(' ')' {$$=[]TypeName{}} | '(' prepare_type_list ')' {$$=$2} ;
family_drop_items: family_drop_item {$$=[]OperatorFamilyItem{*$1}} | family_drop_items ',' family_drop_item {$$=append($1,*$3)} ;
family_drop_item: family_operator_word NUMBER '(' prepare_type_list ')' {$$=&OperatorFamilyItem{Number:$2.text,Association:$4,Purpose:"s",Pos:$1.pos}}
 | FUNCTION NUMBER '(' prepare_type_list ')' {$$=&OperatorFamilyItem{Function:true,Number:$2.text,Association:$4,Pos:$1.pos}} ;
class_word: OPCLASS {$$=$1} ;
storage_word: KW_U {if $1.text!="storage" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
operator_class_head: CREATE OPERATOR class_word relation_name FOR TYPE type USING name {$$=&CreateOperatorClass{Name:$4,InputType:$7,Method:Identifier{Text:$9.text,Pos:$9.pos},Pos:$1.pos}}
 | CREATE OPERATOR class_word relation_name DEFAULT FOR TYPE type USING name {$$=&CreateOperatorClass{Name:$4,Default:true,InputType:$8,Method:Identifier{Text:$10.text,Pos:$10.pos},Pos:$1.pos}} ;
create_operator_class: operator_class_head AS class_items {$1.Items=$3;$$=$1}
 | operator_class_head family_word relation_name AS class_items {$1.Family=$3;$1.Items=$5;$$=$1} ;
class_items: class_item {$$=[]OperatorFamilyItem{*$1}} | class_items ',' class_item {$$=append($1,*$3)} ;
class_item: family_add_item {$$=$1}
 | storage_word type {t:=$2;$$=&OperatorFamilyItem{Storage:&t,Pos:$1.pos}} ;
operator_class_alter_head: ALTER OPERATOR class_word relation_name USING name {$$=&AlterOperatorClass{Name:$4,Method:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}} ;
alter_operator_class: operator_class_alter_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1}
 | operator_class_alter_head OWNER TO schema_owner {$1.Action="owner";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.OwnerIdentity=$4.keyword!=nil&&$4.keyword.category==Reserved;$$=$1}
 | operator_class_alter_head SET SCHEMA name {$1.Action="schema";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$$=$1} ;
drop_operator_class: DROP OPERATOR class_word relation_name USING name drop_mode {$$=&DropOperatorClass{Name:$4,Method:Identifier{Text:$6.text,Pos:$6.pos},Mode:$7,Pos:$1.pos}}
 | DROP OPERATOR class_word IF EXISTS relation_name USING name drop_mode {$$=&DropOperatorClass{Name:$6,Method:Identifier{Text:$8.text,Pos:$8.pos},IfExists:true,Mode:$9,Pos:$1.pos}} ;
/* Index reloptions use '='; REINDEX controls use whitespace. Keep them separate
 * so malformed option syntax fails before any earlier statement can execute. */
maintenance_concurrent: /* empty */ {$$=lexeme{}} | CONCURRENTLY ;
drop_index: DROP INDEX maintenance_concurrent relation_names drop_mode {$$=&DropIndex{Names:$4,Concurrent:$3.pos,Mode:$5,Pos:$1.pos}}
 | DROP INDEX maintenance_concurrent IF EXISTS relation_names drop_mode {$$=&DropIndex{Names:$6,IfExists:true,Concurrent:$3.pos,Mode:$7,Pos:$1.pos}} ;
/* ATTACH is a literal KW_U action, not an arbitrary name: allowing EXISTS as
 * action lookahead would steal IF EXISTS from the shared index target prefix. */
alter_index_head: ALTER INDEX relation_name {$$=&AlterIndex{Name:$3,Pos:$1.pos}}
 | ALTER INDEX IF EXISTS relation_name {$$=&AlterIndex{Name:$5,IfExists:true,Pos:$1.pos}} ;
alter_index: alter_index_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.ActionPos=$2.pos;$$=$1}
 | alter_index_head KW_U PARTITION relation_name {if $2.text!="attach"{sqllex.(*lexer).syntax($2);return 1};$1.Action="attach";$1.Partition=$4;$1.ActionPos=$2.pos;$$=$1}
 | alter_index_head SET '(' maintenance_index_option_list ')' {$1.Action="set";$1.Options=$4;$1.ActionPos=$2.pos;$$=$1}
 | alter_index_head RESET '(' maintenance_index_option_list ')' {$1.Action="reset";$1.Options=$4;$1.ActionPos=$2.pos;$$=$1}
 | alter_index_head SET name name {if $3.text!="tablespace"{sqllex.(*lexer).syntax($3);return 1};$1.Action="tablespace";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.ActionPos=$3.pos;$$=$1}
 | alter_index_head ALTER COLUMN NUMBER SET name maintenance_index_option_value {if $6.text!="statistics"{sqllex.(*lexer).syntax($6);return 1};$1.Action="statistics";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.ActionPos=$6.pos;$$=$1} ;
index_option_name: name | CONCURRENTLY ;
maintenance_index_option_value: name {$$=Value{Kind:StringValue,Text:$1.text,Pos:$1.pos}}
 | STRING {$$=Value{Kind:StringValue,Text:$1.text,Pos:$1.pos}}
 | NUMBER {$$=numberValue($1,"");$$.Text=$1.text}
 | TRUE {$$=Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$1.pos}}
 | FALSE {$$=Value{Kind:BooleanValue,Text:"false",Pos:$1.pos}}
 | ON {$$=Value{Kind:StringValue,Text:$1.text,Pos:$1.pos}}
 | '-' NUMBER {$$=numberValue($2,"-");$$.Text="-"+$2.text;$$.Pos=$1.pos}
 | '+' NUMBER {$$=numberValue($2,"+");$$.Text="+"+$2.text;$$.Pos=$1.pos} ;
maintenance_index_option_list: maintenance_index_option {$$=[]IndexOption{*$1}} | maintenance_index_option_list ',' maintenance_index_option {$$=append($1,*$3)} ;
maintenance_index_option: index_option_name {$$=&IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$1.pos}}}
 | index_option_name '=' maintenance_index_option_value {$$=&IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3,HasValue:true}}
 | name '.' index_option_name {$$=&IndexOption{Namespace:$1.text,Name:Identifier{Text:$3.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$3.pos}}}
 | name '.' index_option_name '=' maintenance_index_option_value {$$=&IndexOption{Namespace:$1.text,Name:Identifier{Text:$3.text,Pos:$1.pos},Value:$5,HasValue:true}} ;
reindex_options: /* empty */ {$$=nil} | '(' reindex_option_list ')' {$$=$2} ;
reindex_option_list: reindex_option {$$=[]IndexOption{*$1}} | reindex_option_list ',' reindex_option {$$=append($1,*$3)} ;
reindex_option: col_label {$$=&IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$1.pos}}}
 | col_label maintenance_index_option_value {$$=&IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$2,HasValue:true}} ;
reindex_kind: name | TABLE ;
reindex: REINDEX reindex_options reindex_kind maintenance_concurrent relation_name {if $3.text!="index"&&$3.text!="table"&&$3.text!="schema"&&$3.text!="database"&&$3.text!="system"{sqllex.(*lexer).syntax($3);return 1};$$=&Reindex{Name:$5,Kind:Identifier{Text:$3.text,Pos:$3.pos},Options:$2,Concurrent:$4.pos,Pos:$1.pos}} ;

vacuum: VACUUM vacuum_options vacuum_targets {$$=&Vacuum{Options:$2,Targets:$3,Pos:$1.pos}} ;
vacuum_options: '(' vacuum_option_list ')' {$$=$2} | vacuum_legacy ;
vacuum_option_list: vacuum_option {$$=[]VacuumOption{*$1}} | vacuum_option_list ',' vacuum_option {$$=append($1,*$3)} ;
vacuum_option: col_label {$$=&VacuumOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$1.pos}}}
 | col_label maintenance_index_option_value {$$=&VacuumOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$2,HasValue:true}} ;
vacuum_legacy: vacuum_full vacuum_freeze vacuum_verbose vacuum_analyze {$$=nil;for _,t:=range []lexeme{$1,$2,$3,$4} {if t.pos.Character!=0 {$$=append($$,VacuumOption{Name:Identifier{Text:t.text,Pos:t.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:t.pos}})}}} ;
vacuum_full: /* empty */ {$$=lexeme{}} | FULL ;
vacuum_freeze: /* empty */ {$$=lexeme{}} | FREEZE ;
vacuum_verbose: /* empty */ {$$=lexeme{}} | VERBOSE ;
vacuum_analyze: /* empty */ {$$=lexeme{}} | ANALYZE ;
vacuum_targets: /* empty */ {$$=nil} | vacuum_target_list ;
vacuum_target_list: vacuum_target {$$=[]VacuumTarget{$1}} | vacuum_target_list ',' vacuum_target {$$=append($1,$3)} ;
vacuum_target: relation_name vacuum_star view_columns {$$=VacuumTarget{Name:$1,Star:$2,Columns:$3}}
 | ONLY relation_name vacuum_star view_columns {$$=VacuumTarget{Name:$2,Only:true,Star:$3,Columns:$4}} ;
vacuum_star: /* empty */ {$$=false} | '*' {$$=true} ;

analyze: ANALYZE analyze_options vacuum_targets {$$=&Analyze{Options:$2,Targets:$3,Pos:$1.pos}} ;
analyze_options: '(' vacuum_option_list ')' {$$=$2}
 | vacuum_verbose {$$=nil;if $1.pos.Character!=0 {$$=[]VacuumOption{{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$1.pos}}}}} ;

index_head: CREATE INDEX ON relation_name { $$=&CreateIndex{Table:$4,Pos:$1.pos} }
 | CREATE INDEX name ON relation_name { $$=&CreateIndex{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Pos:$1.pos} }
 | CREATE INDEX IF NOT EXISTS name ON relation_name { $$=&CreateIndex{Name:Identifier{Text:$6.text,Pos:$6.pos},Table:$8,IfNotExists:true,Pos:$1.pos} }
 | CREATE INDEX CONCURRENTLY ON relation_name { $$=&CreateIndex{Table:$5,Concurrent:$3.pos,Pos:$1.pos} }
 | CREATE INDEX CONCURRENTLY name ON relation_name { $$=&CreateIndex{Name:Identifier{Text:$4.text,Pos:$4.pos},Table:$6,Concurrent:$3.pos,Pos:$1.pos} }
 | CREATE INDEX CONCURRENTLY IF NOT EXISTS name ON relation_name { $$=&CreateIndex{Name:Identifier{Text:$7.text,Pos:$7.pos},Table:$9,IfNotExists:true,Concurrent:$3.pos,Pos:$1.pos} }
 | CREATE UNIQUE INDEX ON relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Table:$5,Pos:$1.pos}}
 | CREATE UNIQUE INDEX name ON relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Name:Identifier{Text:$4.text,Pos:$4.pos},Table:$6,Pos:$1.pos}}
 | CREATE UNIQUE INDEX IF NOT EXISTS name ON relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Name:Identifier{Text:$7.text,Pos:$7.pos},Table:$9,IfNotExists:true,Pos:$1.pos}}
 | CREATE UNIQUE INDEX CONCURRENTLY ON relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Table:$6,Concurrent:$4.pos,Pos:$1.pos}}
 | CREATE UNIQUE INDEX CONCURRENTLY name ON relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Name:Identifier{Text:$5.text,Pos:$5.pos},Table:$7,Concurrent:$4.pos,Pos:$1.pos}}
 | CREATE UNIQUE INDEX name ON ONLY relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Name:Identifier{Text:$4.text,Pos:$4.pos},Only:true,Table:$7,Pos:$1.pos}}
 | CREATE UNIQUE INDEX ON ONLY relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Only:true,Table:$6,Pos:$1.pos}}
 | CREATE UNIQUE INDEX IF NOT EXISTS name ON ONLY relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Name:Identifier{Text:$7.text,Pos:$7.pos},Only:true,Table:$10,IfNotExists:true,Pos:$1.pos}}
 | CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS name ON relation_name {$$=&CreateIndex{Unique:true,UniquePos:$2.pos,Name:Identifier{Text:$8.text,Pos:$8.pos},Table:$10,IfNotExists:true,Concurrent:$4.pos,Pos:$1.pos}} ;
index_method: /* empty */ { sqllex.(*lexer).indexExpression=true; $$=lexeme{text:"btree"} } | USING name { sqllex.(*lexer).indexExpression=true; $$=$2 } ;
index_definition: index_head index_method '(' index_keys ')' index_include {
 $1.Method=$2.text;$1.Keys=$4;$1.Include=$6;$$=$1
} ;
index_null_policy: /* empty */ {$$=lexeme{}} | NULLS_LA DISTINCT {$$=$1;$$.text="distinct"} | NULLS_LA NOT DISTINCT {$$=$1;$$.text="not distinct"} ;
index_policy: index_definition index_null_policy {$1.NullsNotDistinct=$2.text=="not distinct";$1.NullsPos=$2.pos;$$=$1} ;
index_storage: index_policy {$$=$1}
 | index_policy WITH '(' maintenance_index_option_list ')' {$1.Options=$4;$1.WithPos=$2.pos;$$=$1} ;
index_tablespace: index_storage {$$=$1}
 | index_storage TABLESPACE name {sqllex.(*lexer).deferUnsupported($2,"index tablespaces are unsupported");$$=$1} ;
create_index: index_tablespace {sqllex.(*lexer).indexExpression=false;$$=$1}
 | index_tablespace WHERE {sqllex.(*lexer).indexExpression=true} target_or {sqllex.(*lexer).indexExpression=false;$1.Predicate=$4;$$=$1} ;
index_include: /* empty */ {$$=nil} | INCLUDE '(' index_keys ')' {$$=$3} ;
index_keys: index_key { $$=[]IndexKey{*$1} } | index_keys ',' index_key { $$=append($1,*$3) } ;
index_key: index_atom column_collation index_class index_direction nulls {
 $$=$1;$$.Collation=$2;$$.Opclass=$3.Opclass;$$.Options=$3.Options
 $$.Desc=$4.text=="desc";$$.DirectionExplicit=$4.text!="";$$.Nulls=$5
} ;
index_atom: name {$$=&IndexKey{Column:Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos}}
 | '(' scalar_projection ')' {$$=&IndexKey{Expression:$2,Pos:$1.pos}}
 | function {$$=&IndexKey{Expression:$1,Pos:$1.SourcePosition()}}
 | conditional_call {$$=&IndexKey{Expression:$1,Pos:$1.SourcePosition()}}
 | sqljson_query {$$=&IndexKey{Expression:$1,Pos:$1.SourcePosition()}}
 | check_function_head function_args ')' {call,ok:=selectedScalarCall(sqllex,$1,$2);if !ok{return 1};$$=&IndexKey{Expression:call,Pos:$1.pos}}
 | name '.' check_function_head function_args ')' {$$=&IndexKey{Expression:FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$4.Args,ArgNames:$4.ArgNames,Variadic:$4.Variadic,ResolutionOnly:$3.resolutionOnly,Pos:$1.pos},Pos:$1.pos}}
 | string_function {$$=&IndexKey{Expression:$1,Pos:$1.SourcePosition()}}
 | name '(' function_args ')' {$$=&IndexKey{Expression:FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos},Pos:$1.pos}}
 | name '.' col_label '(' function_args ')' {$$=&IndexKey{Expression:FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos},Pos:$1.pos}} ;
index_class: /* empty */ {$$=&IndexKey{}}
 | relation_name index_options {name:=$1;$$=&IndexKey{Opclass:&name,Options:$2}} ;
index_options: /* empty */ {$$=nil} | '(' index_option_list ')' {$$=$2} ;
index_option_list: index_option {$$=[]ViewOption{$1}} | index_option_list ',' index_option {$$=append($1,$3)} ;
index_option: col_label {$$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:"true"}}
 | col_label '=' index_option_value {$$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text,HasValue:true}} ;
index_option_value: view_option_value | '+' NUMBER {$$=$2;$$.text="+"+$2.text}
 | '-' NUMBER {$$=$2;$$.text="-"+$2.text} ;
index_direction: /* empty */ {$$=lexeme{}} | ASC {$$=$1} | DESC {$$=$1} ;
optional_elements: /* empty */ { $$=nil } | elements { $$=$1 } ;
elements: element { $$=[]TableElement{*$1} } | elements ',' element { $$=append($1,*$3) } ;
element: column { $$=&TableElement{Column:$1,Pos:$1.Pos} }
 | LIKE relation_name like_options { $$=&TableElement{Like:&TableLike{Name:$2,Options:$3},Pos:$1.pos} }
 | create_not_null { $$=&TableElement{NotNull:$1,Pos:$1.Pos} }
 | check_constraint { $$=&TableElement{Check:$1,Pos:$1.Pos} }
 | unique_constraint { $$=&TableElement{Unique:$1,Pos:$1.Pos} }
 | primary_constraint { $$=&TableElement{Unique:$1,Pos:$1.Pos} }
 | foreign_key { $$=&TableElement{ForeignKey:$1,Pos:$1.Pos} }
 | exclusion_constraint { $$=&TableElement{Exclusion:$1,Pos:$1.Pos} }
 | element ENFORCED {if !sqllex.(*lexer).elementEnforcement($1,$2,false){return 1};$$=$1}
 | element NOT ENFORCED {if !sqllex.(*lexer).elementEnforcement($1,$2,true){return 1};$$=$1}
 | element NOT_DEFERRABLE_LA DEFERRABLE {if !sqllex.(*lexer).elementForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | element DEFERRABLE {if !sqllex.(*lexer).elementForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | element INITIALLY name {if !sqllex.(*lexer).elementForeignProperty($1,$2,$3){return 1};$$=$1}
 | element NO nn_inherit {if $1.Check==nil {sqllex.(*lexer).syntax($2);return 1};$1.Check.NoInherit=true;$$=$1} ;
table_not_null: NOT NULL name { $$=&NotNullConstraint{Column:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | CONSTRAINT name NOT NULL name { $$=&NotNullConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Column:Identifier{Text:$5.text,Pos:$5.pos},Pos:$3.pos} } ;
create_not_null: table_not_null {$$=$1}
 | create_not_null NOT nn_modifier {message:="NOT NULL modifiers are unsupported";if $3.text=="enforced"{message="NOT NULL constraints cannot be marked NOT ENFORCED"};sqllex.(*lexer).deferUnsupported($2,message);$$=$1}
 | create_not_null NO nn_inherit {sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported");$$=$1} ;
/* NO belongs to the CHECK before the generic column-modifier path can reduce.
 * The closing ')' otherwise has higher precedence than NO. */
check_constraint: CHECK '(' scalar_projection ')' %prec NAME_LIST { $$=&CheckConstraint{Expression:$3,Pos:$1.pos} }
 | CONSTRAINT name CHECK '(' scalar_projection ')' %prec NAME_LIST { $$=&CheckConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Expression:$5,Pos:$3.pos} }
 | CHECK '(' scalar_projection ')' NO nn_inherit { $$=&CheckConstraint{Expression:$3,NoInherit:true,Pos:$1.pos} }
 | CONSTRAINT name CHECK '(' scalar_projection ')' NO nn_inherit { $$=&CheckConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Expression:$5,NoInherit:true,Pos:$3.pos} } ;
unique_nulls: /* empty */ { $$=false }
 | NULLS_LA DISTINCT { $$=false }
 | NULLS_LA NOT DISTINCT { $$=true } ;
column_unique: UNIQUE unique_nulls { $$=&UniqueConstraint{NullsNotDistinct:$2,Pos:$1.pos} }
 | CONSTRAINT name UNIQUE unique_nulls { $$=&UniqueConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},NullsNotDistinct:$4,Pos:$1.pos} }
 | column_unique DEFERRABLE { sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 }
 | column_unique NOT_DEFERRABLE_LA DEFERRABLE { sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 }
 | column_unique INITIALLY name { if $3.keyword==nil || ($3.text!="deferred" && $3.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 } ;
unique_constraint: UNIQUE unique_nulls '(' view_names ')' { $$=&UniqueConstraint{Columns:$4,NullsNotDistinct:$2,Pos:$1.pos} }
 | CONSTRAINT name UNIQUE unique_nulls '(' view_names ')' { $$=&UniqueConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Columns:$6,NullsNotDistinct:$4,Pos:$1.pos} }
 | unique_constraint DEFERRABLE { sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 }
 | unique_constraint NOT_DEFERRABLE_LA DEFERRABLE { sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 }
 | unique_constraint INITIALLY name { if $3.keyword==nil || ($3.text!="deferred" && $3.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 } ;
primary_head: PRIMARY name { if $2.keyword==nil || $2.text!="key" {sqllex.(*lexer).syntax($2);return 1};$$=$1 } ;
primary_columns: view_names {$$=$1}
 | view_names WITHOUT OVERLAPS {sqllex.(*lexer).deferUnsupported($2,"PRIMARY KEY WITHOUT OVERLAPS is unsupported");$$=$1} ;
column_primary: primary_head {$$=&UniqueConstraint{Primary:true,Pos:$1.pos}}
 | CONSTRAINT name primary_head {$$=&UniqueConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Primary:true,Pos:$1.pos}}
 | column_primary primary_modifier {$$=$1} ;
primary_constraint: primary_head '(' primary_columns ')' {$$=&UniqueConstraint{Columns:$3,Primary:true,Pos:$1.pos}}
 | CONSTRAINT name primary_head '(' primary_columns ')' {$$=&UniqueConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Columns:$5,Primary:true,Pos:$1.pos}}
 | primary_constraint primary_modifier {$$=$1} ;
primary_modifier: DEFERRABLE {sqllex.(*lexer).deferUnsupported($1,"PRIMARY KEY deferrability is unsupported");$$=$1}
 | NOT_DEFERRABLE_LA DEFERRABLE {sqllex.(*lexer).deferUnsupported($1,"PRIMARY KEY deferrability is unsupported");$$=$1}
 | INITIALLY name {if $2.keyword==nil || ($2.text!="deferred" && $2.text!="immediate") {sqllex.(*lexer).syntax($1);return 1};sqllex.(*lexer).deferUnsupported($1,"PRIMARY KEY deferrability is unsupported");$$=$1}
 | INCLUDE '(' view_names ')' {sqllex.(*lexer).deferUnsupported($1,"PRIMARY KEY index parameters are unsupported");$$=$1}
 | WITH '(' index_option_list ')' {sqllex.(*lexer).deferUnsupported($1,"PRIMARY KEY index parameters are unsupported");$$=$1}
 | USING INDEX TABLESPACE name {sqllex.(*lexer).deferUnsupported($1,"PRIMARY KEY index parameters are unsupported");$$=$1} ;
foreign_columns_optional: /* empty */ {$$=nil} | '(' foreign_columns ')' {$$=$2} ;
foreign_columns: view_names {$$=$1}
 | view_names ',' PERIOD name {sqllex.(*lexer).deferUnsupported($3,"temporal foreign keys are unsupported");$$=append($1,Identifier{Text:$4.text,Pos:$4.pos})}
 | PERIOD name {sqllex.(*lexer).deferUnsupported($1,"temporal foreign keys are unsupported");$$=[]Identifier{{Text:$2.text,Pos:$2.pos}}} ;
foreign_reference: REFERENCES relation_name foreign_columns_optional {$$=&ForeignKeyConstraint{Target:$2,Referenced:$3,Pos:$1.pos}} ;
foreign_match_optional: /* empty */ {$$=lexeme{}}
 | MATCH foreign_match {if $2.text=="partial"{sqllex.(*lexer).deferUnsupported($1,"MATCH PARTIAL not yet implemented")};$$=$2} ;
foreign_update: ON UPDATE foreign_action {if len($3.columns)!=0 {message:="a column list with SET NULL is only supported for ON DELETE actions";if $3.action==ForeignKeySetDefault{message="a column list with SET DEFAULT is only supported for ON DELETE actions"};sqllex.(*lexer).deferUnsupported($1,message)};$$=$3} ;
foreign_delete: ON DELETE foreign_action {$$=$3} ;
foreign_actions: /* empty */ {$$=&ForeignKeyConstraint{}}
 | foreign_update {$$=&ForeignKeyConstraint{UpdateAction:$1.action}}
 | foreign_delete {$$=&ForeignKeyConstraint{DeleteAction:$1.action,DeleteColumns:$1.columns}}
 | foreign_update foreign_delete {$$=&ForeignKeyConstraint{UpdateAction:$1.action,DeleteAction:$2.action,DeleteColumns:$2.columns}}
 | foreign_delete foreign_update {$$=&ForeignKeyConstraint{DeleteAction:$1.action,DeleteColumns:$1.columns,UpdateAction:$2.action}} ;
foreign_body: foreign_reference foreign_match_optional foreign_actions {$$=$1;$$.Full=$2.text=="full";$$.UpdateAction=$3.UpdateAction;$$.DeleteAction=$3.DeleteAction;$$.DeleteColumns=$3.DeleteColumns} ;
foreign_definition: foreign_body {$$=$1}
 | foreign_definition NOT_DEFERRABLE_LA DEFERRABLE {if !sqllex.(*lexer).foreignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | foreign_definition DEFERRABLE {if !sqllex.(*lexer).foreignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | foreign_definition INITIALLY name {if !sqllex.(*lexer).foreignProperty($1,$2,$3){return 1};$$=$1} ;
foreign_match: FULL | name {if $1.keyword==nil||($1.text!="simple"&&$1.text!="partial"){sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
foreign_set_columns: /* empty */ {$$=nil} | '(' view_names ')' {$$=$2} ;
foreign_action: NO name {if $2.keyword==nil||$2.text!="action"{sqllex.(*lexer).syntax($2);return 1};$$=foreignActionClause{action:ForeignKeyNoAction}}
 | RESTRICT {$$=foreignActionClause{action:ForeignKeyRestrict}}
 | CASCADE {$$=foreignActionClause{action:ForeignKeyCascade}}
 | SET NULL foreign_set_columns {$$=foreignActionClause{action:ForeignKeySetNull,columns:$3}}
 | SET DEFAULT foreign_set_columns {$$=foreignActionClause{action:ForeignKeySetDefault,columns:$3}} ;
foreign_key: FOREIGN name '(' foreign_columns ')' foreign_definition {if $2.keyword==nil||$2.text!="key"{sqllex.(*lexer).syntax($2);return 1};$$=$6;$$.Columns=$4;$$.Pos=$1.pos}
 | CONSTRAINT name FOREIGN name '(' foreign_columns ')' foreign_definition {if $4.keyword==nil||$4.text!="key"{sqllex.(*lexer).syntax($4);return 1};$$=$8;$$.Name=Identifier{Text:$2.text,Pos:$2.pos};$$.Columns=$6;$$.Pos=$3.pos} ;
using_index_constraint: UNIQUE USING INDEX name {$$=&UsingIndexConstraint{Index:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos,KindPos:$1.pos}}
 | CONSTRAINT name UNIQUE USING INDEX name {$$=&UsingIndexConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Index:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos,KindPos:$3.pos}}
 | primary_head USING INDEX name {$$=&UsingIndexConstraint{Index:Identifier{Text:$4.text,Pos:$4.pos},Primary:true,Pos:$1.pos,KindPos:$1.pos}}
 | CONSTRAINT name primary_head USING INDEX name {$$=&UsingIndexConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Index:Identifier{Text:$6.text,Pos:$6.pos},Primary:true,Pos:$1.pos,KindPos:$3.pos}}
 | using_index_constraint DEFERRABLE {if !usingIndexProperty(sqllex.(*lexer),$1,$2,true,true){return 1};$$=$1}
 | using_index_constraint NOT_DEFERRABLE_LA DEFERRABLE {if !usingIndexProperty(sqllex.(*lexer),$1,$2,true,false){return 1};$$=$1}
 | using_index_constraint INITIALLY name {if $3.keyword==nil || ($3.text!="deferred" && $3.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};if !usingIndexProperty(sqllex.(*lexer),$1,$2,false,$3.text=="deferred"){return 1};$$=$1} ;
exclusion_head: EXCLUDE {$$=&ExclusionConstraint{Pos:$1.pos,Index:CreateIndex{Pos:$1.pos}}}
 | CONSTRAINT name EXCLUDE {$$=&ExclusionConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos,Index:CreateIndex{Pos:$3.pos}}} ;
exclusion_definition: exclusion_head index_method '(' exclusion_elements ')' index_include {
 $1.Index.Method=$2.text;$1.Index.Include=$6
 for _,item:=range $4 {$1.Index.Keys=append($1.Index.Keys,item.Key);$1.Operators=append($1.Operators,item.Operator)}
 $$=$1
} ;
exclusion_elements: exclusion_element {$$=[]ExclusionElement{*$1}} | exclusion_elements ',' exclusion_element {$$=append($1,*$3)} ;
exclusion_element: index_key WITH exclusion_operator {$$=&ExclusionElement{Key:*$1,Operator:$3}} ;
exclusion_operator: operator_symbol {$$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}}} | qualified_operator ;
exclusion_storage: exclusion_definition {$$=$1}
 | exclusion_definition WITH '(' maintenance_index_option_list ')' {$1.Index.Options=$4;$1.Index.WithPos=$2.pos;$$=$1} ;
exclusion_tablespace: exclusion_storage {$$=$1}
 | exclusion_storage TABLESPACE name {sqllex.(*lexer).deferUnsupported($2,"index tablespaces are unsupported");$$=$1} ;
exclusion_predicate: exclusion_tablespace {$$=$1}
 | exclusion_tablespace WHERE '(' scalar_projection ')' {$1.Index.Predicate=$4;$$=$1} ;
exclusion_constraint: exclusion_predicate {sqllex.(*lexer).indexExpression=false;$$=$1}
 | exclusion_constraint DEFERRABLE {if !exclusionProperty(sqllex.(*lexer),$1,$2,true,true){return 1};$$=$1}
 | exclusion_constraint NOT_DEFERRABLE_LA DEFERRABLE {if !exclusionProperty(sqllex.(*lexer),$1,$2,true,false){return 1};$$=$1}
 | exclusion_constraint INITIALLY name {if $3.keyword==nil || ($3.text!="deferred" && $3.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};if !exclusionProperty(sqllex.(*lexer),$1,$2,false,$3.text=="deferred"){return 1};$$=$1} ;
like_options: /* empty */ { $$=nil } | like_options like_option { $$=append($1,$2) } ;
like_option: INCLUDING like_option_name { $$=LikeOption{Name:$2.text,Including:true,Pos:$1.pos} }
 | EXCLUDING like_option_name { $$=LikeOption{Name:$2.text,Pos:$1.pos} } ;
like_option_name: name { if $1.keyword==nil || !validLikeOption($1.text) { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } | ALL ;
drop_table: DROP TABLE relation_names drop_mode { $$=&DropTable{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP TABLE IF EXISTS relation_names drop_mode { $$=&DropTable{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
relation_names: relation_name { $$=[]ObjectName{$1} } | relation_names ',' relation_name { $$=append($1,$3) } ;
drop_mode: /* empty */ { $$=DropRestrict } | RESTRICT { $$=DropRestrict } | CASCADE { $$=DropCascade } ;
/* All ALTER TABLE actions share one target prefix. Reducing alter_target on
 * some paths and rename_target on others steals action lookahead from peers. */
alter_drop_column: ALTER TABLE rename_target alter_actions { if $3.IfExists { for _,a:=range $4 { if (a.Action<EnableRowSecurity || a.Action>NoForceRowSecurity) && a.Action!=SetExpression && a.Action!=DropExpression && a.Action!=SetTablespace && a.Action!=InheritTable && a.Action!=NoInheritTable && a.Action!=SetRelationOptions && a.Action!=ResetRelationOptions && a.Action!=ValidateConstraint && a.Action!=AlterConstraintEnforcement && a.Action!=EnableRule && a.Action!=DisableRule && a.Action!=EnableReplicaRule && a.Action!=EnableAlwaysRule && (a.Action<EnableTrigger || a.Action>EnableAlwaysTrigger) {sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",a.Pos.Offset);return 1} } };$$=&AlterTable{Name:$3.Name,Only:$3.Only,IfExists:$3.IfExists,Actions:$4,Pos:$1.pos} }
 | ALTER TABLE ALL IN TABLESPACE name tablespace_owners SET TABLESPACE name optional_lock_nowait { $$=&AlterTableAllInTablespace{Source:Identifier{Text:$6.text,Pos:$6.pos},Destination:Identifier{Text:$10.text,Pos:$10.pos},Owners:$7,Nowait:$11,Pos:$1.pos} }
 | alter_default_single { $$=$1 }
 | alter_default_single ',' { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset); return 1 }
 | alter_rename_single { $$=$1 }
 | ALTER TABLE rename_target OWNER TO membership_role_spec { $$=&AlterOwner{Kind:"table",Name:$3.Name,IfExists:$3.IfExists,Target:$6,Pos:$1.pos} }
 | alter_schema_single { $$=$1 }
 | ALTER TABLE rename_target KW_U PARTITION relation_name partition_bound {
     if $4.text!="attach" {sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset);return 1}
     $$=&AlterTableAttachPartition{Name:$3.Name,Partition:$6,Bound:*$7,IfExists:$3.IfExists,Only:$3.Only,Pos:$1.pos,AttachPos:$4.pos}
   }
 | ALTER TABLE rename_target KW_U PARTITION relation_name {
     if $4.text!="detach" {sqllex.(*lexer).syntax($4);return 1}
     $$=&AlterTableDetachPartition{Name:$3.Name,Partition:$6,IfExists:$3.IfExists,Only:$3.Only,Pos:$1.pos,DetachPos:$4.pos}
   }
 | ALTER TABLE rename_target KW_U PARTITION relation_name CONCURRENTLY {
     if $4.text!="detach" {sqllex.(*lexer).syntax($4);return 1}
     sqllex.(*lexer).failAt("0A000","DETACH PARTITION CONCURRENTLY is unsupported",$7.pos.Offset);return 1
   }
 | ALTER TABLE rename_target KW_U PARTITION relation_name KW_U {
     if $4.text!="detach" || $7.text!="finalize" {sqllex.(*lexer).syntax($7);return 1}
     sqllex.(*lexer).failAt("0A000","DETACH PARTITION FINALIZE is unsupported",$7.pos.Offset);return 1
   }
 | alter_rename_single ',' {
     if _,ok:=$1.(*AlterTableRenameConstraint);ok {
       sqllex.(*lexer).failAt("42601","syntax error at or near \",\"",$2.pos.Offset)
     } else {
       sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset)
     }
     return 1
   }
 | ALTER TABLE rename_target alter_action { if $4.keyword==nil {sqllex.(*lexer).syntax($4)} else {sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset)}; return 1 } ;
tablespace_owners: /* empty */ { $$=nil } | OWNED BY membership_role_specs { $$=$3 } ;
alter_actions: ordinary_alter_action { $$=[]AlterTableAction{*$1} }
 | alter_actions ',' ordinary_alter_action { $$=append($1,*$3) }
 | alter_actions ',' ALTER optional_column name SET DEFAULT target_or { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$3.pos.Offset); return 1 }
 | alter_actions ',' ALTER optional_column name DROP DEFAULT { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$3.pos.Offset); return 1 } ;
ordinary_alter_action: alter_selected_action {$$=$1}
 | alter_column_add {if $1.ColumnOptions!=nil {sqllex.(*lexer).syntax($1.columnOptionsToken);return 1};$$=$1.Ordinary} ;
alter_column_add: ADD optional_column column {c:=$3;sqllex.(*lexer).excludePrimaryColumnAddition($1,c);$$=&ForeignTableAction{Ordinary:&AlterTableAction{Column:Identifier{Text:c.Name,Pos:c.Pos},Definition:c,Action:AddColumn,Pos:$1.pos}}}
 | ADD optional_column IF NOT EXISTS column {c:=$6;sqllex.(*lexer).excludePrimaryColumnAddition($1,c);$$=&ForeignTableAction{Ordinary:&AlterTableAction{Column:Identifier{Text:c.Name,Pos:c.Pos},Definition:c,IfNotExists:true,Action:AddColumn,Pos:$1.pos}}}
 | ADD optional_column name type options_word '(' foreign_option_list ')' column_collation nullability {c:=&Column{Name:$3.text,TypeName:$4,Collation:$9,Constraints:$10,Pos:$3.pos};$$=&ForeignTableAction{Ordinary:&AlterTableAction{Column:Identifier{Text:c.Name,Pos:c.Pos},Definition:c,Action:AddColumn,Pos:$1.pos},ColumnOptions:$7,columnOptionsToken:$5}}
 | ADD optional_column IF NOT EXISTS name type options_word '(' foreign_option_list ')' column_collation nullability {c:=&Column{Name:$6.text,TypeName:$7,Collation:$12,Constraints:$13,Pos:$6.pos};$$=&ForeignTableAction{Ordinary:&AlterTableAction{Column:Identifier{Text:c.Name,Pos:c.Pos},Definition:c,Action:AddColumn,IfNotExists:true,Pos:$1.pos},ColumnOptions:$10,columnOptionsToken:$8}} ;
alter_selected_action: DROP optional_column drop_target drop_mode { $$=&AlterTableAction{Column:$3.Name,IfExists:$3.IfExists,Mode:$4,Action:DropColumn,Pos:$1.pos} }
 | SET TABLESPACE name { $$=&AlterTableAction{Action:SetTablespace,Tablespace:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | SET '(' maintenance_index_option_list ')' { $$=&AlterTableAction{Action:SetRelationOptions,Options:$3,Pos:$1.pos} }
 | RESET '(' maintenance_index_option_list ')' { $$=&AlterTableAction{Action:ResetRelationOptions,Options:$3,Pos:$1.pos} }
 | INHERIT relation_name { $$=&AlterTableAction{Action:InheritTable,Parent:$2,Pos:$1.pos} }
 | NO INHERIT relation_name { $$=&AlterTableAction{Action:NoInheritTable,Parent:$3,Pos:$1.pos} }
 | ALTER optional_column name SET NOT NULL { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:SetNotNull,Pos:$1.pos} }
 | ALTER optional_column name DROP NOT NULL { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropNotNull,Pos:$1.pos} }
 | ALTER optional_column name SET generation_expression_word AS '(' scalar_projection ')' { expression:=$8;if restriction:=sqllex.(*lexer).generationCompletedRestriction;restriction!=nil {expression=ForbiddenGenerationExpr{*restriction}};$$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:SetExpression,Generation:&GenerationDefinition{Expression:expression,Pos:$5.pos},Pos:$1.pos} }
 | ALTER optional_column name DROP generation_expression_word { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropExpression,Pos:$1.pos} }
 | ALTER optional_column name DROP generation_expression_word IF EXISTS { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropExpression,IfExists:true,Pos:$1.pos} }
 | alter_constraint {$$=$1}
 | DROP CONSTRAINT drop_target drop_mode { $$=&AlterTableAction{Constraint:$3.Name,IfExists:$3.IfExists,Mode:$4,Action:DropConstraint,Pos:$1.pos} }
 | validate_word CONSTRAINT name { $$=&AlterTableAction{Constraint:Identifier{Text:$3.text,Pos:$3.pos},Action:ValidateConstraint,Pos:$1.pos} }
 | ALTER optional_column name ADD identity_definition { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:AddIdentity,Identity:$5,Pos:$1.pos} }
 | ALTER optional_column name DROP IDENTITY { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropIdentity,Pos:$1.pos} }
 | ALTER optional_column name DROP IDENTITY IF EXISTS { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropIdentity,IfExists:true,Pos:$1.pos} }
 | ALTER optional_column name identity_alter_options { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:SetIdentity,Identity:$4,Pos:$1.pos} }
 | ADD CONSTRAINT name column_suffix { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$1.pos.Offset); return 1 }
 | ALTER optional_column name alter_type_keyword type column_collation alter_using { $$=&AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:AlterColumnType,TypeName:$5,Collation:$6,Using:$7,Pos:$1.pos} }
 | KW_U RULE name {kind:=EnableRule;switch $1.text {case "enable":case "disable":kind=DisableRule;default:sqllex.(*lexer).syntax($1);return 1};$$=&AlterTableAction{Action:kind,Rule:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos}}
 | KW_U name RULE name {if $1.text!="enable" {sqllex.(*lexer).syntax($2);return 1};if $2.keyword==nil || $2.text!="replica" {sqllex.(*lexer).syntax($2);return 1};$$=&AlterTableAction{Action:EnableReplicaRule,Rule:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos}}
 | KW_U ALWAYS RULE name {if $1.text!="enable" {sqllex.(*lexer).syntax($2);return 1};$$=&AlterTableAction{Action:EnableAlwaysRule,Rule:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos}}
 | KW_U TRIGGER name {kind:=EnableTrigger;switch $1.text {case "enable":case "disable":kind=DisableTrigger;default:sqllex.(*lexer).syntax($1);return 1};selector:=TriggerNamed;if $3.keyword!=nil && $3.text=="user" {selector=TriggerUser};$$=&AlterTableAction{Action:kind,Trigger:Identifier{Text:$3.text,Pos:$3.pos},TriggerSelector:selector,Pos:$1.pos}}
 | KW_U TRIGGER ALL {kind:=EnableTrigger;switch $1.text {case "enable":case "disable":kind=DisableTrigger;default:sqllex.(*lexer).syntax($1);return 1};$$=&AlterTableAction{Action:kind,TriggerSelector:TriggerAll,Pos:$1.pos}}
 | KW_U TRIGGER USER {kind:=EnableTrigger;switch $1.text {case "enable":case "disable":kind=DisableTrigger;default:sqllex.(*lexer).syntax($1);return 1};$$=&AlterTableAction{Action:kind,TriggerSelector:TriggerUser,Pos:$1.pos}}
 | KW_U name TRIGGER name {if $1.text!="enable" {sqllex.(*lexer).syntax($2);return 1};if $2.keyword==nil || $2.text!="replica" {sqllex.(*lexer).syntax($2);return 1};if $4.keyword!=nil && $4.text=="user" {sqllex.(*lexer).syntax($4);return 1};$$=&AlterTableAction{Action:EnableReplicaTrigger,Trigger:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos}}
 | KW_U ALWAYS TRIGGER name {if $1.text!="enable" {sqllex.(*lexer).syntax($2);return 1};if $4.keyword!=nil && $4.text=="user" {sqllex.(*lexer).syntax($4);return 1};$$=&AlterTableAction{Action:EnableAlwaysTrigger,Trigger:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos}}
 | KW_U ROW LEVEL ROUTINE_SECURITY {kind:=EnableRowSecurity;switch $1.text {case "enable":case "disable":kind=DisableRowSecurity;case "force":kind=ForceRowSecurity;default:sqllex.(*lexer).syntax($1);return 1};$$=&AlterTableAction{Action:kind,Pos:$1.pos}}
 | NO KW_U ROW LEVEL ROUTINE_SECURITY {if $2.text!="force" {sqllex.(*lexer).syntax($2);return 1};$$=&AlterTableAction{Action:NoForceRowSecurity,Pos:$1.pos}} ;
alter_constraint: ADD table_not_null {$$=&AlterTableAction{Column:$2.Column,Constraint:$2.Name,Action:AddNotNull,Pos:$1.pos}}
 | ADD check_constraint {$$=&AlterTableAction{Action:AddCheck,Check:$2,Constraint:$2.Name,Pos:$1.pos}}
 | ADD unique_constraint {$$=&AlterTableAction{Action:AddUnique,Unique:$2,Constraint:$2.Name,Pos:$1.pos}}
 | ADD primary_constraint {$$=&AlterTableAction{Action:AddUnique,Unique:$2,Constraint:$2.Name,Pos:$1.pos}}
 | ADD using_index_constraint {$$=&AlterTableAction{Action:AddUsingIndex,UsingIndex:$2,Constraint:$2.Name,Pos:$1.pos}}
 | ADD exclusion_constraint {$$=&AlterTableAction{Action:AddExclusion,Exclusion:$2,Constraint:$2.Name,Pos:$1.pos}}
 | ADD foreign_key {$$=&AlterTableAction{Action:AddForeignKey,ForeignKey:$2,Constraint:$2.Name,Pos:$1.pos}}
 | ALTER CONSTRAINT name ENFORCED {$$=&AlterTableAction{Constraint:Identifier{Text:$3.text,Pos:$3.pos},Action:AlterConstraintEnforcement,EnforcementSet:true,EnforcementPos:$4.pos,Pos:$1.pos}}
 | ALTER CONSTRAINT name NOT nn_modifier {$$=&AlterTableAction{Constraint:Identifier{Text:$3.text,Pos:$3.pos},Action:AlterConstraintEnforcement,Pos:$1.pos};if !sqllex.(*lexer).alterConstraintValidity($$,$4,$5){return 1}}
 | alter_constraint ENFORCED {if !sqllex.(*lexer).alterEnforcement($1,$2,false){return 1};$$=$1}
 | alter_constraint NOT nn_modifier {if !sqllex.(*lexer).alterConstraintValidity($1,$2,$3){return 1};$$=$1}
 | alter_constraint NOT_DEFERRABLE_LA DEFERRABLE {if !sqllex.(*lexer).foreignProperty($1.ForeignKey,$2,lexeme{}){return 1};$$=$1}
 | alter_constraint DEFERRABLE {if !sqllex.(*lexer).foreignProperty($1.ForeignKey,$2,lexeme{}){return 1};$$=$1}
 | alter_constraint INITIALLY name {if !sqllex.(*lexer).foreignProperty($1.ForeignKey,$2,$3){return 1};$$=$1}
 | alter_constraint NO nn_inherit {if !sqllex.(*lexer).alterConstraintNoInherit($1,$2){return 1};$$=$1} ;
alter_type_keyword: TYPE { $$=$1 }
 | SET DATA TYPE { $$=$1 } ;
column_collation: /* empty */ {$$=nil}
 | COLLATE relation_name {name:=$2;name.Parts[0].Pos=$1.pos;$$=&name} ;
alter_using: /* empty */ {$$=nil} | USING scalar_projection {$$=$2} ;
alter_default_single: ALTER TABLE rename_target ALTER optional_column name SET DEFAULT target_or { if $3.IfExists {sqllex.(*lexer).syntax($4);return 1};$$=&AlterTableDefault{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},Only:$3.Only,Action:SetDefault,Expression:$9,Pos:$1.pos} }
 | ALTER TABLE rename_target ALTER optional_column name DROP DEFAULT { if $3.IfExists {sqllex.(*lexer).syntax($4);return 1};$$=&AlterTableDefault{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},Only:$3.Only,Action:DropDefault,Pos:$1.pos} } ;
alter_rename_single: ALTER TABLE rename_target RENAME TO name { $$=&AlterTableRenameTable{Name:$3.Name,NewName:Identifier{Text:$6.text,Pos:$6.pos},IfExists:$3.IfExists,Pos:$1.pos} }
 | ALTER TABLE rename_target RENAME optional_column name TO name { $$=&AlterTableRenameColumn{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},NewName:Identifier{Text:$8.text,Pos:$8.pos},Only:$3.Only,IfExists:$3.IfExists,Pos:$1.pos} }
 | ALTER TABLE rename_target RENAME CONSTRAINT name TO name { $$=&AlterTableRenameConstraint{Name:$3.Name,Constraint:Identifier{Text:$6.text,Pos:$6.pos},NewName:Identifier{Text:$8.text,Pos:$8.pos},Only:$3.Only,IfExists:$3.IfExists,Pos:$1.pos,ConstraintPos:$5.pos} } ;
alter_schema_single: ALTER TABLE rename_target SET SCHEMA name { $$=&AlterTableSetSchema{Name:$3.Name,Schema:Identifier{Text:$6.text,Pos:$6.pos},IfExists:$3.IfExists,Only:$3.Only,Pos:$1.pos} } ;
rename_target: alter_target {$$=$1}
 | IF EXISTS alter_target {$$=$3;$$.IfExists=true} ;
alter_target: relation_name { $$=alterTarget{Name:$1} }
 | relation_name '*' { $$=alterTarget{Name:$1,Star:true} }
 | ONLY relation_name { $$=alterTarget{Name:$2,Only:true} }
 | ONLY '(' relation_name ')' { $$=alterTarget{Name:$3,Only:true} } ;
optional_column: /* empty */ { $$=false } | COLUMN { $$=true } ;
/* Share the IF prefix with eligible names: DROP if and IF EXISTS both parse. */
drop_target: name { $$=dropTarget{Name:Identifier{Text:$1.text,Pos:$1.pos}} }
 | IF EXISTS name { $$=dropTarget{Name:Identifier{Text:$3.text,Pos:$3.pos},IfExists:true} } ;
/* EXISTS cannot start an ALTER action: retain it for IF EXISTS lookahead,
 * rather than competing with an unqualified relation literally named if. */
alter_action: generic_word_without_exists | SET ;
column: name type column_collation nullability { $$ = &Column{Name:$1.text, TypeName:$2, Collation:$3, Constraints:$4, Pos:$1.pos} } ;
/* Signatures reuse valid declaration builders, never the broad two-word
 * column diagnostic guard. Parameter names and generic type names reduce
 * through the same type_name category, avoiding duplicate name derivations. */
type: declaration_type_base declared_array_bounds {$$=$1;$$.ArrayBounds=$2.ArrayBounds} ;
declaration_type_base: declared_type_base {$$=$1}
 | declaration_invalid_type {$$=$1} ;
declared_type: declared_type_base declared_array_bounds {$$=$1;$$.ArrayBounds=$2.ArrayBounds} ;
declared_type_base: INTEGER { $$=TypeName{Name:"integer",Pos:$1.pos} }
 | BIGINT { $$=TypeName{Name:"bigint",Pos:$1.pos} }
 | TEXT %prec TYPE_NAME { $$=TypeName{Name:"text",Pos:$1.pos} }
 | BOOLEAN { $$=TypeName{Name:"boolean",Pos:$1.pos} }
 | type_name %prec TYPE_NAME { $$=inputColumnType(sqllex,$1) }
 | temporal_type { $$=$1;$$.System=sqllex.(*lexer).typeInputMode||sqllex.(*lexer).operatorDDL }
 | interval_type { $$=$1;$$.System=sqllex.(*lexer).typeInputMode||sqllex.(*lexer).operatorDDL }
 | type_modifier_head generic_modifiers ')' { if sqllex.(*lexer).floatPrecision { $$=$2;$$.Pos=$1.Pos;sqllex.(*lexer).floatPrecision=false } else { $$=$1;$$.Modifiers=$2.Modifiers };sqllex.(*lexer).characterPrecision=false;sqllex.(*lexer).characterGeneric=false;sqllex.(*lexer).bitModifier=false }
 | DOUBLE_TYPE PRECISION_TYPE {$$=TypeName{Name:"double precision",Pos:$1.pos}}
 | CHARACTER_TYPE VARYING_TYPE {$$=TypeName{Name:"character varying",Pos:$1.pos}}
 | BIT_TYPE VARYING_TYPE {$$=TypeName{Name:"bit varying",Pos:$1.pos}}
 | type_name '.' col_label { $$=TypeName{Name:$3.text,Quoted:columnType($3).Quoted,Schema:&Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos,Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}} } ;
declaration_invalid_type: type_name type_name %prec TYPE_NAME { if excludedTypeTail($1,$2) {sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset)} else {sqllex.(*lexer).syntax($2)};return 1 }
 | type_name WITH { if excludedTypeTail($1,$2) {sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset)} else {sqllex.(*lexer).syntax($2)};return 1 }
 | type_name type_name '(' {sqllex.(*lexer).syntax($2);return 1} ;
declared_array_bounds: /* empty */ {$$=TypeName{}}
 | declared_array_bounds '[' ']' {$$=$1;$$.ArrayBounds=append($$.ArrayBounds,0)}
 | declared_array_bounds '[' NUMBER ']' {n,ok:=sequenceInteger(sqllex,$3,"");if !ok{return 1};if n>2147483647{sqllex.(*lexer).failAt("22003","array dimension is out of range",$3.pos.Offset);return 1};$$=$1;$$.ArrayBounds=append($$.ArrayBounds,int32(n))}
 | declared_array_bounds ARRAY {$$=$1;$$.ArrayBounds=append($$.ArrayBounds,0)}
 | declared_array_bounds ARRAY '[' NUMBER ']' {n,ok:=sequenceInteger(sqllex,$4,"");if !ok{return 1};if n>2147483647{sqllex.(*lexer).failAt("22003","array dimension is out of range",$4.pos.Offset);return 1};$$=$1;$$.ArrayBounds=append($$.ArrayBounds,int32(n))} ;
/* Descriptor modifiers share raw constant syntax; FLOAT and temporal types retain
 * their distinct unsigned/single precision contracts. */
type_modifier_head: type_name '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2,false);if !ok{return 1}}
 | TEXT '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2,false);if !ok{return 1}}
 | CHARACTER_TYPE VARYING_TYPE '(' {tok:=$1;tok.text="character varying";var ok bool;$$,ok=typeModifierHead(sqllex,tok,$3,false);if !ok{return 1}}
 | BIT_TYPE VARYING_TYPE '(' {tok:=$1;tok.text="bit varying";var ok bool;$$,ok=typeModifierHead(sqllex,tok,$3,false);if !ok{return 1}}
 | type_name '.' col_label '(' {tok:=$3;tok.raw="\""+$3.text+"\"";tok.keyword=nil;var ok bool;$$,ok=typeModifierHead(sqllex,tok,$4,true);if !ok{return 1};$$.Name=$3.text;$$.Quoted=columnType($3).Quoted;$$.Schema=&Identifier{Text:$1.text,Pos:$1.pos};$$.Pos=$1.pos;if sqllex.(*lexer).operatorDDL {$$.Parts=[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}}} ;
generic_modifiers: generic_precision { $$=$1 }
 | generic_modifiers ',' {if !sqllex.(*lexer).multipleModifiers {sqllex.(*lexer).syntax($2);return 1}} generic_precision {$$=$1;$$.Modifiers=append($$.Modifiers,$4.Modifiers...) } ;
/* Keep keyword tokens in these productions: a temporal_keyword reduction would
 * compete with setting_word on modifier and zone continuations. */
temporal_type: TIMESTAMP %prec TYPE_NAME { $$=TypeName{Name:$1.text,Pos:$1.pos} }
 | TIMESTAMP '(' NUMBER ')' %prec TYPE_NAME {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIMESTAMP WITHOUT TIME ZONE {$$=TypeName{Name:$1.text,Pos:$1.pos}}
 | TIMESTAMP '(' NUMBER ')' WITHOUT TIME ZONE {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIMESTAMP WITH TIME ZONE {$$=TypeName{Name:"timestamptz",Pos:$1.pos}}
 | TIMESTAMP '(' NUMBER ')' WITH TIME ZONE {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name="timestamptz";$$.Pos=$1.pos}
 | TIME %prec TYPE_NAME { $$=TypeName{Name:$1.text,Pos:$1.pos} }
 | TIME '(' NUMBER ')' %prec TYPE_NAME {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIME WITHOUT TIME ZONE {$$=TypeName{Name:$1.text,Pos:$1.pos}}
 | TIME '(' NUMBER ')' WITHOUT TIME ZONE {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIME WITH TIME ZONE {$$=TypeName{Name:"timetz",Pos:$1.pos}}
 | TIME '(' NUMBER ')' WITH TIME ZONE {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name="timetz";$$.Pos=$1.pos} ;
interval_type: INTERVAL %prec TYPE_NAME { $$=TypeName{Name:"interval",Pos:$1.pos} }
 | INTERVAL interval_type_qualifier { $$=$2;$$.Name="interval";$$.Pos=$1.pos }
 | INTERVAL '(' NUMBER ')' %prec TYPE_NAME {var ok bool;$$,ok=intervalPrecision(sqllex,32767,$3,$2);if !ok{return 1};$$.Name="interval";$$.Pos=$1.pos} ;
interval_type_qualifier: interval_fields %prec TYPE_NAME {$$=$1}
 | interval_second_fields '(' NUMBER ')' {var ok bool;$$,ok=intervalPrecision(sqllex,*$1.Modifiers[0].Integer,$3,$2);if !ok{return 1}}
 | interval_fields '(' {sqllex.(*lexer).syntax($2);return 1} ;
interval_fields: YEAR %prec TYPE_NAME {$$=intervalField(4,$1.pos)}
 | MONTH %prec TYPE_NAME {$$=intervalField(2,$1.pos)}
 | DAY %prec TYPE_NAME {$$=intervalField(8,$1.pos)}
 | HOUR %prec TYPE_NAME {$$=intervalField(1024,$1.pos)}
 | MINUTE %prec TYPE_NAME {$$=intervalField(2048,$1.pos)}
 | YEAR TO MONTH {$$=intervalField(6,$1.pos)}
 | DAY TO HOUR {$$=intervalField(1032,$1.pos)}
 | DAY TO MINUTE {$$=intervalField(3080,$1.pos)}
 | HOUR TO MINUTE {$$=intervalField(3072,$1.pos)}
 | interval_second_fields %prec TYPE_NAME {$$=$1} ;
interval_second_fields: SECOND %prec TYPE_NAME {$$=intervalField(4096,$1.pos)}
 | DAY TO SECOND {$$=intervalField(7176,$1.pos)}
 | HOUR TO SECOND {$$=intervalField(7168,$1.pos)}
 | MINUTE TO SECOND {$$=intervalField(6144,$1.pos)} ;
/* Bit modifiers retain only simple constants or nonsimple provenance, never
 * evaluated arithmetic. Chains and parentheses therefore need no value tree. */
generic_precision: generic_modifier_atom {$$=$1}
 | generic_precision bit_modifier_operator generic_modifier_atom {$$=$1;$$.Modifiers[0]=TypeModifier{NonSimple:true,Pos:$1.Modifiers[0].Pos}} ;
bit_modifier_operator: bit_modifier_symbol {if !sqllex.(*lexer).bitModifier {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
bit_modifier_symbol: '+' {$$=$1} | '-' {$$=$1} | '*' {$$=$1} | '/' {$$=$1} | '%' {$$=$1} | '^' {$$=$1} ;
generic_modifier_atom: NUMBER {var ok bool;if sqllex.(*lexer).floatPrecision {$$,ok=floatType(sqllex,lexeme{text:"float"},$1)} else {$$,ok=temporalPrecision(sqllex,$1,false)};if !ok{return 1}}
 | '-' {if sqllex.(*lexer).floatPrecision || sqllex.(*lexer).characterPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$3,true);if !ok{return 1};$$.Modifiers[0].Pos=$1.pos}
 | '+' {if sqllex.(*lexer).floatPrecision || sqllex.(*lexer).characterPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Modifiers[0].UnaryPlus=true;$$.Modifiers[0].NonSimple=sqllex.(*lexer).bitModifier;$$.Modifiers[0].Pos=$1.pos}
 | STRING {if !sqllex.(*lexer).characterGeneric {sqllex.(*lexer).syntax($1);return 1};$$=TypeName{Modifiers:[]TypeModifier{{Raw:$1.text,Pos:$1.pos}}}}
 | name {if !sqllex.(*lexer).characterGeneric {sqllex.(*lexer).syntax($1);return 1};$$=TypeName{Modifiers:[]TypeModifier{{Raw:$1.text,Pos:$1.pos}}}}
 | '(' {if !sqllex.(*lexer).bitModifier {sqllex.(*lexer).syntax($1);return 1}} generic_precision ')' {$$=$3} ;
nullability: /* empty */ { $$ = nil }
 | column_constraints { $$=$1 }
 | column_suffix { sqllex.(*lexer).capability($1,unsupportedSuffixes,"column constraints are unsupported"); return 1 }
 | '(' { sqllex.(*lexer).failAt("0A000","type modifiers are unsupported",$1.pos.Offset); return 1 } ;
/* Orphan attributes remain syntax-only entries for utility-stage diagnostics. */
column_constraints: column_constraint { $$=[]ColumnConstraint{*$1} }
 | column_constraints column_constraint { $$=appendColumnConstraint($1,*$2) }
 | column_constraints NO nn_inherit {c:=&$1[len($1)-1];if c.Check!=nil {if !c.EnforcementSet {sqllex.(*lexer).syntax($2);return 1};c.Check.NoInherit=true} else {sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported")};$$=$1}
 | column_constraints NOT KW_U {if $3.text!="valid" {sqllex.(*lexer).syntax($3);return 1};sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported");$$=$1}
 | column_constraints NOT_DEFERRABLE_LA DEFERRABLE {if !sqllex.(*lexer).columnForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | column_constraints DEFERRABLE {if !sqllex.(*lexer).columnForeignProperty($1,$2,lexeme{}){return 1};$$=$1}
 | column_constraints INITIALLY name {if !sqllex.(*lexer).columnForeignProperty($1,$2,$3){return 1};$$=$1} ;
column_constraint: NULL { $$=&ColumnConstraint{Pos:$1.pos} }
 | NOT NULL { $$=&ColumnConstraint{NotNull:true,Pos:$1.pos} }
 | ENFORCED {$$=&ColumnConstraint{EnforcementSet:true,EnforcementPos:$1.pos}}
 | NOT ENFORCED {$$=&ColumnConstraint{EnforcementSet:true,NotEnforced:true,EnforcementPos:$1.pos}}
 | CONSTRAINT name NULL { $$=&ColumnConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos,NamedDeclarationPos:$1.pos} }
 | CONSTRAINT name NOT NULL { $$=&ColumnConstraint{NotNull:true,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos,NamedDeclarationPos:$1.pos} }
 | DEFAULT create_default_target { if !sqllex.(*lexer).defaultIndexPropertyAllowed($2) {return 1};$$=&ColumnConstraint{Default:$2,Pos:$1.pos} }
 | CONSTRAINT name DEFAULT create_default_target { if !sqllex.(*lexer).defaultIndexPropertyAllowed($4) {return 1};$$=&ColumnConstraint{Default:$4,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos,NamedDeclarationPos:$1.pos} }
 | check_constraint { $$=&ColumnConstraint{Check:$1,Pos:$1.Pos} }
 | column_unique { $$=&ColumnConstraint{Unique:$1,Pos:$1.Pos} }
 | column_primary {$$=&ColumnConstraint{Unique:$1,Pos:$1.Pos}}
 | foreign_definition {$$=&ColumnConstraint{ForeignKey:$1,Pos:$1.Pos}}
 | CONSTRAINT name foreign_definition {$3.Name=Identifier{Text:$2.text,Pos:$2.pos};$$=&ColumnConstraint{ForeignKey:$3,Pos:$3.Pos}}
 | identity_definition { $$=&ColumnConstraint{Identity:$1,Pos:$1.Pos} }
 | CONSTRAINT name identity_definition { $$=&ColumnConstraint{Identity:$3,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | generation_definition { $$=&ColumnConstraint{Generation:$1,Pos:$1.Pos} }
 | CONSTRAINT name generation_definition { $$=&ColumnConstraint{Generation:$3,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.Pos} }
 | CONSTRAINT name column_suffix { sqllex.(*lexer).capability($3,unsupportedSuffixes,"column constraints are unsupported"); return 1 } ;
nn_modifier: KW_U { if $1.text!="valid" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } | ENFORCED {$$=$1} ;
nn_inherit: INHERIT { $$=$1 } ;
column_suffix: LIKE | KW_T | KW_R | RETURNING | HAVING | INTO | ALL | ON | GROUP | WITH | USER | IN | IS | AND ;
update: UPDATE alter_target update_alias SET update_row_assignments update_from mutation_where returning { $$=&Update{Name:$2.Name,Only:$2.Only,Star:$2.Star,Alias:Identifier{Text:$3.text,Pos:$3.pos},Assignments:$5,From:$6,Where:$7.Where,CurrentOf:$7.CurrentOf,Returning:$8,Pos:$1.pos} }
 | UPDATE alter_target update_alias SET update_row_assignments update_from mutation_where suffix { sqllex.(*lexer).capability($8,unsupportedSuffixes,"UPDATE suffix is unsupported"); return 1 } ;
update_alias: /* empty */ %prec '=' { $$=lexeme{} } | name { $$=$1 } | AS name { $$=$2 } ;
update_from: /* empty */ { $$=nil } | FROM from_list { $$=$2 } ;
update_assignments: update_assignment { $$=[]UpdateAssignment{*$1} } | update_assignments ',' update_assignment { $$=append($1,*$3) }
 | '(' { sqllex.(*lexer).failAt("0A000","row assignments are unsupported",$1.pos.Offset);return 1 }
 | update_assignments ',' '(' { sqllex.(*lexer).failAt("0A000","row assignments are unsupported",$3.pos.Offset);return 1 } ;
update_row_assignments: update_row_assignment { $$=[]UpdateAssignment{*$1} } | update_row_assignments ',' update_row_assignment { $$=append($1,*$3) } ;
update_row_assignment: update_assignment { $$=$1 }
 | '(' insert_targets ')' '=' scalar_projection { $$=&UpdateAssignment{Targets:$2,Value:$5,Pos:$1.pos} } ;
update_assignment: name '=' scalar_projection { $$=&UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Value:$3} }
 | name target_steps '=' scalar_projection { $$=&UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Steps:$2,Value:$4} }
 | name target_steps '=' DEFAULT { $$=&UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Steps:$2,Value:DefaultExpr{Pos:$4.pos}} }
 | name '=' DEFAULT { $$=&UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Value:DefaultExpr{Pos:$3.pos}} }
 | name '=' scalar_projection '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '*' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '/' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 } ;
insert: insert_base insert_conflict returning { $1.Conflict=$2;$1.Returning=$3;$$=$1 }
 | insert_base suffix { sqllex.(*lexer).capability($2,unsupportedSuffixes,"INSERT suffix is unsupported");return 1 } ;
insert_head: INSERT INTO {sqllex.(*lexer).insertStatement=true;$$=$1} ;
insert_alias: /* empty */ { $$=lexeme{} } | AS name { $$=$2 } ;
insert_base: insert_head relation_name insert_alias insert_input { $$=insertStatement($2,nil,$4,$1.pos);$$.Alias=Identifier{Text:$3.text,Pos:$3.pos} }
 | insert_head relation_name insert_alias '(' insert_targets ')' insert_input { $$=insertStatement($2,$5,$7,$1.pos);$$.Alias=Identifier{Text:$3.text,Pos:$3.pos} }
 | insert_head relation_name insert_alias insert_override insert_input { $$=insertStatement($2,nil,$5,$1.pos);$$.Override=$4;$$.Alias=Identifier{Text:$3.text,Pos:$3.pos} }
 | insert_head relation_name insert_alias '(' insert_targets ')' insert_override insert_input { $$=insertStatement($2,$5,$8,$1.pos);$$.Override=$7;$$.Alias=Identifier{Text:$3.text,Pos:$3.pos} }
 | insert_head relation_name insert_alias DEFAULT VALUES { $$=&Insert{Name:$2,Alias:Identifier{Text:$3.text,Pos:$3.pos},Rows:[][]Expr{nil},Pos:$1.pos,DefaultValues:true} } ;
insert_conflict: /* empty */ {$$=nil}
 | ON_CONFLICT_LA CONFLICT conflict_target DO NOTHING {$3.OnPos=$1.pos;$$=$3}
 | ON_CONFLICT_LA CONFLICT conflict_target DO UPDATE SET update_assignments where {$3.OnPos=$1.pos;$3.Update=true;$3.Assignments=$7;$3.Where=$8;$$=$3} ;
conflict_target: /* empty */ {$$=&InsertConflict{}}
 | conflict_open index_keys ')' conflict_predicate {sqllex.(*lexer).indexExpression=false;$$=&InsertConflict{Keys:insertConflictKeys($2),Predicate:$4,Pos:$1.pos}}
 | ON CONSTRAINT name {$$=&InsertConflict{Constraint:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos}} ;
conflict_open: '(' {sqllex.(*lexer).indexExpression=true;$$=$1} ;
conflict_predicate: /* empty */ {$$=nil}
 | WHERE target_or {$$=$2} ;
insert_override: OVERRIDING SYSTEM VALUE { $$=OverrideSystem } | OVERRIDING USER VALUE { $$=OverrideUser } ;
merge: MERGE INTO relation_name update_alias USING joined_table ON target_or merge_arms returning {
   $$=&Merge{Name:$3,Alias:Identifier{Text:$4.text,Pos:$4.pos},Source:$6,On:$8,Arms:$9,Returning:$10,Pos:$1.pos}
 } ;
merge_arms: merge_arm { $$=[]MergeArm{*$1} }
 | merge_arms merge_arm { $$=append($1,*$2) } ;
merge_target_class: /* empty */ { $$=lexeme{} } | BY TARGET { $$=$2 } ;
merge_condition: /* empty */ { $$=nil } | AND target_or { $$=$2 } ;
merge_arm: WHEN NOT MATCHED merge_target_class merge_condition THEN merge_insert { $$=$7;$$.Condition=$5;$$.Pos=$1.pos }
 | WHEN NOT MATCHED merge_target_class merge_condition THEN DO NOTHING { $$=&MergeArm{Match:MergeNotMatched,Action:MergeNothingAction,Condition:$5,Pos:$1.pos,ActionPos:$7.pos} }
 | WHEN MATCHED merge_condition THEN UPDATE SET update_assignments { $$=&MergeArm{Match:MergeMatched,Action:MergeUpdateAction,Condition:$3,Assignments:$7,Pos:$1.pos,ActionPos:$5.pos} }
 | WHEN MATCHED merge_condition THEN DELETE { $$=&MergeArm{Match:MergeMatched,Action:MergeDeleteAction,Condition:$3,Pos:$1.pos,ActionPos:$5.pos} }
 | WHEN MATCHED merge_condition THEN DO NOTHING { $$=&MergeArm{Match:MergeMatched,Action:MergeNothingAction,Condition:$3,Pos:$1.pos,ActionPos:$5.pos} }
 | WHEN NOT MATCHED BY SOURCE merge_condition THEN DELETE { $$=&MergeArm{Match:MergeNotMatchedBySource,Action:MergeDeleteAction,Condition:$6,Pos:$1.pos,ActionPos:$8.pos} }
 | WHEN NOT MATCHED BY SOURCE merge_condition THEN UPDATE SET update_assignments { $$=&MergeArm{Match:MergeNotMatchedBySource,Action:MergeUpdateAction,Condition:$6,Assignments:$10,Pos:$1.pos,ActionPos:$8.pos} }
 | WHEN NOT MATCHED BY SOURCE merge_condition THEN DO NOTHING { $$=&MergeArm{Match:MergeNotMatchedBySource,Action:MergeNothingAction,Condition:$6,Pos:$1.pos,ActionPos:$8.pos} } ;
merge_insert: INSERT VALUES '(' query_cells ')' { $$=&MergeArm{Values:$4,ActionPos:$1.pos} }
 | INSERT '(' insert_targets ')' VALUES '(' query_cells ')' { $$=&MergeArm{Targets:$3,Values:$7,ActionPos:$1.pos} }
 | INSERT insert_override VALUES '(' query_cells ')' { $$=&MergeArm{Override:$2,Values:$5,ActionPos:$1.pos} }
 | INSERT '(' insert_targets ')' insert_override VALUES '(' query_cells ')' { $$=&MergeArm{Targets:$3,Override:$5,Values:$8,ActionPos:$1.pos} }
 | INSERT DEFAULT VALUES { $$=&MergeArm{DefaultValues:true,ActionPos:$1.pos} } ;
delete: DELETE FROM alter_target delete_alias delete_using mutation_where returning { $$=&Delete{Name:$3.Name,Only:$3.Only,Star:$3.Star,Alias:Identifier{Text:$4.text,Pos:$4.pos},Using:$5,Where:$6.Where,CurrentOf:$6.CurrentOf,Returning:$7,Pos:$1.pos} }
 | DELETE FROM alter_target delete_alias delete_using mutation_where suffix { if $5!=nil && $7.text=="on" {sqllex.(*lexer).syntax($7);return 1}; sqllex.(*lexer).capability($7,unsupportedSuffixes,"DELETE suffix is unsupported"); return 1 } ;
delete_using: /* empty */ { $$=nil } | USING from_list { $$=$2 } ;
delete_alias: /* empty */ { $$=lexeme{} } | name { $$=$1 } | AS name { $$=$2 } ;
returning: /* empty */ { $$=nil }
 | RETURNING returning_aliases projection { $$=&Returning{Targets:$3.Targets,Labels:$3.Labels,Aliases:$2,Pos:$1.pos} } ;
returning_aliases: /* empty */ { $$=nil }
 | WITH '(' returning_options ')' { $$=$3 } ;
returning_options: returning_option { $$=[]ReturningAlias{$1} }
 | returning_options ',' returning_option { $$=append($1,$3) } ;
returning_option: name AS name {
   if $1.keyword==nil || ($1.text!="old" && $1.text!="new") { sqllex.(*lexer).syntax($1);return 1 }
   $$=ReturningAlias{New:$1.text=="new",Alias:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos}
 } ;
mutation_where: /* empty */ { $$=mutationQualifier{} } | WHERE target_or { $$=mutationQualifier{Where:$2} }
 | WHERE KW_U OF name {
   if $2.keyword==nil || $2.text!="current" { sqllex.(*lexer).syntax($3); return 1 }
   $$=mutationQualifier{CurrentOf:&Identifier{Text:$4.text,Pos:$4.pos}}
 } ;
like_operator: LIKE | ILIKE ;
target_escape: /* empty */ %prec BETWEEN { $$=nil } | ESCAPE target_base { $$=$2 } ;
insert_targets: insert_target { $$=[]InsertTarget{$1} } | insert_targets ',' insert_target { $$=append($1,$3) } ;
insert_target: name { $$=InsertTarget{Name:$1.text,Pos:$1.pos} }
 | name target_steps { $$=InsertTarget{Name:$1.text,Pos:$1.pos,Steps:$2} } ;
target_steps: target_step { $$=[]TargetStep{$1} } | target_steps target_step { $$=append($1,$2) } ;
target_step: '.' col_label { f:=Identifier{Text:$2.text,Pos:$2.pos};$$=TargetStep{Field:&f} }
 | array_subscript { s:=$1;$$=TargetStep{Subscript:&s} } ;
array_subscripts: array_subscript { $$=[]ArraySubscript{$1} } | array_subscripts array_subscript { $$=append($1,$2) } ;
array_subscript: '[' scalar_projection ']' { $$=ArraySubscript{Upper:$2,Pos:$1.pos} }
 | '[' scalar_projection ':' scalar_projection ']' { if $3.text!=":" {sqllex.(*lexer).syntax($3);return 1};$$=ArraySubscript{Lower:$2,Upper:$4,Slice:true,Pos:$1.pos} }
 | '[' ':' scalar_projection ']' { if $2.text!=":" {sqllex.(*lexer).syntax($2);return 1};$$=ArraySubscript{Upper:$3,Slice:true,Pos:$1.pos} }
 | '[' scalar_projection ':' ']' { if $3.text!=":" {sqllex.(*lexer).syntax($3);return 1};$$=ArraySubscript{Lower:$2,Slice:true,Pos:$1.pos} }
 | '[' ':' ']' { if $2.text!=":" {sqllex.(*lexer).syntax($2);return 1};$$=ArraySubscript{Slice:true,Pos:$1.pos} } ;
insert_input: insert_select {
   if $1.Values!=nil {
      if !sqllex.(*lexer).validateInsertValues($1.Values) {return 1}
      if len($1.Locking)==0 {$$=&InsertSource{Values:$1.Values}} else {$$=&InsertSource{Select:$1}}
   } else {$$=&InsertSource{Select:$1}}
 }
 | '(' insert_input ')' { $$=$2 } ;
insert_select: query_select { $$=$1 } ;
names: name %prec NAME_LIST { $$ = []string{$1.text} } | names ',' name %prec NAME_LIST { $$ = append($1,$3.text) } ;
values: scalar { if !sqllex.(*lexer).scalarInput($1) { return 1 }; $$ = []Expr{$1} } | values ',' scalar { if !sqllex.(*lexer).scalarInput($3) { return 1 }; $$ = append($1,$3) } ;
query_values: VALUES query_rows order bounds { if !sqllex.(*lexer).validateTies($4,$3,nil) {return 1};$$=&ValuesQuery{Rows:$2,Order:$3,Bounds:$4,Pos:$1.pos} } ;
query_rows: query_row { $$=[][]Expr{$1} } | query_rows ',' query_row { $$=append($1,$3) } ;
query_row: '(' query_cells ')' { sqllex.(*lexer).queryRowEnd=$3.pos.Offset+1;$$=$2 } ;
query_cells: query_cell { $$=[]Expr{$1} } | query_cells ',' query_cell { $$=append($1,$3) } ;
query_cell: scalar_projection { $$=$1 } | DEFAULT { $$=DefaultExpr{Pos:$1.pos} } ;
insert_scalar_rows: insert_scalar_row { $$=[][]Expr{$1} } | insert_scalar_rows ',' insert_scalar_row { $$=append($1,$3) } ;
insert_scalar_row: '(' insert_scalar_cells ')' { $$=$2 } ;
insert_scalar_cells: insert_scalar_cell { $$=[]Expr{$1} } | insert_scalar_cells ',' insert_scalar_cell { $$=append($1,$3) } ;
insert_scalar_cell: scalar { if !sqllex.(*lexer).scalarInput($1) {return 1};$$=$1 } | DEFAULT { $$=DefaultExpr{Pos:$1.pos} } ;
scalar: scalar_atom { $$=$1 }
 | function { call:=$1.(FunctionExpr);call.directSelectedCall=true;$$=call }
 | field_value { $$=$1 }
 | relation_star { $$=$1 }
 | scalar GENERATION_OVER '(' generation_window_spec ')' { restriction:=GenerationRestriction{Code:"42P20",Message:"window functions are not allowed in column generation expressions",Pos:$1.SourcePosition()};l:=sqllex.(*lexer);if l.generationRestriction==nil {l.generationRestriction=&restriction} else {*l.generationRestriction=restriction};if l.generationDepth==0 {l.generationCompletedRestriction=l.generationRestriction};$$=ForbiddenGenerationExpr{restriction} }
 | select_with_parens %prec NAME_LIST { $$=SubqueryExpr{Kind:SubqueryScalar,Query:$1.Query,Pos:$1.Pos} }
 | EXISTS select_with_parens %prec NAME_LIST { $$=SubqueryExpr{Kind:SubqueryExists,Query:$2.Query,Pos:$1.pos} }
 | overlaps_row %prec NAME_LIST { $$=$1 }
 | overlaps_row OVERLAPS overlaps_row {
   left,right:=$1.(RowExpr),$3.(RowExpr)
   if len(left.Fields)!=2 {sqllex.(*lexer).failAt("42601","wrong number of parameters on left side of OVERLAPS expression",left.Pos.Offset);return 1}
   if len(right.Fields)!=2 {sqllex.(*lexer).failAt("42601","wrong number of parameters on right side of OVERLAPS expression",right.Pos.Offset);return 1}
   $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$2.pos},{Text:"overlaps",Pos:$2.pos}}},Args:[]Expr{left.Fields[0],left.Fields[1],right.Fields[0],right.Fields[1]},Syntax:SQLFunctionSyntax,Pos:$2.pos}
 }
 | CAST '(' scalar AS type ')' { if !selectedScalarChild(sqllex,$3,$4) {return 1};if $5.Name=="regclass" && $5.Schema==nil && len($5.Modifiers)==0 && len($5.ArrayBounds)==0 { $$=RegclassExpr{Child:$3,Pos:$1.pos} } else { $$=CastExpr{Child:$3,TypeName:$5,Syntax:StandardCast,Pos:$1.pos} } }
 | scalar TYPECAST declared_type %prec TYPECAST { if !selectedScalarChild(sqllex,$1,$2) {return 1};if $3.Name=="regclass" && $3.Schema==nil && len($3.Modifiers)==0 && len($3.ArrayBounds)==0 { $$=RegclassExpr{Child:$1,Pos:$2.pos} } else { $$=CastExpr{Child:$1,TypeName:$3,Syntax:PostfixCast,Pos:$2.pos} } }
 | scalar COLLATE relation_name %prec COLLATE { if !selectedScalarChild(sqllex,$1,$2) {return 1};$$=CollateExpr{Child:$1,Name:$3,Pos:$2.pos} }
 | aggregate_application { $$=$1 }
 | sqljson_constructor { $$=$1 }
 | sqljson_query { $$=$1 }
 | GROUPING '(' function_values ')' { $$=GroupingExpr{Args:$3,Pos:$1.pos} }
 /* Share the call opener, but retain raw DATE modifiers instead of reducing
  * them as scalar literals (which would convert overflowing numbers too early). */
 | cast_function date_literal_precision ')' STRING {raw:=columnType($1);raw.Modifiers=$2.Modifiers;$$=TypedLiteralExpr{TypeName:raw,Text:$4.text,Pos:$4.pos}}
 | qualified_call_head date_literal_precision ')' STRING {raw:=$1;raw.Modifiers=$2.Modifiers;$$=TypedLiteralExpr{TypeName:raw,Text:$4.text,Pos:$4.pos}}
 | '+' scalar %prec UNARY { $$=arithmeticExpr(sqllex,$1,$2,nil) }
 | '-' scalar %prec UNARY { $$=negateExpr(sqllex,$1,$2) }
 | SYMBOLIC_OPERATOR scalar %prec SYMBOLIC_OPERATOR { $$=arithmeticExpr(sqllex,$1,$2,nil) }
 | scalar '+' scalar { $$=arithmeticExpr(sqllex,$2,$1,$3) }
 | scalar '-' scalar { $$=arithmeticExpr(sqllex,$2,$1,$3) }
 | scalar '*' scalar { $$=arithmeticExpr(sqllex,$2,$1,$3) }
 | scalar '/' scalar { $$=arithmeticExpr(sqllex,$2,$1,$3) }
 | scalar '%' scalar { $$=arithmeticExpr(sqllex,$2,$1,$3) }
 | scalar '^' scalar { $$=arithmeticExpr(sqllex,$2,$1,$3) }
 | scalar AT TIME ZONE scalar %prec AT { if !selectedScalarChild(sqllex,$1,$2) || !selectedScalarChild(sqllex,$5,sqllex.(*lexer).current) {return 1};$$=TimeZoneExpr{Value:$1,Zone:$5,Pos:$2.pos} }
 | scalar AT LOCAL %prec AT { if !selectedScalarChild(sqllex,$1,$2) {return 1};$$=TimeZoneExpr{Value:$1,Local:true,Pos:$2.pos} }
 | scalar SYMBOLIC_OPERATOR scalar { $$=arithmeticExpr(sqllex,$2,$1,$3) }
 | scalar qualified_operator scalar %prec OPERATOR { $$=OperatorExpr{Name:$2,Args:[]Expr{$1,$3},Pos:$2.Parts[0].Pos} }
 | scalar qualified_operator quantifier select_with_parens %prec OPERATOR {kind:=SubqueryAny;if $3.text=="all" {kind=SubqueryAll};$$=quantifiedQuery($1,$2,kind,$4.Query,false,$2.Parts[0].Pos)}
 | scalar qualified_operator quantifier '(' scalar_projection ')' %prec OPERATOR { $$=ArrayComparisonExpr{Left:$1,Right:$5,Name:$2,All:$3.text=="all",Pos:$2.Parts[0].Pos} }
 | qualified_operator scalar %prec OPERATOR { $$=OperatorExpr{Name:$1,Args:[]Expr{$2},Pos:$1.Parts[0].Pos} } ;
/* All grouped expressions reduce through one primary before any postfix. */
grouped_primary: '(' scalar_projection ')' { expr:=$2;if call,ok:=expr.(FunctionExpr);ok {call.directSelectedCall=false;expr=call};$$=groupedPrimary{Expr:expr,Pos:$1.pos} } ;
field_value: grouped_primary '.' col_label { $$=FieldExpr{Base:$1.Expr,Field:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.Expr.SourcePosition(),OpenPos:$1.Pos} }
 | grouped_primary '.' '*' { $$=FieldExpr{Base:$1.Expr,Expansion:true,Pos:$1.Expr.SourcePosition(),OpenPos:$1.Pos} }
 | PARAM '.' col_label { $$=FieldExpr{Base:ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos},Field:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | column_reference array_subscripts '.' col_label {base:=SubscriptExpr{Base:$1,Subscripts:$2,Pos:$1.SourcePosition()};$$=FieldExpr{Base:base,Field:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.SourcePosition()}}
 | PARAM array_subscripts '.' col_label {base:=SubscriptExpr{Base:ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos},Subscripts:$2,Pos:$1.pos};$$=FieldExpr{Base:base,Field:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos}}
 | field_value '.' col_label { $$=FieldExpr{Base:$1,Field:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.SourcePosition()} }
 | field_value '.' '*' { $$=FieldExpr{Base:$1,Expansion:true,Pos:$1.SourcePosition()} }
 | field_value array_subscripts { $$=SubscriptExpr{Base:$1,Subscripts:$2,Pos:$1.SourcePosition()} } ;
relation_star: name '.' '*' { $$=StarExpr{Qualifier:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos,QualificationPos:$2.pos} }
 | name '.' col_label '.' '*' { $$=StarExpr{Qualifier:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos,QualificationPos:$2.pos} } ;
overlaps_row: ROW '(' ')' { $$=RowExpr{Explicit:true,Pos:$1.pos} }
 | ROW '(' row_fields ')' { $$=RowExpr{Fields:$3,Explicit:true,Pos:$1.pos} }
 | '(' scalar_projection ',' row_fields ')' { $$=RowExpr{Fields:append([]Expr{$2},$4...),Pos:$1.pos} }
 | '(' DEFAULT ',' row_fields ')' { $$=RowExpr{Fields:append([]Expr{DefaultExpr{Pos:$2.pos}},$4...),Pos:$1.pos} } ;
scalar_atom: value %prec NAME_LIST { $$=LiteralExpr{Value:$1} }
 | grouped_primary { $$=$1.Expr }
 | grouped_primary array_subscripts { $$=SubscriptExpr{Base:$1.Expr,Subscripts:$2,Pos:$1.Pos} }
 | ARRAY array_bracket { n:=$2.(ArrayExpr);n.Pos=$1.pos;$$=n }
 | column_reference array_subscripts { $$=SubscriptExpr{Base:$1,Subscripts:$2,Pos:$1.SourcePosition()} }
 | PARAM array_subscripts { $$=SubscriptExpr{Base:ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos},Subscripts:$2,Pos:$1.pos} }
 | string_function { $$=$1 }
 | MERGE_ACTION '(' ')' { $$=MergeActionExpr{Pos:$1.pos} }
 | CURRENT_CATALOG { $$=IdentityExpr{Name:$1.text,Pos:$1.pos} }
 | CURRENT_ROLE { $$=IdentityExpr{Name:$1.text,Pos:$1.pos} }
 | CURRENT_USER { $$=IdentityExpr{Name:$1.text,Pos:$1.pos} }
 | SESSION_USER { $$=IdentityExpr{Name:$1.text,Pos:$1.pos} }
 | USER { $$=IdentityExpr{Name:$1.text,Pos:$1.pos} }
 | SYSTEM_USER { $$=IdentityExpr{Name:$1.text,Pos:$1.pos} }
 | CURRENT_SCHEMA { $$=IdentityExpr{Name:$1.text,Pos:$1.pos} }
 | CURRENT_DATE { $$=CurrentDateTimeExpr{Kind:CurrentDate,Pos:$1.pos} }
 | CURRENT_TIME { $$=CurrentDateTimeExpr{Kind:CurrentTime,Pos:$1.pos} }
 | CURRENT_TIMESTAMP { $$=CurrentDateTimeExpr{Kind:CurrentTimestamp,Pos:$1.pos} }
 | LOCALTIME { $$=CurrentDateTimeExpr{Kind:LocalTime,Pos:$1.pos} }
 | LOCALTIMESTAMP { $$=CurrentDateTimeExpr{Kind:LocalTimestamp,Pos:$1.pos} }
 | CURRENT_DATETIME_FUNC '(' ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos} }
 | name '.' CURRENT_DATETIME_FUNC '(' ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos} }
 | name '.' CURRENT_DATETIME_KEYWORD_FUNC '(' ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos} }
 | CURRENT_TIME '(' NUMBER ')' {raw,ok:=currentDateTimePrecision(sqllex,CurrentTime,$1,$3);if !ok{return 1};$$=raw}
 | CURRENT_TIMESTAMP '(' NUMBER ')' {raw,ok:=currentDateTimePrecision(sqllex,CurrentTimestamp,$1,$3);if !ok{return 1};$$=raw}
 | LOCALTIME '(' NUMBER ')' {raw,ok:=currentDateTimePrecision(sqllex,LocalTime,$1,$3);if !ok{return 1};$$=raw}
 | LOCALTIMESTAMP '(' NUMBER ')' {raw,ok:=currentDateTimePrecision(sqllex,LocalTimestamp,$1,$3);if !ok{return 1};$$=raw}
 | IDENTITY_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' IDENTITY_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos} }
 | IDENTITY_KEYWORD_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' IDENTITY_KEYWORD_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos} }
 | PARAM { $$=ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos} }
 | temporal_type STRING { $$=TypedLiteralExpr{TypeName:$1,Text:$2.text,Pos:$2.pos} }
 | INTERVAL STRING %prec TYPE_NAME {$$=TypedLiteralExpr{TypeName:TypeName{Name:"interval",Pos:$1.pos},Text:$2.text,Pos:$2.pos}}
 | INTERVAL STRING interval_type_qualifier {raw:=$3;raw.Name="interval";raw.Pos=$1.pos;$$=TypedLiteralExpr{TypeName:raw,Text:$2.text,Pos:$2.pos}}
 | INTERVAL '(' NUMBER ')' STRING {raw,ok:=intervalPrecision(sqllex,32767,$3,$2);if !ok{return 1};raw.Name="interval";raw.Pos=$1.pos;$$=TypedLiteralExpr{TypeName:raw,Text:$5.text,Pos:$5.pos}}
 /* DATE is an IDENT (also when quoted). TIME/TIMESTAMP STRING continuations
  * belong exclusively to temporal_type, not the ordinary name reduction. */
 | IDENT STRING {if len($1.raw)==1 && ($1.text=="e" || $1.text=="b" || $1.text=="x") {sqllex.(*lexer).failAt("0A000","typed literals are unsupported",$1.pos.Offset);return 1};requiresDomain:=$1.text!="box" && $1.text!="point" && $1.text!="circle" && $1.text!="line" && $1.text!="lseg" && $1.text!="path" && $1.text!="polygon" && $1.text!="tsvector" && $1.text!="tsquery" && $1.text!="tid" && $1.text!="macaddr" && $1.text!="macaddr8" && $1.text!="inet" && $1.text!="cidr" && $1.text!="date" && $1.text!="timestamptz" && $1.text!="timetz" && $1.text!="json" && $1.text!="jsonb" && $1.text!="jsonpath" && $1.text!="money" && $1.text!="pg_lsn";$$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos,RequiresDomain:requiresDomain} }
 | KW_U STRING { $$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos,RequiresDomain:$1.text!="path"} }
 | XML STRING { $$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos,RequiresDomain:true} }
 | EXPLAIN STRING { $$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos,RequiresDomain:true} }
 | KW_C STRING { if $1.text!="json" {sqllex.(*lexer).syntax($2);return 1};$$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos} }
 | column_reference %prec NAME_LIST { $$=$1 }
 | name '.' col_label STRING {raw,ok:=qualifiedTypedLiteral(sqllex,$1,$3);if !ok{return 1};$$=TypedLiteralExpr{TypeName:raw,Text:$4.text,Pos:$4.pos,RequiresDomain:$1.text!="pg_catalog"}}
 /* Qualified column paths are shared with the subscript postfix. */
 | conditional { $$=$1 }
 | INDEX_SUBQUERY_OPEN query_values ')' { $$=IndexSubqueryExpr{Pos:$1.pos} }
 | SEQUENCE_FUNC '(' sequence_args ')' { $$=CallExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3} }
 | name '.' SEQUENCE_FUNC '(' sequence_args ')' { $$=CallExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5} }
 | check_function_call { $$=$1 }
 | transaction_time_call { $$=$1 }
 | EXTRACT '(' extract_field FROM scalar ')' { if !selectedScalarChild(sqllex,$5,$6) {return 1};$$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$1.pos},{Text:"extract",Pos:$1.pos}}},Args:[]Expr{LiteralExpr{Value:Value{Kind:StringValue,Text:$3.text,Pos:$3.pos}},$5},Syntax:SQLFunctionSyntax,Pos:$1.pos} }
 | OVERLAY '(' scalar PLACING scalar FROM scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) || !selectedScalarChild(sqllex,$7,$8) {return 1};$$=binarySQLCall("overlay",$1.pos,[]Expr{$3,$5,$7}) }
 | OVERLAY '(' scalar PLACING scalar FROM scalar FOR scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) || !selectedScalarChild(sqllex,$7,$8) || !selectedScalarChild(sqllex,$9,$10) {return 1};$$=binarySQLCall("overlay",$1.pos,[]Expr{$3,$5,$7,$9}) }
 | OVERLAY '(' function_values ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"overlay",Pos:$1.pos}}},Args:$3,Pos:$1.pos} }
 | OVERLAY '(' ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"overlay",Pos:$1.pos}}},Pos:$1.pos} }
 | POSITION '(' scalar IN scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) {return 1};$$=binarySQLCall("position",$1.pos,[]Expr{$5,$3}) }
 | SUBSTRING '(' scalar FROM scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) {return 1};$$=binarySQLCall("substring",$1.pos,[]Expr{$3,$5}) }
 | SUBSTRING '(' scalar FROM scalar FOR scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) || !selectedScalarChild(sqllex,$7,$8) {return 1};$$=binarySQLCall("substring",$1.pos,[]Expr{$3,$5,$7}) }
 | SUBSTRING '(' scalar FOR scalar FROM scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) || !selectedScalarChild(sqllex,$7,$8) {return 1};$$=binarySQLCall("substring",$1.pos,[]Expr{$3,$7,$5}) }
 | SUBSTRING '(' scalar SIMILAR scalar ESCAPE scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) || !selectedScalarChild(sqllex,$7,$8) {return 1};$$=binarySQLCall("substring",$1.pos,[]Expr{$3,$5,$7}) }
 | SUBSTRING '(' scalar FOR scalar ')' { if !selectedScalarChild(sqllex,$3,$4) || !selectedScalarChild(sqllex,$5,$6) {return 1};$$=binarySQLCall("substring",$1.pos,[]Expr{$3,binarySubstringStart(),binarySubstringCount($5)}) }
 | SUBSTRING '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"substring",Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 ;
/* Only an actual scalar/index call can enter the failure-only argument context. */
check_function_head: CHECK_FUNC '(' {$$=$1;sqllex.(*lexer).formatCallHead($1)} ;
/* Ordinary checked scalar calls retain failure-only FILTER/OVER decorations
 * for resolution, without changing their lexical or argument admission. */
check_function_call: check_function_head function_args ')' {call,ok:=selectedScalarCall(sqllex,$1,$2);if !ok{return 1};$$=call}
 | name '.' check_function_head function_args ')' { l:=sqllex.(*lexer);if restriction:=l.generationRestriction;restriction!=nil && restriction.Pos==$3.pos {restriction.Pos=$1.pos};$$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$4.Args,ArgNames:$4.ArgNames,Variadic:$4.Variadic,ResolutionOnly:$3.resolutionOnly,Pos:$1.pos} }
 | check_function_call FILTER '(' WHERE target_or ')' {call:=$1.(FunctionExpr);if call.Filter!=nil||call.Window!=nil {sqllex.(*lexer).syntax($2);return 1};call.Filter=$5;$$=call}
 | check_function_call over_clause {call:=$1.(FunctionExpr);if call.Window!=nil {sqllex.(*lexer).syntax($2.token);return 1};call.Window=$2.spec;$$=call} ;
/* Selected ordinary time and enum calls carry failure-only modifiers to resolution.
 * ORDER requires an argument; argument-free ORDER remains a syntax error. */
transaction_time_head: TX_TIME_FUNC '(' { sqllex.(*lexer).formatCallHead($1);$$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos} }
 | name '.' TX_TIME_FUNC '(' { sqllex.(*lexer).formatCallHead($3);$$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos} } ;
transaction_time_args: function_args { $$=$1 }
 | '*' { $$=FunctionExpr{Star:true} }
 | DISTINCT function_argument_list aggregate_order { $$=$2;$$.Distinct=true;$$.Order=$3 }
 | ALL function_argument_list aggregate_order { $$=$2;$$.All=true;$$.Order=$3 }
 | function_argument_list ORDER BY order_keys { $$=$1;$$.Order=$4 } ;
transaction_time_base: transaction_time_head transaction_time_args ')' { $$=$2;$$.Name=$1.Name;$$.Pos=$1.Pos } ;
transaction_time_filtered: transaction_time_base %prec ')' { $$=$1 }
 | transaction_time_base WITHIN GROUP '(' ORDER BY order_keys ')' {if !ArrayTransformFunctionName($1.Name.Parts[len($1.Name.Parts)-1].Text) {sqllex.(*lexer).syntax($2);return 1};call,ok:=withinCall(sqllex,$1,$2,$7);if !ok{return 1};$$=call.(FunctionExpr)}
 | transaction_time_base FILTER '(' WHERE target_or ')' { $$=$1;$$.Filter=$5 } ;
/* Keep selected decorations ordered and nonrepeating. Invalid continuations
 * fail at their token rather than falling through to generic bare-label guards. */
transaction_time_call: transaction_time_filtered %prec ')' { $$=$1 }
 | transaction_time_filtered over_clause %prec ')' { $$=$1;$$.Window=$2.spec }
 | transaction_time_filtered FILTER { sqllex.(*lexer).syntax($2);return 1 }
 | transaction_time_filtered over_clause FILTER { sqllex.(*lexer).syntax($3);return 1 }
 | transaction_time_filtered over_clause OVER { sqllex.(*lexer).syntax($3);return 1 } ;
extract_field: IDENT { $$=$1 } | STRING { $$=$1 } | YEAR { $$=$1 } | MONTH { $$=$1 } | DAY { $$=$1 } | HOUR { $$=$1 } | MINUTE { $$=$1 } | SECOND { $$=$1 } ;
conditional: CASE case_test case_arms case_else END { $$=CaseExpr{Test:$2,Arms:$3,Else:$4,Pos:$1.pos} }
 | conditional_call { $$=$1 } ;
conditional_call: COALESCE '(' conditional_args ')' { $$=ConditionalExpr{Kind:Coalesce,Args:$3,Pos:$1.pos} }
 | GREATEST '(' conditional_args ')' { $$=ConditionalExpr{Kind:Greatest,Args:$3,Pos:$1.pos} }
 | LEAST '(' conditional_args ')' { $$=ConditionalExpr{Kind:Least,Args:$3,Pos:$1.pos} }
 | NULLIF '(' target_or ',' target_or ')' { $$=ConditionalExpr{Kind:NullIf,Args:[]Expr{$3,$5},Pos:$1.pos} } ;
case_test: /* empty */ { $$=nil } | target_or { $$=$1 } ;
case_arms: case_arm { $$=[]CaseArm{$1} } | case_arms case_arm { $$=append($1,$2) } ;
case_arm: WHEN target_or THEN conditional_value { $$=CaseArm{Condition:$2,Result:$4,Pos:$1.pos} } ;
case_else: /* empty */ { $$=nil } | ELSE conditional_value { $$=$2 } ;
conditional_value: target_or { $$=$1 } ;
conditional_args: target_or { $$=[]Expr{$1} } | conditional_args ',' target_or { $$=append($1,$3) } ;
generation_window_spec: /* empty */ {} | ORDER BY order_keys {} | GENERATION_PARTITION BY row_fields {} | GENERATION_PARTITION BY row_fields ORDER BY order_keys {} ;
/* Type/function keywords are legal ordinary callees, not identifier names in
 * table/column positions. Their presence does not select an executable provider. */
ordinary_call_keyword: KW_T | CONCURRENTLY | CROSS | INNER | JOIN | NATURAL
 | FULL | OUTER | IS | LIKE | ILIKE | AUTHORIZATION | ISNULL | NOTNULL ;
cast_function: name '(' { if !sqllex.(*lexer).castFunction($1,$2) { return 1 }; $$=$1 }
 | ordinary_call_keyword '(' { if !sqllex.(*lexer).castFunction($1,$2) { return 1 }; $$=$1 } ;
/* Qualified calls and precision literals share their opener so a function-call
 * shift cannot consume the precision before the literal STRING continuation. */
qualified_call_head: name '.' col_label '(' {$$=TypeName{Name:$3.text,Quoted:columnType($3).Quoted,Schema:&Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos,Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}};if $1.text=="pg_catalog" && ($3.text=="timestamptz" || $3.text=="timetz") && sqllex.(*lexer).temporalLiteralAhead() {sqllex.(*lexer).genericModifier=true;sqllex.(*lexer).dateLiteralModifier=true}} ;
date_literal_precision: DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$1,false);if !ok{return 1}}
 | '-' DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$2,true);if !ok{return 1};$$.Modifiers[0].Pos=$1.pos}
 | '+' DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$2,false);if !ok{return 1};$$.Modifiers[0].UnaryPlus=true;$$.Modifiers[0].Pos=$1.pos} ;
value: NULL { $$ = Value{Kind:NullValue, Pos:$1.pos} }
 | TRUE { $$ = Value{Kind:BooleanValue, Boolean:true, Pos:$1.pos} }
 | FALSE { $$ = Value{Kind:BooleanValue, Pos:$1.pos} }
 | STRING { $$ = Value{Kind:StringValue, Text:$1.text, Pos:$1.pos} }
 | BITSTRING { $$ = Value{Kind:BitStringValue, Text:$1.text, Pos:$1.pos} }
 | NUMBER { var ok bool; $$, ok = integerValue(sqllex,$1,""); if !ok { return 1 } } ;
/* Newly admitted FROM trees diagnose a misplaced ON as syntax. Preserve the
 * legacy single-table suffix capability boundary for otherwise excluded SQL. */
/* Parenthesized set operands preserve arm-local decorations separately from
 * the result-level ORDER/bounds/locking tail. */
select_projection: projection { $$=$1 }
 | DISTINCT projection { $2.Distinct=&DistinctClause{Pos:$1.pos};$$=$2 }
 | DISTINCT ON '(' row_fields ')' projection { $6.Distinct=&DistinctClause{On:$4,Pos:$1.pos};$$=$6 } ;
into_clause: /* empty */ %prec NAME_LIST {$$=nil}
 | INTO relation_name {n:=$2;$$=&n}
 | INTO TABLE relation_name {n:=$3;$$=&n}
 | INTO TEMP relation_name {sqllex.(*lexer).deferUnsupported($2,"temporary tables are unsupported");n:=$3;$$=&n}
 | INTO TEMP TABLE relation_name {sqllex.(*lexer).deferUnsupported($2,"temporary tables are unsupported");n:=$4;$$=&n}
 | INTO UNLOGGED relation_name {sqllex.(*lexer).deferUnsupported($2,"unlogged tables are unsupported");n:=$3;$$=&n}
 | INTO UNLOGGED TABLE relation_name {sqllex.(*lexer).deferUnsupported($2,"unlogged tables are unsupported");n:=$4;$$=&n} ;
select_core: SELECT select_projection into_clause optional_from where group having windows { $2.Into=$3;$2.From=$4;$2.Where=$5;$2.GroupBy=$6;$2.Having=$7;$2.Windows=$8;$2.Pos=$1.pos;$$=$2 }
 | SELECT into_clause optional_from where group having windows %prec NAME_LIST { $$=&Select{Into:$2,From:$3,Where:$4,GroupBy:$5,Having:$6,Windows:$7,Pos:$1.pos} } ;
/* Absorb consecutive parentheses before selecting the scalar, FROM or set-arm
 * category; these category reductions stay below ')' shift precedence. */
select_with_parens: '(' query_select ')' { $$=parenthesizedQuery{Query:$2,Pos:$1.pos} }
 | '(' select_with_parens ')' { $2.Pos=$1.pos;$$=$2 } ;
select_operand: select_core { $$=$1 }
 | VALUES query_rows { $$=&Select{Values:&ValuesQuery{Rows:$2,Pos:$1.pos,rowEnd:sqllex.(*lexer).queryRowEnd},Pos:$1.pos} }
 | select_with_parens %prec NAME_LIST { $1.Query.Parenthesized=true;$$=$1.Query } ;
set_modifier: /* empty */ { $$=false } | ALL { $$=true } | DISTINCT { $$=false } ;
query_set: query_intersection { $$=$1 }
 | query_set UNION set_modifier query_intersection { $$=&Select{Set:&SetQuery{Left:$1,Right:$4,Operator:$2.text,All:$3,Pos:$2.pos},Pos:$1.Pos} }
 | query_set EXCEPT set_modifier query_intersection { $$=&Select{Set:&SetQuery{Left:$1,Right:$4,Operator:$2.text,All:$3,Pos:$2.pos},Pos:$1.Pos} } ;
query_intersection: select_operand { $$=$1 }
 | query_intersection INTERSECT set_modifier select_operand { $$=&Select{Set:&SetQuery{Left:$1,Right:$4,Operator:$2.text,All:$3,Pos:$2.pos},Pos:$1.Pos} } ;
with_clause: WITH cte_list { $$=cteClause{Definitions:$2,Pos:$1.pos} }
 | WITH RECURSIVE cte_list { $$=cteClause{Definitions:$3,Recursive:true,Pos:$1.pos} } ;
cte_list: cte_definition { $$=[]CommonTableExpression{*$1} }
 | cte_list ',' cte_definition { $$=append($1,*$3) } ;
cte_definition: name alias_columns AS cte_materialization '(' cte_body ')' search_clause cycle_clause { $$=&CommonTableExpression{Name:Identifier{Text:$1.text,Pos:$1.pos},Columns:$2,Query:$6,Materialization:$4,Search:$8,Cycle:$9} } ;
search_clause: /* empty */ { $$=nil }
 | SEARCH BREADTH FIRST BY cte_keys SET name { $$=&SearchClause{Breadth:true,Keys:$5,Sequence:Identifier{Text:$7.text,Pos:$7.pos},Pos:$1.pos} }
 | SEARCH DEPTH FIRST BY cte_keys SET name { $$=&SearchClause{Keys:$5,Sequence:Identifier{Text:$7.text,Pos:$7.pos},Pos:$1.pos} } ;
cycle_clause: /* empty */ { $$=nil }
 | CYCLE cte_keys SET name USING name { $$=&CycleClause{Keys:$2,Mark:Identifier{Text:$4.text,Pos:$4.pos},Path:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos} }
 | CYCLE cte_keys SET name TO cycle_constant DEFAULT cycle_constant USING name { $$=&CycleClause{Keys:$2,Mark:Identifier{Text:$4.text,Pos:$4.pos},Value:$6,Default:$8,Path:Identifier{Text:$10.text,Pos:$10.pos},Pos:$1.pos} } ;
cte_keys: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} }
 | cte_keys ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
cycle_constant: value { $$=LiteralExpr{Value:$1} }
 | type STRING { $$=TypedLiteralExpr{TypeName:$1,Text:$2.text,Pos:$2.pos} } ;
cte_body: query_select { $$=$1 } | insert | update | delete | merge | with_mutation ;
with_rule_mutation: with_clause insert { $2.(*Insert).With=$1.Definitions;$2.(*Insert).WithRecursive=$1.Recursive;$$=$2 }
 | with_clause update { $2.(*Update).With=$1.Definitions;$2.(*Update).WithRecursive=$1.Recursive;$$=$2 }
 | with_clause delete { $2.(*Delete).With=$1.Definitions;$2.(*Delete).WithRecursive=$1.Recursive;$$=$2 } ;
with_mutation: with_rule_mutation { $$=$1 }
 | with_clause merge { $2.(*Merge).With=$1.Definitions;$2.(*Merge).WithRecursive=$1.Recursive;$$=$2 } ;
cte_materialization: /* empty */ { $$=CTEDefault }
 | MATERIALIZED { $$=CTEMaterialized }
 | NOT MATERIALIZED { $$=CTENotMaterialized } ;
query_select: query_set order query_tail { if $2!=nil {$1.Order=$2};if $3.Bounds!=nil {$1.Bounds=$3.Bounds};if $3.Locking!=nil {$1.Locking=$3.Locking};if !sqllex.(*lexer).validateTies($1.Bounds,$1.Order,$1.Locking) {return 1};if $1.Values!=nil {$1.Values.Order=$1.Order;$1.Values.Bounds=$1.Bounds};$$=$1 }
 | with_clause query_set order query_tail { if len($2.With)!=0 {sqllex.(*lexer).failAt("42601","multiple WITH clauses not allowed",$1.Pos.Offset);return 1};$2.With=$1.Definitions;$2.WithRecursive=$1.Recursive;if $3!=nil {$2.Order=$3};if $4.Bounds!=nil {$2.Bounds=$4.Bounds};if $4.Locking!=nil {$2.Locking=$4.Locking};if !sqllex.(*lexer).validateTies($2.Bounds,$2.Order,$2.Locking) {return 1};$$=$2 } ;
select_query: query_select { $$=$1 }
 | query_set order suffix { if $3.keyword!=nil && $3.keyword.token==ON {if _,table:=$1.From.(*TableReference);!table {sqllex.(*lexer).syntax($3);return 1}};sqllex.(*lexer).capability($3,unsupportedSuffixes,"SELECT suffix is unsupported"); return 1 }
 | query_set order RETURNING { sqllex.(*lexer).failAt("0A000","SELECT suffix is unsupported",$3.pos.Offset); return 1 } ;
select: select_query { $$=$1 } ;
windows: /* empty */ %prec NAME_LIST { $$=nil } | WINDOW window_definitions { $$=$2 } ;
window_definitions: window_definition { $$=[]WindowDefinition{*$1} } | window_definitions ',' window_definition { $$=append($1,*$3) } ;
window_definition: name AS '(' window_spec ')' { $4.Pos=$3.pos;$$=&WindowDefinition{Name:Identifier{Text:$1.text,Pos:$1.pos},Spec:*$4} } ;
over_clause: OVER name { $$=&windowOverClause{spec:&WindowSpec{Name:&Identifier{Text:$2.text,Pos:$2.pos},Pos:$2.pos,Bare:true},token:$1} }
 | OVER '(' window_spec ')' { $3.Pos=$2.pos;$$=&windowOverClause{spec:$3,token:$1} } ;
window_spec: window_parent window_partition window_order { $$=&WindowSpec{Name:$1,Partition:$2,Order:$3} }
 | window_parent window_partition window_order window_frame {$$=&WindowSpec{Name:$1,Partition:$2,Order:$3,Frame:$4}} ;
window_frame: FRAME_UNIT frame_bound window_exclusion {var ok bool;$$,ok=makeWindowFrame(sqllex.(*lexer),$1,$2,FrameBound{Kind:FrameCurrentRow,Pos:$2.Pos},$3,false);if !ok{return 1}}
 | FRAME_UNIT BETWEEN frame_bound AND frame_bound window_exclusion {var ok bool;$$,ok=makeWindowFrame(sqllex.(*lexer),$1,$3,$5,$6,true);if !ok{return 1}} ;
frame_bound: FRAME_START FRAME_EDGE {kind:=FrameUnboundedPreceding;if $2.text=="following"{kind=FrameUnboundedFollowing};$$=FrameBound{Kind:kind,Pos:$1.pos}}
 | KW_U ROW {if $1.text!="current" {sqllex.(*lexer).syntax($1);return 1};$$=FrameBound{Kind:FrameCurrentRow,Pos:$1.pos}}
 | scalar_projection FRAME_EDGE {kind:=FrameOffsetPreceding;if $2.text=="following"{kind=FrameOffsetFollowing};$$=FrameBound{Kind:kind,Offset:$1,Pos:$<tok>1.pos}} ;
window_exclusion: /* empty */ {$$=ExcludeNoOthers}
 | EXCLUDE KW_U {if $2.text!="ties" {sqllex.(*lexer).syntax($2);return 1};$$=ExcludeTies}
 | EXCLUDE GROUP {$$=ExcludeGroup}
 | EXCLUDE KW_U ROW {if $2.text!="current" {sqllex.(*lexer).syntax($2);return 1};$$=ExcludeCurrentRow}
 | EXCLUDE NO KW_U {if $3.text!="others" {sqllex.(*lexer).syntax($3);return 1};$$=ExcludeNoOthers} ;
window_parent: /* empty */ %prec NAME_LIST { $$=nil } | window_name { $$=&Identifier{Text:$1.text,Pos:$1.pos} } | BETWEEN { $$=&Identifier{Text:$1.text,Pos:$1.pos} } ;
window_partition: /* empty */ %prec NAME_LIST { $$=nil } | PARTITION BY row_fields { $$=$3 } ;
window_order: /* empty */ %prec NAME_LIST { $$=nil } | ORDER BY order_keys { $$=$3 } ;
optional_from: /* empty */ %prec NAME_LIST { $$=nil } | FROM from_list { $$=$2 } ;
row_fields: scalar_projection { $$=[]Expr{$1} } | DEFAULT { $$=[]Expr{DefaultExpr{Pos:$1.pos}} }
 | row_fields ',' scalar_projection { $$=append($1,$3) } | row_fields ',' DEFAULT { $$=append($1,DefaultExpr{Pos:$3.pos}) } ;
quantifier: ANY | SOME | ALL ;
call_args: /* empty */ { $$=nil } | values { $$=$1 } ;
sequence_args: /* empty */ { $$=nil } | sequence_values { $$=$1 } ;
sequence_values: operand { $$=[]Expr{$1} }
 | sequence_values ',' operand { $$=append($1,$3) } ;
array_bracket: '[' array_members ']' { $$=ArrayExpr{Elements:$2,Pos:$1.pos} } ;
array_members: /* empty */ { $$=nil } | array_member_list { $$=$1 } ;
array_member_list: array_member { $$=[]Expr{$1} } | array_member_list ',' array_member { $$=append($1,$3) } ;
array_member: scalar_projection { $$=$1 } | array_bracket { $$=$1 } ;
column_reference: name %prec NAME_LIST { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos} }
 | name '.' col_label %prec NAME_LIST { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos} }
 | name '.' col_label '.' col_label { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}},Pos:$1.pos} } ;
operand: scalar %prec NAME_LIST { if !selectedScalarOperators(sqllex,$1) {return 1};$$=$1 } ;
comparison_operator: '=' | '<' | '>' | LESS_EQUAL | GREATER_EQUAL | NOT_EQUAL ;
qualified_operator: QUALIFIED_OPERATOR '(' name '.' operator_symbol ')' { $$=ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$1.pos},{Text:$5.text,Pos:$5.pos}}} } ;
operator_symbol: '+' | '-' | '*' | '/' | '%' | '^' | SYMBOLIC_OPERATOR | comparison_operator ;
/* Actual CREATE DEFAULT retains its existing direct selected-call boundary.
 * BETWEEN's lower b_expr is not a DEFAULT consumer. Nested ordinary scalar
 * providers and grouped expressions keep their existing consumer policies. */
create_default_target: operand %prec NAME_LIST { if call,ok:=$1.(FunctionExpr);ok && call.directSelectedCall {sqllex.(*lexer).failAt("0A000","functions are unsupported",call.Pos.Offset);return 1};$$=$1 }
 | operand comparison_operator operand { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | create_default_target IS DISTINCT FROM create_default_target %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | create_default_target IS NOT DISTINCT FROM create_default_target %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
default_target: target_base %prec NAME_LIST { $$=$1 }
 | target_base comparison_operator target_base { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | default_target IS DISTINCT FROM default_target %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | default_target IS NOT DISTINCT FROM default_target %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
/* Calls and operator operands reduce through the same scalar primary. */
target_base: operand %prec NAME_LIST { $$=$1 } ;
target_between: target_base %prec IS { $$=$1 }
 | target_between_predicate { $$=$1 }
 | target_base like_operator target_base target_escape %prec BETWEEN { $$=likeExpr($1,$3,$4,$2,false,$2.pos) }
 | target_base NOT like_operator target_base target_escape %prec BETWEEN { $$=likeExpr($1,$4,$5,$3,true,$2.pos) }
 | target_base SIMILAR TO target_base target_escape %prec BETWEEN { $$=similarExpr($1,$4,$5,false,$2.pos) }
 | target_base NOT SIMILAR TO target_base target_escape %prec BETWEEN { $$=similarExpr($1,$5,$6,true,$2.pos) } ;
target_between_predicate: target_base BETWEEN default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$3,$5,$2.pos,false,false) }
 | target_base NOT BETWEEN default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,true,false) }
 | target_base BETWEEN ASYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,false,false) }
 | target_base NOT BETWEEN ASYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$5,$7,$2.pos,true,false) }
 | target_base BETWEEN SYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,false,true) }
 | target_base NOT BETWEEN SYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$5,$7,$2.pos,true,true) } ;
/* Comparison operands may be grouped predicates; the nonrecursive layer keeps
 * ungrouped comparison chains nonassociative. Arithmetic remains scalar-only. */
target_comparison: target_between %prec NAME_LIST { $$=$1 }
 | target_between comparison_operator target_between { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | target_between IN select_with_parens %prec NAME_LIST { $$=quantifiedQuery($1,ObjectName{Parts:[]Identifier{{Text:"=",Pos:$2.pos}}},SubqueryAny,$3.Query,false,$2.pos) }
 | target_between NOT_IN_LA IN select_with_parens %prec NAME_LIST { $$=quantifiedQuery($1,ObjectName{Parts:[]Identifier{{Text:"=",Pos:$2.pos}}},SubqueryAny,$4.Query,true,$2.pos) }
 | target_between IN '(' row_fields ')' %prec NAME_LIST { $$=InListExpr{Left:$1,Items:$4,Pos:$2.pos} }
 | target_between NOT_IN_LA IN '(' row_fields ')' %prec NAME_LIST { $$=InListExpr{Left:$1,Items:$5,Negate:true,Pos:$2.pos} }
 | target_between comparison_operator quantifier select_with_parens %prec NAME_LIST {kind:=SubqueryAny;if $3.text=="all" {kind=SubqueryAll};$$=quantifiedQuery($1,ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},kind,$4.Query,false,$2.pos)}
 | target_between comparison_operator quantifier '(' scalar_projection ')' %prec NAME_LIST { $$=ArrayComparisonExpr{Left:$1,Right:$5,Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},All:$3.text=="all",Pos:$2.pos} } ;
target: target_comparison { $$=$1 }
 | target IS NULL %prec IS { $$=PredicateExpr{Kind:IsNull,Left:$1,Pos:$2.pos} }
 | target IS NOT NULL %prec IS { $$=PredicateExpr{Kind:IsNotNull,Left:$1,Pos:$2.pos} }
 | target IS JSON_PREDICATE json_predicate_kind json_unique %prec IS { $$=JSONPredicateExpr{Child:$1,Kind:$4.text,Unique:$5,Pos:$2.pos} }
 | target IS NOT JSON_PREDICATE json_predicate_kind json_unique %prec IS { $$=JSONPredicateExpr{Child:$1,Kind:$5.text,Unique:$6,Not:true,Pos:$2.pos} }
 | target ISNULL { $$=PredicateExpr{Kind:IsNull,Left:$1,Pos:$2.pos} }
 | target NOTNULL { $$=PredicateExpr{Kind:IsNotNull,Left:$1,Pos:$2.pos} }
 | target IS truth_kind %prec IS { $$=PredicateExpr{Kind:$3,Left:$1,Pos:$2.pos} }
 | target IS DISTINCT FROM target %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | target IS normalized_form NORMALIZED %prec IS { $$=normalizedExpr($1,$3,$2.pos,false) }
 | target IS NOT normalized_form NORMALIZED %prec IS { $$=normalizedExpr($1,$4,$2.pos,true) }
 | target IS NOT DISTINCT FROM target %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
target_not: target { $$=$1 }
 | NOT target_not { $$=LogicalExpr{Op:LogicalNot,Left:$2,Pos:$1.pos} } ;
target_and: target_not { $$=$1 }
 | target_and AND target_not { $$=LogicalExpr{Op:LogicalAnd,Left:$1,Right:$3,Pos:$2.pos} } ;
target_or: target_and { $$=$1 }
 | target_or OR target_and { $$=LogicalExpr{Op:LogicalOr,Left:$1,Right:$3,Pos:$2.pos} } ;
scalar_projection: target_or { $$=$1 } ;
projection_target: scalar_projection %prec NAME_LIST { $$=$1 }
 | '*' { $$=StarExpr{Pos:$1.pos} }
 | DEFAULT { sqllex.(*lexer).failAt("42601","DEFAULT is not allowed in this context",$1.pos.Offset);return 1 } ;
/* Positional/VARIADIC arguments retain the scalar operand grammar. Named
 * right-hand sides below use the ordinary predicate grammar. */
function_argument: operand { if !scalarGrammarOperand($1) {sqllex.(*lexer).syntax(sqllex.(*lexer).current);return 1};$$=$1 } ;
function: SELECTED_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' SELECTED_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos} }
 | name '.' OVERLAPS '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos} }
 | function WITHIN GROUP '(' ORDER BY order_keys ')' { var ok bool;$$,ok=withinCall(sqllex,$1,$2,$7);if !ok{return 1} }
/* Ordinary selected calls must consume the FILTER opener before reaching their
 * unsupported-modifier frontier; incomplete input keeps its natural error. */
 | function FILTER '(' { sqllex.(*lexer).failAt("0A000","aggregate modifiers are unsupported",$2.pos.Offset);return 1 }
 | function over_clause { call:=$1.(FunctionExpr);if call.Window!=nil {sqllex.(*lexer).syntax($2.token);return 1};call.Window=$2.spec;$$=call } ;
function_args: /* empty */ { $$=FunctionExpr{} }
 | function_argument_list { $$=$1 }
 | VARIADIC function_argument { $$=FunctionExpr{Args:[]Expr{$2},Variadic:true} }
 | VARIADIC name EQUALS_GREATER function_argument { $$=appendFunctionArgument(FunctionExpr{},$4,Identifier{Text:$2.text,Pos:$2.pos});$$.Variadic=true }
 | function_argument_list ',' VARIADIC function_argument { $$=appendFunctionArgument($1,$4,Identifier{});$$.Variadic=true }
 | function_argument_list ',' VARIADIC name EQUALS_GREATER function_argument { $$=appendFunctionArgument($1,$6,Identifier{Text:$4.text,Pos:$4.pos});$$.Variadic=true } ;
function_argument_list: function_argument { $$=FunctionExpr{Args:[]Expr{$1}} }
 | name EQUALS_GREATER target_or { $$=appendFunctionArgument(FunctionExpr{},$3,Identifier{Text:$1.text,Pos:$1.pos}) }
 | function_argument_list ',' function_argument { $$=appendFunctionArgument($1,$3,Identifier{}) }
 | function_argument_list ',' name EQUALS_GREATER target_or { $$=appendFunctionArgument($1,$5,Identifier{Text:$3.text,Pos:$3.pos}) } ;
/* Ordinary calls already have separate positional/aggregate productions.
 * A named first argument disambiguates this shared notation-preserving list. */
ordinary_named_arguments: name EQUALS_GREATER target_or { $$=appendFunctionArgument(FunctionExpr{},$3,Identifier{Text:$1.text,Pos:$1.pos}) }
 | VARIADIC name EQUALS_GREATER function_argument {$$=appendFunctionArgument(FunctionExpr{},$4,Identifier{Text:$2.text,Pos:$2.pos});$$.Variadic=true}
 | ordinary_named_arguments ',' function_argument {if $1.Variadic {sqllex.(*lexer).syntax(sqllex.(*lexer).current);return 1};$$=appendFunctionArgument($1,$3,Identifier{})}
 | ordinary_named_arguments ',' name EQUALS_GREATER target_or {if $1.Variadic {sqllex.(*lexer).syntax($3);return 1};$$=appendFunctionArgument($1,$5,Identifier{Text:$3.text,Pos:$3.pos})}
 | ordinary_named_arguments ',' VARIADIC function_argument {$$=appendFunctionArgument($1,$4,Identifier{});$$.Variadic=true}
 | ordinary_named_arguments ',' VARIADIC name EQUALS_GREATER function_argument {$$=appendFunctionArgument($1,$6,Identifier{Text:$4.text,Pos:$4.pos});$$.Variadic=true} ;
/* Preserve the written prefix and the explicit-array marker on the tail. */
ordinary_mixed_arguments: target_or ',' ordinary_named_arguments {call:=FunctionExpr{Args:[]Expr{$1}};for i,arg:=range $3.Args {call=appendFunctionArgument(call,arg,$3.ArgNames[i])};call.Variadic=$3.Variadic;$$=call}
 | target_or ',' count_values ',' ordinary_named_arguments {call:=FunctionExpr{Args:append([]Expr{$1},$3...)};for i,arg:=range $5.Args {call=appendFunctionArgument(call,arg,$5.ArgNames[i])};call.Variadic=$5.Variadic;$$=call}
 | target_or ',' VARIADIC function_argument {$$=appendFunctionArgument(FunctionExpr{Args:[]Expr{$1}},$4,Identifier{});$$.Variadic=true}
 | target_or ',' count_values ',' VARIADIC function_argument {$$=appendFunctionArgument(FunctionExpr{Args:append([]Expr{$1},$3...)},$6,Identifier{});$$.Variadic=true} ;
function_values: function_argument { $$=[]Expr{$1} } | function_values ',' function_argument { $$=append($1,$3) } ;
string_function: STRING_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' STRING_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos} }
 | NORMALIZE '(' scalar ')' { if !selectedScalarChild(sqllex,$3,$4) {return 1};$$=stringBuiltin("normalize",$1.pos,[]Expr{$3}) }
 | NORMALIZE '(' scalar ',' normalization_form ')' { if !selectedScalarChild(sqllex,$3,$4) {return 1};$$=stringBuiltin("normalize",$1.pos,[]Expr{$3,stringForm($5)}) }
 | TRIM '(' trim_direction trim_arguments ')' { $$=stringBuiltin($3.text,$1.pos,$4) } ;
normalization_form: NFC | NFD | NFKC | NFKD ;
normalized_form: /* empty */ { $$=lexeme{text:"NFC"} } | normalization_form ;
trim_direction: /* empty */ { $$=lexeme{text:"btrim"} }
 | BOTH { $$=lexeme{text:"btrim",pos:$1.pos} }
 | LEADING { $$=lexeme{text:"ltrim",pos:$1.pos} }
 | TRAILING { $$=lexeme{text:"rtrim",pos:$1.pos} } ;
trim_list: function_values { $$=$1 } ;
trim_arguments: trim_list { $$=$1 } | FROM trim_list { $$=$2 }
 | scalar FROM trim_list { if !selectedScalarChild(sqllex,$1,$2) {return 1};$$=append($3,$1) } ;
truth_kind: TRUE { $$=IsTrue } | NOT TRUE { $$=IsNotTrue }
 | FALSE { $$=IsFalse } | NOT FALSE { $$=IsNotFalse }
 | UNKNOWN { $$=IsUnknown } | NOT UNKNOWN { $$=IsNotUnknown } ;
/* Keep the scalar COLLATE prefix shared with its expression production. A
 * following name continues the annotation; a clause boundary ends a bare label. */
projection_item: projection_target %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{}}} }
 | scalar COLLATE { if !selectedScalarOperators(sqllex,$1) {return 1};$$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | target_between IN %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | scalar_projection bare_label { if $2.keyword!=nil && $2.text=="filter" {if _,ok:=$1.(GroupingExpr);ok {sqllex.(*lexer).syntax($2)} else {sqllex.(*lexer).failAt("0A000","aggregate modifiers are unsupported",$2.pos.Offset)};return 1};$$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | scalar_projection AS col_label { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$3.text,Pos:$3.pos}}} }
/* A keyword after a complete projection base can be a bare label when no
 * pattern operand follows. Factor here so predicate-starter lookahead retains
 * LIKE/ILIKE matching, without relaxing WHERE or lexical keyword identity. */
 | target_base like_operator %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | target_base SIMILAR %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} } ;
projection: projection_item { $$=$1 }
 | projection ',' projection_item { $1.Targets=append($1.Targets,$3.Targets...);$1.Labels=append($1.Labels,$3.Labels...);$$=$1 } ;
from_list: joined_table { $$=$1 }
 | from_list ',' joined_table { $$=&JoinReference{Left:$1,Right:$3,Comma:true,Pos:$2.pos} } ;
joined_table: from_atom { $$=$1 }
 | joined_table CROSS JOIN from_atom { $$=&JoinReference{Left:$1,Right:$4,Pos:$2.pos} }
 | joined_table inner_join JOIN from_atom on_keyword target_or { $$=&JoinReference{Left:$1,Right:$4,Kind:$2,On:$6,Pos:$3.pos} }
 | joined_table inner_join JOIN from_atom USING '(' using_columns ')' using_alias { $$=&JoinReference{Left:$1,Right:$4,Kind:$2,Using:$7,UsingAlias:$9,Pos:$3.pos} }
 | joined_table NATURAL inner_join JOIN from_atom { $$=&JoinReference{Left:$1,Right:$5,Kind:$3,Natural:true,Pos:$2.pos} } ;
on_keyword: ON {$$=$1} | ON_CONFLICT_LA {$$=$1} ;
inner_join: /* empty */ { $$=JoinInner } | INNER { $$=JoinInner }
 | LEFT optional_outer { $$=JoinLeft }
 | RIGHT optional_outer { $$=JoinRight }
 | FULL optional_outer { $$=JoinFull } ;
optional_outer: /* empty */ { $$=false } | OUTER { $$=true } ;
from_base: relation_name { $$=&TableReference{Name:$1} }
 | relation_name '*' { $$=&TableReference{Name:$1} }
 | ONLY relation_name { $$=&TableReference{Name:$2,Only:true} }
 | ONLY '(' relation_name ')' { $$=&TableReference{Name:$3,Only:true} }
 | json_table {$$=$1}
 | LATERAL json_table {$$=$2;$2.Lateral=true}
 | function_table {
    n:=$1
    selected:=true
    for _,call:=range n.Calls {
       if !RegexpFunctionName(call.Name.Parts[len(call.Name.Parts)-1].Text) {selected=false;break}
    }
    if selected {$$=&FunctionFrom{Calls:n.Calls,Ordinality:n.Ordinality,Lateral:n.Lateral,Pos:n.Pos}} else {$$=n}
   }
 | select_with_parens %prec NAME_LIST { if $1.Query.Values!=nil && len($1.Query.Locking)==0 {$$=$1.Query.Values} else {$$=&DerivedQuery{Query:$1.Query,Pos:$1.Pos}} }
 | '(' from_list ')' {
    if alias,ok:=$2.(*AliasReference);ok {
       if _,values:=alias.Input.(*ValuesQuery);values {sqllex.(*lexer).syntax($<tok>3);return 1}
    }
    $$=$2
   } ;
from_atom: from_base { $$=$1 }
 | from_base AS from_alias alias_columns { $$=&AliasReference{Input:$1,Name:Identifier{Text:$3.text,Pos:$3.pos},Columns:$4} }
 | from_base from_alias alias_columns { $$=&AliasReference{Input:$1,Name:Identifier{Text:$2.text,Pos:$2.pos},Columns:$3} }
 | from_base AS from_alias '(' using_columns type column_collation function_column_definition_tail ')' {
    switch $1.(type) {
    case *FunctionTable, *FunctionFrom:
       if len($5)!=1 {sqllex.(*lexer).syntax(lexeme{text:$6.Name,pos:$6.Pos});return 1}
       $$=&AliasReference{Input:$1,Name:Identifier{Text:$3.text,Pos:$3.pos},ColumnDefinitions:append([]FunctionColumnDefinition{{Name:$5[0],Type:$6,Collation:$7}},$8...)}
    default:
       sqllex.(*lexer).syntax(lexeme{text:$6.Name,pos:$6.Pos});return 1
    }
   }
 | from_base from_alias '(' using_columns type column_collation function_column_definition_tail ')' {
    switch $1.(type) {
    case *FunctionTable, *FunctionFrom:
       if len($4)!=1 {sqllex.(*lexer).syntax(lexeme{text:$5.Name,pos:$5.Pos});return 1}
       $$=&AliasReference{Input:$1,Name:Identifier{Text:$2.text,Pos:$2.pos},ColumnDefinitions:append([]FunctionColumnDefinition{{Name:$4[0],Type:$5,Collation:$6}},$7...)}
    default:
       sqllex.(*lexer).syntax(lexeme{text:$5.Name,pos:$5.Pos});return 1
    }
   }
 | from_base AS '(' name type column_collation function_column_definition_tail ')' {
    switch $1.(type) {
    case *FunctionTable, *FunctionFrom:
       $$=&AliasReference{Input:$1,ColumnDefinitions:append([]FunctionColumnDefinition{{Name:Identifier{Text:$4.text,Pos:$4.pos},Type:$5,Collation:$6}},$7...)}
    default:
       sqllex.(*lexer).syntax(lexeme{text:$5.Name,pos:$5.Pos});return 1
    }
   } ;
function_table_call: check_function_head function_args ')' {call,ok:=selectedScalarCall(sqllex,$1,$2);if !ok{return 1};$$=call}
 | name '.' check_function_head function_args ')' {$$=$4;$$.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}};$$.Pos=$1.pos}
 | STRING_FUNC '(' function_args ')' {$$=$3;$$.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}};$$.Pos=$1.pos}
 | sqljson_object_head sqljson_legacy_values ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$1.pos},{Text:"json_object",Pos:$1.pos}}},Args:$2,Pos:$1.pos} }
 | sqljson_object_head VARIADIC target_or ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$1.pos},{Text:"json_object",Pos:$1.pos}}},Args:[]Expr{$3},Variadic:true,Pos:$1.pos} }
 | sqljson_object_head sqljson_legacy_values ',' VARIADIC target_or ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$1.pos},{Text:"json_object",Pos:$1.pos}}},Args:append($2,$5),Variadic:true,Pos:$1.pos} }
 | SELECTED_FUNC '(' function_args ')' {$$=$3;$$.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}};$$.Pos=$1.pos}
 | name '.' SELECTED_FUNC '(' function_args ')' {$$=$5;$$.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}};$$.Pos=$1.pos}
 | name '.' OVERLAPS '(' function_args ')' {$$=$5;$$.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}};$$.Pos=$1.pos}
 | transaction_time_base {$$=$1}
 | name '(' function_args ')' {$$=$3;$$.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}};$$.Pos=$1.pos}
 | name '.' col_label '(' function_args ')' {$$=$5;$$.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}};$$.Pos=$1.pos} ;
/* Per-call definitions belong only to ROWS FROM members. Keeping AS off the
 * ordinary call lets from_atom own both named aliases and top-level definitions. */
/* Ordinary calls share the scalar source path; typed source admission follows
 * normal callable resolution, including SQL routines. */
rows_function_base: function_table_call {$$=$1} ;
rows_function_call: rows_function_base {$$=$1}
 | rows_function_base AS '(' name type column_collation function_column_definition_tail ')' {$$=$1;$$.ColumnDefinitions=append([]FunctionColumnDefinition{{Name:Identifier{Text:$4.text,Pos:$4.pos},Type:$5,Collation:$6}},$7...)} ;
function_table_calls: rows_function_call {$$=[]FunctionExpr{$1}}
 | function_table_calls ',' rows_function_call {$$=append($1,$3)} ;
function_table_base: function_table_call {$$=&FunctionTable{Calls:[]FunctionExpr{$1},Pos:$1.Pos}}
 | name FROM '(' function_table_calls ')' {if $1.text!="rows"||$1.keyword==nil{sqllex.(*lexer).syntax($1);return 1};$$=&FunctionTable{Calls:$4,RowsFrom:true,Pos:$1.pos}}
 | function_table_call FILTER '(' WHERE {sqllex.(*lexer).syntax($4);return 1}
 | function_table_call OVER '(' ')' {sqllex.(*lexer).syntax($4);return 1} ;
function_table: function_table_base {$$=$1}
 | function_table_base WITH name {if $3.text!="ordinality" {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.Ordinality=true}
 | LATERAL function_table_base {$$=$2;$$.Lateral=true}
 | LATERAL function_table_base WITH name {if $4.text!="ordinality" {sqllex.(*lexer).syntax($3);return 1};$$=$2;$$.Lateral=true;$$.Ordinality=true} ;
from_alias: name {if !$1.keyword.columnName() {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
alias_columns: /* empty */ { $$=nil } | '(' using_columns ')' { $$=$2 } ;
using_columns: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} }
 | using_columns ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
function_column_definition_tail: /* empty */ { $$=nil }
 | function_column_definition_tail ',' name type column_collation { $$=append($1,FunctionColumnDefinition{Name:Identifier{Text:$3.text,Pos:$3.pos},Type:$4,Collation:$5}) } ;
using_alias: /* empty */ { $$=nil } | AS from_alias { $$=&Identifier{Text:$2.text,Pos:$2.pos} } ;
where: /* empty */ { $$ = nil }
 | WHERE target_or { $$=$2 }
 | WHERE name where_predicate_suffix { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 }
 | WHERE name KW_C { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 } ;
/* GROUP/HAVING are admitted clause openers and retained suffix guards. Prefer
 * their real clauses over reducing the optional clause to reach a guard. */
group: /* empty */ %prec NAME_LIST { $$=nil }
 | GROUP BY group_keys { $$=&GroupClause{Items:$3,Pos:$1.pos} }
 | GROUP BY DISTINCT group_keys { $$=&GroupClause{Items:$4,Distinct:true,Pos:$1.pos} }
 | GROUP BY ALL group_keys { $$=&GroupClause{Items:$4,All:true,Pos:$1.pos} } ;
group_keys: group_item { $$=[]GroupItem{$1} } | group_keys ',' group_item { $$=append($1,$3) } ;
group_item: scalar_projection { $$=groupExpressionItem($1) }
 | '(' ')' { $$=GroupItem{Kind:GroupEmpty,Pos:$1.pos} }
 | GROUPING SETS '(' group_keys ')' { $$=GroupItem{Kind:GroupSets,Items:$4,Pos:$1.pos} }
 | ROLLUP '(' producer_units ')' { $$=GroupItem{Kind:GroupRollup,Items:$3,Pos:$1.pos} }
 | CUBE '(' producer_units ')' { $$=GroupItem{Kind:GroupCube,Items:$3,Pos:$1.pos} } ;
/* Unlike GROUPING SETS, producer units are expressions, including implicit row
 * sublists. Nested unreserved function names retain ordinary call ownership. */
producer_units: scalar_projection { $$=[]GroupItem{groupExpressionItem($1)} }
 | producer_units ',' scalar_projection { $$=append($1,groupExpressionItem($3)) } ;
having: /* empty */ %prec NAME_LIST { $$=nil } | HAVING target_or { $$=$2 } ;
order: /* empty */ { $$=nil } | ORDER BY order_keys { $$=&Order{Keys:$3} } ;
aggregate_order: /* empty */ { $$=nil } | ORDER BY order_keys { $$=$3 } ;
order_keys: order_key { $$=[]OrderKey{$1} } | order_keys ',' order_key { $$=append($1,$3) } ;
order_key: scalar_projection direction nulls { $$=OrderKey{Expr:$1,Pos:$1.SourcePosition(),Desc:$2,Nulls:$3} } ;
nulls: /* empty */ { $$=NullsDefault } | nulls_keyword nulls_position { if $2.text=="first" {$$=NullsFirst} else {$$=NullsLast} } ;
nulls_keyword: NULLS_LA ;
nulls_position: FIRST { $$=$1 } | KW_U { if $1.keyword==nil || $1.text!="last" { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
direction: /* empty */ { $$ = false } | ASC { $$ = false } | DESC { $$ = true } ;
locking: /* empty */ { $$=nil } | locking_list { $$=$1 } ;
locking_list: locking_clause { $$=[]RowLockClause{$1} } | locking_list locking_clause { $$=append($1,$2) } ;
locking_clause: lock_strength lock_of lock_wait { $1.Targets=$2;$1.Wait=$3;$$=$1 } ;
lock_strength: FOR UPDATE { $$=RowLockClause{Mode:"FOR UPDATE",Pos:$1.pos} }
 | FOR lock_share { $$=RowLockClause{Mode:"FOR SHARE",Pos:$1.pos} }
 | FOR KW_U lock_share { if $2.text!="key" {sqllex.(*lexer).syntax($2);return 1}; $$=RowLockClause{Mode:"FOR KEY SHARE",Pos:$1.pos} }
 | FOR NO KW_U UPDATE { if $3.text!="key" {sqllex.(*lexer).syntax($3);return 1}; $$=RowLockClause{Mode:"FOR NO KEY UPDATE",Pos:$1.pos} } ;
lock_of: /* empty */ { $$=nil } | OF row_lock_names { $$=$2 } ;
row_lock_names: row_lock_target { $$=[]ObjectName{$1} } | row_lock_names ',' row_lock_target { $$=append($1,$3) } ;
row_lock_target: name { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}} }
 | row_lock_target '.' col_label { $1.Parts=append($1.Parts,Identifier{Text:$3.text,Pos:$3.pos});$$=$1 } ;
lock_wait: /* empty */ { $$=RowLockBlock } | NOWAIT { $$=RowLockNowait } | SKIP LOCKED { $$=RowLockSkip } ;
/* Factor the empty tail separately: FOR must not shift into a path that
 * requires bounds after an otherwise complete standalone locking clause. */
query_tail: /* empty */ { $$=queryTail{} }
 | nonempty_bounds locking { $$=queryTail{Bounds:$1,Locking:$2} }
 | locking_list bounds { $$=queryTail{Bounds:$2,Locking:$1} } ;
bounds: /* empty */ { $$=nil } | nonempty_bounds { $$=$1 } ;
nonempty_bounds: limit_clause { $$=$1 }
 | offset_clause { $$=$1 }
 | limit_clause offset_clause { $1.Offset=$2.Offset;$$=$1 }
 | offset_clause limit_clause { $2.Offset=$1.Offset;$$=$2 } ;
limit_clause: LIMIT limit_value { $$=&QueryBounds{Count:$2} }
 | LIMIT limit_value ',' target_or { sqllex.(*lexer).failDiagnostic("42601","LIMIT #,# syntax is not supported","Use separate LIMIT and OFFSET clauses.",$1.pos.Offset);return 1 }
 | FETCH fetch_direction fetch_count count_rows ONLY { $$=&QueryBounds{Count:$3} }
 | FETCH fetch_direction count_rows ONLY { $$=&QueryBounds{Count:LiteralExpr{Value:Value{Kind:IntegerValue,Integer:1,Pos:$1.pos}}} }
 | FETCH fetch_direction fetch_count count_rows WITH KW_U { if $6.text!="ties" {sqllex.(*lexer).syntax($6);return 1};$$=&QueryBounds{Count:$3,WithTies:true,WithPosition:$5.pos} }
 | FETCH fetch_direction count_rows WITH KW_U { if $5.text!="ties" {sqllex.(*lexer).syntax($5);return 1};$$=&QueryBounds{Count:LiteralExpr{Value:Value{Kind:IntegerValue,Integer:1,Pos:$1.pos}},WithTies:true,WithPosition:$4.pos} } ;
limit_value: scalar_projection { $$=$1 } | ALL { $$=LiteralExpr{Value:Value{Kind:NullValue,Pos:$1.pos}} } ;
offset_clause: OFFSET scalar_projection optional_count_rows { $$=&QueryBounds{Offset:$2} } ;
fetch_direction: FIRST { $$=$1 } | KW_U { if $1.text!="next" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
count_rows: ROW { $$=$1 }
 | FRAME_UNIT { if $1.text!="rows" {sqllex.(*lexer).syntax($1);return 1};$$=$1 }
 | KW_U {sqllex.(*lexer).syntax($1);return 1} ;
optional_count_rows: /* empty */ { $$=false } | count_rows { $$=true } ;
count_values: conditional_value { $$=[]Expr{$1} } | count_values ',' conditional_value { $$=append($1,$3) } ;
/* One call grammar serves scalar and FETCH inputs. FILTER attaches to the
 * actual call, not to a parenthesized or otherwise composed scalar expression. */
aggregate_within: aggregate_call { $$=$1 }
 | aggregate_call WITHIN GROUP '(' ORDER BY order_keys ')' { var ok bool;$$,ok=withinCall(sqllex,$1,$2,$7);if !ok{return 1} } ;
aggregate_application: aggregate_within %prec ')' { $$=$1 }
 | aggregate_within FILTER '(' WHERE target_or ')' { switch call:=$1.(type) {case AggregateExpr:call.Filter=$5;call.FilterPos=$2.pos;$$=call;case FunctionExpr:call.Filter=$5;$$=call;default:sqllex.(*lexer).failAt("0A000","aggregate modifiers are unsupported",$2.pos.Offset);return 1} }
 | aggregate_application over_clause { switch call:=$1.(type) {case AggregateExpr:if call.Window!=nil {sqllex.(*lexer).syntax($2.token);return 1};call.Window=$2.spec;$$=call;case FunctionExpr:if call.Window!=nil {sqllex.(*lexer).syntax($2.token);return 1};call.Window=$2.spec;$$=call;default:sqllex.(*lexer).syntax($2.token);return 1} } ;
aggregate_call: cast_function target_or ')' { $$=ordinaryCall(columnType($1),[]Expr{$2},$1) }
 | qualified_call_head target_or ')' { $$=ordinaryCall($1,[]Expr{$2},lexeme{text:$1.Name,pos:$1.Pos}) }
 | cast_function target_or ',' count_values ')' { $$=ordinaryCall(columnType($1),append([]Expr{$2},$4...),$1) }
 | qualified_call_head target_or ',' count_values ')' { $$=ordinaryCall($1,append([]Expr{$2},$4...),lexeme{text:$1.Name,pos:$1.Pos}) }
 | qualified_call_head '*' ')' { $$=modifiedOrdinaryCall($1,nil,true,false,false,false,nil) }
 | cast_function '*' ')' { $$=modifiedOrdinaryCall(columnType($1),nil,true,false,false,false,nil) }
 | cast_function ')' { $$=ordinaryCall(columnType($1),nil,$1) }
 | qualified_call_head ')' { $$=ordinaryCall($1,nil,lexeme{text:$1.Name,pos:$1.Pos}) }
 | cast_function ordinary_named_arguments ')' { call:=$2;call.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}};call.Pos=$1.pos;call.CompositeCandidate=true;$$=call }
 | qualified_call_head ordinary_named_arguments ')' { call:=$2;call.Name=ordinaryCallName($1);call.Pos=$1.Pos;call.CompositeCandidate=true;$$=call }
 | cast_function ordinary_mixed_arguments ')' {call:=$2;call.Name=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}};call.Pos=$1.pos;call.CompositeCandidate=true;$$=call}
 | qualified_call_head ordinary_mixed_arguments ')' {call:=$2;call.Name=ordinaryCallName($1);call.Pos=$1.Pos;call.CompositeCandidate=true;$$=call}
 | cast_function DISTINCT count_values aggregate_order ')' { $$=modifiedOrdinaryCall(columnType($1),$3,false,true,false,false,$4) }
 | cast_function ALL count_values aggregate_order ')' { $$=modifiedOrdinaryCall(columnType($1),$3,false,false,true,false,$4) }
 | qualified_call_head DISTINCT count_values aggregate_order ')' { $$=modifiedOrdinaryCall($1,$3,false,true,false,false,$4) }
 | qualified_call_head ALL count_values aggregate_order ')' { $$=modifiedOrdinaryCall($1,$3,false,false,true,false,$4) }
 | cast_function target_or ORDER BY order_keys ')' { $$=modifiedOrdinaryCall(columnType($1),[]Expr{$2},false,false,false,false,$5) }
 | qualified_call_head target_or ORDER BY order_keys ')' { $$=modifiedOrdinaryCall($1,[]Expr{$2},false,false,false,false,$5) }
 | cast_function target_or ',' count_values ORDER BY order_keys ')' { $$=modifiedOrdinaryCall(columnType($1),append([]Expr{$2},$4...),false,false,false,false,$7) }
 | qualified_call_head target_or ',' count_values ORDER BY order_keys ')' { $$=modifiedOrdinaryCall($1,append([]Expr{$2},$4...),false,false,false,false,$7) }
 | cast_function VARIADIC target_or ')' { $$=modifiedOrdinaryCall(columnType($1),[]Expr{$3},false,false,false,true,nil) }
 | json_arrayagg_head target_or json_format aggregate_order json_null_policy json_returning ')' { options:=*$6;options.Input=$3;options.Absent=$5.Absent;options.NullPos=$5.NullPos;$$=AggregateExpr{Name:"json_arrayagg",Args:[]Expr{$2},Order:$4,JSON:&options,Pos:$1.pos} }
 | json_objectagg_head json_object_key VALUE target_or json_format json_object_null_policy json_unique_policy json_returning ')' { options:=*$8;options.Input=$5;options.Absent=$6.Absent;options.NullPos=$6.NullPos;options.Unique=$7.Unique;options.UniquePos=$7.UniquePos;$$=AggregateExpr{Name:"json_objectagg",Args:[]Expr{$2,$4},JSON:&options,Pos:$1.pos} }
 | json_objectagg_head target_or ':' target_or json_format json_object_null_policy json_unique_policy json_returning ')' { options:=*$8;options.Input=$5;options.Absent=$6.Absent;options.NullPos=$6.NullPos;options.Unique=$7.Unique;options.UniquePos=$7.UniquePos;$$=AggregateExpr{Name:"json_objectagg",Args:[]Expr{$2,$4},JSON:&options,Pos:$1.pos} }
 | qualified_call_head VARIADIC target_or ')' { $$=modifiedOrdinaryCall($1,[]Expr{$3},false,false,false,true,nil) } ;
/* Declaration names followed by '(' are not SQL/JSON call scopes. */
json_arrayagg_head: JSON_ARRAYAGG '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);$$=$1 } ;
json_objectagg_head: JSON_OBJECTAGG '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);$$=$1 } ;
/* VALUE accepts c_expr, not unparenthesized postfix casts or arithmetic. */
json_object_key: scalar_atom { $$=$1 }
 | field_value { $$=$1 }
 | relation_star { $$=$1 }
 | CAST '(' scalar AS type ')' { if !selectedScalarChild(sqllex,$3,$4) {return 1};if $5.Name=="regclass" && $5.Schema==nil && len($5.Modifiers)==0 && len($5.ArrayBounds)==0 { $$=RegclassExpr{Child:$3,Pos:$1.pos} } else { $$=CastExpr{Child:$3,TypeName:$5,Syntax:StandardCast,Pos:$1.pos} } }
 | EXISTS select_with_parens { $$=SubqueryExpr{Kind:SubqueryExists,Query:$2.Query,Pos:$1.pos} }
 | function { $$=$1 }
 | aggregate_application { $$=$1 }
 | overlaps_row { $$=$1 }
 | select_with_parens { $$=SubqueryExpr{Kind:SubqueryScalar,Query:$1.Query,Pos:$1.Pos} } ;
json_object_null_policy: /* empty */ { $$=&JSONAggregateOptions{} }
 | JSON_ABSENT ON NULL { $$=&JSONAggregateOptions{Absent:true,NullPos:$1.pos} }
 | JSON_NULL ON NULL { $$=&JSONAggregateOptions{NullPos:$1.pos} } ;
json_unique_policy: /* empty */ { $$=&JSONAggregateOptions{} }
 | JSON_WITH UNIQUE { $$=&JSONAggregateOptions{Unique:true,UniquePos:$1.pos} }
 | JSON_WITH UNIQUE JSON_KEYS { $$=&JSONAggregateOptions{Unique:true,UniquePos:$1.pos} }
 | JSON_WITHOUT UNIQUE { $$=&JSONAggregateOptions{UniquePos:$1.pos} }
 | JSON_WITHOUT UNIQUE JSON_KEYS { $$=&JSONAggregateOptions{UniquePos:$1.pos} } ;
json_format: /* empty */ { $$=nil }
 | JSON_FORMAT JSON_FORMAT_TYPE { $$=&JSONFormat{Pos:$1.pos} }
 | JSON_FORMAT JSON_FORMAT_TYPE JSON_ENCODING name { f,ok:=jsonEncoding(sqllex,$1,$4);if !ok{return 1};$$=f } ;
json_null_policy: /* empty */ { $$=&JSONAggregateOptions{Absent:true} }
 | JSON_ABSENT ON NULL { $$=&JSONAggregateOptions{Absent:true,NullPos:$1.pos} }
 | JSON_NULL ON NULL { $$=&JSONAggregateOptions{NullPos:$1.pos} } ;
json_returning: /* empty */ { $$=&JSONAggregateOptions{} }
 | RETURNING json_return_type json_format { typ:=$2;$$=&JSONAggregateOptions{Returning:&typ,Output:$3,ReturningPos:$1.pos} } ;
json_return_type: type { $$=$1 } | SETOF_TYPE type { $$=$2;$$.Setof=true } ;
sqljson_array_head: SQLJSON_ARRAY '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);$$=$1 } ;
sqljson_object_head: SQLJSON_OBJECT '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);l.jsonObjectFirst=true;$$=$1 } ;
sqljson_parse_head: SQLJSON_PARSE '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);$$=$1 } ;
sqljson_scalar_head: SQLJSON_SCALAR '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);$$=$1 } ;
sqljson_serialize_head: SQLJSON_SERIALIZE '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);$$=$1 } ;
sqljson_constructor: sqljson_array_head sqljson_optional_values json_null_policy json_returning ')' {
 n:=*$2;n.Kind=$1.text;n.Pos=$1.pos;n.Absent=$3.Absent;n.NullPos=$3.NullPos;n.Returning=$4.Returning;n.Output=$4.Output;$$=n
 }
 | sqljson_object_head sqljson_members json_object_null_policy json_unique json_returning ')' {
 n:=*$2;n.Kind=$1.text;n.Pos=$1.pos;n.Absent=$3.Absent;n.NullPos=$3.NullPos;n.Unique=$4;n.Returning=$5.Returning;n.Output=$5.Output;$$=n
 }
 | sqljson_object_head json_returning ')' { $$=SQLJSONExpr{Kind:$1.text,Pos:$1.pos,Returning:$2.Returning,Output:$2.Output} }
 | sqljson_object_head sqljson_legacy_values ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$1.pos},{Text:"json_object",Pos:$1.pos}}},Args:$2,Pos:$1.pos} }
 | sqljson_object_head VARIADIC target_or ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$1.pos},{Text:"json_object",Pos:$1.pos}}},Args:[]Expr{$3},Variadic:true,Pos:$1.pos} }
 | sqljson_object_head sqljson_legacy_values ',' VARIADIC target_or ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_catalog",Pos:$1.pos},{Text:"json_object",Pos:$1.pos}}},Args:append($2,$5),Variadic:true,Pos:$1.pos} }
 | sqljson_array_head query_select json_format json_returning ')' { $$=SQLJSONExpr{Kind:$1.text,Query:$2,Formats:[]*JSONFormat{$3},Returning:$4.Returning,Output:$4.Output,Absent:true,Pos:$1.pos} }
 | sqljson_parse_head sqljson_value json_unique ')' { n:=*$2;n.Kind=$1.text;n.Pos=$1.pos;n.Unique=$3;$$=n }
 | sqljson_scalar_head target_or ')' { $$=SQLJSONExpr{Kind:$1.text,Args:[]Expr{$2},Pos:$1.pos} }
 | sqljson_serialize_head sqljson_value json_returning ')' { n:=*$2;n.Kind=$1.text;n.Pos=$1.pos;n.Returning=$3.Returning;n.Output=$3.Output;$$=n } ;
sqljson_legacy_values: target_or { $$=[]Expr{$1} }
 | sqljson_legacy_values ',' target_or { $$=append($1,$3) } ;
sqljson_optional_values: /* empty */ { $$=&SQLJSONExpr{} } | sqljson_values { $$=$1 } ;
sqljson_values: sqljson_value { $$=$1 }
 | sqljson_values ',' sqljson_value { $$=$1;$$.Args=append($$.Args,$3.Args...);$$.Formats=append($$.Formats,$3.Formats...) } ;
sqljson_value: target_or json_format { $$=&SQLJSONExpr{Args:[]Expr{$1},Formats:[]*JSONFormat{$2}} } ;
sqljson_members: sqljson_member { $$=$1 }
 | sqljson_members ',' sqljson_member { $$=$1;$$.Args=append($$.Args,$3.Args...);$$.Formats=append($$.Formats,$3.Formats...) } ;
sqljson_member: target_or ':' target_or json_format { $$=&SQLJSONExpr{Args:[]Expr{$1,$3},Formats:[]*JSONFormat{nil,$4},Members:true} }
 | json_object_key VALUE target_or json_format { $$=&SQLJSONExpr{Args:[]Expr{$1,$3},Formats:[]*JSONFormat{nil,$4},Members:true} } ;
json_unique: /* empty */ { $$=false }
 | WITH UNIQUE json_keys { $$=true }
 | WITHOUT UNIQUE json_keys { $$=false }
 | JSON_WITH UNIQUE json_keys { $$=true }
 | JSON_WITHOUT UNIQUE json_keys { $$=false } ;
json_keys: /* empty */ { $$=lexeme{} }
 | JSON_KEYS { $$=$1 } ;
json_predicate_kind: /* empty */ { $$=lexeme{} }
 | JSON_KIND { $$=$1 } ;
sqljson_query_head: SQLJSON_QUERY_FUNC '(' { l:=sqllex.(*lexer);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);l.jsonFunctionDepths=append(l.jsonFunctionDepths,l.jsonParenDepth);$$=$1 } ;
sqljson_query: sqljson_query_head target_or json_format ',' target_or sqljson_passing json_returning sqljson_wrapper sqljson_quotes sqljson_behaviors ')' {
 l:=sqllex.(*lexer)
 if $1.text=="json_exists" && $7.Returning!=nil {l.syntax(lexeme{raw:"RETURNING",pos:$7.ReturningPos});return 1}
 if $1.text!="json_query" && $8.text!="" {l.syntax($8);return 1}
 if $1.text!="json_query" && $9.text!="" {l.syntax($9);return 1}
 if $1.text=="json_exists" && $10.Empty.Kind!="" {l.syntax(lexeme{raw:"EMPTY",pos:$10.Empty.BranchPos});return 1}
 $$=SQLJSONQueryExpr{Kind:$1.text,Document:$2,Input:$3,Path:$5,Passing:$6,Returning:$7.Returning,Output:$7.Output,Wrapper:$8.text,Quotes:$9.text,Empty:$10.Empty,Error:$10.Error,Pos:$1.pos}
 } ;
sqljson_passing: /* empty */ { $$=nil }
 | JSON_PASSING sqljson_bindings { $$=$2 } ;
sqljson_bindings: target_or AS name { $$=[]SQLJSONPassing{{Value:$1,Name:Identifier{Text:$3.text,Pos:$3.pos}}} }
 | sqljson_bindings ',' target_or AS name { $$=append($1,SQLJSONPassing{Value:$3,Name:Identifier{Text:$5.text,Pos:$5.pos}}) } ;
sqljson_wrapper: /* empty */ { $$=lexeme{} }
 | JSON_WITHOUT JSON_WRAPPER { $$=$1 }
 | JSON_WITHOUT ARRAY JSON_WRAPPER { $$=$1 }
 | JSON_WITH sqljson_wrapper_kind JSON_WRAPPER { $$=$1;$$.text="unconditional";if $2.text=="conditional"{$$.text="conditional"} }
 | JSON_WITH sqljson_wrapper_kind ARRAY JSON_WRAPPER { $$=$1;$$.text="unconditional";if $2.text=="conditional"{$$.text="conditional"} } ;
sqljson_wrapper_kind: /* empty */ { $$=lexeme{} }
 | JSON_CONDITIONAL { $$=$1 }
 | JSON_UNCONDITIONAL { $$=$1 } ;
sqljson_quotes: /* empty */ { $$=lexeme{} }
 | JSON_KEEP JSON_QUOTES sqljson_scalar_string { $$=$1 }
 | JSON_OMIT JSON_QUOTES sqljson_scalar_string { $$=$1 } ;
sqljson_scalar_string: /* empty */ { $$=lexeme{} }
 | ON JSON_SCALAR_STRING JSON_SCALAR_STRING { $$=$2 } ;
sqljson_behaviors: /* empty */ { $$=&SQLJSONQueryExpr{} }
 | sqljson_behavior ON JSON_EMPTY { b:=$1;b.BranchPos=$3.pos;$$=&SQLJSONQueryExpr{Empty:b} }
 | sqljson_behavior ON JSON_ERROR { $$=&SQLJSONQueryExpr{Error:$1} }
 | sqljson_behavior ON JSON_EMPTY sqljson_behavior ON JSON_ERROR { b:=$1;b.BranchPos=$3.pos;$$=&SQLJSONQueryExpr{Empty:b,Error:$4} } ;
/* Keep literals as ordinary tokens: ON lookahead must not reclassify the last
 * literal of a DEFAULT expression as a standalone behavior. */
sqljson_behavior: JSON_BEHAVIOR { $$=SQLJSONBehavior{Kind:$1.text,Pos:$1.pos} }
 | TRUE { $$=SQLJSONBehavior{Kind:$1.text,Pos:$1.pos} }
 | FALSE { $$=SQLJSONBehavior{Kind:$1.text,Pos:$1.pos} }
 | UNKNOWN { $$=SQLJSONBehavior{Kind:$1.text,Pos:$1.pos} }
 | NULL { $$=SQLJSONBehavior{Kind:"null",Pos:$1.pos} }
 | JSON_EMPTY { $$=SQLJSONBehavior{Kind:"empty array",Pos:$1.pos} }
 | JSON_EMPTY ARRAY { $$=SQLJSONBehavior{Kind:"empty array",Pos:$1.pos} }
 | JSON_EMPTY JSON_BEHAVIOR { $$=SQLJSONBehavior{Kind:"empty "+$2.text,Pos:$1.pos} }
 | JSON_ERROR { $$=SQLJSONBehavior{Kind:"error",Pos:$1.pos} }
 | DEFAULT target_or { $$=SQLJSONBehavior{Kind:"default",Default:$2,Pos:$1.pos} } ;
json_table_head: JSON_TABLE_FUNC '(' {l:=sqllex.(*lexer);l.jsonTableDepths=append(l.jsonTableDepths,l.jsonParenDepth);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);l.jsonFunctionDepths=append(l.jsonFunctionDepths,l.jsonParenDepth);$$=$1} ;
json_table: json_table_head target_or json_format ',' target_or json_path_name sqljson_passing json_table_columns sqljson_behaviors ')' {
 if $9.Empty.Kind!="" {sqllex.(*lexer).syntax(lexeme{raw:"EMPTY",pos:$9.Empty.BranchPos});return 1}
 $$=&JSONTable{Document:$2,Input:$3,Path:$5,PathPos:sqllex.(*lexer).jsonTablePathPosition($4.pos),PathName:$6,Passing:$7,Columns:$8,Error:$9.Error,Pos:$1.pos}
} ;
json_path_name: /* empty */ {$$=nil} | AS name {$$=&Identifier{Text:$2.text,Pos:$2.pos}} ;
json_columns_head: JSON_COLUMNS '(' {l:=sqllex.(*lexer);l.jsonTableDepths=append(l.jsonTableDepths,l.jsonParenDepth);l.jsonAggregateDepths=append(l.jsonAggregateDepths,l.jsonParenDepth);l.jsonFunctionDepths=append(l.jsonFunctionDepths,l.jsonParenDepth);$$=$1} ;
json_table_columns: json_columns_head json_table_column_list ')' {$$=$2} ;
json_table_column_list: json_table_column {$$=[]JSONTableColumn{*$1}} | json_table_column_list ',' json_table_column {$$=append($1,*$3)} ;
json_column_path: /* empty */ {$$=nil} | JSON_PATH STRING {$$=LiteralExpr{Value:Value{Kind:StringValue,Text:$2.text,Pos:$2.pos}}} ;
/* Unlike table columns, these declarations are followed by EXISTS. Do not use
 * type's two-word invalid-column guard, which consumes EXISTS as a type name. */
json_table_column: name FOR name {
 if $3.text!="ordinality" {sqllex.(*lexer).syntax($3);return 1}
 $$=&JSONTableColumn{Name:Identifier{Text:$1.text,Pos:$1.pos},Ordinality:true,Pos:$1.pos}
}
 | name declared_type json_format json_column_path sqljson_wrapper sqljson_quotes sqljson_behaviors {
 $$=&JSONTableColumn{Name:Identifier{Text:$1.text,Pos:$1.pos},Type:$2,Format:$3,Path:$4,Wrapper:$5.text,Quotes:$6.text,Empty:$7.Empty,Error:$7.Error,Pos:$1.pos}
}
 | name declared_type EXISTS json_column_path sqljson_behaviors {
 if $5.Empty.Kind!="" {sqllex.(*lexer).syntax(lexeme{raw:"EMPTY",pos:$5.Empty.BranchPos});return 1}
 $$=&JSONTableColumn{Name:Identifier{Text:$1.text,Pos:$1.pos},Type:$2,Exists:true,Path:$4,Error:$5.Error,Pos:$1.pos}
}
 | JSON_NESTED json_column_path json_path_name json_table_columns {
 if $2==nil {sqllex.(*lexer).syntax($1);return 1}
 $$=&JSONTableColumn{Path:$2,PathName:$3,Columns:$4,Pos:$1.pos}
}
 | JSON_NESTED STRING json_path_name json_table_columns {
 $$=&JSONTableColumn{Path:LiteralExpr{Value:Value{Kind:StringValue,Text:$2.text,Pos:$2.pos}},PathName:$3,Columns:$4,Pos:$1.pos}
} ;
fetch_count: scalar_atom { $$=$1 }
 | CAST '(' scalar AS type ')' { if !selectedScalarChild(sqllex,$3,$4) {return 1};if $5.Name=="regclass" && $5.Schema==nil && len($5.Modifiers)==0 && len($5.ArrayBounds)==0 { $$=RegclassExpr{Child:$3,Pos:$1.pos} } else { $$=CastExpr{Child:$3,TypeName:$5,Syntax:StandardCast,Pos:$1.pos} } }
 | aggregate_application { $$=$1 }
 | function { $$=$1 }
 | '+' NUMBER { $$=LiteralExpr{Value:numberValue($2,"")} }
 | '-' NUMBER { $$=LiteralExpr{Value:numberValue($2,"-")} } ;
sequence_name_word: SEQUENCE | INCREMENT | MINVALUE | MAXVALUE | CACHE | CYCLE | OWNED | RESTART | TEMP | UNLOGGED | LOGGED | SEQUENCE_FUNC %prec NAME_LIST
 | GENERATED | ALWAYS | IDENTITY | OVERRIDING | SYSTEM | VALUE
 | SEARCH | BREADTH | DEPTH | FIRST ;
ownership_name_word: REASSIGN ;
conditional_word: COALESCE | NULLIF | GREATEST | LEAST | MERGE_ACTION ;
string_word: STRING_FUNC | NORMALIZE | NORMALIZED | TRIM | NFC | NFD | NFKC | NFKD ;
/* Keep ROLE/LOCAL command disambiguation while preserving ordinary names. */
setting_word_without_between: IDENT | AT | DELETE | SELECTED_FUNC %prec NAME_LIST | KW_U | KW_C | INTEGER | BIGINT | TEXT | BOOLEAN | TIMESTAMP %prec NAME_LIST | WITHOUT | TIME %prec NAME_LIST | ZONE | VALUES | INSERT | UPDATE | BY | SET | SHOW | RESET | SESSION | IF | EXISTS | DROP | LOCK | ACCESS | EXCLUSIVE | MODE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | COMMENT | ADD | INHERITS | transaction_name
 | ENFORCED
 | FDW_SERVER | FDW_MAPPING | FDW_HANDLER | FDW_VALIDATOR | FDW_IMPORT
 | LANGUAGE_TRUSTED | LANGUAGE_PROCEDURAL | LANGUAGE_INLINE
 | TS_CONFIGURATION
 | CONVERSION | STATISTICS
 | RECURSIVE | XML
 | string_word
 | TRUNCATE | CONTINUE
 | COPY
 | MERGE | MATCHED | SOURCE | TARGET | NOTHING | CONFLICT
 | NAMES
 | ownership_name_word
 | EXCLUDE
 | cursor_word
 | collation_name_word
 | OF | NOWAIT | SKIP | LOCKED
 | DOMAIN | TYPE | INCLUDE | TABLESPACE | ROW %prec NAME_LIST
 | routine_name_word
 | grant_external_name_word
 | OUT_ARG | INOUT_ARG | DOUBLE_TYPE | PRECISION_TYPE | CHARACTER_TYPE | BIT_TYPE | VARYING_TYPE
 | INTERVAL %prec NAME_LIST | YEAR | MONTH | DAY | HOUR | MINUTE | SECOND | UNKNOWN | OPERATOR | sequence_name_word | conditional_word
 | ESCAPE | FILTER | WITHIN
 | membership_keyword
 | ROLE_PASSWORD | ROLE_CONNECTION | ROLE_VALID | ROLE_ENCRYPTED | ROLE_UNENCRYPTED
 | CURRENT_DATETIME_FUNC %prec NAME_LIST
 | GROUPING | SETS | ROLLUP | CUBE
 | MATERIALIZED
 | ORDINALITY
 | IDENTITY_FUNC %prec NAME_LIST {if !$1.keyword.columnName() {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
setting_word: setting_word_without_between | BETWEEN ;
/* SQL-special function terminals retain their released ColumnName legality.
 * A call opener shifts on the scalar path; declaration/name contexts reduce. */
non_between_name: window_name | PARTITION | FRAME_UNIT | MATCH | PERIOD ;
window_name: setting_word_without_between | ROLE | LOCAL | VIEW | INDEX | REINDEX | OVER | POLICY | RULE | ALSO | INSTEAD | NOTIFY | LISTEN | UNLISTEN | LOAD
 | TRIGGER | EVENT
 | VACUUM | EXPLAIN
 | FRAME_START %prec NAME_LIST | FRAME_EDGE %prec NAME_LIST
 | EXTRACT %prec NAME_LIST | OVERLAY %prec NAME_LIST | POSITION %prec NAME_LIST | SUBSTRING %prec NAME_LIST
 | CHECK_FUNC %prec NAME_LIST { if $1.keyword!=nil && !$1.keyword.columnName() {sqllex.(*lexer).syntax($1);return 1};$$=$1 }
 | JSON_ARRAYAGG %prec NAME_LIST { $$=$1 }
 | JSON_OBJECTAGG %prec NAME_LIST { $$=$1 }
 | SQLJSON_ARRAY %prec NAME_LIST { $$=$1 }
 | SQLJSON_OBJECT %prec NAME_LIST { $$=$1 }
 | SQLJSON_PARSE %prec NAME_LIST { $$=$1 }
 | SQLJSON_SCALAR %prec NAME_LIST { $$=$1 }
 | SQLJSON_SERIALIZE %prec NAME_LIST { $$=$1 }
 | SQLJSON_QUERY_FUNC %prec NAME_LIST { $$=$1 }
 | JSON_TABLE_FUNC %prec NAME_LIST {$$=$1}
 | TX_TIME_FUNC %prec NAME_LIST { $$=$1 } ;
name: non_between_name | BETWEEN | PL_SLICE ;
transaction_name: BEGIN | START | COMMIT | ROLLBACK | ABORT | WORK | TRANSACTION | NO | CHAIN | ISOLATION | LEVEL | READ | SAVEPOINT | RELEASE | PREPARE | PREPARED | EXECUTE | DEALLOCATE | DISCARD | COMMITTED | UNCOMMITTED | REPEATABLE | SERIALIZABLE ;
type_name: IDENT | DELETE | SELECTED_FUNC | KW_U | KW_T | KW_C | WITHOUT | ZONE | VALUES | INSERT | UPDATE | BY | SET | SHOW | RESET | SESSION | LOCAL | IF | EXISTS | DROP | ROLE | AUTHORIZATION | LOCK | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | CURRENT_SCHEMA | COMMENT | IS | ISNULL | NOTNULL | UNKNOWN | ADD | INHERITS | transaction_name | sequence_name_word | conditional_word | BETWEEN
 | ENFORCED
 | PL_SLICE
 | EXPLAIN
 | FDW_SERVER | FDW_MAPPING | FDW_HANDLER | FDW_VALIDATOR | FDW_IMPORT
 | TRIGGER | EVENT
 | LANGUAGE_TRUSTED | LANGUAGE_PROCEDURAL | LANGUAGE_INLINE
 | TS_CONFIGURATION
 | SQLJSON_PARSE
 | CONVERSION | STATISTICS
 | RECURSIVE | XML
 | VACUUM
 | routine_name_word
 | grant_external_name_word
 | DOUBLE_TYPE | PRECISION_TYPE | CHARACTER_TYPE | BIT_TYPE | VARYING_TYPE
 | string_word
 | TRUNCATE | CONTINUE
 | COPY
 | MATCH | PERIOD
 | ORDINALITY
 | MERGE | MATCHED | SOURCE | TARGET | NOTHING | CONFLICT
 | NAMES
 | cursor_word
 | membership_keyword
 | collation_name_word
 | OF | NOWAIT | SKIP | LOCKED
 | DOMAIN | TYPE | INCLUDE | TABLESPACE | ROW
 | YEAR | MONTH | DAY | HOUR | MINUTE | SECOND | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | OPERATOR | IDENTITY_FUNC | CURRENT_DATETIME_FUNC | INDEX | REINDEX | CONCURRENTLY | ILIKE | ESCAPE
 | FREEZE | VERBOSE
 | SIMILAR
 | COLLATION
 | EXCLUDE
 | POLICY
 | RULE | ALSO | INSTEAD | NOTIFY | LISTEN | UNLISTEN | LOAD
 | EXTRACT | OVERLAY | POSITION | SUBSTRING | CHECK_FUNC | TX_TIME_FUNC | FILTER | WITHIN | OVERLAPS | OVER | PARTITION | FRAME_UNIT | FRAME_EDGE | FRAME_START
 | MATERIALIZED
 | GROUPING | SETS | ROLLUP | CUBE ;
generic_word: generic_word_without_exists | EXISTS ;
generic_word_without_exists: generic_word_base | WITH ;
/* A dedicated declaration head must not reduce to the unsupported CREATE
 * fallback at EOF. STATISTICS remains an ordinary unreserved word elsewhere. */
generic_create_word: generic_word_base_without_vacuum_frame | FRAME_UNIT | VACUUM | EXPLAIN | ANALYZE | WITH | EXISTS ;
/* Bare maintenance commands are complete native statements. Preserve their
 * generic-word uses elsewhere without competing with empty legacy options. */
generic_statement_word: generic_word_base_without_vacuum | WITH | EXISTS ;
generic_word_base: generic_word_base_without_vacuum | VACUUM | EXPLAIN | ANALYZE | ENFORCED ;
/* The RANGE declaration has its own frame-unit token frontier; ENUM keeps
 * generic unsupported-word diagnostics without accepting a range body. */
generic_word_without_frame: generic_word_base_without_vacuum_frame | STATISTICS | VACUUM | EXPLAIN | ANALYZE | WITH | EXISTS ;
generic_word_base_without_vacuum: generic_word_base_without_vacuum_frame | STATISTICS | FRAME_UNIT ;
generic_word_base_without_vacuum_frame: IDENT | AT | INTERVAL | HOUR | MINUTE | SECOND | DAY | MONTH | YEAR | SELECTED_FUNC | IDENTITY_FUNC | CURRENT_DATETIME_FUNC | KW_U | KW_C | KW_T | KW_R | COLLATE | FOR | HAVING | TIMESTAMP | WITHOUT | TIME | ZONE | IF | ALL | LOCAL | ON | AUTHORIZATION | IN | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | CASCADE | RESTRICT | COLUMN | IS | ISNULL | NOTNULL | DISTINCT | UNKNOWN | AND | OR | ONLY | DEFERRABLE | CONSTRAINT | CHECK | conditional_word | BETWEEN | SYMMETRIC | ASYMMETRIC | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | USING | OPERATOR | ILIKE | ESCAPE
 | FDW_SERVER | FDW_MAPPING | FDW_HANDLER | FDW_VALIDATOR | FDW_IMPORT
 | TRIGGER | EVENT
 | LANGUAGE_TRUSTED | LANGUAGE_PROCEDURAL | LANGUAGE_INLINE
 | TS_CONFIGURATION
 | CONVERSION
 | RECURSIVE | XML
 | FREEZE | VERBOSE
 | DOMAIN | TYPE | ARRAY | INCLUDE | TABLESPACE | ROW %prec NAME_LIST | ANY | SOME
 | routine_name_word
 | grant_external_name_word
 | OUT_ARG | INOUT_ARG | DOUBLE_TYPE | PRECISION_TYPE | CHARACTER_TYPE | BIT_TYPE | VARYING_TYPE
 | COPY
 | ORDINALITY | LATERAL
 | EXCLUDE
 | NAMES
 | COLLATION
 | FOREIGN | REFERENCES | MATCH | PERIOD | INITIALLY
 | collation_name_word
 | OF | NOWAIT | SKIP | LOCKED | UNION | INTERSECT | EXCEPT
 | membership_keyword
 | SIMILAR
 | string_word | LEADING | TRAILING | BOTH | UNIQUE | NOT_DEFERRABLE_LA | FILTER | WITHIN | TX_TIME_FUNC | OVERLAPS | OVER | PARTITION | WINDOW | FRAME_EDGE | FRAME_START
 | MATERIALIZED
 | GROUPING | SETS | ROLLUP | CUBE
 | PRIMARY ;
/* Predicate continuation tokens are not excluded bare-alias guards. In
 * particular NOT/BETWEEN must not compete with a real RHS BETWEEN predicate
 * after an ordinary comparison. AS labels retain the full keyword surface. */
/* FILTER's failure-only bare-label guard needs its preceding expression:
 * GROUPING is dedicated syntax, not an unsupported aggregate provider. */
bare_label: non_between_name { if $1.keyword!=nil && $1.text=="filter" {$$=$1} else {if !$1.keyword.label() {sqllex.(*lexer).syntax($1);return 1};$$=$1} }
 | COLLATE { $$=$1 }
 | LATERAL { if !$1.keyword.label() {sqllex.(*lexer).syntax($1);return 1};$$=$1 }
 | COLUMN { $$=$1 }
 | KW_T { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | COLLATION { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | KW_R { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | FREEZE {if !$1.keyword.label(){sqllex.(*lexer).syntax($1);return 1};$$=$1}
 | VERBOSE {if !$1.keyword.label(){sqllex.(*lexer).syntax($1);return 1};$$=$1}
 | ANALYZE {if !$1.keyword.label(){sqllex.(*lexer).syntax($1);return 1};$$=$1}
 | UNIQUE { if !$1.keyword.label() {sqllex.(*lexer).syntax($1);return 1};$$=$1 }
 | PRIMARY {$$=$1}
 | FOREIGN | REFERENCES | INITIALLY
 | BOTH | LEADING | TRAILING | PLACING
 | ANY | SOME | DO
 | IN { $$=$1 }
 | ON { sqllex.(*lexer).syntax($1); return 1 }
 | VARIADIC | SELECT | TABLE | ASC | DESC | NULL | TRUE | FALSE | DEFAULT | CAST | ALL | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | END | ONLY | DEFERRABLE | CONSTRAINT | CHECK | DISTINCT
 | CASE | WHEN | THEN | ELSE | SYMMETRIC | ASYMMETRIC
 | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | USING | CURRENT_CATALOG | SYSTEM_USER | CURRENT_DATE | CURRENT_TIME | CURRENT_TIMESTAMP | LOCALTIME | LOCALTIMESTAMP | CONCURRENTLY ;
col_label: name | ANY | SOME | ARRAY | BOTH | LEADING | TRAILING | PLACING | CONCURRENTLY | IDENTITY_KEYWORD_FUNC | CURRENT_DATETIME_KEYWORD_FUNC | CURRENT_CATALOG | SYSTEM_USER | CURRENT_DATE | CURRENT_TIME | CURRENT_TIMESTAMP | LOCALTIME | LOCALTIMESTAMP | RETURNING | VARIADIC | LIKE | KW_T | KW_R | COLLATE | FOR | LIMIT | OFFSET | FETCH | HAVING | CREATE | TABLE | INTO | SELECT | FROM | WHERE | ORDER | ASC | DESC | NOT | NULL | TRUE | FALSE | TO | DEFAULT | AS | CAST | ALL | ON | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | GROUP | WITH | IN | COLUMN | IS | ISNULL | NOTNULL | DISTINCT | AND | OR | END | ONLY | DEFERRABLE | CONSTRAINT | CHECK | CASE | WHEN | THEN | ELSE | SYMMETRIC | ASYMMETRIC | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | USING | ILIKE | UNIQUE
 | SIMILAR
 | FREEZE | VERBOSE | ANALYZE
 | FOREIGN | REFERENCES | INITIALLY
 | LATERAL
 | UNION | INTERSECT | EXCEPT | OVERLAPS | WINDOW | DO | GRANT | PRIMARY | COLLATION ;
suffix: predicate_suffix | GROUP | HAVING ;
predicate_suffix: where_predicate_suffix | IN ;
where_predicate_suffix: KW_T | KW_R | UNIQUE | PRIMARY | INTO | DEFAULT | ALL | ON | WITH | USER | CONSTRAINT ;
relation_name: name { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos}}} }
 | name '.' col_label %prec NAME_LIST { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos},{Text:$3.text, Pos:$3.pos}}} }
 | name '.' error { sqllex.(*lexer).fail("42601","incomplete relation name"); return 1 }
 | name '.' col_label '.' col_label { sqllex.(*lexer).fail("0A000","three-part relation names are unsupported"); return 1 } ;
membership_role_name: name { $$=RoleSpec{Name:$1.text,Pos:$1.pos} } ;
membership_keyword: ADMIN | INHERIT | OPTION | GRANTED | REVOKE ;
membership_role_names: membership_role_name { $$=[]RoleSpec{$1} }
 | membership_role_names ',' membership_role_name { $$=append($1,$3) } ;
membership_role_spec: membership_role_name {
 if $1.Name=="none" {sqllex.(*lexer).failAt("42939","role name \"none\" is reserved",$1.Pos.Offset);return 1};$$=$1
 }
 | CURRENT_USER { $$=RoleSpec{Identity:"current",Pos:$1.pos} }
 | CURRENT_ROLE { $$=RoleSpec{Identity:"current",Pos:$1.pos} }
 | SESSION_USER { $$=RoleSpec{Identity:"session",Pos:$1.pos} } ;
membership_role_specs: membership_role_spec { $$=[]RoleSpec{$1} }
 | membership_role_specs ',' membership_role_spec { $$=append($1,$3) } ;
membership_options: /* empty */ { $$=nil } | WITH membership_option_list { $$=$2 } ;
membership_option_list: membership_option { $$=[]MembershipOption{$1} }
 | membership_option_list ',' membership_option { $$=append($1,$3) } ;
membership_option: col_label membership_option_value { $$=MembershipOption{Name:$1.text,Value:$2,Pos:$1.pos} } ;
membership_option_value: TRUE { $$=true } | FALSE { $$=false } | OPTION { $$=true } ;
membership_grantor: /* empty */ { $$=nil }
 | GRANTED BY membership_role_spec { spec:=$3;$$=&spec } ;
membership_revoke_option: ADMIN OPTION FOR { $$=$1 }
 | INHERIT OPTION FOR { $$=$1 }
 | SET OPTION FOR { $$=$1 } ;
role_membership: GRANT grant_privileges TO membership_role_specs membership_options membership_grantor {
 roles,ok:=grantMembershipRoles($2);if !ok {sqllex.(*lexer).failAt("42601","syntax error",$2[0].Pos.Offset);return 1};$$=&RoleMembership{Targets:roles,Members:$4,Options:$5,Grantor:$6}
 }
 | REVOKE grant_privileges FROM membership_role_specs membership_grantor drop_behavior {
 roles,ok:=grantMembershipRoles($2);if !ok {sqllex.(*lexer).failAt("42601","syntax error",$2[0].Pos.Offset);return 1};$$=&RoleMembership{Targets:roles,Members:$4,Grantor:$5,Revoke:true,Cascade:$6}
 }
 | REVOKE membership_revoke_option membership_role_names FROM membership_role_specs membership_grantor drop_behavior {
 $$=&RoleMembership{Targets:$3,Members:$5,Grantor:$6,Revoke:true,RevokeOption:$2.text,Cascade:$7}
 }
 | ALTER GROUP membership_role_spec ADD USER membership_role_specs {
 $$=&RoleMembership{Targets:[]RoleSpec{$3},Members:$6,Alias:true}
 }
 | ALTER GROUP membership_role_spec DROP USER membership_role_specs {
 $$=&RoleMembership{Targets:[]RoleSpec{$3},Members:$6,Alias:true,Revoke:true}
 }
 | GRANT grant_privileges ON grant_targets TO grant_grantees grant_option membership_grantor {
 g:=$4;g.Privileges=$2;g.Grantees=$6;g.GrantOption=$7;g.Grantor=$8;$$=g
 }
 | REVOKE grant_privileges ON grant_targets FROM grant_grantees membership_grantor drop_behavior {
 g:=$4;g.Privileges=$2;g.Grantees=$6;g.Grantor=$7;g.Revoke=true;g.Cascade=$8;$$=g
 }
 | REVOKE GRANT OPTION FOR grant_privileges ON grant_targets FROM grant_grantees membership_grantor drop_behavior {
 g:=$7;g.Privileges=$5;g.Grantees=$9;g.Grantor=$10;g.Revoke=true;g.OptionOnly=true;g.Cascade=$11;$$=g
 } ;
alter_default_privileges: ALTER DEFAULT name default_privileges_options default_privileges_action {
 if $3.text!="privileges" || $3.keyword==nil {sqllex.(*lexer).syntax($3);return 1}
 s:=$5;s.Options=$4;$$=s
 } ;
default_privileges_options: /* empty */ {$$=nil}
 | default_privileges_options default_privileges_option {$$=append($1,$2)} ;
default_privileges_option: FOR ROLE membership_role_specs {$$=DefaultPrivilegesOption{Pos:$1.pos,Roles:$3}}
 | FOR USER membership_role_specs {$$=DefaultPrivilegesOption{Pos:$1.pos,Roles:$3}}
 | IN SCHEMA grant_schema_list {$$=DefaultPrivilegesOption{Pos:$1.pos,Schemas:$3}} ;
default_privileges_class: name {
 if $1.keyword==nil {sqllex.(*lexer).syntax($1);return 1}
 switch $1.text {case "tables","sequences","functions","routines","types","schemas":case "large":sqllex.(*lexer).Error("");return 1;default:sqllex.(*lexer).syntax($1);return 1};$$=$1
 }
 | name name {
 if $1.keyword==nil {sqllex.(*lexer).syntax($1);return 1}
 if $1.text!="large" {
  switch $1.text {case "tables","sequences","functions","routines","types","schemas":sqllex.(*lexer).syntax($2);default:sqllex.(*lexer).syntax($1)};return 1
 }
 if $2.text!="objects" || $2.keyword==nil {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.text="large objects"
 } ;
default_privileges_action: GRANT grant_privileges ON default_privileges_class TO grant_grantees grant_option {
 $$=&AlterDefaultPrivileges{Class:$4.text,Privileges:$2,Grantees:$6,GrantOption:$7}
 }
 | REVOKE grant_privileges ON default_privileges_class FROM grant_grantees drop_behavior {
 $$=&AlterDefaultPrivileges{Class:$4.text,Privileges:$2,Grantees:$6,Revoke:true,Cascade:$7}
 }
 | REVOKE GRANT OPTION FOR grant_privileges ON default_privileges_class FROM grant_grantees drop_behavior {
 $$=&AlterDefaultPrivileges{Class:$7.text,Privileges:$5,Grantees:$9,Revoke:true,OptionOnly:true,Cascade:$10}
 } ;
grant_privileges: grant_privilege { $$=[]GrantPrivilege{$1} }
 | grant_privileges ',' grant_privilege { $$=append($1,$3) } ;
grant_privilege: name grant_columns { $$=GrantPrivilege{Name:$1.text,Pos:$1.pos,Columns:$2,RoleName:true} }
 | grant_privilege_word grant_columns { $$=GrantPrivilege{Name:$1.text,Pos:$1.pos,Columns:$2} }
 | ALTER SYSTEM grant_columns {$$=GrantPrivilege{Name:"alter system",Pos:$1.pos,Columns:$3}}
 | ALL grant_columns { $$=GrantPrivilege{All:true,Pos:$1.pos,Columns:$2} }
 | ALL name grant_columns { if $2.text!="privileges" || $2.keyword==nil {sqllex.(*lexer).syntax($2);return 1};$$=GrantPrivilege{All:true,Pos:$1.pos,Columns:$3} } ;
grant_privilege_word: SELECT | CREATE | KW_R | KW_T | REFERENCES ;
grant_columns: /* empty */ { $$=nil }
 | '(' grant_column_list ')' { $$=$2 } ;
grant_column_list: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} }
 | grant_column_list ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
grant_relations: relation_name { $$=[]ObjectName{$1} }
 | grant_relations ',' relation_name { $$=append($1,$3) } ;
grant_simple_names: name {$$=[]ObjectName{{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}}}}
 | grant_simple_names ',' name {$$=append($1,ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}})} ;
grant_parameter_names: setting_name {$$=[]ObjectName{{Parts:[]Identifier{$1.identifier()}}}}
 | grant_parameter_names ',' setting_name {$$=append($1,ObjectName{Parts:[]Identifier{$3.identifier()}})} ;
grant_object_number: NUMBER { $$=$1 }
 | '+' NUMBER { $$=$2;$$.text="+"+$2.text;$$.pos=$1.pos }
 | '-' NUMBER { $$=$2;$$.text="-"+$2.text;$$.pos=$1.pos } ;
grant_large_objects: grant_object_number { $$=[]ObjectName{{Parts:[]Identifier{{$1.text,$1.pos}}}} }
 | grant_large_objects ',' grant_object_number { $$=append($1,ObjectName{Parts:[]Identifier{{$3.text,$3.pos}}}) } ;
grant_targets: grant_relations { $$=&ObjectGrant{Relations:$1} }
 | TABLE grant_relations { $$=&ObjectGrant{Relations:$2} }
 | SEQUENCE grant_relations { $$=&ObjectGrant{Kind:"sequence",Relations:$2} }
 | DATABASE grant_simple_names { $$=&ObjectGrant{Kind:"database",Relations:$2} }
 | FOREIGN FDW_SERVER grant_relations { $$=&ObjectGrant{Kind:"foreign server",Relations:$3} }
 | FOREIGN data_word wrapper_word grant_relations { $$=&ObjectGrant{Kind:"foreign-data wrapper",Relations:$4} }
 | grant_routine_kind grant_routines { $$=&ObjectGrant{Kind:$1.text,Routines:$2} }
 | SCHEMA grant_relations { $$=&ObjectGrant{Kind:"schema",Relations:$2} }
 | TYPE grant_relations { $$=&ObjectGrant{Kind:"type",Relations:$2} }
 | DOMAIN grant_relations { $$=&ObjectGrant{Kind:"domain",Relations:$2} }
 | TABLESPACE grant_simple_names {$$=&ObjectGrant{Kind:"tablespace",Relations:$2}}
 | PARAMETER grant_parameter_names {$$=&ObjectGrant{Kind:"parameter",Relations:$2}}
 | LANGUAGE grant_simple_names { $$=&ObjectGrant{Kind:"language",Relations:$2} }
 | LARGE OBJECT grant_large_objects { $$=&ObjectGrant{Kind:"large object",Relations:$3} }
 | ALL name IN SCHEMA grant_schema_list { if $2.keyword==nil {sqllex.(*lexer).syntax($2);return 1};kind:="";switch $2.text {case "tables":case "sequences":kind="sequence";case "functions":kind="function";case "procedures":kind="procedure";case "routines":kind="routine";default:sqllex.(*lexer).syntax($2);return 1};$$=&ObjectGrant{Kind:kind,Schemas:$5} } ;
grant_routine_kind: FUNCTION | PROCEDURE | ROUTINE ;
routine_name_word: CALL | FUNCTION | PROCEDURE | ROUTINE | AGGREGATE | PARAMETER | RETURN_BODY | ATOMIC_BODY | RETURNS_CLAUSE
 | ROUTINE_CALLED | ROUTINE_COST | ROUTINE_SUPPORT | ROUTINE_PARALLEL | ROUTINE_SECURITY | ROUTINE_EXTERNAL ;
grant_external_name_word: LANGUAGE | LARGE | OBJECT | DATA | WRAPPER ;
grant_routines: grant_routine { $$=[]GrantRoutineTarget{$1} }
 | grant_routines ',' grant_routine { $$=append($1,$3) } ;
grant_routine: relation_name { $$=GrantRoutineTarget{Name:$1} }
 | relation_name '(' grant_routine_arguments ')' { $$=GrantRoutineTarget{Name:$1,Arguments:$3,SignatureSpecified:true} } ;
aggregate_utility_targets: aggregate_utility_target {$$=[]GrantRoutineTarget{$1}}
 | aggregate_utility_targets ',' aggregate_utility_target {$$=append($1,$3)} ;
aggregate_utility_target: relation_name '(' aggregate_utility_arguments ')' {$$=GrantRoutineTarget{Name:$1,Arguments:$3,SignatureSpecified:true}} ;
aggregate_utility_arguments: '*' {$$=nil}
 | grant_routine_argument_list {var ok bool;$$,ok=sqllex.(*lexer).aggregateUtilityArguments($1,nil);if !ok {return 1}}
 | ORDER BY grant_routine_argument_list {var ok bool;$$,ok=sqllex.(*lexer).aggregateUtilityArguments(nil,$3);if !ok {return 1}}
 | grant_routine_argument_list ORDER BY grant_routine_argument_list {var ok bool;$$,ok=sqllex.(*lexer).aggregateUtilityArguments($1,$4);if !ok {return 1}} ;
grant_routine_arguments: /* empty */ { $$=nil }
 | grant_routine_argument_list { $$=$1 } ;
grant_routine_argument_list: grant_routine_argument { $$=[]GrantRoutineArgument{*$1} }
 | grant_routine_argument_list ',' grant_routine_argument { $$=append($1,*$3) } ;
grant_routine_mode: IN | OUT_ARG | INOUT_ARG | VARIADIC | IN OUT_ARG { $$=lexeme{text:"inout",pos:$1.pos} } ;
grant_parameter_name: type_name {if $1.keyword!=nil && !$1.keyword.typeFunctionName() {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
routine_argument_type: declared_type {$$=$1}
 | SETOF_TYPE declared_type {$$=$2;$$.Setof=true} ;
grant_routine_argument: routine_argument_type { $$=&GrantRoutineArgument{Type:$1,Pos:$1.Pos} }
 | grant_parameter_name routine_argument_type { $$=&GrantRoutineArgument{Name:Identifier{Text:$1.text,Pos:$1.pos},Type:$2,Pos:$1.pos} }
 | grant_routine_mode routine_argument_type { $$=&GrantRoutineArgument{Mode:$1.text,Type:$2,Pos:$1.pos} }
 | grant_routine_mode grant_parameter_name routine_argument_type { $$=&GrantRoutineArgument{Mode:$1.text,Name:Identifier{Text:$2.text,Pos:$2.pos},Type:$3,Pos:$1.pos} }
 | grant_parameter_name grant_routine_mode routine_argument_type { $$=&GrantRoutineArgument{Mode:$2.text,Name:Identifier{Text:$1.text,Pos:$1.pos},Type:$3,Pos:$1.pos} } ;
grant_schema_list: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} }
 | grant_schema_list ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
grant_grantee: grant_role_spec { $$=$1 } | GROUP grant_role_spec { $$=$2 } ;
/* PUBLIC is a decoded RoleSpec spelling, not a PostgreSQL keyword. */
grant_role_spec: name { $$=GrantGrantee{Role:RoleSpec{Name:$1.text,Pos:$1.pos},Public:$1.text=="public"} }
 | CURRENT_USER { $$=GrantGrantee{Role:RoleSpec{Identity:"current",Pos:$1.pos}} }
 | CURRENT_ROLE { $$=GrantGrantee{Role:RoleSpec{Identity:"current",Pos:$1.pos}} }
 | SESSION_USER { $$=GrantGrantee{Role:RoleSpec{Identity:"session",Pos:$1.pos}} } ;
grant_grantees: grant_grantee { $$=[]GrantGrantee{$1} }
 | grant_grantees ',' grant_grantee { $$=append($1,$3) } ;
grant_option: /* empty */ { $$=false } | WITH GRANT OPTION { $$=true } ;
role_kind: ROLE { $$=false } | USER { $$=true } | GROUP { $$=false } ;
optional_with: /* empty */ | WITH ;
role_options: /* empty */ { $$=&RoleOptions{} }
 | role_options role_option_word {
   next,ok:=$1.toggle($2); $$=&next
   if !ok {
    l:=sqllex.(*lexer)
    if unsupportedRoleOptions[$2.text] { l.failAt("0A000","unsupported role option",$2.pos.Offset) } else if $2.keyword!=nil && !$2.keyword.columnName() { l.syntax($2) } else { l.failAt("42601","unrecognized role option \""+$2.text+"\"",$2.pos.Offset) }; return 1
   }
 }
 | role_options IN ROLE membership_role_names { $$=cell($1.members("in",$2,$4) )}
 | role_options IN GROUP membership_role_names { $$=cell($1.members("in",$2,$4) )}
 | role_options ROLE membership_role_names { $$=cell($1.members("role",$2,$3) )}
 | role_options USER membership_role_names { $$=cell($1.members("role",$2,$3) )}
 | role_options ADMIN membership_role_names { $$=cell($1.members("admin",$2,$3) )}
 | role_options ROLE_PASSWORD STRING { $$=cell($1.add(RolePassword,$2.pos)); $$.Password=$3.text; $$.PasswordNull=false }
 | role_options ROLE_PASSWORD NULL { $$=cell($1.add(RolePassword,$2.pos)); $$.Password=""; $$.PasswordNull=true }
 | role_options ROLE_ENCRYPTED ROLE_PASSWORD STRING { $$=cell($1.add(RolePassword,$2.pos)); $$.Password=$4.text; $$.PasswordNull=false }
 | role_options ROLE_UNENCRYPTED ROLE_PASSWORD STRING { sqllex.(*lexer).failDiagnostic("0A000","UNENCRYPTED PASSWORD is no longer supported","Remove UNENCRYPTED to store the password in encrypted form instead.",$2.pos.Offset); return 1 }
 | role_options ROLE_CONNECTION LIMIT role_limit { $$=cell($1.add(RoleConnectionLimit,$2.pos)); $$.ConnectionLimit=int32($4) }
 | role_options ROLE_VALID role_option_word STRING { if $3.text!="until" {sqllex.(*lexer).syntax($3);return 1}; $$=cell($1.add(RoleValidUntil,$2.pos)); $$.ValidUntil=$4.text } ;
role_limit: NUMBER { var ok bool; $$,ok=roleLimit(sqllex,$1,""); if !ok{return 1} }
 | '-' NUMBER { var ok bool; $$,ok=roleLimit(sqllex,$2,"-");if !ok{return 1} }
 | '+' NUMBER { var ok bool; $$,ok=roleLimit(sqllex,$2,"");if !ok{return 1} } ;
/* WITH is a reserved introducer, never a role option after optional_with. */
/* Attribute words are contextual identifiers. Do not include the membership
 * introducers ADMIN/IN/ROLE/USER in this alternative: their list productions
 * must consume those tokens rather than competing with an option-word loop. */
role_option_word: IDENT | KW_U | XML | INHERIT | GROUP | GRANTED | BY ;
role_name: name {
 if $1.text=="public" || $1.text=="none" { sqllex.(*lexer).failAt("42939","role name \""+$1.text+"\" is reserved",$1.pos.Offset); return 1 }; $$=$1
 }
 | CURRENT_USER { sqllex.(*lexer).failAt("42939","CURRENT_USER cannot be used as a role name here",$1.pos.Offset); return 1 }
 | SESSION_USER { sqllex.(*lexer).failAt("42939","SESSION_USER cannot be used as a role name here",$1.pos.Offset); return 1 }
 | CURRENT_ROLE { sqllex.(*lexer).failAt("42939","CURRENT_ROLE cannot be used as a role name here",$1.pos.Offset); return 1 } ;
create_role: CREATE role_kind role_name optional_with role_options {
 $$=&CreateRole{Name:Identifier{Text:$3.text,Pos:$3.pos},DefaultLogin:$2,Options:*$5}
 } ;
alter_role_target: name | CURRENT_ROLE | CURRENT_USER | SESSION_USER ;
alter_role: ALTER role_alias alter_role_target optional_with role_options {
 if !$5.alter(sqllex) {return 1}
 identity:=""; if $3.keyword!=nil && ($3.text=="current_role" || $3.text=="current_user" || $3.text=="session_user") {identity=$3.text}
 $$=&AlterRole{Name:Identifier{Text:$3.text,Pos:$3.pos},Identity:identity,Options:*$5}
 }
 | ALTER role_alias alter_role_target RENAME TO role_name {$$=&RenameRole{Role:roleSettingSpec($3),Name:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER role_alias alter_role_target role_setting_database role_setting_action {
 s:=$5.(*AlterRoleSettings);s.Role=roleSettingSpec($3);s.Database=$4;s.Pos=$1.pos;$$=s
 }
 | ALTER role_alias ALL role_setting_database role_setting_action {
 s:=$5.(*AlterRoleSettings);s.All=true;s.Database=$4;s.Pos=$1.pos;$$=s
 } ;
role_alias: ROLE | USER ;
role_setting_database: /* empty */ {$$=nil}
 | IN DATABASE name {$$=&Identifier{Text:$3.text,Pos:$3.pos}} ;
current_setting_word: KW_U {if $1.keyword==nil || $1.text!="current" {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
role_setting_action: SET setting_name setting_assignment setting_values {$$=&AlterRoleSettings{Set:parsedSet($2.identifier(),$4,$1.pos)}}
 | SET setting_name setting_assignment DEFAULT {$$=&AlterRoleSettings{Set:&Set{Name:$2.identifier(),Default:true,Pos:$1.pos}}}
 | SET setting_name FROM current_setting_word {$$=&AlterRoleSettings{Set:&Set{Name:$2.identifier(),Pos:$1.pos},FromCurrent:true}}
 | SET TIME ZONE zone_setting {$4.Name=Identifier{Text:"timezone",Pos:$2.pos};$4.Pos=$1.pos;$$=&AlterRoleSettings{Set:$4}}
 | RESET setting_name {$$=&AlterRoleSettings{Reset:&Reset{Name:$2.identifier(),Pos:$1.pos}}}
 | RESET TIME ZONE {$$=&AlterRoleSettings{Reset:&Reset{Name:Identifier{Text:"timezone",Pos:$2.pos},Pos:$1.pos}}}
 | RESET ALL {$$=&AlterRoleSettings{Reset:&Reset{All:true,Pos:$1.pos}}} ;
/* IF remains a name unless followed by EXISTS; that lookahead cannot continue
 * an ordinary comma-list. Defer exclusion so malformed suffixes stay 42601. */
drop_role: DROP role_kind names { $$=&DropRole{Names:$3} }
 | DROP role_kind IF EXISTS names { sqllex.(*lexer).deferUnsupported($3,"DROP ROLE IF EXISTS is unsupported"); $$=nil } ;
session_authorization: SET SESSION AUTHORIZATION name { $$=&SessionAuthorization{Name:$4.text,OwnerDirective:true} }
 | SET SESSION AUTHORIZATION STRING { $$=&SessionAuthorization{Name:$4.text,OwnerDirective:true} }
 | SET SESSION AUTHORIZATION DEFAULT { $$=&SessionAuthorization{Default:true} }
 | SET AUTHORIZATION name { $$=&SessionAuthorization{Name:$3.text} }
 | SET AUTHORIZATION STRING { $$=&SessionAuthorization{Name:$3.text} }
 | SET AUTHORIZATION DEFAULT { $$=&SessionAuthorization{Default:true} }
 | RESET SESSION AUTHORIZATION { $$=&SessionAuthorization{Default:true,ResetCommand:true} } ;
/* All role selection input parses before execution, including LOCAL. */
role_selection: SET ROLE role_selection_assignment role_selection_target { if $4.keyword!=nil && $4.keyword.token==DEFAULT && $3.text=="" {sqllex.(*lexer).syntax($4);return 1};$$=&RoleSelection{Name:$4.text,Reset:$4.keyword!=nil && $4.keyword.token==DEFAULT,Pos:$2.pos} }
 | SET SESSION ROLE role_selection_assignment role_selection_target { if $5.keyword!=nil && $5.keyword.token==DEFAULT && $4.text=="" {sqllex.(*lexer).syntax($5);return 1};$$=&RoleSelection{Name:$5.text,Reset:$5.keyword!=nil && $5.keyword.token==DEFAULT,Pos:$3.pos} }
 | SET LOCAL ROLE role_selection_assignment role_selection_target { if $5.keyword!=nil && $5.keyword.token==DEFAULT && $4.text=="" {sqllex.(*lexer).syntax($5);return 1};$$=&RoleSelection{Name:$5.text,Reset:$5.keyword!=nil && $5.keyword.token==DEFAULT,Local:true,Pos:$3.pos} }
 | RESET ROLE { $$=&RoleSelection{Reset:true,ResetCommand:true,Pos:$2.pos} } ;
role_selection_assignment: /* empty */ { $$=lexeme{} } | TO { $$=$1 } | '=' { $$=$1 } ;
role_selection_target: name | STRING | DEFAULT ;
schema_owner: name | CURRENT_USER | CURRENT_ROLE | SESSION_USER ;
schema_table: create_table { $$=$1.(*CreateTable) }
 | CREATE generic_create_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
schema_tables: /* empty */ { $$=nil } | schema_tables schema_table { $$=append($1,$2) }
 | schema_tables KW_R { sqllex.(*lexer).capability($2,unsupportedCommands,"schema element is unsupported"); return 1 }
 | schema_tables UNIQUE { sqllex.(*lexer).capability($2,unsupportedCommands,"schema element is unsupported"); return 1 } ;
create_schema: CREATE SCHEMA name schema_tables { $$=&CreateSchema{Name:$3.text,Tables:$4} }
 | CREATE SCHEMA name AUTHORIZATION schema_owner schema_tables { $$=&CreateSchema{Name:$3.text,Authorization:$5.text,AuthorizationIdentity:$5.keyword!=nil&&$5.keyword.category==Reserved,Tables:$6} }
 | CREATE SCHEMA AUTHORIZATION schema_owner schema_tables { $$=&CreateSchema{Authorization:$4.text,AuthorizationIdentity:$4.keyword!=nil&&$4.keyword.category==Reserved,Tables:$5} }
 | CREATE SCHEMA IF NOT EXISTS name { $$=&CreateSchema{Name:$6.text,IfNotExists:true} }
 | CREATE SCHEMA IF NOT EXISTS name AUTHORIZATION schema_owner { $$=&CreateSchema{Name:$6.text,Authorization:$8.text,AuthorizationIdentity:$8.keyword!=nil&&$8.keyword.category==Reserved,IfNotExists:true} }
 | CREATE SCHEMA IF NOT EXISTS AUTHORIZATION schema_owner { $$=&CreateSchema{Authorization:$7.text,AuthorizationIdentity:$7.keyword!=nil&&$7.keyword.category==Reserved,IfNotExists:true} } ;
alter_schema: ALTER SCHEMA name RENAME TO name { $$=&AlterSchema{Name:$3.text,NewName:$6.text} }
 | ALTER SCHEMA name OWNER TO schema_owner { $$=&AlterSchema{Name:$3.text,Owner:$6.text,OwnerIdentity:$6.keyword!=nil&&$6.keyword.category==Reserved} } ;
drop_behavior: /* empty */ { $$=false } | RESTRICT { $$=false } | CASCADE { $$=true } ;
drop_schema: DROP SCHEMA names drop_behavior { $$=&DropSchema{Names:$3,Cascade:$4} }
 | DROP SCHEMA IF EXISTS names drop_behavior { $$=&DropSchema{Names:$5,IfExists:true,Cascade:$6} } ;
create_extension: CREATE EXTENSION name extension_options { $$=&CreateExtension{Name:Identifier{Text:$3.text,Pos:$3.pos},Options:$4,Pos:$1.pos} }
 | CREATE EXTENSION IF NOT EXISTS name extension_options { $$=&CreateExtension{Name:Identifier{Text:$6.text,Pos:$6.pos},IfNotExists:true,Options:$7,Pos:$1.pos} } ;
drop_extension: DROP EXTENSION view_names drop_mode { $$=&DropExtension{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP EXTENSION IF EXISTS view_names drop_mode { $$=&DropExtension{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
extension_options: extension_option_list { $$=$1 } | WITH extension_option_list { $$=$2 } ;
extension_option_list: /* empty */ { $$=nil } | extension_option_list extension_option { $$=append($1,$2) } ;
extension_option: SCHEMA name { $$=ExtensionOption{Name:"schema",Value:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | VERSION extension_version { $$=ExtensionOption{Name:"version",Value:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} } ;
extension_version: name | STRING ;
/* Keep excluded actions explicit: the UPDATE prefix now bypasses the generic
 * ALTER guard, but SET SCHEMA and member ADD/DROP remain command boundaries. */
alter_extension_update: ALTER EXTENSION name UPDATE extension_update_options { $$=&AlterExtensionUpdate{Name:Identifier{Text:$3.text,Pos:$3.pos},Options:$5,Pos:$1.pos} }
 | ALTER EXTENSION name SET {sqllex.(*lexer).failAt("0A000","statement is unsupported",$1.pos.Offset);return 1}
 | ALTER EXTENSION name ADD {sqllex.(*lexer).failAt("0A000","statement is unsupported",$1.pos.Offset);return 1}
 | ALTER EXTENSION name DROP {sqllex.(*lexer).failAt("0A000","statement is unsupported",$1.pos.Offset);return 1} ;
extension_update_options: /* empty */ { $$=nil } | extension_update_options extension_update_option { $$=append($1,$2) } ;
extension_update_option: TO extension_version { $$=ExtensionOption{Name:"version",Value:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} } ;
alter_owner: ALTER TYPE relation_name OWNER TO membership_role_spec { $$=&AlterOwner{Kind:"type",Name:$3,Target:$6,Pos:$1.pos} }
 | ALTER DOMAIN relation_name OWNER TO membership_role_spec { $$=&AlterOwner{Kind:"domain",Name:$3,Target:$6,Pos:$1.pos} }
 | statistics_alter_head relation_name OWNER TO membership_role_spec {if !sqllex.(*lexer).options.StatisticsOwnerReplay {sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported");return 1};$$=&AlterOwner{Kind:"statistics",Name:$2,Target:$5,Pos:$1.pos}}
 | ALTER DATABASE name OWNER TO membership_role_spec { $$=&AlterOwner{Kind:"database",Name:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}},Target:$6,Pos:$1.pos} }
 | ALTER LARGE OBJECT grant_object_number OWNER TO membership_role_spec { $$=&AlterOwner{Kind:"large object",Number:ownershipNumber($4),Target:$7,Pos:$1.pos} } ;
reassign_owned: REASSIGN OWNED BY membership_role_specs TO membership_role_spec { $$=&ReassignOwned{Sources:$4,Target:$6,Pos:$1.pos} } ;
drop_owned: DROP OWNED BY membership_role_specs drop_mode { $$=&DropOwned{Sources:$4,Mode:$5,Pos:$1.pos} } ;

/* SQL/MED utilities share the positioned relation, type, role and action ASTs. */
wrapper_word: WRAPPER {$$=$1} ;
foreign_names: name {$$=[]ObjectName{{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}}}}
 | foreign_names ',' name {$$=append($1,ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}})} ;
foreign_options: /* empty */ {$$=nil}
 | options_word '(' foreign_option_list ')' {$$=$3} ;
foreign_optional_alter_options: /* empty */ {$$=nil} | foreign_alter_options {$$=$1} ;
foreign_role: name {$$=GrantGrantee{Role:RoleSpec{Name:$1.text,Pos:$1.pos},Public:$1.text=="public"}}
 | CURRENT_USER {$$=GrantGrantee{Role:RoleSpec{Identity:"current",Pos:$1.pos}}}
 | CURRENT_ROLE {$$=GrantGrantee{Role:RoleSpec{Identity:"current",Pos:$1.pos}}}
 | USER {$$=GrantGrantee{Role:RoleSpec{Identity:"current",Pos:$1.pos}}} ;
foreign_alter_role: foreign_role {$$=$1} | SESSION_USER {$$=GrantGrantee{Role:RoleSpec{Identity:"session",Pos:$1.pos}}} ;
foreign_option_list: foreign_option {$$=[]ForeignOption{$1}}
 | foreign_option_list ',' foreign_option {$$=append($1,$3)} ;
foreign_option: col_label STRING {$$=ForeignOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$2.text}} ;
foreign_alter_options: options_word '(' foreign_alter_option_list ')' {$$=$3} ;
foreign_alter_option_list: foreign_alter_option {$$=[]ForeignOption{$1}}
 | foreign_alter_option_list ',' foreign_alter_option {$$=append($1,$3)} ;
foreign_alter_option: foreign_option {$$=$1}
 | ADD foreign_option {$$=$2;$$.Action="add"}
 | SET foreign_option {$$=$2;$$.Action="set"}
 | DROP col_label {$$=ForeignOption{Name:Identifier{Text:$2.text,Pos:$2.pos},Action:"drop"}} ;
foreign_wrapper_head: CREATE FOREIGN data_word wrapper_word name {
 $$=&ForeignDDL{Action:"create",Kind:"wrapper",Name:ObjectName{Parts:[]Identifier{{Text:$5.text,Pos:$5.pos}}},Pos:$1.pos}
} ;
foreign_wrapper_definition: foreign_wrapper_head {$$=$1}
 | foreign_wrapper_definition FDW_HANDLER relation_name {if $1.HandlerSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.HandlerSet=true;n:=$3;$$.Handler=&n}
 | foreign_wrapper_definition NO FDW_HANDLER {if $1.HandlerSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.HandlerSet=true}
 | foreign_wrapper_definition FDW_VALIDATOR relation_name {if $1.ValidatorSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.ValidatorSet=true;n:=$3;$$.Validator=&n}
 | foreign_wrapper_definition NO FDW_VALIDATOR {if $1.ValidatorSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.ValidatorSet=true} ;
foreign_server_head: CREATE FDW_SERVER name {$$=&ForeignDDL{Action:"create",Kind:"server",Name:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos}}
 | CREATE FDW_SERVER IF NOT EXISTS name {$$=&ForeignDDL{Action:"create",Kind:"server",Name:ObjectName{Parts:[]Identifier{{Text:$6.text,Pos:$6.pos}}},IfNotExists:true,Pos:$1.pos}} ;
foreign_server_definition: foreign_server_head {$$=$1}
 | foreign_server_head TYPE STRING {$$=$1;s:=$3.text;$$.ServerType=&s}
 | foreign_server_head VERSION STRING {$$=$1;s:=$3.text;$$.Version=&s;$$.VersionSet=true}
 | foreign_server_head TYPE STRING VERSION STRING {$$=$1;s,v:=$3.text,$5.text;$$.ServerType=&s;$$.Version=&v;$$.VersionSet=true} ;
foreign_mapping_head: CREATE USER FDW_MAPPING FOR foreign_role FDW_SERVER name {
 $$=&ForeignDDL{Action:"create",Kind:"mapping",Role:$5,Server:Identifier{Text:$7.text,Pos:$7.pos},Pos:$1.pos}
} | CREATE USER FDW_MAPPING IF NOT EXISTS FOR foreign_role FDW_SERVER name {
 $$=&ForeignDDL{Action:"create",Kind:"mapping",Role:$8,Server:Identifier{Text:$10.text,Pos:$10.pos},IfNotExists:true,Pos:$1.pos}
} ;
foreign_table_head: CREATE FOREIGN TABLE relation_name '(' foreign_optional_elements ')' table_parents FDW_SERVER name {
 $$=&ForeignDDL{Action:"create",Kind:"table",Name:$4,Table:&CreateTable{Name:$4,Elements:$6.elements,Parents:$8,Pos:$1.pos,Persistence:"logged"},ColumnOptions:$6.options,Server:Identifier{Text:$10.text,Pos:$10.pos},Pos:$1.pos}
} | CREATE FOREIGN TABLE IF NOT EXISTS relation_name '(' foreign_optional_elements ')' table_parents FDW_SERVER name {
 $$=&ForeignDDL{Action:"create",Kind:"table",Name:$7,Table:&CreateTable{Name:$7,Elements:$9.elements,Parents:$11,Pos:$1.pos,Persistence:"logged",IfNotExists:true},ColumnOptions:$9.options,Server:Identifier{Text:$13.text,Pos:$13.pos},IfNotExists:true,Pos:$1.pos}
} ;
foreign_optional_elements: /* empty */ {$$=foreignElements{}} | foreign_elements {$$=$1} ;
foreign_elements: foreign_element {$$=$1} | foreign_elements ',' foreign_element {$$=appendForeignElements($1,$3)} ;
foreign_element: name type foreign_options column_collation nullability {$$=foreignColumn(&Column{Name:$1.text,TypeName:$2,Collation:$4,Constraints:$5,Pos:$1.pos},$3)}
 | LIKE relation_name like_options {$$=foreignElements{elements:[]TableElement{{Like:&TableLike{Name:$2,Options:$3},Pos:$1.pos}}}}
 | check_constraint {$$=foreignElements{elements:[]TableElement{{Check:$1,Pos:$1.Pos}}}}
 | create_not_null {$$=foreignElements{elements:[]TableElement{{NotNull:$1,Pos:$1.Pos}}}}
 | unique_constraint {$$=foreignElements{elements:[]TableElement{{Unique:$1,Pos:$1.Pos}}}}
 | primary_constraint {$$=foreignElements{elements:[]TableElement{{Unique:$1,Pos:$1.Pos}}}}
 | foreign_key {$$=foreignElements{elements:[]TableElement{{ForeignKey:$1,Pos:$1.Pos}}}} ;
foreign_alter_wrapper_head: ALTER FOREIGN data_word wrapper_word name {
 $$=&ForeignDDL{Action:"alter",Kind:"wrapper",Name:ObjectName{Parts:[]Identifier{{Text:$5.text,Pos:$5.pos}}},Pos:$1.pos}
} ;
foreign_alter_wrapper_prefix: foreign_alter_wrapper_head {$$=$1} | foreign_alter_wrapper {$$=$1} ;
foreign_alter_wrapper: foreign_alter_wrapper_prefix FDW_HANDLER relation_name {if $1.HandlerSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.HandlerSet=true;n:=$3;$$.Handler=&n}
 | foreign_alter_wrapper_prefix NO FDW_HANDLER {if $1.HandlerSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.HandlerSet=true}
 | foreign_alter_wrapper_prefix FDW_VALIDATOR relation_name {if $1.ValidatorSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.ValidatorSet=true;n:=$3;$$.Validator=&n}
 | foreign_alter_wrapper_prefix NO FDW_VALIDATOR {if $1.ValidatorSet {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.ValidatorSet=true} ;
foreign_version: STRING {v:=$1.text;$$=&v} | NULL {$$=nil} ;
foreign_table_actions: foreign_table_action {$$=[]ForeignTableAction{*$1}}
 | foreign_table_actions ',' foreign_table_action {$$=append($1,*$3)} ;
foreign_table_action: alter_selected_action {$$=&ForeignTableAction{Ordinary:$1}}
 | alter_column_add {$$=$1}
 | foreign_alter_options {$$=&ForeignTableAction{Options:$1}}
 | ALTER optional_column name foreign_alter_options {$$=&ForeignTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Options:$4}}
 | ALTER optional_column name SET DEFAULT target_or {$$=&ForeignTableAction{Default:&AlterTableDefault{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:SetDefault,Expression:$6,Pos:$1.pos}}}
 | ALTER optional_column name DROP DEFAULT {$$=&ForeignTableAction{Default:&AlterTableDefault{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropDefault,Pos:$1.pos}}} ;
foreign_import_head: FDW_IMPORT FOREIGN SCHEMA name {
 $$=&ForeignDDL{Action:"import",Kind:"table",RemoteSchema:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos}
} | foreign_import_head LIMIT TO '(' grant_column_list ')' {if $1.ImportFilter!=nil {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.ImportFilter=$5}
 | foreign_import_head EXCEPT '(' grant_column_list ')' {if $1.ImportFilter!=nil {sqllex.(*lexer).syntax($2);return 1};$$=$1;$$.ImportFilter=$4;$$.ImportExcept=true} ;
foreign_statement: foreign_wrapper_definition foreign_options {$$=$1;$1.Options=$2}
 | foreign_server_definition FOREIGN data_word wrapper_word name foreign_options {$$=$1;$1.Server=Identifier{Text:$5.text,Pos:$5.pos};$1.Options=$6}
 | foreign_mapping_head foreign_options {$$=$1;$1.Options=$2}
 | foreign_table_head foreign_options {$$=$1;$1.Options=$2}
 | foreign_alter_wrapper {$$=$1}
 | foreign_alter_wrapper foreign_alter_options {$$=$1;$1.Options=$2}
 | foreign_alter_wrapper_head foreign_alter_options {$$=$1;$1.Options=$2}
 | foreign_alter_wrapper_head RENAME TO name {$$=$1;$1.NewName=Identifier{Text:$4.text,Pos:$4.pos}}
 | foreign_alter_wrapper_head OWNER TO membership_role_spec {$$=$1;o:=$4;$1.Owner=&o}
 | ALTER FDW_SERVER name foreign_alter_options {$$=&ForeignDDL{Action:"alter",Kind:"server",Name:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}},Options:$4,Pos:$1.pos}}
 | ALTER FDW_SERVER name VERSION foreign_version foreign_optional_alter_options {$$=&ForeignDDL{Action:"alter",Kind:"server",Name:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}},VersionSet:true,Version:$5,Options:$6,Pos:$1.pos}}
 | ALTER FDW_SERVER name RENAME TO name {$$=&ForeignDDL{Action:"alter",Kind:"server",Name:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}},NewName:Identifier{Text:$6.text,Pos:$6.pos},Pos:$1.pos}}
 | ALTER FDW_SERVER name OWNER TO membership_role_spec {o:=$6;$$=&ForeignDDL{Action:"alter",Kind:"server",Name:ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$3.pos}}},Owner:&o,Pos:$1.pos}}
 | ALTER USER FDW_MAPPING FOR foreign_alter_role FDW_SERVER name foreign_alter_options {$$=&ForeignDDL{Action:"alter",Kind:"mapping",Role:$5,Server:Identifier{Text:$7.text,Pos:$7.pos},Options:$8,Pos:$1.pos}}
 | DROP FOREIGN data_word wrapper_word foreign_names drop_mode {$$=&ForeignDDL{Action:"drop",Kind:"wrapper",Names:$5,Mode:$6,Pos:$1.pos}}
 | DROP FOREIGN data_word wrapper_word IF EXISTS foreign_names drop_mode {$$=&ForeignDDL{Action:"drop",Kind:"wrapper",Names:$7,IfExists:true,Mode:$8,Pos:$1.pos}}
 | DROP FDW_SERVER foreign_names drop_mode {$$=&ForeignDDL{Action:"drop",Kind:"server",Names:$3,Mode:$4,Pos:$1.pos}}
 | DROP FDW_SERVER IF EXISTS foreign_names drop_mode {$$=&ForeignDDL{Action:"drop",Kind:"server",Names:$5,IfExists:true,Mode:$6,Pos:$1.pos}}
 | DROP USER FDW_MAPPING FOR foreign_role FDW_SERVER name {$$=&ForeignDDL{Action:"drop",Kind:"mapping",Role:$5,Server:Identifier{Text:$7.text,Pos:$7.pos},Pos:$1.pos}}
 | DROP USER FDW_MAPPING IF EXISTS FOR foreign_role FDW_SERVER name {$$=&ForeignDDL{Action:"drop",Kind:"mapping",Role:$7,Server:Identifier{Text:$9.text,Pos:$9.pos},IfExists:true,Pos:$1.pos}}
 | DROP FOREIGN TABLE relation_names drop_mode {$$=&ForeignDDL{Action:"drop",Kind:"table",Names:$4,Mode:$5,Pos:$1.pos}}
 | DROP FOREIGN TABLE IF EXISTS relation_names drop_mode {$$=&ForeignDDL{Action:"drop",Kind:"table",Names:$6,Mode:$7,IfExists:true,Pos:$1.pos}}
 | ALTER FOREIGN TABLE rename_target foreign_table_actions {$$=&ForeignDDL{Action:"alter",Kind:"table",Name:$4.Name,IfExists:$4.IfExists,TableActions:$5,Pos:$1.pos}}
 | ALTER FOREIGN TABLE rename_target RENAME TO name {$$=&ForeignDDL{Action:"alter",Kind:"table",Name:$4.Name,IfExists:$4.IfExists,Inner:&AlterTableRenameTable{Name:$4.Name,NewName:Identifier{Text:$7.text,Pos:$7.pos},IfExists:$4.IfExists,Pos:$1.pos},Pos:$1.pos}}
 | ALTER FOREIGN TABLE rename_target RENAME optional_column name TO name {$$=&ForeignDDL{Action:"alter",Kind:"table",Name:$4.Name,IfExists:$4.IfExists,Inner:&AlterTableRenameColumn{Name:$4.Name,Column:Identifier{Text:$7.text,Pos:$7.pos},NewName:Identifier{Text:$9.text,Pos:$9.pos},IfExists:$4.IfExists,Pos:$1.pos},Pos:$1.pos}}
 | ALTER FOREIGN TABLE rename_target SET SCHEMA name {$$=&ForeignDDL{Action:"alter",Kind:"table",Name:$4.Name,IfExists:$4.IfExists,Inner:&AlterTableSetSchema{Name:$4.Name,Schema:Identifier{Text:$7.text,Pos:$7.pos},IfExists:$4.IfExists,Pos:$1.pos},Pos:$1.pos}}
 | ALTER FOREIGN TABLE rename_target OWNER TO membership_role_spec {o:=$7;$$=&ForeignDDL{Action:"alter",Kind:"table",Name:$4.Name,IfExists:$4.IfExists,Owner:&o,Pos:$1.pos}}
 | foreign_import_head FROM FDW_SERVER name INTO name foreign_options {$$=$1;$1.Server=Identifier{Text:$4.text,Pos:$4.pos};$1.LocalSchema=Identifier{Text:$6.text,Pos:$6.pos};$1.Options=$7}
 ;
%%
