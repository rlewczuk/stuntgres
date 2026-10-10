%{
package parser
%}
%union {
 tok lexeme
 stmt Statement
 col Column
 elements []TableElement
 element TableElement
 likeOptions []LikeOption
 likeOption LikeOption
 objectNames []ObjectName
 dropMode DropMode
 typ TypeName
 constraints []ColumnConstraint
 constraint ColumnConstraint
 notNull *NotNullConstraint
 check *CheckConstraint
 unique *UniqueConstraint
 alterTarget alterTarget
 lockTargets []LockTarget
 lockMode TableLockMode
 alterActions []AlterTableAction
 alterAction AlterTableAction
 dropTarget dropTarget
 targets []InsertTarget
 assignments []UpdateAssignment
 assignment UpdateAssignment
 names []string
 rows [][]Expr
 row []Expr
 value Value
 expr Expr
 call FunctionExpr
 predicate PredicateKind
 selectStmt *Select
 parenthesizedQuery parenthesizedQuery
 insertSource *InsertSource
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
 roleOptions roleOptionState
 settingName settingName
 tables []*CreateTable
 table *CreateTable
 modes []TransactionMode
 mode TransactionMode
 isolation TransactionIsolation
 sequenceOptions []SequenceOption
 sequenceOption SequenceOption
 sequence *CreateSequence
 alterSequence *AlterSequence
 integer int64
 joinKind JoinKind
 persistence string
 identity *IdentityDefinition
 generation *GenerationDefinition
 identityMode IdentityMode
 override InsertOverride
 caseArms []CaseArm
 caseArm CaseArm
 fromItem FromItem
 identifiers []Identifier
 identifier *Identifier
 viewOptions []ViewOption
 viewOption ViewOption
 zoneSet *Set
 returning *Returning
 returningAliases []ReturningAlias
 returningAlias ReturningAlias
 indexKeys []IndexKey
 indexKey IndexKey
 createIndex *CreateIndex
 alterView *AlterView
 enumLabels []EnumLabel
 alterIndex *AlterIndex
 indexOptions []IndexOption
 indexOption IndexOption
 indexValue Value
}
%token <tok> IDENT NUMBER STRING BITSTRING PARAM
/* Parser-private numeric continuation for DATE literals, never a keyword. */
%token <tok> DATE_MODIFIER_NUMBER
%token <tok> DOT_DOT
%token <tok> LESS_EQUAL GREATER_EQUAL NOT_EQUAL EQUALS_GREATER
%token <tok> SYMBOLIC_OPERATOR
%token <tok> OPERATOR
%token <tok> '+' '-' '*' '/' '%' '^' '[' ']' ':' '(' ')' '=' '<' '>' ',' '.'
%token <tok> CREATE TABLE INSERT UPDATE INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE OR
%token <tok> DOMAIN TYPE
%token <tok> ARRAY
%token <tok> HAVING
%token <tok> FILTER
%token <tok> UNIQUE
%token <tok> NOT_DEFERRABLE_LA NOT_IN_LA
%token <tok> FOR
%token <tok> LIMIT OFFSET FETCH
%token <tok> OF NOWAIT SKIP LOCKED UNION INTERSECT EXCEPT
%token <tok> CROSS INNER JOIN NATURAL USING VIEW LEFT RIGHT FULL OUTER
%token <tok> DELETE
%token <tok> RETURNING
%token <tok> BETWEEN SYMMETRIC
%token <tok> INTEGER BIGINT TEXT BOOLEAN KW_U KW_C KW_T KW_R AS CAST NULLS_LA
%token <tok> TIMESTAMP WITHOUT TIME ZONE
%token <tok> AT
%token <tok> INTERVAL HOUR MINUTE SECOND DAY MONTH YEAR
%token <tok> SET SHOW RESET ALL SESSION TO DEFAULT LOCAL SETTING_FUNC ON
%token <tok> SELECTED_FUNC VARIADIC
%token <tok> STRING_FUNC NORMALIZE NORMALIZED TRIM NFC NFD NFKC NFKD LEADING TRAILING BOTH
%token <tok> CASE WHEN THEN ELSE COALESCE NULLIF GREATEST LEAST
%token <tok> IF EXISTS
%token <tok> ANY SOME ROW
%token <tok> ROLE USER GROUP DROP WITH AUTHORIZATION SESSION_USER CURRENT_USER CURRENT_ROLE
%token <tok> CURRENT_CATALOG SYSTEM_USER IDENTITY_FUNC IDENTITY_KEYWORD_FUNC
%token <tok> CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP LOCALTIME LOCALTIMESTAMP
%token <tok> CURRENT_DATETIME_FUNC CURRENT_DATETIME_KEYWORD_FUNC
%token <tok> LOCK ACCESS EXCLUSIVE MODE IN
%token <tok> LIKE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CASCADE RESTRICT CURRENT_SCHEMA
%token <tok> ILIKE ESCAPE
%token <tok> COMMENT COLUMN IS
%token <tok> COLLATE
%token <tok> ISNULL NOTNULL DISTINCT UNKNOWN
%token <tok> CONSTRAINT ADD INHERITS
%token <tok> CHECK
%token <tok> CHECK_FUNC
%token <tok> INDEX_SUBQUERY_OPEN
%token <tok> GENERATION_OVER GENERATION_PARTITION
%token <tok> OVERLAY POSITION SUBSTRING PLACING
%token <tok> BEGIN START COMMIT END ROLLBACK ABORT WORK TRANSACTION AND NO CHAIN
%token <tok> ISOLATION LEVEL READ ONLY DEFERRABLE SAVEPOINT RELEASE PREPARE PREPARED
%token <tok> COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE
%token <tok> SEQUENCE INCREMENT MINVALUE MAXVALUE CACHE CYCLE OWNED RESTART TEMP UNLOGGED LOGGED SEQUENCE_FUNC
%token <tok> GENERATED ALWAYS IDENTITY OVERRIDING SYSTEM VALUE
%token <tok> INDEX REINDEX
%token <tok> CONCURRENTLY
%token <tok> INCLUDE TABLESPACE
/* Contextual command continuations remain below arithmetic precedence. */
%nonassoc NAME_LIST
%nonassoc INTO KW_R UNIQUE CONSTRAINT COLUMN ALL DEFAULT ON USER GROUP HAVING IN AND SAVEPOINT
/* Predicate continuations reduce a completed cast type before consuming IS,
 * ISNULL or NOTNULL. Keep their shared nonassociativity below comparisons. */
%nonassoc IS ISNULL NOTNULL
%nonassoc BETWEEN NOT NOT_IN_LA
/* ESCAPE shifts after a complete pattern, but cannot become a cast type tail. */
%nonassoc ESCAPE
%left SYMBOLIC_OPERATOR OPERATOR
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
%nonassoc TYPE_NAME
/* Retain contextual type-tail shifts instead of relying on yacc defaults. */
%nonassoc INDEX CONCURRENTLY
/* Postfix casts put projection labels in the follow set of type names.
 * Preserve declaration grammar's deliberate type-tail shift rather than
 * leaving those decisions as implicit shift/reduce conflicts. Interval field
 * keywords also shift qualifier tails after INTERVAL and INTERVAL STRING. */
/* REINDEX previously used KW_U: keep that same type-tail shift when promoting
 * it to a utility token. INDEX/CONCURRENTLY retain their existing decisions. */
%nonassoc IDENT DOMAIN TYPE INCLUDE TABLESPACE SELECTED_FUNC UNKNOWN INSERT UPDATE DELETE VALUES BY KW_U KW_C ZONE SET SHOW RESET SESSION LOCAL SETTING_FUNC IF EXISTS ROLE DROP AUTHORIZATION LOCK ACCESS EXCLUSIVE MODE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CASCADE RESTRICT CURRENT_SCHEMA COMMENT ADD INHERITS BEGIN COMMIT ROLLBACK ABORT WORK TRANSACTION CHAIN ISOLATION LEVEL READ RELEASE PREPARE PREPARED COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE SEQUENCE TEMP UNLOGGED LOGGED SEQUENCE_FUNC ALWAYS IDENTITY OVERRIDING SYSTEM VALUE YEAR MONTH DAY HOUR MINUTE SECOND REINDEX '.'
/* Preserve the existing generic type-tail shift for contextual call terminals. */
%nonassoc CHECK_FUNC OVERLAY POSITION SUBSTRING ROW
%nonassoc GENERATION_OVER
%nonassoc COALESCE NULLIF GREATEST LEAST CASE WHEN THEN ELSE
%nonassoc IDENTITY_FUNC IDENTITY_KEYWORD_FUNC CURRENT_DATETIME_FUNC CURRENT_DATETIME_KEYWORD_FUNC
%nonassoc STRING_FUNC NORMALIZE NORMALIZED TRIM NFC NFD NFKC NFKD
%nonassoc '('
%nonassoc KW_T CROSS INNER JOIN NATURAL LEFT RIGHT FULL OUTER LIKE ILIKE WITH WITHOUT
%nonassoc '='
%left COLLATE
%left ':'
%nonassoc '['
/* A parenthesized scalar stays on the scalar path; Boolean grouping uses
 * target_or only when a logical/comparison continuation requires it. */
