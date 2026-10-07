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
 alterTarget alterTarget
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
 insertSource *InsertSource
 order *Order
 orderKey OrderKey
 orderKeys []OrderKey
 nulls NullPlacement
 desc bool
 objectName ObjectName
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
 persistence string
 caseArms []CaseArm
 caseArm CaseArm
 fromItem FromItem
 identifiers []Identifier
 identifier *Identifier
 viewOptions []ViewOption
 viewOption ViewOption
}
%token <tok> IDENT NUMBER STRING PARAM
/* Parser-private numeric continuation for DATE literals, never a keyword. */
%token <tok> DATE_MODIFIER_NUMBER
%token <tok> DOT_DOT
%token <tok> LESS_EQUAL GREATER_EQUAL NOT_EQUAL EQUALS_GREATER
%token <tok> SYMBOLIC_OPERATOR
%token <tok> OPERATOR
%token <tok> '+' '-' '*' '/' '%' '[' ':' '(' '=' '<' '>' ',' '.'
%token <tok> CREATE TABLE INSERT UPDATE INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE OR
%token <tok> HAVING
%token <tok> CROSS INNER JOIN NATURAL USING VIEW
%token <tok> DELETE
%token <tok> BETWEEN SYMMETRIC
%token <tok> INTEGER BIGINT TEXT BOOLEAN KW_U KW_C KW_T KW_R AS CAST NULLS_LA
%token <tok> TIMESTAMP WITHOUT TIME ZONE
%token <tok> SET SHOW RESET ALL SESSION TO DEFAULT LOCAL SETTING_FUNC ON
%token <tok> SELECTED_FUNC VARIADIC
%token <tok> CASE WHEN THEN ELSE COALESCE NULLIF GREATEST LEAST
%token <tok> IF EXISTS
%token <tok> ROLE USER GROUP DROP WITH AUTHORIZATION SESSION_USER CURRENT_USER CURRENT_ROLE
%token <tok> LOCK ACCESS EXCLUSIVE MODE IN
%token <tok> LIKE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CASCADE RESTRICT CURRENT_SCHEMA
%token <tok> COMMENT COLUMN IS
%token <tok> ISNULL NOTNULL DISTINCT UNKNOWN
%token <tok> CONSTRAINT ADD INHERITS
%token <tok> BEGIN START COMMIT END ROLLBACK ABORT WORK TRANSACTION AND NO CHAIN
%token <tok> ISOLATION LEVEL READ ONLY DEFERRABLE SAVEPOINT RELEASE PREPARE PREPARED
%token <tok> COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE
%token <tok> SEQUENCE INCREMENT MINVALUE MAXVALUE CACHE CYCLE OWNED RESTART TEMP UNLOGGED LOGGED SEQUENCE_FUNC
/* Contextual command continuations remain below arithmetic precedence. */
%nonassoc NAME_LIST
%nonassoc INTO KW_R CONSTRAINT COLUMN ALL DEFAULT ON USER GROUP HAVING IN AND SAVEPOINT
/* Predicate continuations reduce a completed cast type before consuming IS,
 * ISNULL or NOTNULL. Keep their shared nonassociativity below comparisons. */
%nonassoc IS ISNULL NOTNULL
%nonassoc BETWEEN NOT
%left SYMBOLIC_OPERATOR OPERATOR
%left '+' '-'
%left '*' '/' '%'
%right UNARY
/* KW_T, LIKE and WITH also start suffixes. Shift an adjacent type tail before
 * completing a single type name: known excluded families use their first type
 * location, while arbitrary tails are syntax errors at the second token. */
/* Sequence options terminate an AS type name, rather than extending an
 * arbitrary two-word type spelling. Keep their precedence below TYPE_NAME. */
%nonassoc START NO INCREMENT MINVALUE MAXVALUE CACHE CYCLE OWNED RESTART
%nonassoc TYPE_NAME
/* Postfix casts put projection labels in the follow set of type names.
 * Preserve declaration grammar's deliberate type-tail shift rather than
 * leaving those decisions as implicit shift/reduce conflicts. */
%nonassoc IDENT SELECTED_FUNC UNKNOWN INSERT UPDATE DELETE VALUES BY KW_U KW_C ZONE SET SHOW RESET SESSION LOCAL SETTING_FUNC IF EXISTS ROLE DROP AUTHORIZATION LOCK ACCESS EXCLUSIVE MODE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CASCADE RESTRICT CURRENT_SCHEMA COMMENT ADD INHERITS BEGIN COMMIT ROLLBACK ABORT WORK TRANSACTION CHAIN ISOLATION LEVEL READ RELEASE PREPARE PREPARED COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE SEQUENCE TEMP UNLOGGED LOGGED SEQUENCE_FUNC '.'
%nonassoc COALESCE NULLIF GREATEST LEAST CASE WHEN THEN ELSE
%nonassoc '('
%nonassoc KW_T CROSS INNER JOIN NATURAL LIKE WITH WITHOUT
%nonassoc '='
%left ':'
/* A parenthesized scalar stays on the scalar path; Boolean grouping uses
 * target_or only when a logical/comparison continuation requires it. */
%nonassoc ')'
%type <stmt> statement create create_table drop_table alter_drop_column alter_drop_single alter_nn_single alter_rename_single insert select set show reset create_role drop_role session_authorization identity_select excluded_role lock_table create_schema alter_schema drop_schema transaction_control
%type <stmt> delete
%type <tok> delete_alias from_alias
%type <stmt> create_view
%type <identifiers> view_columns view_names
%type <viewOptions> view_options view_option_list
%type <viewOption> view_option
%type <tok> view_option_value
%type <expr> delete_where delete_operand delete_base delete_lower delete_between delete_between_predicate delete_comparison delete_target delete_not delete_and delete_or
%type <expr> update_where scalar_projection
%type <stmt> update
%type <assignments> update_assignments
%type <assignment> update_assignment
%type <col> column
%type <elements> elements optional_elements
%type <element> element
%type <likeOptions> like_options
%type <likeOption> like_option
%type <tok> like_option_name
%type <objectNames> relation_names
%type <objectName> qualified_operator
%type <tok> operator_symbol
%type <dropMode> drop_mode
%type <typ> type temporal_type generic_precision generic_modifiers type_modifier_head date_literal_precision
%type <constraints> nullability column_constraints
%type <constraint> column_constraint
%type <notNull> table_not_null
%type <tok> nn_modifier nn_inherit
%type <tok> column_suffix
%type <tok> name non_between_name setting_word setting_word_without_between role_name role_option_word type_name bare_label col_label generic_word generic_word_without_with generic_word_without_exists generic_word_base suffix nulls_keyword nulls_position
%type <tok> predicate_suffix
%type <tok> cast_function
%type <settingName> setting_name set_name local_setting_name
%type <targets> insert_targets
%type <objectName> relation_name
%type <fromItem> from_list joined_table from_atom from_base
%type <identifiers> alias_columns using_columns
%type <identifier> using_alias
%type <desc> inner_join
%type <alterTarget> alter_target rename_target
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
%type <expr> conditional case_test case_else
%type <caseArms> case_arms
%type <caseArm> case_arm
%type <row> conditional_args
%type <tok> conditional_word
%type <tok> comparison_operator
%type <call> function_args
%type <predicate> truth_kind
%type <selectStmt> projection projection_item
%type <insertSource> insert_input
%type <selectStmt> insert_select
%type <order> order
%type <orderKeys> order_keys
%type <orderKey> order_key
%type <nulls> nulls
%type <desc> direction
%type <settingValues> setting_values
%type <settingValue> setting_value
%type <desc> role_kind
%type <roleOptions> role_options
%type <names> identity_names
%type <tok> identity_name
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
%%
script: separators optional_statements ;
separators: /* empty */ | separators ';' ;
optional_statements: /* empty */ | accepted tail ;
tail: /* empty */ | ';' separators optional_statements ;
accepted: statement { if $1 != nil { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } } ;
statement: create | create_sequence | alter_sequence | drop_sequence | drop_table | alter_drop_column | insert | update | delete | select | set | show | reset | create_role | drop_role | session_authorization | identity_select | excluded_role | lock_table | create_schema | alter_schema | drop_schema | comment | transaction_control
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
comment_excluded_word: IDENT | KW_U | KW_T | KW_R | VIEW ;
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
 | DEFERRABLE { $$=TransactionMode{Unsupported:true} }
 | NOT DEFERRABLE { $$=TransactionMode{Unsupported:true} } ;
