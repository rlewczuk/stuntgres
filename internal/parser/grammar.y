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
 names []string
 rows [][]Expr
 row []Expr
 value Value
 expr Expr
 selectStmt *Select
 order *Order
 desc bool
 objectName ObjectName
 settingValues settingList
 settingValue settingAtom
 roleOptions roleOptionState
 settingName settingName
 tables []*CreateTable
 table *CreateTable
}
%token <tok> IDENT NUMBER STRING PARAM
%token <tok> DOT_DOT
%token <tok> LESS_EQUAL GREATER_EQUAL NOT_EQUAL
%token <tok> '+' '-' '[' ':' '(' '=' '<' '>' ','
%token <tok> CREATE TABLE INSERT INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE OR
%token <tok> INTEGER BIGINT TEXT BOOLEAN KW_U KW_C KW_T KW_R AS CAST NULLS_LA
%token <tok> TIMESTAMP WITHOUT TIME ZONE
%token <tok> SET SHOW RESET ALL SESSION TO DEFAULT LOCAL SETTING_FUNC ON
%token <tok> IF EXISTS
%token <tok> ROLE USER GROUP DROP WITH AUTHORIZATION SESSION_USER CURRENT_USER CURRENT_ROLE
%token <tok> LOCK ACCESS EXCLUSIVE MODE IN
%token <tok> LIKE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CASCADE RESTRICT CURRENT_SCHEMA
%token <tok> COMMENT COLUMN IS
%token <tok> CONSTRAINT ADD INHERITS
%token <tok> BEGIN START COMMIT END ROLLBACK ABORT WORK TRANSACTION AND NO CHAIN
%token <tok> ISOLATION LEVEL READ ONLY DEFERRABLE SAVEPOINT RELEASE PREPARE PREPARED
%token <tok> COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE
/* Reduce a scalar/name only after checking failure-only continuations.
 * Arithmetic remains unsupported; WHERE name suffix keeps its positioned
 * predicate diagnostic rather than reducing to a completed bare scalar.
 * Dedicated CONSTRAINT retains KW_R's contextual continuation precedence.
 * '(' selects a function path, not a completed ordinary name/name list. */
%nonassoc NAME_LIST
%nonassoc '+' '-' INTO KW_R CONSTRAINT COLUMN ALL DEFAULT ON USER GROUP IN AND SAVEPOINT
/* KW_T, LIKE and WITH also start suffixes. Shift an adjacent type tail before
 * completing a single type name: known excluded families use their first type
 * location, while arbitrary tails are syntax errors at the second token. */