%nonassoc ')'
%nonassoc FILTER
%type <stmt> statement create create_table drop_table alter_drop_column alter_default_single alter_rename_single alter_schema_single insert select set show reset create_role drop_role session_authorization excluded_role lock_table create_schema alter_schema drop_schema transaction_control
%type <stmt> select_query
%type <selectStmt> query_select query_set query_intersection select_core select_operand select_projection
%type <parenthesizedQuery> select_with_parens
%type <desc> set_modifier
%type <stmt> create_domain drop_domain create_enum
%type <enumLabels> enum_labels enum_label_list
%type <tok> enum_word
%type <tok> check_function_head
%type <alterActions> alter_actions
%type <alterAction> alter_selected_action
%type <tok> alter_type_keyword
%type <collation> column_collation
%type <expr> alter_using
%type <stmt> delete
%type <tok> delete_alias update_alias from_alias
%type <returning> returning
%type <returningAliases> returning_aliases returning_options
%type <returningAlias> returning_option
%type <stmt> create_view alter_view drop_view
%type <alterView> view_alter_head
%type <tok> replace_word
%type <stmt> create_index
%type <createIndex> index_head index_definition
%type <tok> index_method index_direction
%type <indexKeys> index_keys index_include
%type <indexKey> index_key index_atom index_class
%type <viewOptions> index_options index_option_list
%type <viewOption> index_option
%type <tok> index_option_value
%type <stmt> alter_index drop_index reindex
%type <alterIndex> alter_index_head
%type <indexOptions> maintenance_index_option_list reindex_options reindex_option_list
%type <indexOption> maintenance_index_option reindex_option
%type <tok> index_option_name reindex_kind maintenance_concurrent
%type <indexValue> maintenance_index_option_value
%type <identifiers> view_columns view_names
%type <viewOptions> view_options view_option_list
%type <viewOption> view_option
%type <tok> view_option_value
%type <expr> delete_where
%type <expr> scalar_projection
%type <fromItem> optional_from
%type <row> row_fields
%type <tok> quantifier
%type <valuesQuery> query_values parenthesized_values
%type <rows> query_rows
%type <row> query_row query_cells
%type <expr> query_cell
%type <stmt> update
%type <assignments> update_assignments
%type <assignment> update_assignment
%type <col> column
%type <elements> elements optional_elements
%type <element> element
%type <check> check_constraint
%type <unique> unique_constraint column_unique
%type <desc> unique_nulls
%type <likeOptions> like_options
%type <likeOption> like_option
%type <tok> like_option_name
%type <objectNames> relation_names
%type <objectName> qualified_operator
%type <tok> operator_symbol
%type <dropMode> drop_mode
%type <typ> type temporal_type generic_precision generic_modifier_atom generic_modifiers type_modifier_head date_literal_precision
%type <tok> bit_modifier_operator bit_modifier_symbol
%type <typ> interval_type interval_fields interval_second_fields interval_type_qualifier
%type <constraints> nullability column_constraints domain_constraints
%type <constraint> column_constraint
%type <notNull> table_not_null
%type <tok> nn_modifier nn_inherit
%type <tok> column_suffix
%type <tok> name non_between_name setting_word setting_word_without_between role_name role_option_word type_name bare_label col_label generic_word generic_word_without_with generic_word_without_exists generic_word_base suffix nulls_keyword nulls_position
%type <tok> predicate_suffix where_predicate_suffix
%type <tok> excluded_role_target excluded_role_assignment
%type <tok> cast_function
%type <expr> string_function
%type <tok> string_word normalization_form normalized_form trim_direction
%type <row> trim_arguments trim_list
%type <settingName> setting_name set_name local_setting_name
%type <zoneSet> zone_setting
%type <integer> interval_precision
%type <tok> interval_qualifier interval_field
%type <typ> qualified_call_head
%type <targets> insert_targets
%type <objectName> relation_name
%type <fromItem> from_list joined_table from_atom from_base update_from
%type <identifiers> alias_columns using_columns
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
%type <rows> rows
%type <row> row values call_args function_values insert_values sequence_args sequence_values
%type <row> group_keys group
%type <expr> having
%type <value> value
%type <expr> scalar scalar_atom operand target_base target_between target_between_predicate target_comparison target default_target where target_not target_and target_or projection_target function function_argument insert_value
%type <expr> aggregate_call aggregate_application
%type <tok> like_operator
%type <expr> target_escape
%type <expr> conditional conditional_call case_test case_else conditional_value
%type <caseArms> case_arms
%type <caseArm> case_arm
%type <row> conditional_args count_values
%type <tok> conditional_word
%type <tok> comparison_operator
%type <call> function_args function_argument_list
%type <predicate> truth_kind
%type <selectStmt> projection projection_item
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
%type <tok> sequence_name_word
%type <identity> identity_definition identity_alter_options
%type <identityMode> identity_mode
%type <generation> generation_definition
%type <tok> generation_kind
%type <sequenceOptions> identity_options identity_option_list
%type <sequenceOption> identity_option identity_set_option
%type <override> insert_override
%%
script: separators optional_statements ;
separators: /* empty */ | separators ';' ;
optional_statements: /* empty */ | accepted tail ;
tail: /* empty */ | ';' separators optional_statements ;
accepted: statement { if $1 != nil { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } } ;
statement: create | create_sequence | alter_sequence | drop_sequence | alter_index | drop_index | reindex | alter_view | drop_view | drop_table | alter_drop_column | insert | update | delete | select | set | show | reset | create_role | drop_role | session_authorization | excluded_role | lock_table | create_schema | alter_schema | drop_schema | comment | transaction_control
 | drop_domain
 | generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | DROP { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER ROLE { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER USER { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER GROUP { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 } ;
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
sequence_options: /* empty */ { $$=nil } | sequence_options sequence_option { $$=append($1,$2) } ;
sequence_integer: NUMBER { n,ok:=sequenceInteger(sqllex,$1,"");if !ok{return 1};$$=n }
 | '+' NUMBER { n,ok:=sequenceInteger(sqllex,$2,"+");if !ok{return 1};$$=n }
 | '-' NUMBER { n,ok:=sequenceInteger(sqllex,$2,"-");if !ok{return 1};$$=n } ;
sequence_option: AS type { $$=SequenceOption{Name:"as",Type:$2,Pos:$1.pos} }
 | INCREMENT sequence_integer { $$=SequenceOption{Name:"increment",Value:$2,Pos:$1.pos} }
 | INCREMENT BY sequence_integer { $$=SequenceOption{Name:"increment",Value:$3,Pos:$1.pos} }
 | MINVALUE sequence_integer { $$=SequenceOption{Name:"min",Value:$2,Pos:$1.pos} }
 | NO MINVALUE { $$=SequenceOption{Name:"min",Type:TypeName{Name:"default"},Pos:$1.pos} }
 | MAXVALUE sequence_integer { $$=SequenceOption{Name:"max",Value:$2,Pos:$1.pos} }
 | NO MAXVALUE { $$=SequenceOption{Name:"max",Type:TypeName{Name:"default"},Pos:$1.pos} }
 | START sequence_integer { $$=SequenceOption{Name:"start",Value:$2,Pos:$1.pos} }
 | START WITH sequence_integer { $$=SequenceOption{Name:"start",Value:$3,Pos:$1.pos} }
 | CACHE sequence_integer { $$=SequenceOption{Name:"cache",Value:$2,Pos:$1.pos} }
 | CYCLE { $$=SequenceOption{Name:"cycle",Value:1,Pos:$1.pos} }
 | NO CYCLE { $$=SequenceOption{Name:"cycle",Pos:$1.pos} }
 | RESTART { $$=SequenceOption{Name:"restart",Type:TypeName{Name:"default"},Pos:$1.pos} }
 | RESTART sequence_integer { $$=SequenceOption{Name:"restart",Value:$2,Pos:$1.pos} }
 | RESTART WITH sequence_integer { $$=SequenceOption{Name:"restart",Value:$3,Pos:$1.pos} }
 | OWNED BY comment_column_name { $$=SequenceOption{Name:"owned",Owned:$3,Pos:$1.pos} } ;
identity_mode: ALWAYS { $$=IdentityAlways } | BY DEFAULT { $$=IdentityByDefault } ;
identity_definition: GENERATED identity_mode AS IDENTITY identity_options { $$=&IdentityDefinition{Mode:$2,Options:$5,Pos:$1.pos} } ;
generation_definition: GENERATED identity_mode AS '(' scalar_projection ')' generation_kind { expression:=$5;if restriction:=sqllex.(*lexer).generationCompletedRestriction;restriction!=nil {expression=ForbiddenGenerationExpr{*restriction}};$$=&GenerationDefinition{Expression:expression,Mode:$2,Kind:$7.text,Pos:$1.pos,ModePos:sqllex.(*lexer).generationModePos} } ;
generation_kind: /* empty */ { $$=lexeme{} } | KW_U { if $1.text!="stored" && $1.text!="virtual" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
identity_options: /* empty */ { $$=nil } | '(' identity_option_list ')' { $$=$2 } ;
identity_option_list: /* empty */ { $$=nil } | identity_option_list identity_option { $$=append($1,$2) } ;
identity_option: sequence_option { $$=$1 } | SEQUENCE name relation_name { if $2.text!="name" {sqllex.(*lexer).syntax($2);return 1}; $$=SequenceOption{Name:"name",Owned:$3,Pos:$1.pos} }
 | LOGGED { $$=SequenceOption{Name:"logged",Pos:$1.pos} }
 | UNLOGGED { $$=SequenceOption{Name:"unlogged",Pos:$1.pos} } ;
identity_set_option: sequence_option { $$=$1 }
 | LOGGED { $$=SequenceOption{Name:"logged",Pos:$1.pos} }
 | UNLOGGED { $$=SequenceOption{Name:"unlogged",Pos:$1.pos} } ;
identity_alter_options: SET GENERATED identity_mode { $$=&IdentityDefinition{Mode:$3} }
 | SET identity_set_option { if $2.Name=="as" || $2.Name=="owned" || $2.Name=="restart" {name:=$2.Name;if name=="owned"{name="owned_by"};sqllex.(*lexer).failAt("42601","sequence option \""+name+"\" not supported here",$2.Pos.Offset);return 1};$$=&IdentityDefinition{Options:[]SequenceOption{$2}} }
 | RESTART { $$=&IdentityDefinition{Options:[]SequenceOption{{Name:"restart",Type:TypeName{Name:"default"},Pos:$1.pos}}} }
 | RESTART sequence_integer { $$=&IdentityDefinition{Options:[]SequenceOption{{Name:"restart",Value:$2,Pos:$1.pos}}} }
 | RESTART WITH sequence_integer { $$=&IdentityDefinition{Options:[]SequenceOption{{Name:"restart",Value:$3,Pos:$1.pos}}} }
 | identity_alter_options SET GENERATED identity_mode { $$=$1; if $$.Mode!=IdentityNone {$$.ConflictingModes=true};$$.Mode=$4 }
 | identity_alter_options SET identity_set_option { if $3.Name=="as" || $3.Name=="owned" || $3.Name=="restart" {name:=$3.Name;if name=="owned"{name="owned_by"};sqllex.(*lexer).failAt("42601","sequence option \""+name+"\" not supported here",$3.Pos.Offset);return 1};$$=$1;$$.Options=append($$.Options,$3) }
 | identity_alter_options RESTART { $$=$1;$$.Options=append($$.Options,SequenceOption{Name:"restart",Type:TypeName{Name:"default"},Pos:$2.pos}) }
 | identity_alter_options RESTART sequence_integer { $$=$1;$$.Options=append($$.Options,SequenceOption{Name:"restart",Value:$3,Pos:$2.pos}) }
 | identity_alter_options RESTART WITH sequence_integer { $$=$1;$$.Options=append($$.Options,SequenceOption{Name:"restart",Value:$4,Pos:$2.pos}) } ;
comment: COMMENT ON TABLE relation_name IS comment_text { $$=&Comment{Kind:CommentTable,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
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
comment_excluded_word: IDENT | KW_U | KW_T | KW_R | UNIQUE | VIEW | INDEX | TYPE ;
comment_excluded_kind: comment_excluded_word { if $1.keyword==nil || !unsupportedCommentKinds[$1.text] { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
transaction_control: transaction_start { $$=&TransactionControl{Kind:KindStart,Pos:$1.pos} }
 | transaction_start transaction_modes { $$=&TransactionControl{Kind:KindStart,Pos:$1.pos,Modes:$2} }
 | SET TRANSACTION transaction_modes { $$=&TransactionCharacteristics{Pos:$1.pos,Modes:$3} }
 | SET SESSION characteristics_word AS TRANSACTION transaction_modes { $$=&TransactionCharacteristics{Session:true,Pos:$1.pos,Modes:$6} }
 | commit_word opt_transaction opt_chain { $$=&TransactionControl{Kind:KindCommit,Chain:$3,Pos:$1.pos} }
 | rollback_start opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$2,Pos:$1.pos} }
 | ABORT opt_transaction opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$3,Pos:$1.pos} }
 | SAVEPOINT name { $$=&SavepointControl{Kind:CreateSavepoint,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | RELEASE name { $$=&SavepointControl{Kind:ReleaseSavepoint,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | RELEASE SAVEPOINT name { $$=&SavepointControl{Kind:ReleaseSavepoint,Name:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | rollback_start TO name { $$=&SavepointControl{Kind:RollbackSavepoint,Name:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | rollback_start TO SAVEPOINT name { $$=&SavepointControl{Kind:RollbackSavepoint,Name:Identifier{Text:$4.text,Pos:$4.pos},Pos:$1.pos} }
 | PREPARE TRANSACTION STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | COMMIT PREPARED STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | ROLLBACK PREPARED STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil } ;
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
set: SET set_name setting_assignment setting_values { $$=parsedSet($2.identifier(),$4,$1.pos) }
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
 | SET SCHEMA STRING { $$=parsedSet(Identifier{Text:"search_path",Pos:$2.pos},settingList{values:[]string{$3.text},stringValues:[]bool{true}},$1.pos) }
 | SET SESSION SCHEMA STRING { $$=parsedSet(Identifier{Text:"search_path",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{true}},$1.pos) }
 | SET TIME ZONE zone_setting { $$=$4; $4.Name=Identifier{Text:"timezone",Pos:$2.pos};$4.Pos=$1.pos }
 | SET SESSION TIME ZONE zone_setting { $$=$5; $5.Name=Identifier{Text:"timezone",Pos:$3.pos};$5.Pos=$1.pos }
 | SET LOCAL TIME ZONE zone_setting { $$=$5; $5.Name=Identifier{Text:"timezone",Pos:$3.pos};$5.Pos=$1.pos;$5.Local=true }
 | SET LOCAL SCHEMA STRING { s:=parsedSet(Identifier{Text:"search_path",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{true}},$1.pos);s.Local=true;$$=s } ;
setting_assignment: TO | '=' ;
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
 | SHOW ALL { $$=&Show{All:true,Pos:$1.pos} } ;
reset: RESET set_name { $$=&Reset{Name:$2.identifier(),Pos:$1.pos} }
 | RESET TIME ZONE { $$=&Reset{Name:Identifier{Text:"timezone",Pos:$2.pos},Pos:$1.pos} }
 | RESET LOCAL { $$=&Reset{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | RESET ALL { $$=&Reset{All:true,Pos:$1.pos} } ;
setting_name: name { $$=settingName{first:$1} }
 | setting_name '.' name { $$=$1; if $$.parts==nil { $$.parts=[]string{$$.first.text,$3.text} } else { $$.parts=append($$.parts,$3.text) } } ;
set_name: setting_word { $$=settingName{first:$1} }
 | LOCAL '.' name { $$=settingName{first:$1,parts:[]string{$1.text,$3.text}} }
 | ROLE '.' name { $$=settingName{first:$1,parts:[]string{$1.text,$3.text}} }
 | set_name '.' name { $$=$1; if $$.parts==nil { $$.parts=[]string{$$.first.text,$3.text} } else { $$.parts=append($$.parts,$3.text) } } ;
/* An undotted ROLE after SET LOCAL belongs only to the excluded role command,
 * never to the setting-name reduction. Dotted ROLE and ordinary LOCAL remain
 * parameter names, and ROLE stays name-eligible everywhere else. */
local_setting_name: set_name { $$=$1 }
 | LOCAL { $$=settingName{first:$1} } ;
create_table: CREATE TABLE relation_name '(' optional_elements ')' { $$ = &CreateTable{Name:$3, Elements:$5, Pos:$1.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name '(' optional_elements ')' { $$ = &CreateTable{Name:$6, Elements:$8, Pos:$1.pos, IfNotExists:true} } ;
create_view: CREATE VIEW relation_name view_columns view_options AS select { query,ok:=$7.(*Select); if !ok { sqllex.(*lexer).failAt("0A000","view body requires an ordinary SELECT",$1.pos.Offset); return 1 }; $$=&CreateView{Name:$3,Columns:$4,Options:$5,Query:query,Pos:$1.pos} }
 | CREATE OR replace_word VIEW relation_name view_columns view_options AS select { query,ok:=$9.(*Select); if !ok { sqllex.(*lexer).failAt("0A000","view body requires an ordinary SELECT",$1.pos.Offset); return 1 }; $$=&CreateView{Name:$5,Columns:$6,Options:$7,Query:query,Replace:true,Pos:$1.pos} } ;
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
 | view_alter_head ALTER optional_column name DROP DEFAULT {sqllex.(*lexer).failAt("0A000","ALTER VIEW column defaults are unsupported",$2.pos.Offset);return 1} ;
drop_view: DROP VIEW relation_names drop_mode {$$=&DropView{Names:$3,Mode:$4,Pos:$1.pos}}
 | DROP VIEW IF EXISTS relation_names drop_mode {$$=&DropView{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos}} ;
view_columns: /* empty */ { $$=nil } | '(' view_names ')' { $$=$2 } ;
view_names: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} } | view_names ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
view_options: /* empty */ { $$=nil } | WITH '(' view_option_list ')' { $$=$3 } ;
view_option_list: view_option { $$=[]ViewOption{$1} } | view_option_list ',' view_option { $$=append($1,$3) } ;
view_option: name { $$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:"true"} } | name '=' view_option_value { $$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text,HasValue:true} } ;
view_option_value: name | STRING | NUMBER | TRUE | FALSE | ON ;
create: create_table { $$=$1 }
 | create_view { $$=$1 }
 | create_index { $$=$1 }
 | create_domain { $$=$1 }
 | create_enum { $$=$1 }
 | create_table INHERITS '(' relation_names ')' { sqllex.(*lexer).deferUnsupported($2,"table inheritance is unsupported"); $$=nil }
 | CREATE TEMP TABLE { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 }
 | CREATE UNLOGGED TABLE { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 }
 | CREATE generic_word { if $2.keyword!=nil && $2.text=="unique" {sqllex.(*lexer).capability($2,unsupportedCreateObjects,"unique indexes are unsupported")} else {sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported")}; return 1 } ;
/* Use a distinct unreserved head token, like DOMAIN, so generic-word
 * reductions cannot steal another CREATE declaration's continuation. */
create_enum: CREATE TYPE relation_name AS enum_word '(' enum_labels ')' {
 $$=&CreateEnum{Name:$3,Labels:$7,Pos:$1.pos}
} ;
enum_word: generic_word {
 if $1.keyword==nil || $1.text!="enum" {sqllex.(*lexer).failAt("0A000","CREATE TYPE syntax is unsupported",$1.pos.Offset);return 1}
 $$=$1
} ;
enum_labels: /* empty */ {$$=nil} | enum_label_list {$$=$1} ;
enum_label_list: STRING {$$=[]EnumLabel{{Text:$1.text,Pos:$1.pos}}}
 | enum_label_list ',' STRING {$$=append($1,EnumLabel{Text:$3.text,Pos:$3.pos})} ;
create_domain: CREATE DOMAIN relation_name domain_as type column_collation domain_constraints { $$=&CreateDomain{Name:$3,Base:$5,Collation:$6,Constraints:$7,Pos:$1.pos} } ;
domain_as: /* empty */ | AS ;
domain_constraints: /* empty */ {$$=nil}
 | column_constraints {$$=$1}
 | '(' {sqllex.(*lexer).syntax($1);return 1}
 | column_suffix {sqllex.(*lexer).capability($1,unsupportedSuffixes,"domain constraints are unsupported");return 1} ;
drop_domain: DROP DOMAIN relation_names drop_mode { $$=&DropDomain{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP DOMAIN IF EXISTS relation_names drop_mode { $$=&DropDomain{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
/* Index reloptions use '='; REINDEX controls use whitespace. Keep them separate
 * so malformed option syntax fails before any earlier statement can execute. */
maintenance_concurrent: /* empty */ {$$=lexeme{}} | CONCURRENTLY ;
drop_index: DROP INDEX maintenance_concurrent relation_names drop_mode {$$=&DropIndex{Names:$4,Concurrent:$3.pos,Mode:$5,Pos:$1.pos}}
 | DROP INDEX maintenance_concurrent IF EXISTS relation_names drop_mode {$$=&DropIndex{Names:$6,IfExists:true,Concurrent:$3.pos,Mode:$7,Pos:$1.pos}} ;
alter_index_head: ALTER INDEX relation_name {$$=&AlterIndex{Name:$3,Pos:$1.pos}}
 | ALTER INDEX IF EXISTS relation_name {$$=&AlterIndex{Name:$5,IfExists:true,Pos:$1.pos}} ;
alter_index: alter_index_head RENAME TO name {$1.Action="rename";$1.Target=Identifier{Text:$4.text,Pos:$4.pos};$1.ActionPos=$2.pos;$$=$1}
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
maintenance_index_option_list: maintenance_index_option {$$=[]IndexOption{$1}} | maintenance_index_option_list ',' maintenance_index_option {$$=append($1,$3)} ;
maintenance_index_option: index_option_name {$$=IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$1.pos}}}
 | index_option_name '=' maintenance_index_option_value {$$=IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3,HasValue:true}}
 | name '.' index_option_name {$$=IndexOption{Namespace:$1.text,Name:Identifier{Text:$3.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$3.pos}}}
 | name '.' index_option_name '=' maintenance_index_option_value {$$=IndexOption{Namespace:$1.text,Name:Identifier{Text:$3.text,Pos:$1.pos},Value:$5,HasValue:true}} ;
reindex_options: /* empty */ {$$=nil} | '(' reindex_option_list ')' {$$=$2} ;
reindex_option_list: reindex_option {$$=[]IndexOption{$1}} | reindex_option_list ',' reindex_option {$$=append($1,$3)} ;
reindex_option: col_label {$$=IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:Value{Kind:BooleanValue,Boolean:true,Text:"true",Pos:$1.pos}}}
 | col_label maintenance_index_option_value {$$=IndexOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$2,HasValue:true}} ;
reindex_kind: name | TABLE ;
reindex: REINDEX reindex_options reindex_kind maintenance_concurrent relation_name {if $3.text!="index"&&$3.text!="table"&&$3.text!="schema"&&$3.text!="database"&&$3.text!="system"{sqllex.(*lexer).syntax($3);return 1};$$=&Reindex{Name:$5,Kind:Identifier{Text:$3.text,Pos:$3.pos},Options:$2,Concurrent:$4.pos,Pos:$1.pos}} ;

index_head: CREATE INDEX ON relation_name { $$=&CreateIndex{Table:$4,Pos:$1.pos} }
 | CREATE INDEX name ON relation_name { $$=&CreateIndex{Name:Identifier{Text:$3.text,Pos:$3.pos},Table:$5,Pos:$1.pos} }
 | CREATE INDEX IF NOT EXISTS name ON relation_name { $$=&CreateIndex{Name:Identifier{Text:$6.text,Pos:$6.pos},Table:$8,IfNotExists:true,Pos:$1.pos} }
 | CREATE INDEX CONCURRENTLY ON relation_name { sqllex.(*lexer).deferUnsupported($3,"concurrent indexes are unsupported");$$=&CreateIndex{Table:$5,Pos:$1.pos} }
 | CREATE INDEX CONCURRENTLY name ON relation_name { sqllex.(*lexer).deferUnsupported($3,"concurrent indexes are unsupported");$$=&CreateIndex{Table:$6,Pos:$1.pos} }
 | CREATE INDEX CONCURRENTLY IF NOT EXISTS name ON relation_name { sqllex.(*lexer).deferUnsupported($3,"concurrent indexes are unsupported");$$=&CreateIndex{Table:$9,Pos:$1.pos} } ;
index_method: /* empty */ { sqllex.(*lexer).indexExpression=true; $$=lexeme{text:"btree"} } | USING name { sqllex.(*lexer).indexExpression=true; $$=$2 } ;
index_definition: index_head index_method '(' index_keys ')' index_include {
 sqllex.(*lexer).indexExpression=false;$1.Method=$2.text;$1.Keys=$4;$1.Include=$6;$$=$1
} ;
create_index: index_definition {$$=$1}
 | create_index WHERE target_or { sqllex.(*lexer).deferUnsupported($2,"partial indexes are unsupported");$$=$1 }
 | create_index WITH '(' view_option_list ')' { sqllex.(*lexer).deferUnsupported($2,"index storage parameters are unsupported");$$=$1 }
 | create_index TABLESPACE name { sqllex.(*lexer).deferUnsupported($2,"index tablespaces are unsupported");$$=$1 } ;
index_include: /* empty */ {$$=nil} | INCLUDE '(' index_keys ')' {$$=$3} ;
index_keys: index_key { $$=[]IndexKey{$1} } | index_keys ',' index_key { $$=append($1,$3) } ;
index_key: index_atom column_collation index_class index_direction nulls {
 $$=$1;$$.Collation=$2;$$.Opclass=$3.Opclass;$$.Options=$3.Options
 $$.Desc=$4.text=="desc";$$.DirectionExplicit=$4.text!="";$$.Nulls=$5
} ;
index_atom: name {$$=IndexKey{Column:Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos}}
 | '(' scalar_projection ')' {$$=IndexKey{Expression:$2,Pos:$1.pos}}
 | function {$$=IndexKey{Expression:$1,Pos:$1.SourcePosition()}}
 | conditional_call {$$=IndexKey{Expression:$1,Pos:$1.SourcePosition()}}
 | check_function_head function_args ')' {$$=IndexKey{Expression:FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$2.Args,ArgNames:$2.ArgNames,Variadic:$2.Variadic,ResolutionOnly:$1.resolutionOnly,Pos:$1.pos},Pos:$1.pos}}
 | name '.' check_function_head function_args ')' {$$=IndexKey{Expression:FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$4.Args,ArgNames:$4.ArgNames,Variadic:$4.Variadic,ResolutionOnly:$3.resolutionOnly,Pos:$1.pos},Pos:$1.pos}}
 | string_function {$$=IndexKey{Expression:$1,Pos:$1.SourcePosition()}}
 | name '(' function_args ')' {$$=IndexKey{Expression:FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos},Pos:$1.pos}}
 | name '.' col_label '(' function_args ')' {$$=IndexKey{Expression:FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos},Pos:$1.pos}} ;
index_class: /* empty */ {$$=IndexKey{}}
 | relation_name index_options {name:=$1;$$=IndexKey{Opclass:&name,Options:$2}} ;
index_options: /* empty */ {$$=nil} | '(' index_option_list ')' {$$=$2} ;
index_option_list: index_option {$$=[]ViewOption{$1}} | index_option_list ',' index_option {$$=append($1,$3)} ;
index_option: col_label {$$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:"true"}}
 | col_label '=' index_option_value {$$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text,HasValue:true}} ;
index_option_value: view_option_value | '+' NUMBER {$$=$2;$$.text="+"+$2.text}
 | '-' NUMBER {$$=$2;$$.text="-"+$2.text} ;
index_direction: /* empty */ {$$=lexeme{}} | ASC {$$=$1} | DESC {$$=$1} ;
optional_elements: /* empty */ { $$=nil } | elements { $$=$1 } ;
elements: element { $$=[]TableElement{$1} } | elements ',' element { $$=append($1,$3) } ;
element: column { c:=$1; $$=TableElement{Column:&c,Pos:c.Pos} }
 | LIKE relation_name like_options { $$=TableElement{Like:&TableLike{Name:$2,Options:$3},Pos:$1.pos} }
 | table_not_null { $$=TableElement{NotNull:$1,Pos:$1.Pos} }
 | check_constraint { $$=TableElement{Check:$1,Pos:$1.Pos} }
 | unique_constraint { $$=TableElement{Unique:$1,Pos:$1.Pos} } ;
table_not_null: NOT NULL name { $$=&NotNullConstraint{Column:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | CONSTRAINT name NOT NULL name { $$=&NotNullConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Column:Identifier{Text:$5.text,Pos:$5.pos},Pos:$3.pos} }
 | table_not_null NOT nn_modifier { sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 }
 | table_not_null NO nn_inherit { sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 } ;
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
 | column_unique KW_R name { if $2.text!="initially" || $3.keyword==nil || ($3.text!="deferred" && $3.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 } ;
unique_constraint: UNIQUE unique_nulls '(' view_names ')' { $$=&UniqueConstraint{Columns:$4,NullsNotDistinct:$2,Pos:$1.pos} }
 | CONSTRAINT name UNIQUE unique_nulls '(' view_names ')' { $$=&UniqueConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Columns:$6,NullsNotDistinct:$4,Pos:$1.pos} }
 | unique_constraint DEFERRABLE { sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 }
 | unique_constraint NOT_DEFERRABLE_LA DEFERRABLE { sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 }
 | unique_constraint KW_R name { if $2.text!="initially" || $3.keyword==nil || ($3.text!="deferred" && $3.text!="immediate") {sqllex.(*lexer).syntax($2);return 1};sqllex.(*lexer).deferUnsupported($2,"UNIQUE deferrability is unsupported");$$=$1 } ;
like_options: /* empty */ { $$=nil } | like_options like_option { $$=append($1,$2) } ;
like_option: INCLUDING like_option_name { $$=LikeOption{Name:$2.text,Including:true,Pos:$1.pos} }
 | EXCLUDING like_option_name { $$=LikeOption{Name:$2.text,Pos:$1.pos} } ;
like_option_name: name { if $1.keyword==nil || !validLikeOption($1.text) { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } | ALL ;
drop_table: DROP TABLE relation_names drop_mode { $$=&DropTable{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP TABLE IF EXISTS relation_names drop_mode { $$=&DropTable{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
relation_names: relation_name { $$=[]ObjectName{$1} } | relation_names ',' relation_name { $$=append($1,$3) } ;
drop_mode: /* empty */ { $$=DropRestrict } | RESTRICT { $$=DropRestrict } | CASCADE { $$=DropCascade } ;
alter_drop_column: ALTER TABLE alter_target alter_actions { $$=&AlterTable{Name:$3.Name,Only:$3.Only,Actions:$4,Pos:$1.pos} }
 | alter_default_single { $$=$1 }
 | alter_default_single ',' { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset); return 1 }
 | alter_rename_single { $$=$1 }
 | alter_schema_single { $$=$1 }
 | alter_rename_single ',' { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset); return 1 }
 | ALTER TABLE rename_target alter_action { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset); return 1 } ;
alter_actions: alter_selected_action { $$=[]AlterTableAction{$1} }
 | alter_actions ',' alter_selected_action { $$=append($1,$3) }
 | alter_actions ',' ALTER optional_column name SET DEFAULT target_or { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$3.pos.Offset); return 1 }
 | alter_actions ',' ALTER optional_column name DROP DEFAULT { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$3.pos.Offset); return 1 } ;
alter_selected_action: DROP optional_column drop_target drop_mode { $$=AlterTableAction{Column:$3.Name,IfExists:$3.IfExists,Mode:$4,Action:DropColumn,Pos:$1.pos} }
 | ALTER optional_column name SET NOT NULL { $$=AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:SetNotNull,Pos:$1.pos} }
 | ALTER optional_column name DROP NOT NULL { $$=AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropNotNull,Pos:$1.pos} }
 | ADD table_not_null { $$=AlterTableAction{Column:$2.Column,Constraint:$2.Name,Action:AddNotNull,Pos:$1.pos} }
 | DROP CONSTRAINT drop_target drop_mode { $$=AlterTableAction{Constraint:$3.Name,Action:DropConstraint,IfExists:$3.IfExists,Mode:$4,Pos:$1.pos} }
 | ALTER optional_column name ADD identity_definition { $$=AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:AddIdentity,Identity:$5,Pos:$1.pos} }
 | ALTER optional_column name DROP IDENTITY { $$=AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropIdentity,Pos:$1.pos} }
 | ALTER optional_column name DROP IDENTITY IF EXISTS { $$=AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:DropIdentity,IfExists:true,Pos:$1.pos} }
 | ALTER optional_column name identity_alter_options { $$=AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:SetIdentity,Identity:$4,Pos:$1.pos} }
 | ADD optional_column column { c:=$3; $$=AlterTableAction{Column:Identifier{Text:c.Name,Pos:c.Pos},Definition:&c,Action:AddColumn,Pos:$1.pos} }
 | ADD optional_column IF NOT EXISTS column { c:=$6; $$=AlterTableAction{Column:Identifier{Text:c.Name,Pos:c.Pos},Definition:&c,IfNotExists:true,Action:AddColumn,Pos:$1.pos} }
 | ADD CONSTRAINT name column_suffix { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$1.pos.Offset); return 1 }
 | ADD unique_constraint { sqllex.(*lexer).deferUnsupported($1,"ALTER TABLE action is unsupported");$$=AlterTableAction{} }
 | ALTER optional_column name alter_type_keyword type column_collation alter_using { $$=AlterTableAction{Column:Identifier{Text:$3.text,Pos:$3.pos},Action:AlterColumnType,TypeName:$5,Collation:$6,Using:$7,Pos:$1.pos} } ;