isolation_mode: READ COMMITTED { $$=IsolationReadCommitted }
 | READ UNCOMMITTED { $$=IsolationReadUncommitted }
 | REPEATABLE READ { $$=IsolationRepeatableRead }
 | SERIALIZABLE { $$=IsolationSerializable } ;
mode_write: KW_U { if $1.text!="write" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_table: LOCK TABLE relation_name IN ACCESS EXCLUSIVE MODE { $$=&LockTable{Name:$3,Pos:$1.pos} }
 | LOCK TABLE relation_name IN ACCESS EXCLUSIVE MODE lock_nowait { $$=&LockTable{Name:$3,Pos:$1.pos,Nowait:true} }
 | LOCK TABLE relation_name { sqllex.(*lexer).deferUnsupported($1,"LOCK requires explicit ACCESS EXCLUSIVE MODE"); $$=nil }
 | LOCK TABLE relation_name lock_nowait { sqllex.(*lexer).deferUnsupported($1,"LOCK options are unsupported"); $$=nil }
 | LOCK TABLE relation_name IN excluded_lock_mode MODE optional_lock_nowait { sqllex.(*lexer).deferUnsupported($1,"LOCK mode is unsupported"); $$=nil }
 | LOCK TABLE relation_name ',' excluded_lock_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK target lists are unsupported"); $$=nil }
 | LOCK TABLE relation_name '*' lock_more_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK descendants are unsupported"); $$=nil }
 | LOCK TABLE lock_only relation_name optional_lock_star lock_more_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK ONLY is unsupported"); $$=nil }
 | LOCK excluded_lock_target lock_more_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK requires TABLE and explicit ACCESS EXCLUSIVE MODE"); $$=nil } ;
/* Excluded release forms consume complete input so malformed suffixes
 * retain their syntax diagnostics. */
excluded_lock_targets: excluded_lock_target | excluded_lock_targets ',' excluded_lock_target ;
excluded_lock_target: relation_name optional_lock_star | lock_only relation_name optional_lock_star ;
optional_lock_star: /* empty */ | '*' ;
lock_more_targets: /* empty */ | ',' excluded_lock_targets ;
lock_options: optional_lock_nowait | IN lock_any_mode MODE optional_lock_nowait ;
optional_lock_nowait: /* empty */ | lock_nowait ;
lock_any_mode: ACCESS EXCLUSIVE | excluded_lock_mode ;
excluded_lock_mode: ACCESS lock_share | lock_row lock_share | lock_row EXCLUSIVE
 | lock_share | lock_share lock_update EXCLUSIVE | lock_share lock_row EXCLUSIVE | EXCLUSIVE ;
lock_share: KW_U { if $1.text!="share" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_row: KW_C { if $1.text!="row" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_update: UPDATE ;
lock_only: ONLY ;
lock_nowait: KW_U { if $1.text!="nowait" { sqllex.(*lexer).syntax($1); return 1 } } ;
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
 | SET LOCAL SCHEMA STRING { s:=parsedSet(Identifier{Text:"search_path",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{true}},$1.pos);s.Local=true;$$=s } ;
setting_assignment: TO | '=' ;
setting_values: setting_value { $$=settingList{values:[]string{$1.text},stringValues:[]bool{$1.isString}} } | setting_values ',' setting_value { $$=$1;$$.values=append($$.values,$3.text);$$.stringValues=append($$.stringValues,$3.isString) } ;
setting_value: STRING { $$=settingAtom{text:$1.text,isString:true} } | name { $$=settingAtom{text:$1.text,isString:true} }
 | TRUE { $$=settingAtom{text:"true",isString:true} } | FALSE { $$=settingAtom{text:"false",isString:true} }
 | ON { $$=settingAtom{text:$1.text,isString:true} }
 | NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$1,false); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$2,false); if !ok { return 1 } }
 | '-' NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$2,true); if !ok { return 1 } } ;
show: SHOW setting_name { $$=&Show{Name:$2.identifier(),Pos:$1.pos} }
 | SHOW ALL { $$=&Show{All:true,Pos:$1.pos} } ;
reset: RESET set_name { $$=&Reset{Name:$2.identifier(),Pos:$1.pos} }
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
create_view: CREATE VIEW relation_name view_columns view_options AS select { query,ok:=$7.(*Select); if !ok { sqllex.(*lexer).failAt("0A000","view body requires an ordinary SELECT",$1.pos.Offset); return 1 }; $$=&CreateView{Name:$3,Columns:$4,Options:$5,Query:query,Pos:$1.pos} } ;
view_columns: /* empty */ { $$=nil } | '(' view_names ')' { $$=$2 } ;
view_names: name { $$=[]Identifier{{Text:$1.text,Pos:$1.pos}} } | view_names ',' name { $$=append($1,Identifier{Text:$3.text,Pos:$3.pos}) } ;
view_options: /* empty */ { $$=nil } | WITH '(' view_option_list ')' { $$=$3 } ;
view_option_list: view_option { $$=[]ViewOption{$1} } | view_option_list ',' view_option { $$=append($1,$3) } ;
view_option: name { $$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:"true"} } | name '=' view_option_value { $$=ViewOption{Name:Identifier{Text:$1.text,Pos:$1.pos},Value:$3.text} } ;
view_option_value: name | STRING | NUMBER | TRUE | FALSE | ON ;
create: create_table { $$=$1 }
 | create_view { $$=$1 }
 | create_table INHERITS '(' relation_names ')' { sqllex.(*lexer).deferUnsupported($2,"table inheritance is unsupported"); $$=nil }
 | CREATE TEMP TABLE { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 }
 | CREATE UNLOGGED TABLE { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 }
 | CREATE generic_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
optional_elements: /* empty */ { $$=nil } | elements { $$=$1 } ;
elements: element { $$=[]TableElement{$1} } | elements ',' element { $$=append($1,$3) } ;
element: column { c:=$1; $$=TableElement{Column:&c,Pos:c.Pos} }
 | LIKE relation_name like_options { $$=TableElement{Like:&TableLike{Name:$2,Options:$3},Pos:$1.pos} }
 | table_not_null { $$=TableElement{NotNull:$1,Pos:$1.Pos} } ;
table_not_null: NOT NULL name { $$=&NotNullConstraint{Column:Identifier{Text:$3.text,Pos:$3.pos},Pos:$1.pos} }
 | CONSTRAINT name NOT NULL name { $$=&NotNullConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Column:Identifier{Text:$5.text,Pos:$5.pos},Pos:$3.pos} }
 | table_not_null NOT nn_modifier { sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 }
 | table_not_null NO nn_inherit { sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 } ;
