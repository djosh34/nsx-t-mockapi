package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.uber.org/zap"
)

const (
	searchFieldText searchFieldKind = iota
	searchFieldNumber
	searchFieldBool
)

const (
	boolAND = "AND"
	boolOR  = "OR"
)

const (
	// SearchSyntaxQuery selects the documented full-text search query syntax.
	SearchSyntaxQuery SearchSyntax = "query"
	// SearchSyntaxDSL selects the documented domain-specific search syntax.
	SearchSyntaxDSL SearchSyntax = "dsl"
)

const compareOperatorWidth = 2

var (
	errEmptySearchQuery      = errors.New("search query is empty")
	errMalformedSearchQuery  = errors.New("malformed search query")
	errUnknownSearchField    = errors.New("unknown search field")
	errInvalidSearchFieldUse = errors.New("invalid search field use")
	errInvalidSearchBool     = errors.New("invalid search bool")
	errUnsupportedSearchSort = errors.New("unsupported search sort field")
)

var searchableFields = map[string]searchFieldMeta{
	"id":                  {kind: searchFieldText},
	"display_name":        {kind: searchFieldText},
	"description":         {kind: searchFieldText},
	"resource_type":       {kind: searchFieldText},
	"path":                {kind: searchFieldText},
	"parent_path":         {kind: searchFieldText},
	"relative_path":       {kind: searchFieldText},
	"tags.scope":          {kind: searchFieldText},
	"tags.tag":            {kind: searchFieldText},
	"_revision":           {kind: searchFieldNumber},
	"_create_time":        {kind: searchFieldNumber},
	"_last_modified_time": {kind: searchFieldNumber},
	"vni":                 {kind: searchFieldNumber},
	"marked_for_delete":   {kind: searchFieldBool},
	"_system_owned":       {kind: searchFieldBool},
}

type searchFieldKind int

type searchFieldMeta struct {
	kind searchFieldKind
}

// SearchSyntax identifies which documented NSX-T search language should parse Query.
type SearchSyntax string

type searchFieldRef struct {
	name string
	meta searchFieldMeta
	set  bool
}

type searchBoolOperator int

const (
	searchBoolAnd searchBoolOperator = iota + 1
	searchBoolOr
)

func (op searchBoolOperator) sql() (string, error) {
	switch op {
	case searchBoolAnd:
		return boolAND, nil
	case searchBoolOr:
		return boolOR, nil
	default:
		return "", fmt.Errorf("%w: unsupported boolean operator %d", errMalformedSearchQuery, op)
	}
}

type searchRangeOperator int

const (
	searchRangeGreaterThan searchRangeOperator = iota + 1
	searchRangeGreaterThanOrEqual
	searchRangeLessThan
	searchRangeLessThanOrEqual
)

func parseSearchRangeOperator(text string) (searchRangeOperator, error) {
	switch text {
	case ">":
		return searchRangeGreaterThan, nil
	case ">=":
		return searchRangeGreaterThanOrEqual, nil
	case "<":
		return searchRangeLessThan, nil
	case "<=":
		return searchRangeLessThanOrEqual, nil
	default:
		return 0, fmt.Errorf("%w: unsupported range operator %q", errMalformedSearchQuery, text)
	}
}

func (op searchRangeOperator) sql() (string, error) {
	switch op {
	case searchRangeGreaterThan:
		return ">", nil
	case searchRangeGreaterThanOrEqual:
		return ">=", nil
	case searchRangeLessThan:
		return "<", nil
	case searchRangeLessThanOrEqual:
		return "<=", nil
	default:
		return "", fmt.Errorf("%w: unsupported range operator %d", errMalformedSearchQuery, op)
	}
}

// SearchQueryOptions configures an NSX-T search query against stored resources.
type SearchQueryOptions struct {
	Syntax                 SearchSyntax
	Query                  string
	IncludeMarkedForDelete bool
	Limit                  int
	Offset                 int
	SortBy                 string
	SortAscending          bool
}

// SearchPage is one page of canonical resources plus total match metadata.
type SearchPage struct {
	Resources   []StoredResource
	ResultCount int
	Cursor      string
}

// PreparedSearchQuery is a compiled SQLite query plus its bound arguments.
type PreparedSearchQuery struct {
	SQL  string
	Args []any
}

// SafeSearchQuery is a parsed NSX-T search query intent before SQLite compilation.
type SafeSearchQuery struct {
	root searchNode
}

type searchTokenKind int

const (
	searchTokenEOF searchTokenKind = iota
	searchTokenTerm
	searchTokenPhrase
	searchTokenColon
	searchTokenLParen
	searchTokenRParen
	searchTokenAND
	searchTokenOR
	searchTokenNOT
	searchTokenCompare
)

type searchToken struct {
	kind searchTokenKind
	text string
}

type dslTokenKind int

const (
	dslTokenEOF dslTokenKind = iota
	dslTokenTerm
	dslTokenPhrase
	dslTokenWhere
	dslTokenAnd
	dslTokenOr
	dslTokenLParen
	dslTokenRParen
	dslTokenEqual
	dslTokenNotEqual
	dslTokenCompare
	dslTokenLike
)

type dslToken struct {
	kind dslTokenKind
	text string
}

type searchTermNode struct {
	field  searchFieldRef
	value  string
	phrase bool
}

type searchRangeNode struct {
	field searchFieldRef
	op    searchRangeOperator
	value string
}