alter_type_keyword: TYPE { $$=$1 }
 | SET KW_U TYPE { if $2.text!="data" {sqllex.(*lexer).syntax($2);return 1};$$=$1 } ;
column_collation: /* empty */ {$$=nil}
 | COLLATE relation_name {name:=$2;name.Parts[0].Pos=$1.pos;$$=&name} ;
alter_using: /* empty */ {$$=nil} | USING scalar_projection {$$=$2} ;
alter_default_single: ALTER TABLE alter_target ALTER optional_column name SET DEFAULT target_or { $$=&AlterTableDefault{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},Only:$3.Only,Action:SetDefault,Expression:$9,Pos:$1.pos} }
 | ALTER TABLE alter_target ALTER optional_column name DROP DEFAULT { $$=&AlterTableDefault{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},Only:$3.Only,Action:DropDefault,Pos:$1.pos} } ;
alter_rename_single: ALTER TABLE rename_target RENAME TO name { $$=&AlterTableRenameTable{Name:$3.Name,NewName:Identifier{Text:$6.text,Pos:$6.pos},IfExists:$3.IfExists,Pos:$1.pos} }
 | ALTER TABLE rename_target RENAME optional_column name TO name { $$=&AlterTableRenameColumn{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},NewName:Identifier{Text:$8.text,Pos:$8.pos},Only:$3.Only,IfExists:$3.IfExists,Pos:$1.pos} }
 | ALTER TABLE rename_target RENAME CONSTRAINT name TO name { sqllex.(*lexer).deferUnsupported($5,"RENAME CONSTRAINT is unsupported"); $$=nil } ;