like_options: /* empty */ { $$=nil } | like_options like_option { $$=append($1,$2) } ;
like_option: INCLUDING like_option_name { $$=LikeOption{Name:$2.text,Including:true,Pos:$1.pos} }
 | EXCLUDING like_option_name { $$=LikeOption{Name:$2.text,Pos:$1.pos} } ;
like_option_name: name { if $1.keyword==nil || !validLikeOption($1.text) { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } | ALL ;
drop_table: DROP TABLE relation_names drop_mode { $$=&DropTable{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP TABLE IF EXISTS relation_names drop_mode { $$=&DropTable{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
relation_names: relation_name { $$=[]ObjectName{$1} } | relation_names ',' relation_name { $$=append($1,$3) } ;
drop_mode: /* empty */ { $$=DropRestrict } | RESTRICT { $$=DropRestrict } | CASCADE { $$=DropCascade } ;
alter_drop_column: alter_drop_single { $$=$1 }
 | alter_nn_single { $$=$1 }
 | alter_rename_single { $$=$1 }
 | alter_rename_single ',' { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset); return 1 }
 | alter_nn_single ',' { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset); return 1 }
 | alter_drop_single ',' { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset); return 1 }
 | ALTER TABLE alter_target alter_action { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset); return 1 } ;
alter_drop_single: ALTER TABLE alter_target DROP optional_column drop_target drop_mode { $$=&AlterTableDropColumn{Name:$3.Name,Column:$6.Name,IfExists:$6.IfExists,Mode:$7,Only:$3.Only,Pos:$1.pos} } ;
alter_nn_single: ALTER TABLE alter_target ALTER optional_column name SET NOT NULL { $$=&AlterTableNotNull{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},Action:SetNotNull,Only:$3.Only,Pos:$1.pos} }
 | ALTER TABLE alter_target ALTER optional_column name DROP NOT NULL { $$=&AlterTableNotNull{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},Action:DropNotNull,Only:$3.Only,Pos:$1.pos} }
 | ALTER TABLE alter_target ADD table_not_null { $$=&AlterTableNotNull{Name:$3.Name,Column:$5.Column,Constraint:$5.Name,Action:AddNotNull,Only:$3.Only,Pos:$1.pos} }
 | ALTER TABLE alter_target DROP CONSTRAINT drop_target drop_mode { $$=&AlterTableNotNull{Name:$3.Name,Constraint:$6.Name,Action:DropConstraint,IfExists:$6.IfExists,Mode:$7,Only:$3.Only,Pos:$1.pos} }
 | ALTER TABLE alter_target ADD COLUMN { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset); return 1 }
 | ALTER TABLE alter_target ADD name { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset); return 1 }
 | ALTER TABLE alter_target ADD CONSTRAINT name column_suffix { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset); return 1 }
 | ALTER TABLE alter_target ALTER optional_column name KW_U { if $7.text!="type" { sqllex.(*lexer).syntax($7);return 1 };sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset); return 1 } ;
alter_rename_single: ALTER TABLE rename_target RENAME TO name { $$=&AlterTableRenameTable{Name:$3.Name,NewName:Identifier{Text:$6.text,Pos:$6.pos},IfExists:$3.IfExists,Pos:$1.pos} }
 | ALTER TABLE rename_target RENAME optional_column name TO name { $$=&AlterTableRenameColumn{Name:$3.Name,Column:Identifier{Text:$6.text,Pos:$6.pos},NewName:Identifier{Text:$8.text,Pos:$8.pos},Only:$3.Only,IfExists:$3.IfExists,Pos:$1.pos} }
 | ALTER TABLE rename_target RENAME CONSTRAINT name TO name { sqllex.(*lexer).deferUnsupported($5,"RENAME CONSTRAINT is unsupported"); $$=nil } ;
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
column: name type nullability { $$ = Column{Name:$1.text, TypeName:$2, Constraints:$3, Pos:$1.pos} } ;
type: INTEGER { $$ = TypeName{Name:"integer",Pos:$1.pos} } | BIGINT { $$ = TypeName{Name:"bigint",Pos:$1.pos} } | TEXT %prec TYPE_NAME { $$ = TypeName{Name:"text",Pos:$1.pos} } | BOOLEAN { $$ = TypeName{Name:"boolean",Pos:$1.pos} }
 | type_name %prec TYPE_NAME { $$=columnType($1) }
 | temporal_type { $$=$1 }
 | type_modifier_head generic_modifiers ')' { if sqllex.(*lexer).floatPrecision { $$=$2;$$.Pos=$1.Pos;sqllex.(*lexer).floatPrecision=false } else { $$=$1;$$.Modifiers=$2.Modifiers };sqllex.(*lexer).characterPrecision=false;sqllex.(*lexer).characterGeneric=false }
 | type_name type_name %prec TYPE_NAME { if $1.keyword!=nil && $2.keyword!=nil && ($1.text=="double" && $2.text=="precision" || $1.text=="character" && $2.text=="varying") { $$=TypeName{Name:$1.text+" "+$2.text,Pos:$1.pos} } else { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 } }
 | type_name WITH { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 }
 | type_name '.' col_label { $$=TypeName{Name:$3.text,Quoted:columnType($3).Quoted,Schema:&Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos} } ;
/* Descriptor modifiers share raw constant syntax; FLOAT and temporal types retain
 * their distinct unsigned/single precision contracts. */
type_modifier_head: type_name '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2);if !ok{return 1}}
 | TEXT '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2);if !ok{return 1}}
 | type_name type_name '(' {if $1.keyword==nil || $2.keyword==nil || $1.text!="character" || $2.text!="varying" {sqllex.(*lexer).syntax($2);return 1};tok:=$1;tok.text="character varying";var ok bool;$$,ok=typeModifierHead(sqllex,tok,$3);if !ok{return 1}}
 | type_name '.' col_label '(' {tok:=$3;tok.raw="\""+$3.text+"\"";tok.keyword=nil;var ok bool;$$,ok=typeModifierHead(sqllex,tok,$4);if !ok{return 1};$$.Name=$3.text;$$.Quoted=columnType($3).Quoted;$$.Schema=&Identifier{Text:$1.text,Pos:$1.pos};$$.Pos=$1.pos} ;
generic_modifiers: generic_precision { $$=$1 }
 | generic_modifiers ',' {if !sqllex.(*lexer).multipleModifiers {sqllex.(*lexer).syntax($2);return 1}} generic_precision {$$=$1;$$.Modifiers=append($$.Modifiers,$4.Modifiers...) } ;
/* Keep keyword tokens in these productions: a temporal_keyword reduction would
 * compete with setting_word on modifier and zone continuations. */
temporal_type: TIMESTAMP %prec TYPE_NAME { $$=TypeName{Name:$1.text,Pos:$1.pos} }
 | TIMESTAMP '(' NUMBER ')' %prec TYPE_NAME {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIMESTAMP WITHOUT TIME ZONE {$$=TypeName{Name:$1.text,Pos:$1.pos}}
 | TIMESTAMP '(' NUMBER ')' WITHOUT TIME ZONE {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIMESTAMP WITH {sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset);return 1}
 | TIMESTAMP '(' NUMBER ')' WITH {if _,ok:=temporalPrecision(sqllex,$3,false);!ok{return 1};sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset);return 1}
 | TIME %prec TYPE_NAME { $$=TypeName{Name:$1.text,Pos:$1.pos} }
 | TIME '(' NUMBER ')' %prec TYPE_NAME {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIME WITHOUT TIME ZONE {$$=TypeName{Name:$1.text,Pos:$1.pos}}
 | TIME '(' NUMBER ')' WITHOUT TIME ZONE {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Name=$1.text;$$.Pos=$1.pos}
 | TIME WITH {sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset);return 1}
 | TIME '(' NUMBER ')' WITH {if _,ok:=temporalPrecision(sqllex,$3,false);!ok{return 1};sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset);return 1} ;