type searchBoolNode struct {
	op    searchBoolOperator
	left  searchNode
	right searchNode
}

type searchNotNode struct {
	child searchNode
}

type searchNode interface {
	searchNode()
}

func (searchTermNode) searchNode()  {}
func (searchRangeNode) searchNode() {}
func (searchBoolNode) searchNode()  {}
func (searchNotNode) searchNode()   {}

type searchParser struct {
	tokens []searchToken
	pos    int
}

type dslParser struct {
	tokens []dslToken
	pos    int
}

type searchPredicate struct {
	sql  string
	args []any
}

// CompileSearchQuery parses an NSX-T search query and compiles it into prepared SQLite SQL.
func CompileSearchQuery(opts SearchQueryOptions) (PreparedSearchQuery, error) {
	query, err := parseSearchQueryBySyntax(opts)
	if err != nil {
		return PreparedSearchQuery{}, err
	}
	return CompileSafeSearchQuery(opts, query)
}

// CompileSafeSearchQuery compiles a parsed safe search query into prepared SQLite SQL.
func CompileSafeSearchQuery(opts SearchQueryOptions, query SafeSearchQuery) (PreparedSearchQuery, error) {
	predicate, args, err := compileSafeSearchQueryPredicate(opts, query)
	if err != nil {
		return PreparedSearchQuery{}, err
	}
	orderSQL, err := searchOrderSQL(opts)
	if err != nil {
		return PreparedSearchQuery{}, err
	}

	sqlText := searchSelectSQL(predicate.sql, orderSQL)
	if opts.Limit > 0 {
		sqlText += "\nLIMIT ?"
		args = append(args, opts.Limit)
	}
	if opts.Offset > 0 {
		if opts.Limit <= 0 {
			sqlText += "\nLIMIT -1"
		}
		sqlText += "\nOFFSET ?"
		args = append(args, opts.Offset)
	}

	return PreparedSearchQuery{SQL: sqlText, Args: args}, nil
}

func compileSafeSearchCountQuery(opts SearchQueryOptions, query SafeSearchQuery) (PreparedSearchQuery, error) {
	predicate, args, err := compileSafeSearchQueryPredicate(opts, query)
	if err != nil {
		return PreparedSearchQuery{}, err
	}
	sqlText := `SELECT COUNT(*)
FROM resources AS r
WHERE (? = 1 OR r.marked_for_delete = 0)
  AND (` + predicate.sql + `)`
	return PreparedSearchQuery{SQL: sqlText, Args: args}, nil
}

// ParseSearchQuery parses an NSX-T search query into typed query intent.
func ParseSearchQuery(query string) (SafeSearchQuery, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SafeSearchQuery{}, errEmptySearchQuery
	}

	tokens, err := tokenizeSearchQuery(query)
	if err != nil {
		return SafeSearchQuery{}, err
	}
	ast, err := parseSearchQuery(tokens)
	if err != nil {
		return SafeSearchQuery{}, err
	}
	if err = validateSearchNode(ast); err != nil {
		return SafeSearchQuery{}, err
	}
	return SafeSearchQuery{root: ast}, nil
}

// ParseDSLSearchQuery parses an NSX-T DSL search query into typed query intent.
func ParseDSLSearchQuery(query string) (SafeSearchQuery, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SafeSearchQuery{}, errEmptySearchQuery
	}
	tokens, err := tokenizeDSLSearchQuery(query)
	if err != nil {
		return SafeSearchQuery{}, err
	}
	ast, err := parseDSLSearchQuery(tokens)
	if err != nil {
		return SafeSearchQuery{}, err
	}
	if err = validateSearchNode(ast); err != nil {
		return SafeSearchQuery{}, err
	}
	return SafeSearchQuery{root: ast}, nil
}

func tokenizeDSLSearchQuery(query string) ([]dslToken, error) {
	tokens := []dslToken{}
	for pos := 0; pos < len(query); {
		next, ok := nextNonSpaceSearchByte(query, pos)
		if !ok {
			break
		}
		if next > pos {
			pos = next
			continue
		}

		token, tokenEnd, err := tokenizeNextDSLToken(query, pos)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
		pos = tokenEnd
	}
	tokens = append(tokens, dslToken{kind: dslTokenEOF})
	return tokens, nil
}

func tokenizeNextDSLToken(query string, pos int) (dslToken, int, error) {
	switch query[pos] {
	case '"':
		token, next, err := tokenizeSearchPhrase(query, pos)
		return dslToken{kind: dslTokenPhrase, text: token.text}, next, err
	case '!':
		if pos+1 < len(query) && query[pos+1] == '=' {
			return dslToken{kind: dslTokenNotEqual, text: "!="}, pos + compareOperatorWidth, nil
		}
		return dslToken{}, 0, fmt.Errorf("%w: unexpected %q at byte %d", errMalformedSearchQuery, query[pos], pos)
	case '<', '>':
		token, next := tokenizeSearchCompare(query, pos)
		return dslToken{kind: dslTokenCompare, text: token.text}, next, nil
	case '(', ')', '=':
		return tokenizeSingleByteDSLToken(query[pos]), pos + 1, nil
	default:
		text, next, err := scanDSLTermText(query, pos)
		if err != nil {
			return dslToken{}, 0, err
		}
		return classifyDSLTerm(text), next, nil
	}
}