alter_schema_single: ALTER TABLE rename_target SET SCHEMA name { $$=&AlterTableSetSchema{Name:$3.Name,Schema:Identifier{Text:$6.text,Pos:$6.pos},IfExists:$3.IfExists,Only:$3.Only,Pos:$1.pos} } ;
rename_target: alter_target {$$=$1}
 | IF EXISTS alter_target {$$=$3;$$.IfExists=true} ;
alter_target: relation_name { $$=alterTarget{Name:$1} }
 | relation_name '*' { $$=alterTarget{Name:$1} }
 | ONLY relation_name { $$=alterTarget{Name:$2,Only:true} }
 | ONLY '(' relation_name ')' { $$=alterTarget{Name:$3,Only:true} } ;
optional_column: /* empty */ { $$=false } | COLUMN { $$=true } ;
/* Share the IF prefix with eligible names: DROP if and IF EXISTS both parse. */
drop_target: name { $$=dropTarget{Name:Identifier{Text:$1.text,Pos:$1.pos}} }
 | IF EXISTS name { $$=dropTarget{Name:Identifier{Text:$3.text,Pos:$3.pos},IfExists:true} } ;
/* EXISTS cannot start an ALTER action: retain it for IF EXISTS lookahead,
 * rather than competing with an unqualified relation literally named if. */
alter_action: generic_word_without_exists | OWNER | SET ;
column: name type column_collation nullability { $$ = Column{Name:$1.text, TypeName:$2, Collation:$3, Constraints:$4, Pos:$1.pos} } ;
type: INTEGER { $$ = TypeName{Name:"integer",Pos:$1.pos} } | BIGINT { $$ = TypeName{Name:"bigint",Pos:$1.pos} } | TEXT %prec TYPE_NAME { $$ = TypeName{Name:"text",Pos:$1.pos} } | BOOLEAN { $$ = TypeName{Name:"boolean",Pos:$1.pos} }
 | type_name %prec TYPE_NAME { $$=columnType($1) }
 | temporal_type { $$=$1 }
 | interval_type { $$=$1 }
 | type_modifier_head generic_modifiers ')' { if sqllex.(*lexer).floatPrecision { $$=$2;$$.Pos=$1.Pos;sqllex.(*lexer).floatPrecision=false } else { $$=$1;$$.Modifiers=$2.Modifiers };sqllex.(*lexer).characterPrecision=false;sqllex.(*lexer).characterGeneric=false;sqllex.(*lexer).bitModifier=false }
 | type_name type_name %prec TYPE_NAME { if $1.keyword!=nil && $2.keyword!=nil && ($1.text=="double" && $2.text=="precision" || ($1.text=="character" || $1.text=="bit") && $2.text=="varying") { $$=TypeName{Name:$1.text+" "+$2.text,Pos:$1.pos} } else { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 } }
 | type_name WITH { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 }
 | type_name '.' col_label { $$=TypeName{Name:$3.text,Quoted:columnType($3).Quoted,Schema:&Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos} }
 | type '[' ']' { $$=$1;$$.ArrayBounds=append($$.ArrayBounds,0) }
 | type '[' NUMBER ']' { n,ok:=sequenceInteger(sqllex,$3,"");if !ok{return 1};if n>2147483647{sqllex.(*lexer).failAt("22003","array dimension is out of range",$3.pos.Offset);return 1};$$=$1;$$.ArrayBounds=append($$.ArrayBounds,int32(n)) }
 | type ARRAY { $$=$1;$$.ArrayBounds=append($$.ArrayBounds,0) }
 | type ARRAY '[' NUMBER ']' { n,ok:=sequenceInteger(sqllex,$4,"");if !ok{return 1};if n>2147483647{sqllex.(*lexer).failAt("22003","array dimension is out of range",$4.pos.Offset);return 1};$$=$1;$$.ArrayBounds=append($$.ArrayBounds,int32(n)) } ;
/* Descriptor modifiers share raw constant syntax; FLOAT and temporal types retain
 * their distinct unsigned/single precision contracts. */
type_modifier_head: type_name '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2,false);if !ok{return 1}}
 | TEXT '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2,false);if !ok{return 1}}
 | type_name type_name '(' {if $1.keyword==nil || $2.keyword==nil || ($1.text!="character" && $1.text!="bit") || $2.text!="varying" {sqllex.(*lexer).syntax($2);return 1};tok:=$1;tok.text=$1.text+" varying";var ok bool;$$,ok=typeModifierHead(sqllex,tok,$3,false);if !ok{return 1}}
 | type_name '.' col_label '(' {tok:=$3;tok.raw="\""+$3.text+"\"";tok.keyword=nil;var ok bool;$$,ok=typeModifierHead(sqllex,tok,$4,true);if !ok{return 1};$$.Name=$3.text;$$.Quoted=columnType($3).Quoted;$$.Schema=&Identifier{Text:$1.text,Pos:$1.pos};$$.Pos=$1.pos} ;
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
/* Modifiers require a preceding constraint, never an empty nullability list. */
column_constraints: column_constraint { $$=[]ColumnConstraint{$1} }
 | column_constraints column_constraint { $$=append($1,$2) }
 | column_constraints NO nn_inherit { if $1[len($1)-1].Check!=nil {sqllex.(*lexer).syntax($2);return 1};sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 }
 | column_constraints NOT nn_modifier { sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 } ;