generic_precision: NUMBER {var ok bool;if sqllex.(*lexer).floatPrecision {$$,ok=floatType(sqllex,lexeme{text:"float"},$1)} else {$$,ok=temporalPrecision(sqllex,$1,false)};if !ok{return 1}}
 | '-' {if sqllex.(*lexer).floatPrecision || sqllex.(*lexer).characterPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$3,true);if !ok{return 1};$$.Modifiers[0].Pos=$1.pos}
 | '+' {if sqllex.(*lexer).floatPrecision || sqllex.(*lexer).characterPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Modifiers[0].UnaryPlus=true;$$.Modifiers[0].Pos=$1.pos}
 | STRING {if !sqllex.(*lexer).characterGeneric {sqllex.(*lexer).syntax($1);return 1};$$=TypeName{Modifiers:[]TypeModifier{{Raw:$1.text,Pos:$1.pos}}}}
 | name {if !sqllex.(*lexer).characterGeneric {sqllex.(*lexer).syntax($1);return 1};$$=TypeName{Modifiers:[]TypeModifier{{Raw:$1.text,Pos:$1.pos}}}} ;
nullability: /* empty */ { $$ = nil }
 | column_constraints { $$=$1 }
 | column_suffix { sqllex.(*lexer).capability($1,unsupportedSuffixes,"column constraints are unsupported"); return 1 }
 | '[' { sqllex.(*lexer).failAt("0A000","array types are unsupported",$1.pos.Offset); return 1 }
 | '(' { sqllex.(*lexer).failAt("0A000","type modifiers are unsupported",$1.pos.Offset); return 1 } ;
/* Modifiers require a preceding constraint, never an empty nullability list. */
column_constraints: column_constraint { $$=[]ColumnConstraint{$1} }
 | column_constraints column_constraint { $$=append($1,$2) }
 | column_constraints NO nn_inherit { sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 }
 | column_constraints NOT nn_modifier { sqllex.(*lexer).deferUnsupported($2,"NOT NULL modifiers are unsupported"); $$=$1 } ;
column_constraint: NULL { $$=ColumnConstraint{Pos:$1.pos} }
 | NOT NULL { $$=ColumnConstraint{NotNull:true,Pos:$1.pos} }
 | CONSTRAINT name NULL { $$=ColumnConstraint{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos} }
 | CONSTRAINT name NOT NULL { $$=ColumnConstraint{NotNull:true,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos} }
 | DEFAULT default_target { if token,_:=nonqueryQualification($2,false);token!=nil {sqllex.(*lexer).syntax(*token);return 1};$$=ColumnConstraint{Default:$2,Pos:$1.pos} }
 | CONSTRAINT name DEFAULT default_target { if token,_:=nonqueryQualification($4,false);token!=nil {sqllex.(*lexer).syntax(*token);return 1};$$=ColumnConstraint{Default:$4,Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$3.pos} }
 | CONSTRAINT name column_suffix { sqllex.(*lexer).capability($3,unsupportedSuffixes,"column constraints are unsupported"); return 1 } ;
nn_modifier: KW_U { if $1.text!="valid" && $1.text!="enforced" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
nn_inherit: KW_U { if $1.text!="inherit" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
column_suffix: LIKE | KW_T | KW_R | HAVING | INTO | ALL | ON | GROUP | WITH | USER | IN | IS | AND ;
update: UPDATE relation_name SET update_assignments update_where { $$=&Update{Name:$2,Assignments:$4,Where:$5,Pos:$1.pos} }
 | UPDATE relation_name SET update_assignments update_where suffix { sqllex.(*lexer).capability($6,unsupportedSuffixes,"UPDATE suffix is unsupported"); return 1 }
 | UPDATE relation_name SET update_assignments update_where FROM { sqllex.(*lexer).failAt("0A000","UPDATE FROM is unsupported",$6.pos.Offset);return 1 }
 | UPDATE relation_name name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$3.pos.Offset);return 1 }
 | UPDATE relation_name AS name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$4.pos.Offset);return 1 }
 | UPDATE relation_name SET '(' { sqllex.(*lexer).failAt("0A000","row assignments are unsupported",$4.pos.Offset);return 1 } ;
update_assignments: update_assignment { $$=[]UpdateAssignment{$1} } | update_assignments ',' update_assignment { $$=append($1,$3) } ;
update_assignment: name '=' scalar_projection { if token,unsupported:=nonqueryQualification($3,true);token!=nil {if unsupported {sqllex.(*lexer).failAt("0A000","qualified column references are unsupported",token.pos.Offset)} else {sqllex.(*lexer).syntax(*token)};return 1}; $$=UpdateAssignment{Target:Identifier{Text:$1.text,Pos:$1.pos},Value:$3} }
 | name '=' scalar_projection '.' { sqllex.(*lexer).failAt("0A000","qualified column references are unsupported",$4.pos.Offset);return 1 }
 | name '=' scalar_projection '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '*' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 }
 | name '=' scalar_projection '/' { sqllex.(*lexer).fail("0A000","expressions are unsupported");return 1 } ;
update_where: where { if token,_:=nonqueryQualification($1,false);token!=nil {sqllex.(*lexer).syntax(*token);return 1};$$=$1 } ;
insert: INSERT INTO relation_name insert_input { $$=insertStatement($3,nil,$4,$1.pos) }
 | INSERT INTO relation_name '(' insert_targets ')' insert_input { $$=insertStatement($3,$5,$7,$1.pos) }
 | INSERT INTO relation_name insert_input suffix { sqllex.(*lexer).capability($5,unsupportedSuffixes,"INSERT suffix is unsupported"); return 1 }
 | INSERT INTO relation_name '(' insert_targets ')' insert_input suffix { sqllex.(*lexer).capability($8,unsupportedSuffixes,"INSERT suffix is unsupported"); return 1 }
 | INSERT INTO relation_name DEFAULT VALUES { $$=&Insert{Name:$3,Rows:[][]Expr{nil},Pos:$1.pos,DefaultValues:true} } ;
delete: DELETE FROM relation_name delete_alias delete_where { if token:=deleteQualification($5,false); token!=nil {sqllex.(*lexer).syntax(*token);return 1}; $$=&Delete{Name:$3,Alias:Identifier{Text:$4.text,Pos:$4.pos},Where:$5,Pos:$1.pos} }
 | DELETE FROM relation_name delete_alias delete_where suffix { sqllex.(*lexer).capability($6,unsupportedSuffixes,"DELETE suffix is unsupported"); return 1 }
 | DELETE FROM relation_name delete_alias delete_where USING { sqllex.(*lexer).failAt("0A000","DELETE suffix is unsupported",$6.pos.Offset);return 1 } ;
delete_alias: /* empty */ { $$=lexeme{} } | name { $$=$1 } | AS name { $$=$2 } ;
delete_where: /* empty */ { $$=nil } | WHERE delete_or { $$=$2 }
 | WHERE KW_U KW_U name {
   if $2.keyword==nil || $2.text!="current" { sqllex.(*lexer).syntax($3); return 1 }
   if $3.keyword==nil || $3.text!="of" { sqllex.(*lexer).syntax($3); return 1 }
   sqllex.(*lexer).deferUnsupported($2,"DELETE CURRENT OF is unsupported"); $$=nil
 } ;
delete_operand: operand { $$=$1 } ;
delete_base: delete_operand { $$=$1 }
 | '(' delete_or ')' { $$=$2 } ;