%nonassoc TYPE_NAME
%nonassoc '('
%nonassoc KW_T IS LIKE WITH WITHOUT
%type <stmt> statement create create_table drop_table alter_drop_column alter_drop_single alter_nn_single insert select set show reset create_role drop_role session_authorization identity_select excluded_role lock_table create_schema alter_schema drop_schema transaction_control
%type <col> column
%type <elements> elements optional_elements
%type <element> element
%type <likeOptions> like_options
%type <likeOption> like_option
%type <tok> like_option_name
%type <objectNames> relation_names
%type <dropMode> drop_mode
%type <typ> type temporal_type generic_precision generic_modifiers type_modifier_head
%type <constraints> nullability column_constraints
%type <constraint> column_constraint
%type <notNull> table_not_null
%type <tok> nn_modifier nn_inherit
%type <tok> column_suffix
%type <tok> name setting_word role_name role_option_word type_name bare_label col_label generic_word generic_word_without_with suffix unsupported_query nulls_keyword nulls_position
%type <settingName> setting_name set_name local_setting_name
%type <targets> insert_columns insert_targets
%type <objectName> relation_name
%type <objectName> alter_relation
%type <alterTarget> alter_target
%type <desc> optional_column
%type <dropTarget> drop_target
%type <tok> alter_action
%type <names> names
%type <rows> rows
%type <row> row values call_args scalar_targets
%type <value> value
%type <expr> scalar operand target where target_not target_and target_or
%type <tok> comparison_operator
%type <selectStmt> projection
%type <order> order
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
%type <tok> transaction_modes transaction_mode transaction_start commit_word rollback_start
%type <desc> opt_chain
%type <tok> transaction_name
%%
script: separators optional_statements ;
separators: /* empty */ | separators ';' ;
optional_statements: /* empty */ | accepted tail ;
tail: /* empty */ | ';' separators optional_statements ;
accepted: statement { if $1 != nil { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } } ;
statement: create | drop_table | alter_drop_column | insert | select | set | show | reset | create_role | drop_role | session_authorization | identity_select | excluded_role | lock_table | create_schema | alter_schema | drop_schema | comment | transaction_control
 | generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | DROP { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER ROLE { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER USER { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER GROUP { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 } ;
comment: COMMENT ON TABLE relation_name IS comment_text { $$=&Comment{Kind:CommentTable,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON COLUMN comment_column_name IS comment_text { $$=&Comment{Kind:CommentColumn,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON SCHEMA name IS comment_text { $$=&Comment{Kind:CommentSchema,Name:ObjectName{Parts:[]Identifier{{Text:$4.text,Pos:$4.pos}}},Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON ROLE name IS comment_text { $$=&Comment{Kind:CommentRole,Name:ObjectName{Parts:[]Identifier{{Text:$4.text,Pos:$4.pos}}},Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON comment_excluded_kind relation_name IS comment_text { sqllex.(*lexer).deferUnsupported($3,"COMMENT object kind is unsupported"); $$=nil } ;
comment_column_name: name { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}} }
 | name '.' col_label { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}} }
 | name '.' col_label '.' col_label { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}} } ;
comment_text: STRING { $$=Value{Kind:StringValue,Text:$1.text,Pos:$1.pos} }
 | NULL { $$=Value{Kind:NullValue,Pos:$1.pos} } ;
comment_excluded_word: IDENT | KW_U | KW_T | KW_R ;
comment_excluded_kind: comment_excluded_word { if $1.keyword==nil || !unsupportedCommentKinds[$1.text] { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
transaction_control: transaction_start { $$=&TransactionControl{Kind:KindStart,Pos:$1.pos} }
 | transaction_start transaction_modes { sqllex.(*lexer).deferUnsupported($2,"transaction modes are unsupported"); $$=nil }
 | commit_word opt_transaction opt_chain { $$=&TransactionControl{Kind:KindCommit,Chain:$3,Pos:$1.pos} }
 | rollback_start opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$2,Pos:$1.pos} }
 | ABORT opt_transaction opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$3,Pos:$1.pos} }
 | SAVEPOINT name { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | RELEASE optional_savepoint name { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | rollback_start TO optional_savepoint name { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | PREPARE TRANSACTION STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | COMMIT PREPARED STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | ROLLBACK PREPARED STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil } ;
transaction_start: BEGIN opt_transaction { $$=$1 } | START TRANSACTION { $$=$1 } ;
commit_word: COMMIT | END ;
/* Share the entire ROLLBACK [WORK|TRANSACTION] prefix before choosing an
 * executable end or failure-only TO. Never shift optional words into TO-only
 * syntax while reducing a competing bare ROLLBACK end introducer. */
rollback_start: ROLLBACK opt_transaction { $$=$1 } ;
opt_transaction: /* empty */ | WORK | TRANSACTION ;
opt_chain: /* empty */ { $$=false } | AND CHAIN { $$=true } | AND NO CHAIN { $$=false } ;
/* PostgreSQL shifts the optional SAVEPOINT keyword before parsing its name:
 * RELEASE SAVEPOINT and ROLLBACK TO SAVEPOINT still require an operand.
 * SAVEPOINT stays name-eligible in actual name positions. */
optional_savepoint: /* empty */ %prec NAME_LIST | SAVEPOINT ;
transaction_modes: transaction_mode | transaction_modes ',' transaction_mode { $$=$1 } | transaction_modes transaction_mode { $$=$1 } ;
transaction_mode: ISOLATION LEVEL isolation_mode { $$=$1 }
 | READ ONLY { $$=$1 }
 | READ mode_write { $$=$1 }
 | DEFERRABLE
 | NOT DEFERRABLE { $$=$1 } ;
isolation_mode: READ COMMITTED | READ UNCOMMITTED | REPEATABLE READ | SERIALIZABLE ;
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
lock_update: KW_U { if $1.text!="update" { sqllex.(*lexer).syntax($1); return 1 } } ;
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
create: create_table { $$=$1 }
 | create_table INHERITS '(' relation_names ')' { sqllex.(*lexer).deferUnsupported($2,"table inheritance is unsupported"); $$=nil }
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
alter_target: alter_relation { $$=alterTarget{Name:$1} }
 | ONLY relation_name { $$=alterTarget{Name:$2,Only:true} }
 | ONLY '(' relation_name ')' { $$=alterTarget{Name:$3,Only:true} } ;
alter_relation: relation_name { $$=$1 } | relation_name '*' { $$=$1 } ;
optional_column: /* empty */ { $$=false } | COLUMN { $$=true } ;
/* Share the IF prefix with eligible names: DROP if and IF EXISTS both parse. */
drop_target: name { $$=dropTarget{Name:Identifier{Text:$1.text,Pos:$1.pos}} }
 | IF EXISTS name { $$=dropTarget{Name:Identifier{Text:$3.text,Pos:$3.pos},IfExists:true} } ;
alter_action: generic_word | RENAME | OWNER | SET ;
column: name type nullability { $$ = Column{Name:$1.text, TypeName:$2, Constraints:$3, Pos:$1.pos} } ;
type: INTEGER { $$ = TypeName{Name:"integer",Pos:$1.pos} } | BIGINT { $$ = TypeName{Name:"bigint",Pos:$1.pos} } | TEXT { $$ = TypeName{Name:"text",Pos:$1.pos} } | BOOLEAN { $$ = TypeName{Name:"boolean",Pos:$1.pos} }
 | type_name %prec TYPE_NAME { $$=columnType($1) }
 | temporal_type { $$=$1 }
 | type_modifier_head generic_modifiers ')' { if sqllex.(*lexer).floatPrecision { $$=$2;$$.Pos=$1.Pos;sqllex.(*lexer).floatPrecision=false } else { $$=$1;$$.Modifiers=$2.Modifiers } }
 | type_name type_name { if $1.keyword!=nil && $2.keyword!=nil && $1.text=="double" && $2.text=="precision" { $$=TypeName{Name:"double precision",Pos:$1.pos} } else { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 } }
 | type_name WITH { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 }
 | type_name '.' { sqllex.(*lexer).failAt("0A000","qualified types are unsupported",$1.pos.Offset); return 1 } ;
/* Descriptor modifiers share raw constant syntax; FLOAT and temporal types retain
 * their distinct unsigned/single precision contracts. */
type_modifier_head: type_name '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2);if !ok{return 1}} ;
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
 | '-' {if sqllex.(*lexer).floatPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$3,true);if !ok{return 1};$$.Modifiers[0].Pos=$1.pos}
 | '+' {if sqllex.(*lexer).floatPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=temporalPrecision(sqllex,$3,false);if !ok{return 1};$$.Modifiers[0].UnaryPlus=true;$$.Modifiers[0].Pos=$1.pos} ;
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
 | CONSTRAINT name column_suffix { sqllex.(*lexer).capability($3,unsupportedSuffixes,"column constraints are unsupported"); return 1 } ;
nn_modifier: KW_U { if $1.text!="valid" && $1.text!="enforced" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
nn_inherit: KW_U { if $1.text!="inherit" {sqllex.(*lexer).syntax($1);return 1};$$=$1 } ;
column_suffix: LIKE | KW_T | KW_R | INTO | DEFAULT | ALL | ON | GROUP | WITH | USER | IN | IS | AND ;
insert: INSERT INTO relation_name insert_columns VALUES rows { $$ = &Insert{Name:$3, Targets:$4, Rows:$6, Pos:$1.pos} }
 | INSERT INTO relation_name insert_columns VALUES rows suffix { sqllex.(*lexer).capability($7,unsupportedSuffixes,"INSERT suffix is unsupported"); return 1 }
 | INSERT INTO relation_name insert_columns DEFAULT VALUES { if !sqllex.(*lexer).options.RestoreDefaultValues { sqllex.(*lexer).failAt("0A000","INSERT defaults are unsupported",$5.pos.Offset); return 1 }; $$=&Insert{Name:$3,Targets:$4,Rows:[][]Expr{nil},Pos:$1.pos,RestoreDefaultValues:true} }
 | INSERT INTO relation_name unsupported_query { sqllex.(*lexer).deferUnsupported($4,"INSERT SELECT is unsupported"); $$=nil }
 | INSERT INTO relation_name '(' insert_targets ')' unsupported_query { sqllex.(*lexer).deferUnsupported($7,"INSERT SELECT is unsupported"); $$=nil } ;
insert_columns: /* empty */ { $$ = nil } | '(' insert_targets ')' { $$ = $2 } ;
insert_targets: name { $$ = []InsertTarget{{Name:$1.text, Pos:$1.pos}} } | insert_targets ',' name { $$ = append($1, InsertTarget{Name:$3.text, Pos:$3.pos}) } ;
/* Failure-only query input recognition, not an executable SELECT expansion.
 * In INSERT, (SELECT) is a query input, not a reserved column name. Continue
 * parsing so that a trailing VALUES (or any other malformed suffix) wins. */
unsupported_query: SELECT query_projection query_from query_where query_order { $$=$1 }
 | '(' unsupported_query ')' { $$=$2 } ;
query_projection: /* empty */ | '*' | query_targets ;
query_targets: query_target | query_targets ',' query_target ;
query_target: name | NUMBER | STRING | PARAM | TRUE | FALSE | NULL | '+' NUMBER | '-' NUMBER ;
query_from: /* empty */ | FROM relation_name ;
query_where: /* empty */ | WHERE name '=' query_value ;
query_value: NUMBER | STRING | PARAM | TRUE | FALSE | NULL | '+' NUMBER | '-' NUMBER ;
query_order: /* empty */ | ORDER BY name direction ;
names: name %prec NAME_LIST { $$ = []string{$1.text} } | names ',' name %prec NAME_LIST { $$ = append($1,$3.text) } ;
rows: row { $$ = [][]Expr{$1} } | rows ',' row { $$ = append($1,$3) } ;
row: '(' values ')' { $$ = $2 } ;
values: scalar { $$ = []Expr{$1} } | values ',' scalar { $$ = append($1,$3) } ;
scalar: value %prec NAME_LIST { $$=LiteralExpr{Value:$1} }
 | PARAM { $$=ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos} }
 | temporal_type STRING { $$=TypedLiteralExpr{TypeName:$1,Text:$2.text,Pos:$2.pos} }
 | scalar ':' {sqllex.(*lexer).failAt("0A000","casts are unsupported",$2.pos.Offset);return 1} ;
value: NULL { $$ = Value{Kind:NullValue, Pos:$1.pos} }
 | TRUE { $$ = Value{Kind:BooleanValue, Boolean:true, Pos:$1.pos} }
 | FALSE { $$ = Value{Kind:BooleanValue, Pos:$1.pos} }
 | STRING { $$ = Value{Kind:StringValue, Text:$1.text, Pos:$1.pos} }
 | NUMBER { var ok bool; $$, ok = integerValue(sqllex,$1,""); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"+"); if !ok { return 1 }; $$.Pos=$1.pos }
 | '-' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"-"); if !ok { return 1 }; $$.Pos=$1.pos }
 | value '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '*' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '/' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | name '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | KW_T '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | IS '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | LIKE '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | AUTHORIZATION '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | DEFAULT { sqllex.(*lexer).failAt("0A000","default values are unsupported",$1.pos.Offset); return 1 }
 | CAST '(' { sqllex.(*lexer).failAt("0A000","casts are unsupported",$1.pos.Offset); return 1 } ;
select: SELECT projection FROM relation_name where order { $2.Name=$4; $2.Where=$5; $2.Order=$6; $2.Pos=$1.pos; $$=$2 }
 | SELECT projection { sqllex.(*lexer).deferUnsupported($1,"SELECT without FROM is unsupported"); $$=nil }
 | SELECT projection FROM relation_name name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$5.pos.Offset); return 1 }
 | SELECT projection FROM relation_name AS name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$6.pos.Offset); return 1 }
 | SELECT projection INTO { sqllex.(*lexer).failAt("0A000","SELECT INTO is unsupported",$3.pos.Offset); return 1 }
 | SELECT projection FROM relation_name where order suffix { sqllex.(*lexer).capability($7,unsupportedSuffixes,"SELECT suffix is unsupported"); return 1 }
 | SELECT SETTING_FUNC '(' call_args ')' { var ok bool; $$,ok=settingCall(sqllex,$2,$4); if !ok { return 1 } } ;