column_constraint: NULL { $$=ColumnConstraint{Pos:$1.pos} }
 | NOT NULL { $$=ColumnConstraint{NotNull:true,Pos:$1.pos} }
 | CONSTRAINT name NULL { $$=ColumnConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos} }
 | CONSTRAINT name NOT NULL { $$=ColumnConstraint{NotNull:true,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos} }
 | DEFAULT default_target { if token,_:=nonqueryQualification($2,false);token!=nil {sqllex.(*lexer).syntax(*token);return 1};if !sqllex.(*lexer).defaultIndexPropertyAllowed($2) {return 1};$$=ColumnConstraint{Default:$2,Pos:$1.pos} }
 | CONSTRAINT name DEFAULT default_target { if token,_:=nonqueryQualification($4,false);token!=nil {sqllex.(*lexer).syntax(*token);return 1};if !sqllex.(*lexer).defaultIndexPropertyAllowed($4) {return 1};$$=ColumnConstraint{Default:$4,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos} }
 | check_constraint { $$=ColumnConstraint{Check:$1,Pos:$1.Pos} }
 | column_unique { $$=ColumnConstraint{Unique:$1,Pos:$1.Pos} }
 | identity_definition { $$=ColumnConstraint{Identity:$1,Pos:$1.Pos} }
 | CONSTRAINT name identity_definition { $$=ColumnConstraint{Identity:$3,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | generation_definition { $$=ColumnConstraint{Generation:$1,Pos:$1.Pos} }
 | CONSTRAINT name generation_definition { $$=ColumnConstraint{Generation:$3,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.Pos} }
 | CONSTRAINT name column_suffix { sqllex.(*lexer).capability($3,unsupportedSuffixes,"column constraints are unsupported"); return 1 } ;
nn_modifier: KW_U { if $1.text!="valid" && $1.text!="enforced" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
nn_inherit: KW_U { if $1.text!="inherit" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
column_suffix: LIKE | KW_T | KW_R | RETURNING | HAVING | INTO | ALL | ON | GROUP | WITH | USER | IN | IS | AND ;
update: UPDATE relation_name update_alias SET update_assignments update_from where returning { $$=&Update{Name:$2,Alias:Identifier{Text:$3.text,Pos:$3.pos},Assignments:$5,From:$6,Where:$7,Returning:$8,Pos:$1.pos} }
 | UPDATE relation_name update_alias SET update_assignments update_from where suffix { sqllex.(*lexer).capability($8,unsupportedSuffixes,"UPDATE suffix is unsupported"); return 1 }
 | UPDATE relation_name update_alias SET '(' { sqllex.(*lexer).failAt("0A000","row assignments are unsupported",$5.pos.Offset);return 1 } ;
update_alias: /* empty */ %prec '=' { $$=lexeme{} } | name { $$=$1 } | AS name { $$=$2 } ;
update_from: /* empty */ { $$=nil } | FROM from_list { $$=$2 } ;
update_assignments: update_assignment { $$=[]UpdateAssignment{$1} } | update_assignments ',' update_assignment { $$=append($1,$3) } ;
update_assignment: name '=' scalar_projection { $$=UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Value:$3} }
 | name '=' DEFAULT { $$=UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Value:DefaultExpr{Pos:$3.pos}} }
 | name '.' name '=' scalar_projection { $$=UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Indirection:[]Identifier{{Text:$3.text,Pos:$3.pos}},Value:$5} }
 | name '=' scalar_projection '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '*' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '/' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 } ;
insert: INSERT INTO relation_name insert_input { $$=insertStatement($3,nil,$4,$1.pos) }
 | INSERT INTO relation_name '(' insert_targets ')' insert_input { $$=insertStatement($3,$5,$7,$1.pos) }
 | INSERT INTO relation_name insert_input suffix { sqllex.(*lexer).capability($5,unsupportedSuffixes,"INSERT suffix is unsupported"); return 1 }
 | INSERT INTO relation_name '(' insert_targets ')' insert_input suffix { sqllex.(*lexer).capability($8,unsupportedSuffixes,"INSERT suffix is unsupported"); return 1 }
 | INSERT INTO relation_name insert_input RETURNING { sqllex.(*lexer).failAt("0A000","INSERT suffix is unsupported",$5.pos.Offset);return 1 }
 | INSERT INTO relation_name '(' insert_targets ')' insert_input RETURNING { sqllex.(*lexer).failAt("0A000","INSERT suffix is unsupported",$8.pos.Offset);return 1 }
 | INSERT INTO relation_name insert_override insert_input { s:=insertStatement($3,nil,$5,$1.pos);s.Override=$4;$$=s }
 | INSERT INTO relation_name '(' insert_targets ')' insert_override insert_input { s:=insertStatement($3,$5,$8,$1.pos);s.Override=$7;$$=s }
 | INSERT INTO relation_name DEFAULT VALUES { $$=&Insert{Name:$3,Rows:[][]Expr{nil},Pos:$1.pos,DefaultValues:true} } ;
insert_override: OVERRIDING SYSTEM VALUE { $$=OverrideSystem } | OVERRIDING USER VALUE { $$=OverrideUser } ;
delete: DELETE FROM relation_name delete_alias delete_where returning { if token:=deleteQualification($5,false); token!=nil {sqllex.(*lexer).syntax(*token);return 1}; $$=&Delete{Name:$3,Alias:Identifier{Text:$4.text,Pos:$4.pos},Where:$5,Returning:$6,Pos:$1.pos} }
 | DELETE FROM relation_name delete_alias delete_where suffix { sqllex.(*lexer).capability($6,unsupportedSuffixes,"DELETE suffix is unsupported"); return 1 }
 | DELETE FROM relation_name delete_alias delete_where USING { sqllex.(*lexer).failAt("0A000","DELETE suffix is unsupported",$6.pos.Offset);return 1 } ;
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
delete_where: /* empty */ { $$=nil } | WHERE target_or { $$=$2 }
 | WHERE KW_U OF name {
   if $2.keyword==nil || $2.text!="current" { sqllex.(*lexer).syntax($3); return 1 }
   sqllex.(*lexer).deferUnsupported($2,"DELETE CURRENT OF is unsupported"); $$=nil
 } ;
like_operator: LIKE | ILIKE ;
target_escape: /* empty */ %prec BETWEEN { $$=nil } | ESCAPE target_base { $$=$2 } ;
insert_targets: name { $$ = []InsertTarget{{Name:$1.text, Pos:$1.pos}} } | insert_targets ',' name { $$ = append($1, InsertTarget{Name:$3.text, Pos:$3.pos}) } ;
insert_input: VALUES rows order bounds { $$=&InsertSource{Values:&ValuesQuery{Rows:$2,Order:$3,Bounds:$4,Pos:$1.pos}} }
 | insert_select { $$=&InsertSource{Select:$1} }
 | '(' insert_input ')' { $$=$2 } ;
insert_select: query_select { deferDistinctOnBoundary(sqllex.(*lexer),$1);$$=$1 } ;
names: name %prec NAME_LIST { $$ = []string{$1.text} } | names ',' name %prec NAME_LIST { $$ = append($1,$3.text) } ;
rows: row { $$ = [][]Expr{$1} } | rows ',' row { $$ = append($1,$3) } ;
row: '(' insert_values ')' { $$ = $2 } ;
values: scalar { if !sqllex.(*lexer).scalarInput($1) { return 1 }; $$ = []Expr{$1} } | values ',' scalar { if !sqllex.(*lexer).scalarInput($3) { return 1 }; $$ = append($1,$3) } ;
insert_value: scalar { if !sqllex.(*lexer).scalarInput($1) { return 1 }; $$=$1 } | DEFAULT { $$=DefaultExpr{Pos:$1.pos} } ;
insert_values: insert_value { $$=[]Expr{$1} } | insert_values ',' insert_value { $$=append($1,$3) } ;
query_values: VALUES query_rows order bounds { $$=&ValuesQuery{Rows:$2,Order:$3,Bounds:$4,Pos:$1.pos} } ;
parenthesized_values: '(' query_values ')' { $$=$2 }
 | '(' parenthesized_values ')' { $$=$2 } ;
query_rows: query_row { $$=[][]Expr{$1} } | query_rows ',' query_row { $$=append($1,$3) } ;
query_row: '(' query_cells ')' { $$=$2 } ;
query_cells: query_cell { $$=[]Expr{$1} } | query_cells ',' query_cell { $$=append($1,$3) } ;
query_cell: scalar_projection { $$=$1 } | DEFAULT { $$=DefaultExpr{Pos:$1.pos} } ;
scalar: scalar_atom { $$=$1 }
 | scalar GENERATION_OVER '(' generation_window_spec ')' { restriction:=GenerationRestriction{Code:"42P20",Message:"window functions are not allowed in column generation expressions",Pos:$1.SourcePosition()};l:=sqllex.(*lexer);if l.generationRestriction==nil {l.generationRestriction=&restriction} else {*l.generationRestriction=restriction};if l.generationDepth==0 {l.generationCompletedRestriction=l.generationRestriction};$$=ForbiddenGenerationExpr{restriction} }
 | select_with_parens %prec NAME_LIST { $$=SubqueryExpr{Kind:SubqueryScalar,Query:$1.Query,Pos:$1.Pos} }
 | EXISTS select_with_parens %prec NAME_LIST { $$=SubqueryExpr{Kind:SubqueryExists,Query:$2.Query,Pos:$1.pos} }
 | ROW '(' ')' { $$=RowExpr{Explicit:true,Pos:$1.pos} }
 | ROW '(' row_fields ')' { $$=RowExpr{Fields:$3,Explicit:true,Pos:$1.pos} }
 | '(' scalar_projection ',' row_fields ')' { $$=RowExpr{Fields:append([]Expr{$2},$4...),Pos:$1.pos} }
 | '(' scalar ')' { $$=groupedQualification($2) }
 | CAST '(' scalar AS type ')' { if $5.Name=="regclass" && $5.Schema==nil && len($5.Modifiers)==0 && len($5.ArrayBounds)==0 { $$=RegclassExpr{Child:$3,Pos:$1.pos} } else { $$=CastExpr{Child:$3,TypeName:$5,Syntax:StandardCast,Pos:$1.pos} } }
 | scalar ':' type %prec ':' { if $2.text!="::" { sqllex.(*lexer).syntax($2); return 1 }; if $3.Name=="regclass" && $3.Schema==nil && len($3.Modifiers)==0 && len($3.ArrayBounds)==0 { $$=RegclassExpr{Child:$1,Pos:$2.pos} } else { $$=CastExpr{Child:$1,TypeName:$3,Syntax:PostfixCast,Pos:$2.pos} } }
 | scalar COLLATE relation_name %prec COLLATE { $$=CollateExpr{Child:$1,Name:$3,Pos:$2.pos} }
 | aggregate_application { $$=$1 }
 /* Share the call opener, but retain raw DATE modifiers instead of reducing
  * them as scalar literals (which would convert overflowing numbers too early). */
 | cast_function date_literal_precision ')' STRING {raw:=columnType($1);raw.Modifiers=$2.Modifiers;$$=TypedLiteralExpr{TypeName:raw,Text:$4.text,Pos:$4.pos}}
 | qualified_call_head date_literal_precision ')' STRING {raw:=$1;raw.Modifiers=$2.Modifiers;$$=TypedLiteralExpr{TypeName:raw,Text:$4.text,Pos:$4.pos}}
 | '+' scalar %prec UNARY { $$=arithmeticExpr($1,$2,nil) }
 | '-' scalar %prec UNARY { $$=negateExpr(sqllex,$1,$2) }
 | SYMBOLIC_OPERATOR scalar %prec SYMBOLIC_OPERATOR { $$=arithmeticExpr($1,$2,nil) }
 | scalar '+' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '-' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '*' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '/' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '%' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '^' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar AT TIME ZONE scalar %prec AT { $$=TimeZoneExpr{Value:$1,Zone:$5,Pos:$2.pos} }
 | scalar AT LOCAL %prec AT { $$=TimeZoneExpr{Value:$1,Local:true,Pos:$2.pos} }
 | scalar SYMBOLIC_OPERATOR scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar SYMBOLIC_OPERATOR function { if $2.text!="||" {sqllex.(*lexer).syntax($2);return 1};$$=arithmeticExpr($2,$1,$3) }
 | function SYMBOLIC_OPERATOR scalar { if $2.text!="||" {sqllex.(*lexer).syntax($2);return 1};$$=arithmeticExpr($2,$1,$3) }
 | scalar qualified_operator scalar %prec OPERATOR { $$=OperatorExpr{Name:$2,Args:[]Expr{$1,$3},Pos:$2.Parts[0].Pos} }
 | scalar qualified_operator quantifier select_with_parens %prec OPERATOR {kind:=SubqueryAny;if $3.text=="all" {kind=SubqueryAll};$$=quantifiedQuery($1,$2,kind,$4.Query,false,$2.Parts[0].Pos)}
 | qualified_operator scalar %prec OPERATOR { $$=OperatorExpr{Name:$1,Args:[]Expr{$2},Pos:$1.Parts[0].Pos} } ;
scalar_atom: value %prec NAME_LIST { $$=LiteralExpr{Value:$1} }
 | string_function { $$=$1 }
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
 | IDENT STRING { if $1.text!="date" && $1.text!="timestamptz" && $1.text!="timetz" && $1.text!="json" && $1.text!="jsonb" {sqllex.(*lexer).failAt("0A000","typed literals are unsupported",$1.pos.Offset);return 1};$$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos} }
 | KW_C STRING { if $1.text!="json" {sqllex.(*lexer).syntax($2);return 1};$$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos} }
 | name %prec NAME_LIST { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos} }
 /* A second dot continues the existing three-part column path; it must not
  * inherit the nonassociative precedence of the first dot. */
 | name '.' col_label %prec NAME_LIST { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos,QualificationPos:$2.pos} }
 | name '.' col_label STRING {raw,ok:=qualifiedTypedLiteral(sqllex,$1,$3);if !ok{return 1};$$=TypedLiteralExpr{TypeName:raw,Text:$4.text,Pos:$4.pos}}
 | name '.' col_label '.' col_label { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}},Pos:$1.pos,QualificationPos:$2.pos,ExtraQualificationPos:$4.pos} }
 | conditional { $$=$1 }
 | INDEX_SUBQUERY_OPEN query_values ')' { $$=IndexSubqueryExpr{Pos:$1.pos} }
 | SEQUENCE_FUNC '(' sequence_args ')' { $$=CallExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3} }
 | name '.' SEQUENCE_FUNC '(' sequence_args ')' { $$=CallExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5} }
 | check_function_head function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$2.Args,ArgNames:$2.ArgNames,Variadic:$2.Variadic,ResolutionOnly:$1.resolutionOnly,Pos:$1.pos} }
 | OVERLAY '(' scalar PLACING scalar FROM scalar ')' { $$=binarySQLCall("overlay",$1.pos,[]Expr{$3,$5,$7}) }
 | OVERLAY '(' scalar PLACING scalar FROM scalar FOR scalar ')' { $$=binarySQLCall("overlay",$1.pos,[]Expr{$3,$5,$7,$9}) }
 | OVERLAY '(' function_values ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"overlay",Pos:$1.pos}}},Args:$3,Pos:$1.pos} }
 | OVERLAY '(' ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"overlay",Pos:$1.pos}}},Pos:$1.pos} }
 | POSITION '(' scalar IN scalar ')' { $$=binarySQLCall("position",$1.pos,[]Expr{$5,$3}) }
 | SUBSTRING '(' scalar FROM scalar ')' { $$=binarySQLCall("substring",$1.pos,[]Expr{$3,$5}) }
 | SUBSTRING '(' scalar FROM scalar FOR scalar ')' { $$=binarySQLCall("substring",$1.pos,[]Expr{$3,$5,$7}) }
 | SUBSTRING '(' scalar FOR scalar FROM scalar ')' { $$=binarySQLCall("substring",$1.pos,[]Expr{$3,$7,$5}) }
 | SUBSTRING '(' scalar FOR scalar ')' { $$=binarySQLCall("substring",$1.pos,[]Expr{$3,binarySubstringStart(),binarySubstringCount($5)}) }
 | SUBSTRING '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"substring",Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' check_function_head function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$4.Args,ArgNames:$4.ArgNames,Variadic:$4.Variadic,ResolutionOnly:$3.resolutionOnly,Pos:$1.pos} } ;