func tokenizeSingleByteDSLToken(char byte) dslToken {
	switch char {
	case '(':
		return dslToken{kind: dslTokenLParen, text: "("}
	case ')':
		return dslToken{kind: dslTokenRParen, text: ")"}
	case '=':
		return dslToken{kind: dslTokenEqual, text: "="}
	default:
		return dslToken{kind: dslTokenEOF}
	}
}

func scanDSLTermText(query string, pos int) (string, int, error) {
	var builder strings.Builder
	current := pos
	for current < len(query) {
		char := query[current]
		if unicode.IsSpace(rune(char)) || strings.ContainsRune("=()<>!\"", rune(char)) {
			break
		}
		if char == '\\' {
			escaped, next, err := consumeEscapedSearchByte(query, current)
			if err != nil {
				return "", 0, err
			}
			builder.WriteByte(escaped)
			current = next
			continue
		}
		builder.WriteByte(char)
		current++
	}
	if builder.Len() == 0 {
		return "", 0, fmt.Errorf(
			"%w: unexpected character %q at byte %d",
			errMalformedSearchQuery,
			query[pos],
			pos,
		)
	}
	return builder.String(), current, nil
}

func classifyDSLTerm(text string) dslToken {
	switch strings.ToLower(text) {
	case "where":
		return dslToken{kind: dslTokenWhere, text: text}
	case "and":
		return dslToken{kind: dslTokenAnd, text: text}
	case "or":
		return dslToken{kind: dslTokenOr, text: text}
	case "like":
		return dslToken{kind: dslTokenLike, text: text}
	default:
		return dslToken{kind: dslTokenTerm, text: text}
	}
}

func parseDSLSearchQuery(tokens []dslToken) (searchNode, error) {
	parser := dslParser{tokens: tokens}
	entity := parser.peek()
	if entity.kind != dslTokenTerm && entity.kind != dslTokenPhrase {
		return nil, fmt.Errorf("%w: expected DSL entity type, got %q", errMalformedSearchQuery, entity.text)
	}
	parser.advance()
	entityField, err := newSearchFieldRef("resource_type")
	if err != nil {
		return nil, err
	}
	entityNode := searchTermNode{field: entityField, value: entity.text, phrase: entity.kind == dslTokenPhrase}
	if parser.match(dslTokenWhere) {
		predicate, parseErr := parser.parseOr()
		if parseErr != nil {
			return nil, parseErr
		}
		if parser.peek().kind != dslTokenEOF {
			return nil, fmt.Errorf("%w: unexpected DSL token %q", errMalformedSearchQuery, parser.peek().text)
		}
		return searchBoolNode{op: searchBoolAnd, left: entityNode, right: predicate}, nil
	}
	if parser.peek().kind != dslTokenEOF {
		return nil, fmt.Errorf("%w: unexpected DSL token %q", errMalformedSearchQuery, parser.peek().text)
	}
	return entityNode, nil
}

func (p *dslParser) parseOr() (searchNode, error) {
	node, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.match(dslTokenOr) {
		var right searchNode
		right, err = p.parseAnd()
		if err != nil {
			return nil, err
		}
		node = searchBoolNode{op: searchBoolOr, left: node, right: right}
	}
	return node, nil
}

func (p *dslParser) parseAnd() (searchNode, error) {
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.match(dslTokenAnd) {
		var right searchNode
		right, err = p.parsePrimary()
		if err != nil {
			return nil, err
		}
		node = searchBoolNode{op: searchBoolAnd, left: node, right: right}
	}
	return node, nil
}

func (p *dslParser) parsePrimary() (searchNode, error) {
	if p.match(dslTokenLParen) {
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.match(dslTokenRParen) {
			return nil, fmt.Errorf("%w: missing closing DSL parenthesis", errMalformedSearchQuery)
		}
		return node, nil
	}
	return p.parsePredicate()
}

func (p *dslParser) parsePredicate() (searchNode, error) {
	fieldToken := p.peek()
	if fieldToken.kind != dslTokenTerm {
		return nil, fmt.Errorf("%w: expected DSL property name, got %q", errMalformedSearchQuery, fieldToken.text)
	}
	p.advance()
	field, err := newSearchFieldRef(strings.ToLower(fieldToken.text))
	if err != nil {
		return nil, err
	}
	operator := p.advance()
	value, err := p.consumeTermOrPhrase("DSL property value")
	if err != nil {
		return nil, err
	}
	switch operator.kind {
	case dslTokenEqual:
		return searchTermNode{field: field, value: value.text, phrase: value.kind == dslTokenPhrase}, nil
	case dslTokenNotEqual:
		return searchNotNode{
			child: searchTermNode{field: field, value: value.text, phrase: value.kind == dslTokenPhrase},
		}, nil
	case dslTokenCompare:
		rangeOperator, parseErr := parseSearchRangeOperator(operator.text)
		if parseErr != nil {
			return nil, parseErr
		}
		return searchRangeNode{field: field, op: rangeOperator, value: value.text}, nil
	case dslTokenLike:
		return searchTermNode{field: field, value: "*" + value.text + "*"}, nil
	case dslTokenEOF, dslTokenTerm, dslTokenPhrase, dslTokenWhere, dslTokenAnd, dslTokenOr,
		dslTokenLParen, dslTokenRParen:
		return nil, fmt.Errorf("%w: expected DSL relational operator, got %q", errMalformedSearchQuery, operator.text)
	}
	return nil, fmt.Errorf("%w: unsupported DSL operator %q", errMalformedSearchQuery, operator.text)
}