call_args: /* empty */ { $$=nil } | values { $$=$1 } ;
operand: name %prec NAME_LIST { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos} }
 | scalar { $$=$1 } ;
comparison_operator: '=' | '<' | '>' | LESS_EQUAL | GREATER_EQUAL | NOT_EQUAL ;
target: operand { $$=$1 }
 | operand comparison_operator operand { $$=OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Args:[]Expr{$1,$3},Pos:$2.pos} }
 | '(' target_or ')' { $$=$2 } ;
target_not: target { $$=$1 }
 | NOT target_not { $$=LogicalExpr{Op:LogicalNot,Left:$2,Pos:$1.pos} } ;
target_and: target_not { $$=$1 }
 | target_and AND target_not { $$=LogicalExpr{Op:LogicalAnd,Left:$1,Right:$3,Pos:$2.pos} } ;
target_or: target_and { $$=$1 }
 | target_or OR target_and { $$=LogicalExpr{Op:LogicalOr,Left:$1,Right:$3,Pos:$2.pos} } ;
scalar_targets: target_or { $$=[]Expr{$1} } | scalar_targets ',' target_or { $$=append($1,$3) } ;
projection: '*' { $$ = &Select{Star:true} } | scalar_targets { $$ = &Select{Targets:$1} }
 | scalar_targets bare_label { sqllex.(*lexer).failAt("0A000","projection aliases are unsupported",$2.pos.Offset); return 1 }
 | scalar_targets AS col_label { sqllex.(*lexer).failAt("0A000","projection aliases are unsupported",$3.pos.Offset); return 1 }
 | scalar_targets '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | scalar_targets '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | scalar_targets '.' { sqllex.(*lexer).fail("0A000","qualified column references are unsupported"); return 1 } ;