delete_lower: delete_base { $$=$1 }
 | delete_operand comparison_operator delete_operand { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | delete_lower IS DISTINCT FROM delete_lower %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | delete_lower IS NOT DISTINCT FROM delete_lower %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
delete_between: delete_base %prec IS { $$=$1 }
 | delete_between_predicate { $$=$1 } ;
delete_between_predicate: delete_base BETWEEN delete_lower AND delete_base %prec BETWEEN { $$=betweenExpr($1,$3,$5,$2.pos,false,false) }
 | delete_base NOT BETWEEN delete_lower AND delete_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,true,false) }
 | delete_base BETWEEN SYMMETRIC delete_lower AND delete_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,false,true) }
 | delete_base NOT BETWEEN SYMMETRIC delete_lower AND delete_base %prec BETWEEN { $$=betweenExpr($1,$5,$7,$2.pos,true,true) } ;
/* General grouping belongs to BETWEEN values/endpoints, not to baseline
 * ordinary comparisons. Keep the real predicate as a separate category. */
delete_comparison: delete_between { $$=$1 }
 | delete_operand comparison_operator delete_operand { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | delete_between_predicate comparison_operator delete_between { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | delete_operand comparison_operator delete_between_predicate { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} } ;
delete_target: delete_comparison { $$=$1 }
 | delete_target IS NULL %prec IS { $$=PredicateExpr{Kind:IsNull,Left:$1,Pos:$2.pos} }
 | delete_target IS NOT NULL %prec IS { $$=PredicateExpr{Kind:IsNotNull,Left:$1,Pos:$2.pos} }
 | delete_target ISNULL { $$=PredicateExpr{Kind:IsNull,Left:$1,Pos:$2.pos} }
 | delete_target NOTNULL { $$=PredicateExpr{Kind:IsNotNull,Left:$1,Pos:$2.pos} }
 | delete_target IS truth_kind %prec IS { $$=PredicateExpr{Kind:$3,Left:$1,Pos:$2.pos} }
 | delete_target IS DISTINCT FROM delete_target %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | delete_target IS NOT DISTINCT FROM delete_target %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
delete_not: delete_target { $$=$1 } | NOT delete_not { $$=LogicalExpr{Op:LogicalNot,Left:$2,Pos:$1.pos} } ;
delete_and: delete_not { $$=$1 } | delete_and AND delete_not { $$=LogicalExpr{Op:LogicalAnd,Left:$1,Right:$3,Pos:$2.pos} } ;
delete_or: delete_and { $$=$1 } | delete_or OR delete_and { $$=LogicalExpr{Op:LogicalOr,Left:$1,Right:$3,Pos:$2.pos} } ;
insert_targets: name { $$ = []InsertTarget{{Name:$1.text, Pos:$1.pos}} } | insert_targets ',' name { $$ = append($1, InsertTarget{Name:$3.text, Pos:$3.pos}) } ;
insert_input: VALUES rows order { $$=&InsertSource{Rows:$2,Order:$3} }
 | insert_select { $$=&InsertSource{Select:$1} }
 | '(' insert_input ')' { $$=$2 } ;
insert_select: SELECT projection FROM from_list where group having order { $2.From=$4; $2.Where=$5; $2.GroupBy=$6; $2.Having=$7; $2.Order=$8; $2.Pos=$1.pos; $$=$2 }
 | SELECT projection { if !sqllex.(*lexer).nonqueryProjection($2.Targets) {return 1};$2.Pos=$1.pos; $$=$2 }
 | SELECT FROM from_list where group having order { $$=&Select{From:$3,Where:$4,GroupBy:$5,Having:$6,Order:$7,Pos:$1.pos} }
 /* Prefer a projection operand/suffix over completing the zero-width source. */
 | SELECT %prec NAME_LIST { $$=&Select{Pos:$1.pos} } ;
names: name %prec NAME_LIST { $$ = []string{$1.text} } | names ',' name %prec NAME_LIST { $$ = append($1,$3.text) } ;
rows: row { $$ = [][]Expr{$1} } | rows ',' row { $$ = append($1,$3) } ;
row: '(' insert_values ')' { $$ = $2 } ;
values: scalar { if !sqllex.(*lexer).scalarInput($1) { return 1 }; $$ = []Expr{$1} } | values ',' scalar { if !sqllex.(*lexer).scalarInput($3) { return 1 }; $$ = append($1,$3) } ;
insert_value: scalar { if !sqllex.(*lexer).scalarInput($1) { return 1 }; $$=$1 } | DEFAULT { $$=DefaultExpr{Pos:$1.pos} } ;
insert_values: insert_value { $$=[]Expr{$1} } | insert_values ',' insert_value { $$=append($1,$3) } ;
scalar: scalar_atom { $$=$1 }
 | '(' scalar ')' { $$=groupedQualification($2) }
 | CAST '(' scalar AS type ')' { if $5.Name=="regclass" && $5.Schema==nil && len($5.Modifiers)==0 { $$=RegclassExpr{Child:$3,Pos:$1.pos} } else { $$=CastExpr{Child:$3,TypeName:$5,Syntax:StandardCast,Pos:$1.pos} } }
 | scalar ':' type %prec ':' { if $2.text!="::" { sqllex.(*lexer).syntax($2); return 1 }; if $3.Name=="regclass" && $3.Schema==nil && len($3.Modifiers)==0 { $$=RegclassExpr{Child:$1,Pos:$2.pos} } else { $$=CastExpr{Child:$1,TypeName:$3,Syntax:PostfixCast,Pos:$2.pos} } }
 | cast_function scalar ')' { if $1.text=="count" {sqllex.(*lexer).failAt("0A000","count arguments are unsupported",$1.pos.Offset);return 1}; $$=CastExpr{Child:$2,TypeName:columnType($1),Syntax:FunctionCast,Callee:$1.text,Pos:$1.pos} }
 | name '.' name '(' scalar ')' { $$=CastExpr{Child:$5,TypeName:TypeName{Name:$3.text,Quoted:columnType($3).Quoted,Schema:&Identifier{Text:$1.text,Pos:$1.pos},Pos:$1.pos},Syntax:FunctionCast,Callee:$3.text,Pos:$1.pos} }
 | name '.' name '(' '*' ')' { if $3.text!="count" {sqllex.(*lexer).failAt("0A000","aggregate call is unsupported",$1.pos.Offset);return 1};$$=AggregateExpr{Name:$3.text,Schema:&Identifier{Text:$1.text,Pos:$1.pos},Star:true,Pos:$1.pos} }
 | cast_function '*' ')' { if $1.text!="count" {sqllex.(*lexer).failAt("0A000","aggregate call is unsupported",$1.pos.Offset);return 1}; $$=AggregateExpr{Name:$1.text,Star:true,Pos:$1.pos} }
 | cast_function ')' { sqllex.(*lexer).failAt("0A000","zero-argument call is unsupported",$1.pos.Offset);return 1 }
 | cast_function DISTINCT scalar ')' { sqllex.(*lexer).failAt("0A000","aggregate DISTINCT is unsupported",$1.pos.Offset);return 1 }
 | cast_function '*' ORDER BY name direction ')' { sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.pos.Offset);return 1 }
 | cast_function scalar ORDER BY name direction ')' { sqllex.(*lexer).failAt("0A000","aggregate ordering is unsupported",$1.pos.Offset);return 1 }
 /* Share the call opener, but retain raw DATE modifiers instead of reducing
  * them as scalar literals (which would convert overflowing numbers too early). */
 | cast_function date_literal_precision ')' STRING {raw:=columnType($1);raw.Modifiers=$2.Modifiers;$$=TypedLiteralExpr{TypeName:raw,Text:$4.text,Pos:$4.pos}}
 | '+' scalar %prec UNARY { $$=arithmeticExpr($1,$2,nil) }
 | '-' scalar %prec UNARY { $$=negateExpr(sqllex,$1,$2) }
 | SYMBOLIC_OPERATOR scalar %prec SYMBOLIC_OPERATOR { $$=arithmeticExpr($1,$2,nil) }
 | scalar '+' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '-' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '*' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '/' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar '%' scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar SYMBOLIC_OPERATOR scalar { $$=arithmeticExpr($2,$1,$3) }
 | scalar qualified_operator scalar %prec OPERATOR { $$=OperatorExpr{Name:$2,Args:[]Expr{$1,$3},Pos:$2.Parts[0].Pos} }
 | qualified_operator scalar %prec OPERATOR { $$=OperatorExpr{Name:$1,Args:[]Expr{$2},Pos:$1.Parts[0].Pos} } ;