func (p *dslParser) consumeTermOrPhrase(name string) (dslToken, error) {
	token := p.peek()
	if token.kind != dslTokenTerm && token.kind != dslTokenPhrase {
		return dslToken{}, fmt.Errorf("%w: expected %s, got %q", errMalformedSearchQuery, name, token.text)
	}
	p.advance()
	return token, nil
}

func (p *dslParser) match(kind dslTokenKind) bool {
	if p.peek().kind != kind {
		return false
	}
	p.advance()
	return true
}

func (p *dslParser) advance() dslToken {
	token := p.peek()
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return token
}

func (p *dslParser) peek() dslToken {
	if p.pos >= len(p.tokens) {
		return dslToken{kind: dslTokenEOF}
	}
	return p.tokens[p.pos]
}

func parseSearchQueryBySyntax(opts SearchQueryOptions) (SafeSearchQuery, error) {
	switch opts.Syntax {
	case "", SearchSyntaxQuery:
		return ParseSearchQuery(opts.Query)
	case SearchSyntaxDSL:
		return ParseDSLSearchQuery(opts.Query)
	default:
		return SafeSearchQuery{}, fmt.Errorf("%w: unsupported search syntax %q", errMalformedSearchQuery, opts.Syntax)
	}
}

func compileSafeSearchQueryPredicate(opts SearchQueryOptions, query SafeSearchQuery) (searchPredicate, []any, error) {
	if query.root == nil {
		return searchPredicate{}, nil, errEmptySearchQuery
	}
	predicate, err := compileSearchNode(query.root)
	if err != nil {
		return searchPredicate{}, nil, err
	}

	args := make([]any, 0, 1+len(predicate.args))
	includeMarkedForDelete := 0
	if opts.IncludeMarkedForDelete {
		includeMarkedForDelete = 1
	}
	args = append(args, includeMarkedForDelete)
	args = append(args, predicate.args...)
	return predicate, args, nil
}

func searchSelectSQL(predicateSQL string, orderSQL string) string {
	return `SELECT r.path,
       r.api_family,
       json(r.payload),
       r.revision,
       rr.valid_from_ms,
       rr.desired_status,
       rr.pending_status
FROM resources AS r
LEFT JOIN resource_realization AS rr
  ON rr.resource_path = r.path
 AND rr.enforcement_point_id = 'default'
WHERE (? = 1 OR r.marked_for_delete = 0)
  AND (` + predicateSQL + `)
` + orderSQL
}

func searchOrderSQL(opts SearchQueryOptions) (string, error) {
	direction := "ASC"
	if opts.SortBy != "" && !opts.SortAscending {
		direction = "DESC"
	}
	sortBy := opts.SortBy
	if sortBy == "" {
		sortBy = "display_name"
	}
	sortColumns := map[string]string{
		"display_name":        "r.display_name COLLATE NOCASE",
		"id":                  "r.id COLLATE NOCASE",
		"path":                "r.path COLLATE NOCASE",
		"parent_path":         "r.parent_path COLLATE NOCASE",
		"relative_path":       "r.relative_path COLLATE NOCASE",
		"resource_type":       "r.resource_type COLLATE NOCASE",
		"_revision":           "r.revision",
		"_create_time":        "r.create_time_ms",
		"_last_modified_time": "r.last_modified_time_ms",
	}
	sortColumn, ok := sortColumns[sortBy]
	if !ok {
		return "", fmt.Errorf("%w: %q", errUnsupportedSearchSort, sortBy)
	}
	return "ORDER BY " + sortColumn + " " + direction + ", r.id COLLATE NOCASE ASC", nil
}

// SearchQueryIncludesMarkedForDelete reports whether a query explicitly asks for tombstoned resources.
func SearchQueryIncludesMarkedForDelete(query string) (bool, error) {
	parsed, err := parseSearchQueryBySyntax(SearchQueryOptions{Query: query})
	if err != nil {
		return false, err
	}
	return searchNodeIncludesMarkedForDelete(parsed.root), nil
}

// SearchIncludesMarkedForDelete reports whether a search explicitly asks for tombstoned resources.
func SearchIncludesMarkedForDelete(opts SearchQueryOptions) (bool, error) {
	parsed, err := parseSearchQueryBySyntax(opts)
	if err != nil {
		return false, err
	}
	return searchNodeIncludesMarkedForDelete(parsed.root), nil
}

// IsSearchQueryError reports whether an error came from user-controlled search syntax or options.
func IsSearchQueryError(err error) bool {
	return errors.Is(err, errEmptySearchQuery) ||
		errors.Is(err, errMalformedSearchQuery) ||
		errors.Is(err, errUnknownSearchField) ||
		errors.Is(err, errInvalidSearchFieldUse) ||
		errors.Is(err, errInvalidSearchBool) ||
		errors.Is(err, errUnsupportedSearchSort)
}

// Search returns canonical resources matching an NSX-T search query.
func (s ResourceStore) Search(ctx context.Context, opts SearchQueryOptions) (resources []StoredResource, retErr error) {
	page, err := s.SearchPage(ctx, opts)
	if err != nil {
		return nil, err
	}
	return page.Resources, nil
}