/* Only an actual scalar/index call can enter the failure-only argument context. */
check_function_head: CHECK_FUNC '(' {$$=$1;sqllex.(*lexer).formatCallHead($1)} ;
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
conditional_value: target_or { $$=$1 } | function { $$=$1 } ;
conditional_args: target_or { $$=[]Expr{$1} } | conditional_args ',' target_or { $$=append($1,$3) } ;
generation_window_spec: /* empty */ {} | ORDER BY order_keys {} | GENERATION_PARTITION BY group_keys {} | GENERATION_PARTITION BY group_keys ORDER BY order_keys {} ;
cast_function: name '(' { if !sqllex.(*lexer).castFunction($1,$2) { return 1 }; $$=$1 } ;
/* Qualified calls and precision literals share their opener so a function-call
 * shift cannot consume the precision before the literal STRING continuation. */
qualified_call_head: name '.' col_label '(' {$$=TypeName{Name:$3.text,Quoted:columnType($3).Quoted,Schema:&Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos};if $1.text=="pg_catalog" && ($3.text=="timestamptz" || $3.text=="timetz") {sqllex.(*lexer).genericModifier=true;sqllex.(*lexer).dateLiteralModifier=true}} ;
date_literal_precision: DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$1,false);if !ok{return 1}}
 | '-' DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$2,true);if !ok{return 1};$$.Modifiers[0].Pos=$1.pos}
 | '+' DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$2,false);if !ok{return 1};$$.Modifiers[0].UnaryPlus=true;$$.Modifiers[0].Pos=$1.pos} ;
value: NULL { $$ = Value{Kind:NullValue, Pos:$1.pos} }
 | TRUE { $$ = Value{Kind:BooleanValue, Boolean:true, Pos:$1.pos} }
 | FALSE { $$ = Value{Kind:BooleanValue, Pos:$1.pos} }
 | STRING { $$ = Value{Kind:StringValue, Text:$1.text, Pos:$1.pos} }
 | BITSTRING { $$ = Value{Kind:BitStringValue, Text:$1.text, Pos:$1.pos} }
 | NUMBER { var ok bool; $$, ok = integerValue(sqllex,$1,""); if !ok { return 1 } }
 | KW_T '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | CONCURRENTLY '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | CROSS '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | INNER '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | JOIN '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | NATURAL '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | LEFT '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | RIGHT '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | FULL '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | OUTER '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | IS '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | LIKE '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | ILIKE '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | AUTHORIZATION '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | ISNULL '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | NOTNULL '(' { sqllex.(*lexer).call($1,$2); return 1 } ;
/* Newly admitted FROM trees diagnose a misplaced ON as syntax. Preserve the
 * legacy single-table suffix capability boundary for otherwise excluded SQL. */
/* Set-operation syntax is structural, but its execution remains excluded.
 * Parenthesized operands preserve arm-local decorations separately from the
 * result-level ORDER/bounds/locking tail. */
select_projection: projection { $$=$1 }
 | DISTINCT projection { $2.Distinct=&DistinctClause{Pos:$1.pos};$$=$2 }
 | DISTINCT ON '(' group_keys ')' projection { $6.Distinct=&DistinctClause{On:$4,Pos:$1.pos};$$=$6 } ;
select_core: SELECT select_projection optional_from where group having { $2.From=$3;$2.Where=$4;$2.GroupBy=$5;$2.Having=$6;$2.Pos=$1.pos;$$=$2 }
 | SELECT optional_from where group having %prec NAME_LIST { $$=&Select{From:$2,Where:$3,GroupBy:$4,Having:$5,Pos:$1.pos} } ;
/* Absorb consecutive parentheses before selecting the scalar, FROM or set-arm
 * category; these category reductions stay below ')' shift precedence. */
select_with_parens: '(' query_select ')' { $$=parenthesizedQuery{Query:$2,Pos:$1.pos} }
 | '(' select_with_parens ')' { $2.Pos=$1.pos;$$=$2 } ;
select_operand: select_core { $$=$1 } | select_with_parens %prec NAME_LIST { $1.Query.Parenthesized=true;$$=$1.Query } ;
set_modifier: /* empty */ { $$=false } | ALL { $$=true } | DISTINCT { $$=false } ;
query_set: query_intersection { $$=$1 }
 | query_set UNION set_modifier query_intersection { $$=&Select{Set:&SetQuery{Left:$1,Right:$4,Operator:$2.text,All:$3,Pos:$2.pos},Pos:$1.Pos} }
 | query_set EXCEPT set_modifier query_intersection { $$=&Select{Set:&SetQuery{Left:$1,Right:$4,Operator:$2.text,All:$3,Pos:$2.pos},Pos:$1.Pos} } ;
query_intersection: select_operand { $$=$1 }
 | query_intersection INTERSECT set_modifier select_operand { $$=&Select{Set:&SetQuery{Left:$1,Right:$4,Operator:$2.text,All:$3,Pos:$2.pos},Pos:$1.Pos} } ;
query_select: query_set order query_tail { if $2!=nil {$1.Order=$2};if $3.Bounds!=nil {$1.Bounds=$3.Bounds};if $3.Locking!=nil {$1.Locking=$3.Locking};$$=$1 } ;
select_query: query_select { deferDistinctOnBoundary(sqllex.(*lexer),$1);$$=$1 }
 | SELECT projection INTO { sqllex.(*lexer).failAt("0A000","SELECT INTO is unsupported",$3.pos.Offset); return 1 }
 | query_set order suffix { if $3.keyword!=nil && $3.keyword.token==ON {if _,table:=$1.From.(*TableReference);!table {sqllex.(*lexer).syntax($3);return 1}};sqllex.(*lexer).capability($3,unsupportedSuffixes,"SELECT suffix is unsupported"); return 1 }
 | query_set order RETURNING { sqllex.(*lexer).failAt("0A000","SELECT suffix is unsupported",$3.pos.Offset); return 1 }
 | SELECT SETTING_FUNC '(' call_args ')' { var ok bool; $$,ok=settingCall(sqllex,$2,$4); if !ok { return 1 } }
 | SELECT name '.' SETTING_FUNC '(' call_args ')' { var ok bool; $$,ok=settingCall(sqllex,$4,$6); if !ok { return 1 };$$.(*SettingCall).Schema=&Identifier{Text:$2.text,Pos:$2.pos};$$.(*SettingCall).Pos=$2.pos } ;
select: select_query { $$=$1 }
 | query_values locking { $$=&Select{Values:$1,Locking:$2,Pos:$1.Pos} }
 | parenthesized_values locking { $$=&Select{Values:$1,Locking:$2,Pos:$1.Pos} } ;
optional_from: /* empty */ %prec NAME_LIST { $$=nil } | FROM from_list { $$=$2 } ;
row_fields: scalar_projection { $$=[]Expr{$1} } | row_fields ',' scalar_projection { $$=append($1,$3) } ;
quantifier: ANY | SOME | ALL ;
call_args: /* empty */ { $$=nil } | values { $$=$1 } ;
sequence_args: /* empty */ { $$=nil } | sequence_values { $$=$1 } ;
sequence_values: operand { $$=[]Expr{$1} }
 | sequence_values ',' operand { $$=append($1,$3) } ;
operand: scalar %prec NAME_LIST { $$=$1 } ;
comparison_operator: '=' | '<' | '>' | LESS_EQUAL | GREATER_EQUAL | NOT_EQUAL ;
qualified_operator: OPERATOR '(' name '.' operator_symbol ')' { $$=ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$1.pos},{Text:$5.text,Pos:$5.pos}}} } ;
operator_symbol: '+' | '-' | '*' | '/' | '%' | '^' | SYMBOLIC_OPERATOR | comparison_operator ;
/* CREATE DEFAULT follows restricted b_expr; parenthesized targets retain the
 * ordinary predicate/logical surface without changing SELECT admission. */
default_target: target_base %prec NAME_LIST { $$=$1 }
 | function %prec NAME_LIST { sqllex.(*lexer).failAt("0A000","functions are unsupported",$1.SourcePosition().Offset);return 1 }
 | target_base comparison_operator target_base { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | default_target IS DISTINCT FROM default_target %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | default_target IS NOT DISTINCT FROM default_target %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
/* On ')', keep a grouped scalar in the operand layer so arithmetic may follow.
 * Comparisons/predicates/logical forms instead use target_or grouping. Both
 * layers erase grouping; this explicit shift preserves their existing ASTs
 * without admitting Boolean targets as arithmetic operands. */
target_base: operand %prec NAME_LIST { $$=$1 }
 | '(' target_or ')' { $$=groupedQualification($2) } ;
target_between: target_base %prec IS { $$=$1 }
 | target_between_predicate { $$=$1 }
 | target_base like_operator target_base target_escape %prec BETWEEN { $$=likeExpr($1,$3,$4,$2,false,$2.pos) }
 | target_base NOT like_operator target_base target_escape %prec BETWEEN { $$=likeExpr($1,$4,$5,$3,true,$2.pos) } ;
target_between_predicate: target_base BETWEEN default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$3,$5,$2.pos,false,false) }
 | target_base NOT BETWEEN default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,true,false) }
 | target_base BETWEEN SYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,false,true) }
 | target_base NOT BETWEEN SYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$5,$7,$2.pos,true,true) } ;
/* Comparison operands may be grouped predicates; the nonrecursive layer keeps
 * ungrouped comparison chains nonassociative. Arithmetic remains scalar-only. */
target_comparison: target_between %prec NAME_LIST { $$=$1 }
 | target_between comparison_operator target_between { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | target_between IN select_with_parens %prec NAME_LIST { $$=quantifiedQuery($1,ObjectName{Parts:[]Identifier{{Text:"=",Pos:$2.pos}}},SubqueryAny,$3.Query,false,$2.pos) }
 | target_between NOT_IN_LA IN select_with_parens %prec NAME_LIST { $$=quantifiedQuery($1,ObjectName{Parts:[]Identifier{{Text:"=",Pos:$2.pos}}},SubqueryAny,$4.Query,true,$2.pos) }
 | target_between comparison_operator quantifier select_with_parens %prec NAME_LIST {kind:=SubqueryAny;if $3.text=="all" {kind=SubqueryAll};$$=quantifiedQuery($1,ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},kind,$4.Query,false,$2.pos)} ;
target: target_comparison { $$=$1 }
 | target IS NULL %prec IS { $$=PredicateExpr{Kind:IsNull,Left:$1,Pos:$2.pos} }
 | target IS NOT NULL %prec IS { $$=PredicateExpr{Kind:IsNotNull,Left:$1,Pos:$2.pos} }
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
scalar_projection: target_or { $$=$1 } | function { $$=$1 } ;
projection_target: scalar_projection %prec NAME_LIST { $$=$1 }
 | '*' { $$=StarExpr{Pos:$1.pos} }
 | name '.' '*' { $$=StarExpr{Qualifier:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos,QualificationPos:$2.pos} }
 | name '.' col_label '.' '*' { $$=StarExpr{Qualifier:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos,QualificationPos:$2.pos} } ;
/* Selected calls remain standalone projection roots, not Boolean/comparison
 * leaves. Their arguments remain ordinary operands or nested selected calls. */
function_argument: operand { $$=$1 } | function { $$=$1 } ;
function: SELECTED_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' SELECTED_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos} } ;
function_args: /* empty */ { $$=FunctionExpr{} }
 | function_argument_list { $$=$1 }
 | VARIADIC function_argument { $$=FunctionExpr{Args:[]Expr{$2},Variadic:true} }
 | function_argument_list ',' VARIADIC function_argument { $$=appendFunctionArgument($1,$4,Identifier{});$$.Variadic=true } ;
function_argument_list: function_argument { $$=FunctionExpr{Args:[]Expr{$1}} }
 | name EQUALS_GREATER function_argument { $$=appendFunctionArgument(FunctionExpr{},$3,Identifier{Text:$1.text,Pos:$1.pos}) }
 | function_argument_list ',' function_argument { $$=appendFunctionArgument($1,$3,Identifier{}) }
 | function_argument_list ',' name EQUALS_GREATER function_argument { $$=appendFunctionArgument($1,$5,Identifier{Text:$3.text,Pos:$3.pos}) } ;