scalar_atom: value %prec NAME_LIST { $$=LiteralExpr{Value:$1} }
 | PARAM { $$=ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos} }
 | temporal_type STRING { $$=TypedLiteralExpr{TypeName:$1,Text:$2.text,Pos:$2.pos} }
 /* DATE is an IDENT (also when quoted). TIME/TIMESTAMP STRING continuations
  * belong exclusively to temporal_type, not the ordinary name reduction. */
 | IDENT STRING { if $1.text!="date" {sqllex.(*lexer).failAt("0A000","typed literals are unsupported",$1.pos.Offset);return 1};$$=TypedLiteralExpr{TypeName:columnType($1),Text:$2.text,Pos:$2.pos} }
 | name %prec NAME_LIST { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos} }
 | name '.' col_label { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos,QualificationPos:$2.pos} }
 | name '.' col_label '.' col_label { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}},Pos:$1.pos,QualificationPos:$2.pos,ExtraQualificationPos:$4.pos} }
 | conditional { $$=$1 }
 | SEQUENCE_FUNC '(' sequence_args ')' { $$=CallExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3} }
 | name '.' SEQUENCE_FUNC '(' sequence_args ')' { $$=CallExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5} } ;
conditional: CASE case_test case_arms case_else END { $$=CaseExpr{Test:$2,Arms:$3,Else:$4,Pos:$1.pos} }
 | COALESCE '(' conditional_args ')' { $$=ConditionalExpr{Kind:Coalesce,Args:$3,Pos:$1.pos} }
 | GREATEST '(' conditional_args ')' { $$=ConditionalExpr{Kind:Greatest,Args:$3,Pos:$1.pos} }
 | LEAST '(' conditional_args ')' { $$=ConditionalExpr{Kind:Least,Args:$3,Pos:$1.pos} }
 | NULLIF '(' delete_or ',' delete_or ')' { $$=ConditionalExpr{Kind:NullIf,Args:[]Expr{$3,$5},Pos:$1.pos} } ;
case_test: /* empty */ { $$=nil } | delete_or { $$=$1 } ;
case_arms: case_arm { $$=[]CaseArm{$1} } | case_arms case_arm { $$=append($1,$2) } ;
case_arm: WHEN delete_or THEN delete_or { $$=CaseArm{Condition:$2,Result:$4,Pos:$1.pos} } ;
case_else: /* empty */ { $$=nil } | ELSE delete_or { $$=$2 } ;
conditional_args: delete_or { $$=[]Expr{$1} } | conditional_args ',' delete_or { $$=append($1,$3) } ;
cast_function: name '(' { if !sqllex.(*lexer).castFunction($1,$2) { return 1 }; $$=$1 } ;
date_literal_precision: DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$1,false);if !ok{return 1}}
 | '-' DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$2,true);if !ok{return 1};$$.Modifiers[0].Pos=$1.pos}
 | '+' DATE_MODIFIER_NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$2,false);if !ok{return 1};$$.Modifiers[0].UnaryPlus=true;$$.Modifiers[0].Pos=$1.pos} ;
value: NULL { $$ = Value{Kind:NullValue, Pos:$1.pos} }
 | TRUE { $$ = Value{Kind:BooleanValue, Boolean:true, Pos:$1.pos} }
 | FALSE { $$ = Value{Kind:BooleanValue, Pos:$1.pos} }
 | STRING { $$ = Value{Kind:StringValue, Text:$1.text, Pos:$1.pos} }
 | NUMBER { var ok bool; $$, ok = integerValue(sqllex,$1,""); if !ok { return 1 } }
 | KW_T '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | CROSS '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | INNER '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | JOIN '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | NATURAL '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | IS '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | LIKE '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | AUTHORIZATION '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | ISNULL '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | NOTNULL '(' { sqllex.(*lexer).call($1,$2); return 1 } ;
/* Newly admitted FROM trees diagnose a misplaced ON as syntax. Preserve the
 * legacy single-table suffix capability boundary for otherwise excluded SQL. */
select: SELECT projection FROM from_list where group having order { $2.From=$4; $2.Where=$5; $2.GroupBy=$6; $2.Having=$7; $2.Order=$8; $2.Pos=$1.pos; $$=$2 }
 | SELECT FROM from_list where group having order { $$=&Select{From:$3,Where:$4,GroupBy:$5,Having:$6,Order:$7,Pos:$1.pos} }
 | SELECT projection { if !sqllex.(*lexer).nonqueryProjection($2.Targets) {return 1};$2.Pos=$1.pos; if $2.IsSingleton() { $$=$2 } else { sqllex.(*lexer).deferUnsupported($1,"SELECT without FROM is unsupported"); $$=nil } }
 | SELECT projection INTO { sqllex.(*lexer).failAt("0A000","SELECT INTO is unsupported",$3.pos.Offset); return 1 }
 | SELECT projection FROM from_list where group having order suffix { if $9.keyword!=nil && $9.keyword.token==ON {if _,table:=$4.(*TableReference);!table {sqllex.(*lexer).syntax($9);return 1}};sqllex.(*lexer).capability($9,unsupportedSuffixes,"SELECT suffix is unsupported"); return 1 }
 | SELECT SETTING_FUNC '(' call_args ')' { var ok bool; $$,ok=settingCall(sqllex,$2,$4); if !ok { return 1 } }
 | SELECT name '.' SETTING_FUNC '(' call_args ')' { var ok bool; $$,ok=settingCall(sqllex,$4,$6); if !ok { return 1 };$$.(*SettingCall).Schema=&Identifier{Text:$2.text,Pos:$2.pos};$$.(*SettingCall).Pos=$2.pos } ;
call_args: /* empty */ { $$=nil } | values { $$=$1 } ;
sequence_args: /* empty */ { $$=nil } | sequence_values { $$=$1 } ;
sequence_values: operand { $$=[]Expr{$1} }
 | sequence_values ',' operand { $$=append($1,$3) } ;
operand: scalar %prec NAME_LIST { $$=$1 } ;
comparison_operator: '=' | '<' | '>' | LESS_EQUAL | GREATER_EQUAL | NOT_EQUAL ;
qualified_operator: OPERATOR '(' name '.' operator_symbol ')' { $$=ObjectName{Parts:[]Identifier{{Text:$3.text,Pos:$1.pos},{Text:$5.text,Pos:$5.pos}}} } ;
operator_symbol: '+' | '-' | '*' | '/' | '%' | SYMBOLIC_OPERATOR | comparison_operator ;
/* CREATE DEFAULT follows restricted b_expr; parenthesized targets retain the
 * ordinary predicate/logical surface without changing SELECT admission. */
default_target: target_base %prec NAME_LIST { $$=$1 }
 | operand comparison_operator operand { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | default_target IS DISTINCT FROM default_target %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | default_target IS NOT DISTINCT FROM default_target %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
/* On ')', keep a grouped scalar in the operand layer so arithmetic may follow.
 * Comparisons/predicates/logical forms instead use target_or grouping. Both
 * layers erase grouping; this explicit shift preserves their existing ASTs
 * without admitting Boolean targets as arithmetic operands. */
target_base: operand %prec NAME_LIST { $$=$1 }
 | '(' target_or ')' { $$=groupedQualification($2) } ;
target_between: target_base %prec IS { $$=$1 }
 | target_between_predicate { $$=$1 } ;
target_between_predicate: target_base BETWEEN default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$3,$5,$2.pos,false,false) }
 | target_base NOT BETWEEN default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,true,false) }
 | target_base BETWEEN SYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$4,$6,$2.pos,false,true) }
 | target_base NOT BETWEEN SYMMETRIC default_target AND target_base %prec BETWEEN { $$=betweenExpr($1,$5,$7,$2.pos,true,true) } ;