// SearchPage returns canonical resources and total count matching an NSX-T search query.
func (s ResourceStore) SearchPage(ctx context.Context, opts SearchQueryOptions) (page SearchPage, retErr error) {
	s.logger.Info(
		"starting resource search",
		zap.String("syntax", string(opts.Syntax)),
		zap.String("query", opts.Query),
		zap.Bool("include_marked_for_delete", opts.IncludeMarkedForDelete),
		zap.Int("limit", opts.Limit),
		zap.Int("offset", opts.Offset),
		zap.String("sort_by", opts.SortBy),
		zap.Bool("sort_ascending", opts.SortAscending),
	)
	query, err := parseSearchQueryBySyntax(opts)
	if err != nil {
		return SearchPage{}, err
	}
	countQuery, err := compileSafeSearchCountQuery(opts, query)
	if err != nil {
		return SearchPage{}, err
	}
	var resultCount int
	if err = s.db.QueryRowContext(ctx, countQuery.SQL, countQuery.Args...).Scan(&resultCount); err != nil {
		return SearchPage{}, fmt.Errorf("count resource search: %w", err)
	}

	prepared, err := CompileSafeSearchQuery(opts, query)
	if err != nil {
		return SearchPage{}, err
	}
	s.logger.Debug(
		"compiled resource search query",
		zap.String("sql", prepared.SQL),
		zap.Int("arg_count", len(prepared.Args)),
	)

	stmt, err := s.db.PrepareContext(ctx, prepared.SQL)
	if err != nil {
		return SearchPage{}, fmt.Errorf("prepare resource search: %w", err)
	}
	defer closeStatement(&retErr, stmt, "resource search")

	rows, err := stmt.QueryContext(ctx, prepared.Args...)
	if err != nil {
		return SearchPage{}, fmt.Errorf("query resource search: %w", err)
	}
	defer closeRows(&retErr, rows, "resource search")

	resources, err := s.scanListedResources(rows)
	if err != nil {
		return SearchPage{}, err
	}
	cursor := ""
	if opts.Limit > 0 && opts.Offset+len(resources) < resultCount {
		cursor = strconv.Itoa(opts.Offset + len(resources))
	}
	s.logger.Info(
		"resource search completed",
		zap.String("syntax", string(opts.Syntax)),
		zap.String("query", opts.Query),
		zap.Int("resource_count", len(resources)),
		zap.Int("result_count", resultCount),
		zap.String("cursor", cursor),
	)
	return SearchPage{Resources: resources, ResultCount: resultCount, Cursor: cursor}, nil
}

func searchNodeIncludesMarkedForDelete(node searchNode) bool {
	switch typed := node.(type) {
	case searchTermNode:
		return typed.field.set && typed.field.name == "marked_for_delete" && strings.EqualFold(typed.value, "true")
	case searchBoolNode:
		return searchNodeIncludesMarkedForDelete(typed.left) || searchNodeIncludesMarkedForDelete(typed.right)
	case searchNotNode:
		return false
	default:
		return false
	}
}

func validateSearchNode(node searchNode) error {
	switch typed := node.(type) {
	case searchTermNode:
		return nil
	case searchRangeNode:
		if !typed.field.set {
			return fmt.Errorf("%w: range operator requires a field", errMalformedSearchQuery)
		}
		if _, err := typed.op.sql(); err != nil {
			return err
		}
		return nil
	case searchBoolNode:
		if _, err := typed.op.sql(); err != nil {
			return err
		}
		if err := validateSearchNode(typed.left); err != nil {
			return err
		}
		return validateSearchNode(typed.right)
	case searchNotNode:
		return validateSearchNode(typed.child)
	default:
		return fmt.Errorf("%w: unsupported search node", errMalformedSearchQuery)
	}
}

func newSearchFieldRef(field string) (searchFieldRef, error) {
	if field == "" {
		return searchFieldRef{}, nil
	}
	meta, err := requireSearchField(field)
	if err != nil {
		return searchFieldRef{}, err
	}
	return searchFieldRef{name: field, meta: meta, set: true}, nil
}

func tokenizeSearchQuery(query string) ([]searchToken, error) {
	tokens := []searchToken{}
	for pos := 0; pos < len(query); {
		next, ok := nextNonSpaceSearchByte(query, pos)
		if !ok {
			break
		}
		if next > pos {
			pos = next
			continue
		}

		token, next, err := tokenizeNextSearchToken(query, pos)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
		pos = next
	}
	tokens = append(tokens, searchToken{kind: searchTokenEOF})
	return tokens, nil
}

func nextNonSpaceSearchByte(query string, pos int) (int, bool) {
	for pos < len(query) {
		r, width := rune(query[pos]), 1
		if r >= utf8.RuneSelf {
			r, width = utf8.DecodeRuneInString(query[pos:])
		}
		if !unicode.IsSpace(r) {
			return pos, true
		}
		pos += width
	}
	return pos, false
}

func tokenizeNextSearchToken(query string, pos int) (searchToken, int, error) {
	switch query[pos] {
	case '"':
		return tokenizeSearchPhrase(query, pos)
	case ':':
		return searchToken{kind: searchTokenColon, text: ":"}, pos + 1, nil
	case '(':
		return searchToken{kind: searchTokenLParen, text: "("}, pos + 1, nil
	case ')':
		return searchToken{kind: searchTokenRParen, text: ")"}, pos + 1, nil
	case '&':
		return tokenizeRepeatedOperator(query, pos, '&', searchTokenAND, "&&")
	case '|':
		return tokenizeRepeatedOperator(query, pos, '|', searchTokenOR, "||")
	case '!':
		return searchToken{kind: searchTokenNOT, text: "!"}, pos + 1, nil
	case '<', '>':
		token, next := tokenizeSearchCompare(query, pos)
		return token, next, nil
	default:
		return tokenizeSearchTerm(query, pos)
	}
}