where: /* empty */ { $$ = nil }
 | WHERE target_or { $$=$2 }
 | WHERE name suffix { if $3.keyword!=nil && ($3.text=="isnull" || $3.text=="notnull") { sqllex.(*lexer).failAt("0A000","expressions are unsupported",$3.pos.Offset) } else { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported") }; return 1 }
 | WHERE name KW_C { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 } ;
order: /* empty */ { $$ = nil } | ORDER BY name direction { $$ = &Order{Column:$3.text, Pos:$3.pos, Desc:$4} }
 | ORDER BY name direction ',' { sqllex.(*lexer).fail("0A000","multiple sort keys are unsupported"); return 1 }
 | ORDER BY name direction nulls_keyword nulls_position { sqllex.(*lexer).failAt("0A000","NULLS ordering is unsupported",$5.pos.Offset); return 1 } ;
nulls_keyword: NULLS_LA ;
nulls_position: KW_U { if $1.keyword==nil || ($1.text!="first" && $1.text!="last") { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
direction: /* empty */ { $$ = false } | ASC { $$ = false } | DESC { $$ = true } ;
/* Keep ROLE/LOCAL command disambiguation while preserving ordinary names. */
setting_word: IDENT | KW_U | KW_C | INTEGER | BIGINT | TEXT | BOOLEAN | TIMESTAMP %prec NAME_LIST | WITHOUT | TIME %prec NAME_LIST | ZONE | VALUES | INSERT | BY | SET | SHOW | RESET | SESSION | SETTING_FUNC %prec NAME_LIST | IF | EXISTS | DROP | LOCK | ACCESS | EXCLUSIVE | MODE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | COMMENT | ADD | INHERITS | transaction_name ;
name: setting_word | ROLE | LOCAL ;
transaction_name: BEGIN | START | COMMIT | ROLLBACK | ABORT | WORK | TRANSACTION | NO | CHAIN | ISOLATION | LEVEL | READ | SAVEPOINT | RELEASE | PREPARE | PREPARED | COMMITTED | UNCOMMITTED | REPEATABLE | SERIALIZABLE ;
type_name: IDENT | KW_U | KW_T | KW_C | WITHOUT | ZONE | VALUES | INSERT | BY | SET | SHOW | RESET | SESSION | LOCAL | SETTING_FUNC | IF | EXISTS | DROP | ROLE | AUTHORIZATION | LOCK | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | CURRENT_SCHEMA | COMMENT | IS | ADD | INHERITS | transaction_name ;
generic_word: generic_word_without_with | WITH ;
generic_word_without_with: IDENT | KW_U | KW_C | KW_T | KW_R | TIMESTAMP | WITHOUT | TIME | ZONE | IF | EXISTS | ALL | LOCAL | ON | AUTHORIZATION | IN | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | CASCADE | RESTRICT | COLUMN | IS | AND | OR | ONLY | DEFERRABLE | CONSTRAINT ;
/* Structural SELECT delimiters cannot be reduced to bare labels. */
bare_label: name { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | LIKE { $$=$1 }
 | IS { $$=$1 }
 | COLUMN { $$=$1 }
 | KW_T { if !$1.keyword.label() { if $1.text=="isnull" || $1.text=="notnull" { sqllex.(*lexer).failAt("0A000","expressions are unsupported",$1.pos.Offset) } else { sqllex.(*lexer).syntax($1) }; return 1 }; $$=$1 }
 | KW_R { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | IN { $$=$1 }
 | ON { sqllex.(*lexer).syntax($1); return 1 }
 | SELECT | TABLE | ASC | DESC | NOT | NULL | TRUE | FALSE | DEFAULT | CAST | ALL | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | END | ONLY | DEFERRABLE | CONSTRAINT ;
col_label: name | LIKE | KW_T | KW_R | CREATE | TABLE | INTO | SELECT | FROM | WHERE | ORDER | ASC | DESC | NOT | NULL | TRUE | FALSE | TO | DEFAULT | AS | CAST | ALL | ON | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | GROUP | WITH | IN | COLUMN | IS | AND | OR | END | ONLY | DEFERRABLE | CONSTRAINT ;
suffix: LIKE | KW_T | KW_R | INTO | DEFAULT | ALL | ON | GROUP | WITH | USER | IN | IS | CONSTRAINT ;
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
identity_select: SELECT identity_names { $$=&IdentitySelect{Names:$2} } ;
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