/* Preserve scalar-only ordinary comparison operands. A real BETWEEN predicate
 * can participate in the release's lower-precedence comparison layer without
 * admitting standalone comparisons of arbitrary grouped logical values. */
target_comparison: target_between { $$=$1 }
 | operand comparison_operator operand { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | target_between_predicate comparison_operator target_between { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | operand comparison_operator target_between_predicate { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} } ;
target: target_comparison { $$=$1 }
 | target IS NULL %prec IS { $$=PredicateExpr{Kind:IsNull,Left:$1,Pos:$2.pos} }
 | target IS NOT NULL %prec IS { $$=PredicateExpr{Kind:IsNotNull,Left:$1,Pos:$2.pos} }
 | target ISNULL { $$=PredicateExpr{Kind:IsNull,Left:$1,Pos:$2.pos} }
 | target NOTNULL { $$=PredicateExpr{Kind:IsNotNull,Left:$1,Pos:$2.pos} }
 | target IS truth_kind %prec IS { $$=PredicateExpr{Kind:$3,Left:$1,Pos:$2.pos} }
 | target IS DISTINCT FROM target %prec IS { $$=PredicateExpr{Kind:IsDistinct,Left:$1,Right:$5,Pos:$2.pos} }
 | target IS NOT DISTINCT FROM target %prec IS { $$=PredicateExpr{Kind:IsNotDistinct,Left:$1,Right:$6,Pos:$2.pos} } ;
target_not: target { $$=$1 }
 | NOT target_not { $$=LogicalExpr{Op:LogicalNot,Left:$2,Pos:$1.pos} } ;
target_and: target_not { $$=$1 }
 | target_and AND target_not { $$=LogicalExpr{Op:LogicalAnd,Left:$1,Right:$3,Pos:$2.pos} } ;
target_or: target_and { $$=$1 }
 | target_or OR target_and { $$=LogicalExpr{Op:LogicalOr,Left:$1,Right:$3,Pos:$2.pos} } ;
scalar_projection: target_or { $$=$1 } | function { $$=$1 } ;
projection_target: scalar_projection { $$=$1 }
 | '*' { $$=StarExpr{Pos:$1.pos} }
 | name '.' '*' { $$=StarExpr{Qualifier:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos,QualificationPos:$2.pos} }
 | name '.' col_label '.' '*' { $$=StarExpr{Qualifier:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Pos:$1.pos,QualificationPos:$2.pos} } ;
/* Selected calls remain standalone projection roots, not Boolean/comparison
 * leaves. Their arguments remain ordinary operands or nested selected calls. */
function_argument: operand { $$=$1 } | function { $$=$1 } ;
function: SELECTED_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Args:$3.Args,Variadic:$3.Variadic,Pos:$1.pos} }
 | name '.' SELECTED_FUNC '(' function_args ')' { $$=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}},Args:$5.Args,Variadic:$5.Variadic,Pos:$1.pos} } ;
function_args: /* empty */ { $$=FunctionExpr{} }
 | function_values { $$=FunctionExpr{Args:$1} }
 | VARIADIC function_argument { $$=FunctionExpr{Args:[]Expr{$2},Variadic:true} }
 | function_values ',' VARIADIC function_argument { $$=FunctionExpr{Args:append($1,$4),Variadic:true} } ;
function_values: function_argument { $$=[]Expr{$1} } | function_values ',' function_argument { $$=append($1,$3) } ;
truth_kind: TRUE { $$=IsTrue } | NOT TRUE { $$=IsNotTrue }
 | FALSE { $$=IsFalse } | NOT FALSE { $$=IsNotFalse }
 | UNKNOWN { $$=IsUnknown } | NOT UNKNOWN { $$=IsNotUnknown } ;
projection_item: projection_target %prec NAME_LIST { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{}}} }
 | scalar_projection bare_label { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$2.text,Pos:$2.pos}}} }
 | scalar_projection AS col_label { $$=&Select{Targets:[]Expr{$1},Labels:[]Identifier{{Text:$3.text,Pos:$3.pos}}} } ;
projection: projection_item { $$=$1 }
 | projection ',' projection_item { $1.Targets=append($1.Targets,$3.Targets...);$1.Labels=append($1.Labels,$3.Labels...);$$=$1 } ;
from_list: joined_table { $$=$1 }
 | from_list ',' joined_table { $$=&JoinReference{Left:$1,Right:$3,Pos:$2.pos} } ;
joined_table: from_atom { $$=$1 }
 | joined_table CROSS JOIN from_atom { $$=&JoinReference{Left:$1,Right:$4,Pos:$2.pos} }
 | joined_table inner_join JOIN from_atom ON target_or { $$=&JoinReference{Left:$1,Right:$4,On:$6,Pos:$3.pos} }
 | joined_table inner_join JOIN from_atom USING '(' using_columns ')' using_alias { $$=&JoinReference{Left:$1,Right:$4,Using:$7,UsingAlias:$9,Pos:$3.pos} }
 | joined_table NATURAL inner_join JOIN from_atom { $$=&JoinReference{Left:$1,Right:$5,Natural:true,Pos:$2.pos} } ;
inner_join: /* empty */ { $$=false } | INNER { $$=true } ;
from_base: relation_name { $$=&TableReference{Name:$1} }
 | '(' from_list ')' { $$=$2 } ;
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
 | WHERE name predicate_suffix { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 }
 | WHERE name KW_C { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 } ;
/* GROUP/HAVING are admitted clause openers and retained suffix guards. Prefer
 * their real clauses over reducing the optional clause to reach a guard. */
group: /* empty */ %prec NAME_LIST { $$=nil } | GROUP BY group_keys { $$=$3 }
 | GROUP BY '(' ')' { sqllex.(*lexer).failAt("0A000","empty grouping sets are unsupported",$1.pos.Offset);return 1 }
 | GROUP BY DISTINCT group_keys { sqllex.(*lexer).failAt("0A000","GROUP BY DISTINCT is unsupported",$3.pos.Offset);return 1 }
 | GROUP BY ALL group_keys { sqllex.(*lexer).failAt("0A000","GROUP BY ALL is unsupported",$3.pos.Offset);return 1 }
 | GROUP BY KW_C name '(' group_keys ')' { if $3.text!="grouping" || $4.text!="sets" {sqllex.(*lexer).syntax($4);return 1};sqllex.(*lexer).failAt("0A000","grouping sets are unsupported",$3.pos.Offset);return 1 } ;