func tokenizeRepeatedOperator(
	query string,
	pos int,
	char byte,
	kind searchTokenKind,
	text string,
) (searchToken, int, error) {
	if pos+1 >= len(query) || query[pos+1] != char {
		return searchToken{}, 0, fmt.Errorf("%w: unexpected %q at byte %d", errMalformedSearchQuery, char, pos)
	}
	return searchToken{kind: kind, text: text}, pos + compareOperatorWidth, nil
}

func tokenizeSearchPhrase(query string, pos int) (searchToken, int, error) {
	var builder strings.Builder
	for current := pos + 1; current < len(query); current++ {
		switch query[current] {
		case '\\':
			current++
			if current >= len(query) {
				return searchToken{}, 0, fmt.Errorf("%w: dangling escape in phrase", errMalformedSearchQuery)
			}
			builder.WriteByte(query[current])
		case '"':
			return searchToken{kind: searchTokenPhrase, text: builder.String()}, current + 1, nil
		default:
			builder.WriteByte(query[current])
		}
	}
	return searchToken{}, 0, fmt.Errorf("%w: unterminated phrase", errMalformedSearchQuery)
}

func tokenizeSearchCompare(query string, pos int) (searchToken, int) {
	if pos+1 < len(query) && query[pos+1] == '=' {
		return searchToken{kind: searchTokenCompare, text: query[pos : pos+compareOperatorWidth]},
			pos + compareOperatorWidth
	}
	return searchToken{kind: searchTokenCompare, text: query[pos : pos+1]}, pos + 1
}

func tokenizeSearchTerm(query string, pos int) (searchToken, int, error) {
	text, current, err := scanSearchTermText(query, pos)
	if err != nil {
		return searchToken{}, 0, err
	}

	switch text {
	case "AND":
		return searchToken{kind: searchTokenAND, text: text}, current, nil
	case "OR":
		return searchToken{kind: searchTokenOR, text: text}, current, nil
	case "NOT":
		return searchToken{kind: searchTokenNOT, text: text}, current, nil
	case "and", "or", "not":
		return searchToken{}, 0, fmt.Errorf("%w: boolean operator %q must be uppercase", errMalformedSearchQuery, text)
	default:
		return searchToken{kind: searchTokenTerm, text: text}, current, nil
	}
}

func scanSearchTermText(query string, pos int) (string, int, error) {
	var builder strings.Builder
	current := pos
	for current < len(query) {
		char := query[current]
		if unicode.IsSpace(rune(char)) || strings.ContainsRune(":=()<>&|!\"", rune(char)) {
			break
		}
		if char == '\\' {
			escaped, next, err := consumeEscapedSearchByte(query, current)
			if err != nil {
				return "", 0, err
			}
			builder.WriteByte(escaped)
			current = next
			continue
		}
		builder.WriteByte(char)
		current++
	}
	if builder.Len() == 0 {
		return "", 0, fmt.Errorf(
			"%w: unexpected character %q at byte %d",
			errMalformedSearchQuery,
			query[pos],
			pos,
		)
	}
	return builder.String(), current, nil
}

func consumeEscapedSearchByte(query string, pos int) (byte, int, error) {
	next := pos + 1
	if next >= len(query) {
		return 0, 0, fmt.Errorf("%w: dangling escape in term", errMalformedSearchQuery)
	}
	return query[next], next + 1, nil
}

func parseSearchQuery(tokens []searchToken) (searchNode, error) {
	parser := searchParser{tokens: tokens}
	node, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.peek().kind != searchTokenEOF {
		return nil, fmt.Errorf("%w: unexpected token %q", errMalformedSearchQuery, parser.peek().text)
	}
	return node, nil
}

func (p *searchParser) parseOr() (searchNode, error) {
	node, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.match(searchTokenOR) {
		var right searchNode
		right, err = p.parseAnd()
		if err != nil {
			return nil, err
		}
		node = searchBoolNode{op: searchBoolOr, left: node, right: right}
	}
	return node, nil
}

func (p *searchParser) parseAnd() (searchNode, error) {
	node, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.match(searchTokenAND) {
		var right searchNode
		right, err = p.parseUnary()
		if err != nil {
			return nil, err
		}
		node = searchBoolNode{op: searchBoolAnd, left: node, right: right}
	}
	return node, nil
}

func (p *searchParser) parseUnary() (searchNode, error) {
	if p.match(searchTokenNOT) {
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return searchNotNode{child: child}, nil
	}
	return p.parsePrimary()
}

func (p *searchParser) parsePrimary() (searchNode, error) {
	token := p.peek()
	switch token.kind {
	case searchTokenLParen:
		return p.parseParenthesized()
	case searchTokenCompare:
		return p.parseUnfieldedRange()
	case searchTokenTerm:
		if p.peekN(1).kind == searchTokenColon {
			return p.parseFielded()
		}
		p.advance()
		return searchTermNode{value: token.text}, nil
	case searchTokenPhrase:
		p.advance()
		return searchTermNode{value: token.text, phrase: true}, nil
	case searchTokenEOF, searchTokenColon, searchTokenRParen, searchTokenAND, searchTokenOR, searchTokenNOT:
		return nil, fmt.Errorf("%w: expected search term, got %q", errMalformedSearchQuery, token.text)
	default:
		return nil, fmt.Errorf("%w: expected search term, got %q", errMalformedSearchQuery, token.text)
	}
}