function_values: function_argument { $$=[]Expr{$1} } | function_values ',' function_argument { $$=append($1,$3) } ;
string_function: STRING_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,ArgNames:$3.ArgNames,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' STRING_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,ArgNames:$5.ArgNames,Variadic:$5.Variadic,Pos:$1.pos} }
 | NORMALIZE '(' scalar ')' { $$=stringBuiltin("normalize",$1.pos,[]Expr{$3}) }
 | NORMALIZE '(' scalar ',' normalization_form ')' { $$=stringBuiltin("normalize",$1.pos,[]Expr{$3,stringForm($5)}) }
 | TRIM '(' trim_direction trim_arguments ')' { $$=stringBuiltin($3.text,$1.pos,$4) } ;
normalization_form: NFC | NFD | NFKC | NFKD ;
normalized_form: /* empty */ { $$=lexeme{text:"NFC"} } | normalization_form ;
trim_direction: /* empty */ { $$=lexeme{text:"btrim"} }
 | BOTH { $$=lexeme{text:"btrim",pos:$1.pos} }
 | LEADING { $$=lexeme{text:"ltrim",pos:$1.pos} }
 | TRAILING { $$=lexeme{text:"rtrim",pos:$1.pos} } ;
trim_list: function_values { $$=$1 } ;
trim_arguments: trim_list { $$=$1 } | FROM trim_list { $$=$2 }
 | scalar FROM trim_list { $$=append($3,$1) } ;
truth_kind: TRUE { $$=IsTrue } | NOT TRUE { $$=IsNotTrue }
 | FALSE { $$=IsFalse } | NOT FALSE { $$=IsNotFalse }
 | UNKNOWN { $$=IsUnknown } | NOT UNKNOWN { $$=IsNotUnknown } ;
/* Keep the scalar COLLATE prefix shared with its expression production. A
 * following name continues the annotation; a clause boundary ends a bare label. */
projection_item: projection_target %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{}}} }
 | scalar COLLATE { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | target_between IN %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | scalar_projection bare_label { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | scalar_projection AS col_label { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$3.text,Pos:$3.pos}}} }
/* A keyword after a complete projection base can be a bare label when no
 * pattern operand follows. Factor here so predicate-starter lookahead retains
 * LIKE/ILIKE matching, without relaxing WHERE or lexical keyword identity. */
 | target_base like_operator %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} } ;
projection: projection_item { $$=$1 }
 | projection ',' projection_item { $1.Targets=append($1.Targets,$3.Targets...);$1.Labels=append($1.Labels,$3.Labels...);$$=$1 } ;
from_list: joined_table { $$=$1 }
 | from_list ',' joined_table { $$=&JoinReference{Left:$1,Right:$3,Pos:$2.pos} } ;
joined_table: from_atom { $$=$1 }
 | joined_table CROSS JOIN from_atom { $$=&JoinReference{Left:$1,Right:$4,Pos:$2.pos} }
 | joined_table inner_join JOIN from_atom ON target_or { $$=&JoinReference{Left:$1,Right:$4,Kind:$2,On:$6,Pos:$3.pos} }
 | joined_table inner_join JOIN from_atom USING '(' using_columns ')' using_alias { $$=&JoinReference{Left:$1,Right:$4,Kind:$2,Using:$7,UsingAlias:$9,Pos:$3.pos} }
 | joined_table NATURAL inner_join JOIN from_atom { $$=&JoinReference{Left:$1,Right:$5,Kind:$3,Natural:true,Pos:$2.pos} } ;
inner_join: /* empty */ { $$=JoinInner } | INNER { $$=JoinInner }
 | LEFT optional_outer { $$=JoinLeft }
 | RIGHT optional_outer { $$=JoinRight }
 | FULL optional_outer { $$=JoinFull } ;
optional_outer: /* empty */ { $$=false } | OUTER { $$=true } ;
from_base: relation_name { $$=&TableReference{Name:$1} }
 | select_with_parens %prec NAME_LIST { $$=&DerivedQuery{Query:$1.Query,Pos:$1.Pos} }
 | '(' from_list ')' {
    if alias,ok:=$2.(*AliasReference);ok {
       if _,values:=alias.Input.(*ValuesQuery);values {sqllex.(*lexer).syntax($<tok>3);return 1}
    }
    $$=$2
   }
 | parenthesized_values %prec NAME_LIST { $$=$1 } ;
from_atom: from_base { $$=$1 }
 | from_base AS from_alias alias_columns { $$=&AliasReference{Input:$1,Name:Identifier{Text:$3.text,Pos:$3.pos},Columns:$4} }
 | from_base from_alias alias_columns { $$=&AliasReference{Input:$1,Name:Identifier{Text:$2.text,Pos:$2.pos},Columns:$3} } ;
from_alias: name {if !$1.keyword.columnName() {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
alias_columns: /* empty */ { $$=nil } | '(' using_columns ')' { $$=$2 } ;
using_columns: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} }
 | using_columns ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
using_alias: /* empty */ { $$=nil } | AS from_alias { $$=&Identifier{Text:$2.text,Pos:$2.pos} } ;
where: /* empty */ { $$ = nil }
 | WHERE target_or { $$=$2 }
 | WHERE name where_predicate_suffix { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 }
 | WHERE name KW_C { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 } ;
/* GROUP/HAVING are admitted clause openers and retained suffix guards. Prefer
 * their real clauses over reducing the optional clause to reach a guard. */
group: /* empty */ %prec NAME_LIST { $$=nil } | GROUP BY group_keys { $$=$3 }
 | GROUP BY '(' ')' { sqllex.(*lexer).failAt("0A000","empty grouping sets are unsupported",$1.pos.Offset);return 1 }
 | GROUP BY DISTINCT group_keys { sqllex.(*lexer).failAt("0A000","GROUP BY DISTINCT is unsupported",$3.pos.Offset);return 1 }
 | GROUP BY ALL group_keys { sqllex.(*lexer).failAt("0A000","GROUP BY ALL is unsupported",$3.pos.Offset);return 1 }
 | GROUP BY KW_C name '(' group_keys ')' { if $3.text!="grouping" || $4.text!="sets" {sqllex.(*lexer).syntax($4);return 1};sqllex.(*lexer).failAt("0A000","grouping sets are unsupported",$3.pos.Offset);return 1 } ;
group_keys: scalar_projection { $$=[]Expr{$1} } | group_keys ',' scalar_projection { $$=append($1,$3) } ;
having: /* empty */ %prec NAME_LIST { $$=nil } | HAVING target_or { $$=$2 } ;
order: /* empty */ { $$=nil } | ORDER BY order_keys { $$=&Order{Keys:$3} } ;
aggregate_order: /* empty */ { $$=nil } | ORDER BY order_keys { $$=$3 } ;
order_keys: order_key { $$=[]OrderKey{$1} } | order_keys ',' order_key { $$=append($1,$3) } ;
order_key: scalar_projection direction nulls { $$=OrderKey{Expr:$1,Pos:$1.SourcePosition(),Desc:$2,Nulls:$3} } ;
nulls: /* empty */ { $$=NullsDefault } | nulls_keyword nulls_position { if $2.text=="first" {$$=NullsFirst} else {$$=NullsLast} } ;
nulls_keyword: NULLS_LA ;
nulls_position: KW_U { if $1.keyword==nil || ($1.text!="first" && $1.text!="last") { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
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
 | offset_clause limit_clause { $1.Count=$2.Count;$$=$1 } ;
limit_clause: LIMIT limit_value { $$=&QueryBounds{Count:$2} }
 | LIMIT limit_value ',' target_or { sqllex.(*lexer).failDiagnostic("42601","LIMIT #,# syntax is not supported","Use separate LIMIT and OFFSET clauses.",$1.pos.Offset);return 1 }
 | FETCH fetch_direction fetch_count count_rows ONLY { $$=&QueryBounds{Count:$3} }
 | FETCH fetch_direction count_rows ONLY { $$=&QueryBounds{Count:LiteralExpr{Value:Value{Kind:IntegerValue,Integer:1,Pos:$1.pos}}} } ;
limit_value: scalar_projection { $$=$1 } | ALL { $$=LiteralExpr{Value:Value{Kind:NullValue,Pos:$1.pos}} } ;
offset_clause: OFFSET scalar_projection optional_count_rows { $$=&QueryBounds{Offset:$2} } ;
fetch_direction: KW_U { if $1.text!="first" && $1.text!="next" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
count_rows: ROW { $$=$1 }
 | KW_U { if $1.text!="rows" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
optional_count_rows: /* empty */ { $$=false } | count_rows { $$=true } ;
count_values: conditional_value { $$=[]Expr{$1} } | count_values ',' conditional_value { $$=append($1,$3) } ;
/* One call grammar serves scalar and FETCH inputs. FILTER attaches to the
 * actual call, not to a parenthesized or otherwise composed scalar expression. */
aggregate_application: aggregate_call %prec ')' { $$=$1 }
 | aggregate_call FILTER '(' WHERE target_or ')' { call,ok:=$1.(AggregateExpr);if !ok {sqllex.(*lexer).failAt("0A000","aggregate modifiers are unsupported",$2.pos.Offset);return 1};call.Filter=$5;$$=call } ;
aggregate_call: cast_function target_or ')' { if ordinaryAggregate($1.text) {$$=AggregateExpr{Name:$1.text,Args:[]Expr{$2},Pos:$1.pos}} else {if !scalarCastArgument($2) {sqllex.(*lexer).failAt("42601","syntax error",$2.SourcePosition().Offset);return 1};if $1.text=="sum" {$$=AggregateExpr{Name:$1.text,Args:[]Expr{$2},Pos:$1.pos}} else {$$=CastExpr{Child:$2,TypeName:columnType($1),Syntax:FunctionCast,Callee:$1.text,Pos:$1.pos}}} }
 | qualified_call_head target_or ')' { if ordinaryAggregate($1.Name) {$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:[]Expr{$2},Pos:$1.Pos}} else {if !scalarCastArgument($2) {sqllex.(*lexer).failAt("42601","syntax error",$2.SourcePosition().Offset);return 1};if $1.Name=="sum" {$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:[]Expr{$2},Pos:$1.Pos}} else {$$=CastExpr{Child:$2,TypeName:$1,Syntax:FunctionCast,Callee:$1.Name,Pos:$1.Pos}}} }
 | cast_function target_or ',' count_values ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","function arguments are unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:append([]Expr{$2},$4...),Pos:$1.pos} }
 | qualified_call_head target_or ',' count_values ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","function arguments are unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:append([]Expr{$2},$4...),Pos:$1.Pos} }
 | cast_function function ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","function arguments are unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:[]Expr{$2},Pos:$1.pos} }
 | qualified_call_head function ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","function arguments are unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:[]Expr{$2},Pos:$1.Pos} }
 | cast_function function ',' count_values ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","function arguments are unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:append([]Expr{$2},$4...),Pos:$1.pos} }
 | qualified_call_head function ',' count_values ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","function arguments are unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:append([]Expr{$2},$4...),Pos:$1.Pos} }
 | qualified_call_head '*' ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","aggregate call is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Star:true,Pos:$1.Pos} }
 | cast_function '*' ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","aggregate call is unsupported",$1.pos.Offset);return 1}; $$=AggregateExpr{Name:$1.text,Star:true,Pos:$1.pos} }
 | cast_function ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","zero-argument call is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Pos:$1.pos} }
 | qualified_call_head ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","zero-argument call is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Pos:$1.Pos} }
 | cast_function DISTINCT count_values aggregate_order ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","aggregate DISTINCT is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:$3,Order:$4,Distinct:true,Pos:$1.pos} }
 | cast_function ALL count_values aggregate_order ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","aggregate ALL is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:$3,Order:$4,Pos:$1.pos} }
 | qualified_call_head DISTINCT count_values aggregate_order ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","aggregate DISTINCT is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:$3,Order:$4,Distinct:true,Pos:$1.Pos} }
 | qualified_call_head ALL count_values aggregate_order ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","aggregate ALL is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:$3,Order:$4,Pos:$1.Pos} }
 | cast_function target_or ORDER BY order_keys ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:[]Expr{$2},Order:$5,Pos:$1.pos} }
 | qualified_call_head target_or ORDER BY order_keys ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:[]Expr{$2},Order:$5,Pos:$1.Pos} }
 | cast_function target_or ',' count_values ORDER BY order_keys ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:append([]Expr{$2},$4...),Order:$7,Pos:$1.pos} }
 | qualified_call_head target_or ',' count_values ORDER BY order_keys ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:append([]Expr{$2},$4...),Order:$7,Pos:$1.Pos} }
 | cast_function function ORDER BY order_keys ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:[]Expr{$2},Order:$5,Pos:$1.pos} }
 | qualified_call_head function ORDER BY order_keys ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:[]Expr{$2},Order:$5,Pos:$1.Pos} }
 | cast_function function ',' count_values ORDER BY order_keys ')' { if !ordinaryAggregate($1.text) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$1.text,Args:append([]Expr{$2},$4...),Order:$7,Pos:$1.pos} }
 | qualified_call_head function ',' count_values ORDER BY order_keys ')' { if !ordinaryAggregate($1.Name) {sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.Pos.Offset);return 1};$$=AggregateExpr{Name:$1.Name,Schema:$1.Schema,Args:append([]Expr{$2},$4...),Order:$7,Pos:$1.Pos} } ;