group_keys: target_or { $$=[]Expr{$1} } | group_keys ',' target_or { $$=append($1,$3) } ;
having: /* empty */ %prec NAME_LIST { $$=nil } | HAVING target_or { $$=$2 } ;
order: /* empty */ { $$=nil } | ORDER BY order_keys { $$=&Order{Keys:$3} } ;
order_keys: order_key { $$=[]OrderKey{$1} } | order_keys ',' order_key { $$=append($1,$3) } ;
order_key: scalar_projection direction nulls { $$=OrderKey{Expr:$1,Pos:$1.SourcePosition(),Desc:$2,Nulls:$3} } ;
nulls: /* empty */ { $$=NullsDefault } | nulls_keyword nulls_position { if $2.text=="first" {$$=NullsFirst} else {$$=NullsLast} } ;
nulls_keyword: NULLS_LA ;
nulls_position: KW_U { if $1.keyword==nil || ($1.text!="first" && $1.text!="last") { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
direction: /* empty */ { $$ = false } | ASC { $$ = false } | DESC { $$ = true } ;
sequence_name_word: SEQUENCE | INCREMENT | MINVALUE | MAXVALUE | CACHE | CYCLE | OWNED | RESTART | TEMP | UNLOGGED | LOGGED | SEQUENCE_FUNC %prec NAME_LIST ;
conditional_word: COALESCE | NULLIF | GREATEST | LEAST ;
/* Keep ROLE/LOCAL command disambiguation while preserving ordinary names. */
setting_word_without_between: IDENT | DELETE | SELECTED_FUNC %prec NAME_LIST | KW_U | KW_C | INTEGER | BIGINT | TEXT | BOOLEAN | TIMESTAMP %prec NAME_LIST | WITHOUT | TIME %prec NAME_LIST | ZONE | VALUES | INSERT | UPDATE | BY | SET | SHOW | RESET | SESSION | SETTING_FUNC %prec NAME_LIST | IF | EXISTS | DROP | LOCK | ACCESS | EXCLUSIVE | MODE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | COMMENT | ADD | INHERITS | transaction_name
 | UNKNOWN | sequence_name_word | conditional_word ;
setting_word: setting_word_without_between | BETWEEN ;
non_between_name: setting_word_without_between | ROLE | LOCAL | VIEW ;
name: non_between_name | BETWEEN ;
transaction_name: BEGIN | START | COMMIT | ROLLBACK | ABORT | WORK | TRANSACTION | NO | CHAIN | ISOLATION | LEVEL | READ | SAVEPOINT | RELEASE | PREPARE | PREPARED | COMMITTED | UNCOMMITTED | REPEATABLE | SERIALIZABLE ;
type_name: IDENT | DELETE | SELECTED_FUNC | KW_U | KW_T | KW_C | WITHOUT | ZONE | VALUES | INSERT | UPDATE | BY | SET | SHOW | RESET | SESSION | LOCAL | SETTING_FUNC | IF | EXISTS | DROP | ROLE | AUTHORIZATION | LOCK | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | CURRENT_SCHEMA | COMMENT | IS | ISNULL | NOTNULL | UNKNOWN | ADD | INHERITS | transaction_name | sequence_name_word | conditional_word | BETWEEN
 | CROSS | INNER | JOIN | NATURAL ;
generic_word: generic_word_without_exists | EXISTS ;
generic_word_without_exists: generic_word_base | WITH ;
generic_word_without_with: generic_word_base | EXISTS ;
generic_word_base: IDENT | SELECTED_FUNC | KW_U | KW_C | KW_T | KW_R | HAVING | TIMESTAMP | WITHOUT | TIME | ZONE | IF | ALL | LOCAL | ON | AUTHORIZATION | IN | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | CASCADE | RESTRICT | COLUMN | IS | ISNULL | NOTNULL | DISTINCT | UNKNOWN | AND | OR | ONLY | DEFERRABLE | CONSTRAINT | conditional_word | BETWEEN | SYMMETRIC | CROSS | INNER | JOIN | NATURAL | USING ;
/* Predicate continuation tokens are not excluded bare-alias guards. In
 * particular NOT/BETWEEN must not compete with a real RHS BETWEEN predicate
 * after an ordinary comparison. AS labels retain the full keyword surface. */
bare_label: non_between_name { if $1.keyword!=nil && ($1.text=="filter" || $1.text=="over") {sqllex.(*lexer).failAt("0A000","aggregate modifiers are unsupported",$1.pos.Offset);return 1}; if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | LIKE { $$=$1 }
 | COLUMN { $$=$1 }
 | KW_T { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | KW_R { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | IN { $$=$1 }
 | ON { sqllex.(*lexer).syntax($1); return 1 }
 | VARIADIC | SELECT | TABLE | ASC | DESC | NULL | TRUE | FALSE | DEFAULT | CAST | ALL | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | END | ONLY | DEFERRABLE | CONSTRAINT | DISTINCT
 | CASE | WHEN | THEN | ELSE | SYMMETRIC
 | CROSS | INNER | JOIN | NATURAL | USING ;
col_label: name | VARIADIC | LIKE | KW_T | KW_R | HAVING | CREATE | TABLE | INTO | SELECT | FROM | WHERE | ORDER | ASC | DESC | NOT | NULL | TRUE | FALSE | TO | DEFAULT | AS | CAST | ALL | ON | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | GROUP | WITH | IN | COLUMN | IS | ISNULL | NOTNULL | DISTINCT | AND | OR | END | ONLY | DEFERRABLE | CONSTRAINT | CASE | WHEN | THEN | ELSE | SYMMETRIC | CROSS | INNER | JOIN | NATURAL | USING ;
suffix: predicate_suffix | GROUP | HAVING ;
predicate_suffix: LIKE | KW_T | KW_R | INTO | DEFAULT | ALL | ON | WITH | USER | IN | CONSTRAINT ;
relation_name: name { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos}}} }
 | name '.' col_label { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos},{Text:$3.text, Pos:$3.pos}}} }
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
/* Recognize adjacent role commands only to reject them before creating an AST.
 * ROLE remains nonreserved in ordinary identifier positions. */
excluded_role: SET ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$2.pos.Offset); return 1 }
 | SET SESSION ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$3.pos.Offset); return 1 }
 | SET LOCAL ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$3.pos.Offset); return 1 }
 | RESET ROLE { sqllex.(*lexer).failAt("0A000","RESET ROLE is unsupported",$2.pos.Offset); return 1 } ;
excluded_role_assignment: /* empty */ | setting_assignment ;
excluded_role_target: name | STRING | DEFAULT ;
identity_name: SESSION_USER | CURRENT_USER | CURRENT_SCHEMA | CURRENT_SCHEMA '(' ')' { $$=$1 } ;
identity_names: identity_name { $$=[]string{$1.text} } | identity_names ',' identity_name { $$=append($1,$3.text) } ;
identity_select: SELECT identity_names { $$=&IdentitySelect{Names:$2} }
 | SELECT name '.' CURRENT_SCHEMA '(' ')' { $$=&IdentitySelect{Names:[]string{"current_schema"},Schema:&Identifier{Text:$2.text,Pos:$2.pos},Pos:$2.pos} } ;
schema_owner: name | CURRENT_USER | CURRENT_ROLE | SESSION_USER ;
schema_table: CREATE TABLE relation_name '(' optional_elements ')' { $$=&CreateTable{Name:$3,Elements:$5,Pos:$1.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name '(' optional_elements ')' { $$=&CreateTable{Name:$6,Elements:$8,Pos:$1.pos,IfNotExists:true} }
 | CREATE generic_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
schema_tables: /* empty */ { $$=nil } | schema_tables schema_table { $$=append($1,$2) }
 | schema_tables KW_R { sqllex.(*lexer).capability($2,unsupportedCommands,"schema element is unsupported"); return 1 } ;
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