func (p *searchParser) parseParenthesized() (searchNode, error) {
	p.advance()
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if !p.match(searchTokenRParen) {
		return nil, fmt.Errorf("%w: missing closing parenthesis", errMalformedSearchQuery)
	}
	return node, nil
}

func (p *searchParser) parseUnfieldedRange() (searchNode, error) {
	operator, err := parseSearchRangeOperator(p.advance().text)
	if err != nil {
		return nil, err
	}
	value, err := p.consumeTermOrPhrase("range value")
	if err != nil {
		return nil, err
	}
	return searchRangeNode{op: operator, value: value.text}, nil
}

func (p *searchParser) parseFielded() (searchNode, error) {
	field, err := newSearchFieldRef(p.advance().text)
	if err != nil {
		return nil, err
	}
	p.advance()

	if p.match(searchTokenLParen) {
		var node searchNode
		node, err = p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.match(searchTokenRParen) {
			return nil, fmt.Errorf("%w: missing closing field group parenthesis", errMalformedSearchQuery)
		}
		return applySearchField(node, field), nil
	}

	if p.peek().kind == searchTokenCompare {
		var operator searchRangeOperator
		operator, err = parseSearchRangeOperator(p.advance().text)
		if err != nil {
			return nil, err
		}
		var value searchToken
		value, err = p.consumeTermOrPhrase("range value")
		if err != nil {
			return nil, err
		}
		return searchRangeNode{field: field, op: operator, value: value.text}, nil
	}

	value, err := p.consumeTermOrPhrase("field value")
	if err != nil {
		return nil, err
	}
	return searchTermNode{field: field, value: value.text, phrase: value.kind == searchTokenPhrase}, nil
}

func (p *searchParser) consumeTermOrPhrase(name string) (searchToken, error) {
	token := p.peek()
	if token.kind != searchTokenTerm && token.kind != searchTokenPhrase {
		return searchToken{}, fmt.Errorf("%w: expected %s, got %q", errMalformedSearchQuery, name, token.text)
	}
	p.advance()
	return token, nil
}

func (p *searchParser) match(kind searchTokenKind) bool {
	if p.peek().kind != kind {
		return false
	}
	p.advance()
	return true
}

func (p *searchParser) advance() searchToken {
	token := p.peek()
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return token
}

func (p *searchParser) peek() searchToken {
	return p.peekN(0)
}

func (p *searchParser) peekN(offset int) searchToken {
	index := p.pos + offset
	if index >= len(p.tokens) {
		return searchToken{kind: searchTokenEOF}
	}
	return p.tokens[index]
}

func applySearchField(node searchNode, field searchFieldRef) searchNode {
	switch typed := node.(type) {
	case searchTermNode:
		if !typed.field.set {
			typed.field = field
		}
		return typed
	case searchRangeNode:
		if !typed.field.set {
			typed.field = field
		}
		return typed
	case searchBoolNode:
		typed.left = applySearchField(typed.left, field)
		typed.right = applySearchField(typed.right, field)
		return typed
	case searchNotNode:
		typed.child = applySearchField(typed.child, field)
		return typed
	default:
		return node
	}
}

func compileSearchNode(node searchNode) (searchPredicate, error) {
	switch typed := node.(type) {
	case searchTermNode:
		return compileSearchTerm(typed)
	case searchRangeNode:
		return compileSearchRange(typed)
	case searchBoolNode:
		return compileSearchBool(typed)
	case searchNotNode:
		child, err := compileSearchNode(typed.child)
		if err != nil {
			return searchPredicate{}, err
		}
		return searchPredicate{sql: "NOT (" + child.sql + ")", args: child.args}, nil
	default:
		return searchPredicate{}, fmt.Errorf("%w: unsupported search node", errMalformedSearchQuery)
	}
}

func compileSearchBool(node searchBoolNode) (searchPredicate, error) {
	left, err := compileSearchNode(node.left)
	if err != nil {
		return searchPredicate{}, err
	}
	right, err := compileSearchNode(node.right)
	if err != nil {
		return searchPredicate{}, err
	}
	operator, err := node.op.sql()
	if err != nil {
		return searchPredicate{}, err
	}
	args := make([]any, 0, len(left.args)+len(right.args))
	args = append(args, left.args...)
	args = append(args, right.args...)
	return searchPredicate{
		sql:  "(" + left.sql + ") " + operator + " (" + right.sql + ")",
		args: args,
	}, nil
}

func compileSearchTerm(node searchTermNode) (searchPredicate, error) {
	if node.value == "" {
		return searchPredicate{}, fmt.Errorf("%w: empty search term", errMalformedSearchQuery)
	}
	if !node.field.set {
		return compileUnfieldedSearchTerm(node), nil
	}

	switch node.field.meta.kind {
	case searchFieldText:
		return compileTextSearchTerm(node), nil
	case searchFieldBool:
		return compileBoolSearchTerm(node)
	case searchFieldNumber:
		return compileNumberSearchTerm(node)
	default:
		return searchPredicate{}, fmt.Errorf("%w: field %q has unsupported kind", errInvalidSearchFieldUse, node.field.name)
	}
}