fetch_count: scalar_atom { $$=$1 }
 | CAST '(' scalar AS type ')' { if $5.Name=="regclass" && $5.Schema==nil && len($5.Modifiers)==0 && len($5.ArrayBounds)==0 { $$=RegclassExpr{Child:$3,Pos:$1.pos} } else { $$=CastExpr{Child:$3,TypeName:$5,Syntax:StandardCast,Pos:$1.pos} } }
 | aggregate_application { $$=$1 }
 | function { $$=$1 }
 | '(' scalar_projection ')' { $$=$2 }
 | '+' NUMBER { $$=LiteralExpr{Value:numberValue($2,"")} }
 | '-' NUMBER { $$=LiteralExpr{Value:numberValue($2,"-")} } ;
sequence_name_word: SEQUENCE | INCREMENT | MINVALUE | MAXVALUE | CACHE | CYCLE | OWNED | RESTART | TEMP | UNLOGGED | LOGGED | SEQUENCE_FUNC %prec NAME_LIST
 | GENERATED | ALWAYS | IDENTITY | OVERRIDING | SYSTEM | VALUE ;
conditional_word: COALESCE | NULLIF | GREATEST | LEAST ;
string_word: STRING_FUNC | NORMALIZE | NORMALIZED | TRIM | NFC | NFD | NFKC | NFKD ;
/* Keep ROLE/LOCAL command disambiguation while preserving ordinary names. */
setting_word_without_between: IDENT | AT | DELETE | SELECTED_FUNC %prec NAME_LIST | KW_U | KW_C | INTEGER | BIGINT | TEXT | BOOLEAN | TIMESTAMP %prec NAME_LIST | WITHOUT | TIME %prec NAME_LIST | ZONE | VALUES | INSERT | UPDATE | BY | SET | SHOW | RESET | SESSION | SETTING_FUNC %prec NAME_LIST | IF | EXISTS | DROP | LOCK | ACCESS | EXCLUSIVE | MODE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | COMMENT | ADD | INHERITS | transaction_name
 | string_word
 | OF | NOWAIT | SKIP | LOCKED
 | DOMAIN | TYPE | INCLUDE | TABLESPACE | ROW %prec NAME_LIST
 | INTERVAL %prec NAME_LIST | YEAR | MONTH | DAY | HOUR | MINUTE | SECOND | UNKNOWN | OPERATOR | sequence_name_word | conditional_word
 | ESCAPE | FILTER
 | CURRENT_DATETIME_FUNC %prec NAME_LIST
 | IDENTITY_FUNC %prec NAME_LIST {if !$1.keyword.columnName() {sqllex.(*lexer).syntax($1);return 1};$$=$1} ;
setting_word: setting_word_without_between | BETWEEN ;
/* SQL-special function terminals retain their released ColumnName legality.
 * A call opener shifts on the scalar path; declaration/name contexts reduce. */
non_between_name: setting_word_without_between | ROLE | LOCAL | VIEW | INDEX | REINDEX
 | OVERLAY %prec NAME_LIST | POSITION %prec NAME_LIST | SUBSTRING %prec NAME_LIST
 | CHECK_FUNC %prec NAME_LIST { if $1.keyword!=nil && !$1.keyword.columnName() {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
name: non_between_name | BETWEEN ;
transaction_name: BEGIN | START | COMMIT | ROLLBACK | ABORT | WORK | TRANSACTION | NO | CHAIN | ISOLATION | LEVEL | READ | SAVEPOINT | RELEASE | PREPARE | PREPARED | COMMITTED | UNCOMMITTED | REPEATABLE | SERIALIZABLE ;
type_name: IDENT | DELETE | SELECTED_FUNC | KW_U | KW_T | KW_C | WITHOUT | ZONE | VALUES | INSERT | UPDATE | BY | SET | SHOW | RESET | SESSION | LOCAL | SETTING_FUNC | IF | EXISTS | DROP | ROLE | AUTHORIZATION | LOCK | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | CURRENT_SCHEMA | COMMENT | IS | ISNULL | NOTNULL | UNKNOWN | ADD | INHERITS | transaction_name | sequence_name_word | conditional_word | BETWEEN
 | string_word
 | OF | NOWAIT | SKIP | LOCKED
 | DOMAIN | TYPE | INCLUDE | TABLESPACE | ROW
 | YEAR | MONTH | DAY | HOUR | MINUTE | SECOND | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | OPERATOR | IDENTITY_FUNC | CURRENT_DATETIME_FUNC | INDEX | REINDEX | CONCURRENTLY | ILIKE | ESCAPE
 | OVERLAY | POSITION | SUBSTRING | CHECK_FUNC | FILTER ;
generic_word: generic_word_without_exists | EXISTS ;
generic_word_without_exists: generic_word_base | WITH ;
generic_word_without_with: generic_word_base | EXISTS ;
generic_word_base: IDENT | AT | INTERVAL | HOUR | MINUTE | SECOND | DAY | MONTH | YEAR | SELECTED_FUNC | IDENTITY_FUNC | CURRENT_DATETIME_FUNC | KW_U | KW_C | KW_T | KW_R | COLLATE | FOR | HAVING | TIMESTAMP | WITHOUT | TIME | ZONE | IF | ALL | LOCAL | ON | AUTHORIZATION | IN | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | CASCADE | RESTRICT | COLUMN | IS | ISNULL | NOTNULL | DISTINCT | UNKNOWN | AND | OR | ONLY | DEFERRABLE | CONSTRAINT | CHECK | conditional_word | BETWEEN | SYMMETRIC | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | USING | OPERATOR | ILIKE | ESCAPE
 | DOMAIN | TYPE | ARRAY | INCLUDE | TABLESPACE | ROW %prec NAME_LIST | ANY | SOME
 | OF | NOWAIT | SKIP | LOCKED | UNION | INTERSECT | EXCEPT
 | string_word | LEADING | TRAILING | BOTH | UNIQUE | NOT_DEFERRABLE_LA | FILTER ;
/* Predicate continuation tokens are not excluded bare-alias guards. In
 * particular NOT/BETWEEN must not compete with a real RHS BETWEEN predicate
 * after an ordinary comparison. AS labels retain the full keyword surface. */
bare_label: non_between_name { if $1.keyword!=nil && ($1.text=="filter" || $1.text=="over") {sqllex.(*lexer).failAt("0A000","aggregate modifiers are unsupported",$1.pos.Offset);return 1}; if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | COLLATE { $$=$1 }
 | COLUMN { $$=$1 }
 | KW_T { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | KW_R { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | UNIQUE { if !$1.keyword.label() {sqllex.(*lexer).syntax($1);return 1};$$=$1 }
 | BOTH | LEADING | TRAILING | PLACING
 | ANY | SOME
 | IN { $$=$1 }
 | ON { sqllex.(*lexer).syntax($1); return 1 }
 | VARIADIC | SELECT | TABLE | ASC | DESC | NULL | TRUE | FALSE | DEFAULT | CAST | ALL | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | END | ONLY | DEFERRABLE | CONSTRAINT | CHECK | DISTINCT
 | CASE | WHEN | THEN | ELSE | SYMMETRIC
 | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | USING | CURRENT_CATALOG | SYSTEM_USER | CURRENT_DATE | CURRENT_TIME | CURRENT_TIMESTAMP | LOCALTIME | LOCALTIMESTAMP | CONCURRENTLY ;
col_label: name | ANY | SOME | ARRAY | BOTH | LEADING | TRAILING | PLACING | CONCURRENTLY | IDENTITY_KEYWORD_FUNC | CURRENT_DATETIME_KEYWORD_FUNC | CURRENT_CATALOG | SYSTEM_USER | CURRENT_DATE | CURRENT_TIME | CURRENT_TIMESTAMP | LOCALTIME | LOCALTIMESTAMP | RETURNING | VARIADIC | LIKE | KW_T | KW_R | COLLATE | FOR | LIMIT | OFFSET | FETCH | HAVING | CREATE | TABLE | INTO | SELECT | FROM | WHERE | ORDER | ASC | DESC | NOT | NULL | TRUE | FALSE | TO | DEFAULT | AS | CAST | ALL | ON | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | GROUP | WITH | IN | COLUMN | IS | ISNULL | NOTNULL | DISTINCT | AND | OR | END | ONLY | DEFERRABLE | CONSTRAINT | CHECK | CASE | WHEN | THEN | ELSE | SYMMETRIC | CROSS | INNER | JOIN | NATURAL | LEFT | RIGHT | FULL | OUTER | USING | ILIKE | UNIQUE
 | UNION | INTERSECT | EXCEPT ;
suffix: predicate_suffix | GROUP | HAVING ;
predicate_suffix: where_predicate_suffix | IN ;
where_predicate_suffix: KW_T | KW_R | UNIQUE | INTO | DEFAULT | ALL | ON | WITH | USER | CONSTRAINT ;
relation_name: name { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos}}} }
 | name '.' col_label %prec NAME_LIST { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos},{Text:$3.text, Pos:$3.pos}}} }
 | name '.' error { sqllex.(*lexer).fail("42601","incomplete relation name"); return 1 }
 | name '.' col_label '.' col_label { sqllex.(*lexer).fail("0A000","three-part relation names are unsupported"); return 1 } ;
role_kind: ROLE { $$=false } | USER { $$=true } | GROUP { $$=false } ;
optional_with: /* empty */ | WITH ;
role_options: /* empty */ { $$=roleOptionState{} }
 | role_options role_option_word {
   switch fold($2.raw) {
   case "login": $$=$1.add($2,true)
   case "nologin": $$=$1.add($2,false)
   default:
    if unsupportedRoleOptions[fold($2.raw)] { sqllex.(*lexer).failAt("0A000","unsupported role option",$2.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1
   }
 } ;
/* WITH is a reserved introducer, never a role option after optional_with. */
role_option_word: generic_word_without_with | ROLE | USER | GROUP | DROP | RESET | BY | transaction_name | END ;
role_name: name {
 if $1.text=="public" || $1.text=="none" { sqllex.(*lexer).failAt("42939","role name \""+$1.text+"\" is reserved",$1.pos.Offset); return 1 }; $$=$1
 }
 | CURRENT_USER { sqllex.(*lexer).failAt("42939","CURRENT_USER cannot be used as a role name here",$1.pos.Offset); return 1 }
 | SESSION_USER { sqllex.(*lexer).failAt("42939","SESSION_USER cannot be used as a role name here",$1.pos.Offset); return 1 }
 | CURRENT_ROLE { sqllex.(*lexer).failAt("42939","CURRENT_ROLE cannot be used as a role name here",$1.pos.Offset); return 1 } ;
create_role: CREATE role_kind role_name optional_with role_options {
 login:=$2
 if $5.count>0 { login=$5.login }
 $$=&CreateRole{Name:Identifier{Text:$3.text,Pos:$3.pos},Login:login,OptionCount:$5.count,SecondOption:$5.second}
 } ;
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
 | RESET SESSION AUTHORIZATION { $$=&SessionAuthorization{Default:true} } ;
/* LOCAL is an explicit native boundary; all input still parses before execution. */
excluded_role: SET ROLE excluded_role_assignment excluded_role_target { if $4.keyword!=nil && $4.keyword.token==DEFAULT && $3.text=="" {sqllex.(*lexer).syntax($4);return 1};$$=&RoleSelection{Name:$4.text,Reset:$4.keyword!=nil && $4.keyword.token==DEFAULT,Pos:$2.pos} }
 | SET SESSION ROLE excluded_role_assignment excluded_role_target { if $5.keyword!=nil && $5.keyword.token==DEFAULT && $4.text=="" {sqllex.(*lexer).syntax($5);return 1};$$=&RoleSelection{Name:$5.text,Reset:$5.keyword!=nil && $5.keyword.token==DEFAULT,Pos:$3.pos} }
 | SET LOCAL ROLE excluded_role_assignment excluded_role_target { if $5.keyword!=nil && $5.keyword.token==DEFAULT && $4.text=="" {sqllex.(*lexer).syntax($5);return 1};sqllex.(*lexer).deferUnsupported($3,"SET LOCAL ROLE is unsupported"); $$=nil }
 | RESET ROLE { $$=&RoleSelection{Reset:true,Pos:$2.pos} } ;
excluded_role_assignment: /* empty */ { $$=lexeme{} } | TO { $$=$1 } | '=' { $$=$1 } ;
excluded_role_target: name | STRING | DEFAULT ;
schema_owner: name | CURRENT_USER | CURRENT_ROLE | SESSION_USER ;
schema_table: CREATE TABLE relation_name '(' optional_elements ')' { $$=&CreateTable{Name:$3,Elements:$5,Pos:$1.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name '(' optional_elements ')' { $$=&CreateTable{Name:$6,Elements:$8,Pos:$1.pos,IfNotExists:true} }
 | CREATE generic_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
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
%%
