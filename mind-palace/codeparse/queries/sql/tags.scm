; SQL: schema objects, the tables and routines queries reach, extensions
; and psql includes, in tree-sitter's tags-query convention plus clued's
; captures (see ../README.md).
;
; The grammar is m-novikov/tree-sitter-sql, the only SQL grammar
; gotreesitter ships: PostgreSQL-flavoured and old. Statements it cannot
; parse (CALL, USE, MySQL table options, PostgreSQL extensions) become
; error nodes; the patterns below still match what parsed around them, and
; a few recover CALL and USE from the error tokens. Routine bodies are
; dollar-quoted strings to this grammar, so nothing inside them is seen.
;
; Names: a schema-qualified name (s.t) is captured as the last part with
; the schema as @receiver, so the symbol is qualified s.t (and nests under
; CREATE SCHEMA s when the file has it); a column added by ALTER TABLE, and
; a trigger, belong to their table the same way. Quotes ("Name", `name`)
; are stripped from names. Doc comments are the comments on the lines just
; above a statement (the extractor's default), signatures its first line.
;
; @package: SQL has no package clause; USE <database> (MySQL, T-SQL) is
; taken as the file's package. SET search_path is not: it lists schemas to
; search, not one to define in.

; ---- package and imports

; USE does not parse: the error node holds the statement (and whatever
; else failed after it), so the name is cut out of its text.
((ERROR) @package
  (#match? @package "(?i)^\\s*use\\s")
  (#strip! @package "(?is)^\\s*use\\s+|[\\s;].*$|[\"`\\[\\]]"))

(create_extension_statement
  (identifier) @import.spec
  (#set! import.kind "extension")) @import

((pg_command) @import @import.spec
  (#match? @import.spec "^\\\\i[r]?\\s")
  (#strip! @import.spec "^\\\\i[r]?\\s+")
  (#set! import.kind "include"))

; ---- schemas

((create_schema_statement
  (identifier) @name) @definition.schema
  (#strip! @name "[\"`]"))

; ---- tables and their columns

((create_table_statement
  . (identifier) @name) @definition.table
  (#strip! @name "[\"`]"))

((create_table_statement
  . (dotted_name (identifier) @receiver . (identifier) @name .)) @definition.table
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

((create_table_statement
  (table_parameters
    (table_column
      name: (identifier) @name) @definition.column))
  (#not-match? @name "^(?i)(alter|check|constraint|create|delete|drop|foreign|insert|primary|select|table|unique|update)$")
  (#strip! @name "[\"`]"))

; ALTER TABLE t ADD COLUMN c: c belongs to t.
((alter_table
  . (identifier) @receiver
  (alter_table_action
    (alter_table_action_add
      (table_column
        name: (identifier) @name) @definition.column)))
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

((alter_table
  . (dotted_name) @receiver
  (alter_table_action
    (alter_table_action_add
      (table_column
        name: (identifier) @name) @definition.column)))
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

; ---- views (materialized views too)

((create_view_statement
  . (identifier) @name) @definition.view
  (#strip! @name "[\"`]"))

((create_view_statement
  . (dotted_name (identifier) @receiver . (identifier) @name .)) @definition.view
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

((create_materialized_view_statement
  . (identifier) @name) @definition.view
  (#strip! @name "[\"`]"))

((create_materialized_view_statement
  . (dotted_name (identifier) @receiver . (identifier) @name .)) @definition.view
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

; ---- functions and procedures (one node type; the keyword tells them apart)

((create_function_statement
  . (identifier) @name) @definition.function
  (#not-match? @definition.function "^(?i)create\\s+(or\\s+replace\\s+)?procedure")
  (#strip! @name "[\"`]"))

((create_function_statement
  . (dotted_name (identifier) @receiver . (identifier) @name .)) @definition.function
  (#not-match? @definition.function "^(?i)create\\s+(or\\s+replace\\s+)?procedure")
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

((create_function_statement
  . (identifier) @name) @definition.procedure
  (#match? @definition.procedure "^(?i)create\\s+(or\\s+replace\\s+)?procedure")
  (#strip! @name "[\"`]"))

((create_function_statement
  . (dotted_name (identifier) @receiver . (identifier) @name .)) @definition.procedure
  (#match? @definition.procedure "^(?i)create\\s+(or\\s+replace\\s+)?procedure")
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

; ---- triggers, which belong to their table

((create_trigger_statement
  name: (identifier) @name
  on_table: (_) @receiver) @definition.trigger
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

; ---- indexes (an unnamed index defines nothing)

((create_index_statement
  name: (identifier) @name) @definition.index
  (#strip! @name "[\"`]"))

; ---- types, domains and sequences

((create_type_statement
  type_name: (identifier) @name
  (type_spec_enum)) @definition.enum
  (#strip! @name "[\"`]"))

((create_type_statement
  type_name: (dotted_name (identifier) @receiver . (identifier) @name .)
  (type_spec_enum)) @definition.enum
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

((create_type_statement
  type_name: (identifier) @name
  [(type_spec_composite) (type_spec_range) (type_spec_base)]) @definition.type
  (#strip! @name "[\"`]"))

((create_type_statement
  type_name: (dotted_name (identifier) @receiver . (identifier) @name .)
  [(type_spec_composite) (type_spec_range) (type_spec_base)]) @definition.type
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

((create_domain_statement
  . (identifier) @name) @definition.domain
  (#strip! @name "[\"`]"))

((create_domain_statement
  . (dotted_name (identifier) @receiver . (identifier) @name .)) @definition.domain
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

((create_statement
  (create_sequence
    . (identifier) @name)) @definition.sequence
  (#strip! @name "[\"`]"))

((create_statement
  (create_sequence
    . (dotted_name (identifier) @receiver . (identifier) @name .))) @definition.sequence
  (#strip! @receiver "[\"`]")
  (#strip! @name "[\"`]"))

; ---- references to tables and views: FROM, JOIN, INSERT INTO, UPDATE,
; DELETE FROM, ALTER TABLE, ON (indexes, triggers) and REFERENCES

((from_clause
  (identifier) @name @reference.table)
  (#strip! @name "[\"`]"))

((from_clause
  (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((join_clause
  (identifier) @name @reference.table)
  (#strip! @name "[\"`]"))

((join_clause
  (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((insert_statement
  . (identifier) @name @reference.table)
  (#strip! @name "[\"`]"))

((insert_statement
  . (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((update_statement
  . (identifier) @name @reference.table
  . [(set_clause) (alias)])
  (#strip! @name "[\"`]"))

; UPDATE s.t does not parse: s is followed by an error node ".t".
((update_statement
  . (identifier) @qualifier
  . (ERROR) @name @reference.table)
  (#match? @name "^\\.")
  (#strip! @qualifier "[\"`]")
  (#strip! @name "^\\.|[\"`]"))

((update_statement
  . (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((alter_table
  . (identifier) @name @reference.table)
  (#strip! @name "[\"`]"))

((alter_table
  . (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((create_index_statement
  table_name: (identifier) @name @reference.table)
  (#strip! @name "[\"`]"))

((create_index_statement
  table_name: (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((create_trigger_statement
  on_table: (identifier) @name @reference.table)
  (#strip! @name "[\"`]"))

((create_trigger_statement
  on_table: (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((references_constraint
  . (identifier) @name @reference.table)
  (#strip! @name "[\"`]"))

((references_constraint
  . (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.table)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

; ---- calls: functions in expressions, a trigger's routine, CALL

((function_call
  function: (identifier) @name) @reference.call
  (#not-match? @name "^(?i)(abs|array_agg|array_length|avg|bool_and|bool_or|cast|ceil|char_length|coalesce|concat|concat_ws|count|current_date|current_setting|current_timestamp|date_part|date_trunc|dense_rank|exists|extract|first_value|floor|format|gen_random_uuid|generate_series|greatest|ifnull|isnull|json_agg|json_build_object|jsonb_agg|jsonb_build_object|lag|last_value|lead|least|left|length|lower|ltrim|max|md5|min|mod|now|nullif|nvl|position|power|random|rank|regexp_replace|replace|right|round|row|row_number|rtrim|split_part|sqrt|string_agg|substr|substring|sum|to_char|to_date|to_timestamp|trim|trunc|unnest|upper|uuid_generate_v4)$")
  (#strip! @name "[\"`]"))

((function_call
  function: (dotted_name (identifier) @qualifier . (identifier) @name .)) @reference.call
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))

((trigger_body
  function: (identifier) @name) @reference.call
  (#strip! @name "[\"`]"))

; CALL does not parse, and inside an error node the lexer splits words at
; keywords (refresh_totals becomes refresh_ to tals), so the routine is cut
; out of the error node's text. One error node can hold several statements
; that failed in a row; only the first is seen.
((ERROR) @name @reference.call
  (#match? @name "(?i)^\\s*call\\s+[\"`]?\\w+[\"`]?\\s*\\(")
  (#strip! @name "(?is)^\\s*call\\s+|\\s*\\(.*$|[\"`]"))

((ERROR) @qualifier @name @reference.call
  (#match? @name "(?i)^\\s*call\\s+[\"`]?\\w+[\"`]?\\.[\"`]?\\w+[\"`]?\\s*\\(")
  (#strip! @qualifier "(?is)^\\s*call\\s+|\\..*$|[\"`]")
  (#strip! @name "(?is)^\\s*call\\s+[\"`]?\\w+[\"`]?\\.|\\s*\\(.*$|[\"`]"))

; ---- types used by columns, parameters, results and casts (not built-in ones)

((type
  . (identifier) @name @reference.type)
  (#not-match? @name "^(?i)(bigint|bigserial|bit|bool|boolean|box|bytea|char|character|cidr|circle|date|datetime|decimal|double|float|float4|float8|inet|int|int2|int4|int8|integer|interval|json|jsonb|line|lseg|macaddr|money|nchar|numeric|nvarchar|path|point|polygon|real|record|serial|serial2|serial4|serial8|setof|smallint|smallserial|table|text|time|timestamp|timestamptz|timetz|tinyint|trigger|tsquery|tsvector|uuid|varbinary|varchar|varying|void|xml|blob|clob|longtext|mediumtext|tinytext|mediumint|year|enum|any|anyelement|regclass|oid|name)$")
  (#strip! @name "[\"`]"))

((type
  . (dotted_name (identifier) @qualifier . (identifier) @name .) @reference.type)
  (#strip! @qualifier "[\"`]")
  (#strip! @name "[\"`]"))