func compileUnfieldedSearchTerm(node searchTermNode) searchPredicate {
	if containsSearchWildcard(node.value) {
		return searchPredicate{
			sql: `EXISTS (
	SELECT 1
	  FROM search_fields AS sf
	 WHERE sf.resource_path = r.path
	   AND sf.text_value LIKE ? ESCAPE '\'
)`,
			args: []any{wildcardLikePattern(node.value)},
		}
	}
	return searchPredicate{
		sql: `EXISTS (
	SELECT 1
	  FROM resource_fts
	 WHERE resource_fts.resource_path = r.path
	   AND resource_fts MATCH ?
)`,
		args: []any{ftsPhraseQuery(node.value)},
	}
}

func compileTextSearchTerm(node searchTermNode) searchPredicate {
	if containsSearchWildcard(node.value) {
		return searchPredicate{
			sql: `EXISTS (
	SELECT 1
	  FROM search_fields AS sf
	 WHERE sf.resource_path = r.path
	   AND sf.field_name = ?
	   AND sf.text_value LIKE ? ESCAPE '\'
)`,
			args: []any{node.field.name, wildcardLikePattern(node.value)},
		}
	}
	return searchPredicate{
		sql: `EXISTS (
	SELECT 1
	  FROM search_fields AS sf
	 WHERE sf.resource_path = r.path
	   AND sf.field_name = ?
	   AND sf.text_value = ?
)`,
		args: []any{node.field.name, strings.ToLower(node.value)},
	}
}

func compileBoolSearchTerm(node searchTermNode) (searchPredicate, error) {
	value, err := parseSearchBool(node.value)
	if err != nil {
		return searchPredicate{}, fmt.Errorf(
			"%w: field %q expects boolean value %q",
			errInvalidSearchFieldUse,
			node.field.name,
			node.value,
		)
	}
	return searchPredicate{
		sql: `EXISTS (
	SELECT 1
	  FROM search_fields AS sf
	 WHERE sf.resource_path = r.path
	   AND sf.field_name = ?
	   AND sf.bool_value = ?
)`,
		args: []any{node.field.name, value},
	}, nil
}

func compileNumberSearchTerm(node searchTermNode) (searchPredicate, error) {
	value, err := strconv.ParseFloat(node.value, 64)
	if err != nil {
		return searchPredicate{}, fmt.Errorf(
			"%w: field %q expects numeric value %q",
			errInvalidSearchFieldUse,
			node.field.name,
			node.value,
		)
	}
	return searchPredicate{
		sql: `EXISTS (
	SELECT 1
	  FROM search_fields AS sf
	 WHERE sf.resource_path = r.path
	   AND sf.field_name = ?
	   AND sf.number_value = ?
)`,
		args: []any{node.field.name, value},
	}, nil
}

func compileSearchRange(node searchRangeNode) (searchPredicate, error) {
	if !node.field.set {
		return searchPredicate{}, fmt.Errorf("%w: range operator requires a field", errMalformedSearchQuery)
	}
	operator, err := node.op.sql()
	if err != nil {
		return searchPredicate{}, err
	}
	switch node.field.meta.kind {
	case searchFieldNumber:
		return compileNumberSearchRange(node, operator)
	case searchFieldText:
		return searchPredicate{
			sql: `EXISTS (
	SELECT 1
	  FROM search_fields AS sf
	 WHERE sf.resource_path = r.path
	   AND sf.field_name = ?
	   AND sf.text_value ` + operator + ` ?
	)`,
			args: []any{node.field.name, strings.ToLower(node.value)},
		}, nil
	case searchFieldBool:
		return searchPredicate{}, fmt.Errorf(
			"%w: field %q does not support range comparisons",
			errInvalidSearchFieldUse,
			node.field.name,
		)
	default:
		return searchPredicate{}, fmt.Errorf(
			"%w: field %q does not support range comparisons",
			errInvalidSearchFieldUse,
			node.field.name,
		)
	}
}

func compileNumberSearchRange(node searchRangeNode, operator string) (searchPredicate, error) {
	value, err := strconv.ParseFloat(node.value, 64)
	if err != nil {
		return searchPredicate{}, fmt.Errorf(
			"%w: field %q expects numeric range value %q",
			errInvalidSearchFieldUse,
			node.field.name,
			node.value,
		)
	}
	return searchPredicate{
		sql: `EXISTS (
	SELECT 1
	  FROM search_fields AS sf
	 WHERE sf.resource_path = r.path
	   AND sf.field_name = ?
	   AND sf.number_value ` + operator + ` ?
)`,
		args: []any{node.field.name, value},
	}, nil
}

func requireSearchField(field string) (searchFieldMeta, error) {
	meta, ok := searchableFields[field]
	if !ok {
		return searchFieldMeta{}, fmt.Errorf("%w: %q", errUnknownSearchField, field)
	}
	return meta, nil
}

func parseSearchBool(value string) (int, error) {
	switch strings.ToLower(value) {
	case "true":
		return 1, nil
	case "false":
		return 0, nil
	default:
		return 0, fmt.Errorf("%w: %q", errInvalidSearchBool, value)
	}
}

func containsSearchWildcard(value string) bool {
	return strings.ContainsAny(value, "*?")
}

func wildcardLikePattern(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		switch char {
		case '*':
			builder.WriteByte('%')
		case '?':
			builder.WriteByte('_')
		case '%', '_', '\\':
			builder.WriteByte('\\')
			builder.WriteRune(char)
		default:
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func ftsPhraseQuery(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
