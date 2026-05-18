package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"nsx-t-mockapi/internal/clock"
)

func TestResourceStoreSearchFieldedANDQueryReturnsOnlyMatchingResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := NewResourceStore(db, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"description":"frontend payment gateway"
	}`))
	createTestResource(ctx, t, store, managerIPSetResourceSpec("web-alpha-service"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"description":"same name wrong type"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("db-beta"), json.RawMessage(`{
		"display_name":"DbBeta",
		"description":"database"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "display_name:WebAlpha AND resource_type:Group",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-alpha")
}

func TestResourceStoreSearchDSLWherePredicateReturnsMatchingResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"tags":[{"scope":"prod","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("web-dev"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"tags":[{"scope":"dev","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("db-beta"), json.RawMessage(`{
		"display_name":"DbBeta",
		"tags":[{"scope":"prod","tag":"database"}]
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Syntax: SearchSyntaxDSL,
		Query:  "Group where display_name = WebAlpha and tags.scope = prod",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-alpha")
}

func TestResourceStoreSearchDSLLikeAndRangePredicatesReturnMatchingResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("app-alpha"), json.RawMessage(`{
		"display_name":"AppAlpha"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("app-beta"), json.RawMessage(`{
		"display_name":"AppBeta"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("db-alpha"), json.RawMessage(`{
		"display_name":"DbAlpha"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Syntax: SearchSyntaxDSL,
		Query:  `group WHERE display_name LIKE app and _revision >= 0`,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results,
		"/infra/domains/default/groups/app-alpha",
		"/infra/domains/default/groups/app-beta",
	)
}

func TestResourceStoreSearchDSLParenthesesOrNotEqualAndQuotedValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("app-alpha"), json.RawMessage(`{
		"display_name":"App Alpha",
		"tags":[{"scope":"prod","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("app-beta"), json.RawMessage(`{
		"display_name":"App Beta",
		"tags":[{"scope":"dev","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("db-alpha"), json.RawMessage(`{
		"display_name":"Db Alpha",
		"tags":[{"scope":"prod","tag":"database"}]
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Syntax: SearchSyntaxDSL,
		Query:  `Group where (display_name = "App Alpha" or display_name = "App Beta") and tags.scope != dev`,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/app-alpha")
}

func TestResourceStoreSearchDSLEscapedValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("app-alpha"), json.RawMessage(`{
		"display_name":"App Alpha"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Syntax: SearchSyntaxDSL,
		Query:  `Group where display_name = App\ Alpha`,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/app-alpha")
}

func TestSearchIncludesMarkedForDeleteSupportsDSL(t *testing.T) {
	t.Parallel()

	includeMarkedForDelete, err := SearchIncludesMarkedForDelete(SearchQueryOptions{
		Syntax: SearchSyntaxDSL,
		Query:  "Group where marked_for_delete = true",
	})
	if err != nil {
		t.Fatalf("SearchIncludesMarkedForDelete() error = %v", err)
	}
	if !includeMarkedForDelete {
		t.Fatal("SearchIncludesMarkedForDelete() = false, want true")
	}

	_, err = SearchIncludesMarkedForDelete(SearchQueryOptions{
		Syntax: SearchSyntaxDSL,
		Query:  "Group where marked_for_delete =",
	})
	if !IsSearchQueryError(err) {
		t.Fatalf("SearchIncludesMarkedForDelete() error = %v, want search query error", err)
	}
}

func TestResourceStoreSearchDSLRejectsMalformedQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "empty", query: ""},
		{name: "missing entity", query: "where display_name = App"},
		{name: "missing predicate", query: "Group where"},
		{name: "unexpected entity tail", query: "Group trailing"},
		{name: "missing closing parenthesis", query: "Group where (display_name = App"},
		{name: "dangling escape", query: `Group where display_name = App\`},
		{name: "unexpected bang", query: "Group where display_name ! App"},
		{name: "unknown field", query: "Group where unsupported = App"},
		{name: "unsupported syntax", query: "Group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := SearchQueryOptions{Syntax: SearchSyntaxDSL, Query: tc.query}
			if tc.name == "unsupported syntax" {
				opts.Syntax = SearchSyntax("unsupported")
			}
			_, err := store.Search(ctx, opts)
			if !IsSearchQueryError(err) {
				t.Fatalf("Search() error = %v, want search query error", err)
			}
		})
	}
}

func TestResourceStoreSearchFieldedORQueryReturnsEitherMatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("db-beta"), json.RawMessage(`{
		"display_name":"DbBeta"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("cache-gamma"), json.RawMessage(`{
		"display_name":"CacheGamma"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "display_name:WebAlpha OR display_name:DbBeta",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results,
		"/infra/domains/default/groups/db-beta",
		"/infra/domains/default/groups/web-alpha",
	)
}

func TestResourceStoreSearchParenthesizedBooleanQueryHonorsGrouping(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha"
	}`))
	createTestResource(ctx, t, store, managerIPSetResourceSpec("db-beta"), json.RawMessage(`{
		"display_name":"DbBeta"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("cache-gamma"), json.RawMessage(`{
		"display_name":"CacheGamma"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "(display_name:WebAlpha OR display_name:DbBeta) AND resource_type:Group",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-alpha")
}

func TestResourceStoreSearchSymbolicBooleanAndNegation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"tags":[{"scope":"prod","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("web-dev"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"tags":[{"scope":"dev","tag":"frontend"}]
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "display_name:WebAlpha && !tags.scope:dev",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-alpha")
}

func TestResourceStoreSearchQuotedPhraseAndFreeTextUseFullTextIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"description":"handles payment gateway traffic",
		"tags":[{"scope":"tier","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("db-beta"), json.RawMessage(`{
		"display_name":"DbBeta",
		"description":"payment ledger backend"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: `"payment gateway" AND frontend`,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-alpha")
}

func TestResourceStoreSearchNestedTagFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha",
		"tags":[{"scope":"prod","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("web-dev"), json.RawMessage(`{
		"display_name":"WebDev",
		"tags":[{"scope":"dev","tag":"frontend"}]
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("worker-prod"), json.RawMessage(`{
		"display_name":"WorkerProd",
		"tags":[{"scope":"prod","tag":"worker"}]
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "tags.scope:prod AND tags.tag:frontend",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-alpha")
}

func TestResourceStoreSearchWildcardTermsUseLIKEFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("app-vm-1"), json.RawMessage(`{
		"display_name":"App-VM-1"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("app-vm-22"), json.RawMessage(`{
		"display_name":"App-VM-22"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("db-vm-1"), json.RawMessage(`{
		"display_name":"Db-VM-1"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "display_name:App-VM-?",
	})
	if err != nil {
		t.Fatalf("Search() single-character wildcard error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/app-vm-1")

	results, err = store.Search(ctx, SearchQueryOptions{
		Query: "display_name:App*",
	})
	if err != nil {
		t.Fatalf("Search() prefix wildcard error = %v", err)
	}
	requireResourcePaths(t, results,
		"/infra/domains/default/groups/app-vm-1",
		"/infra/domains/default/groups/app-vm-22",
	)

	results, err = store.Search(ctx, SearchQueryOptions{
		Query: "*vm*",
	})
	if err != nil {
		t.Fatalf("Search() unfielded wildcard error = %v", err)
	}
	requireResourcePaths(t, results,
		"/infra/domains/default/groups/app-vm-1",
		"/infra/domains/default/groups/app-vm-22",
		"/infra/domains/default/groups/db-vm-1",
	)
}

func TestResourceStoreSearchNumericRangesAndFieldScopedRangeGroups(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("vni-low"), json.RawMessage(`{
		"display_name":"VniLow",
		"vni":40000
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("vni-mid"), json.RawMessage(`{
		"display_name":"VniMid",
		"vni":50001
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("vni-high"), json.RawMessage(`{
		"display_name":"VniHigh",
		"vni":90000
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "vni:>=50001 AND vni:<90000",
	})
	if err != nil {
		t.Fatalf("Search() numeric ranges error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/vni-mid")

	results, err = store.Search(ctx, SearchQueryOptions{
		Query: "vni:(>=50001 AND <90000)",
	})
	if err != nil {
		t.Fatalf("Search() field-scoped range group error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/vni-mid")
}

func TestResourceStoreSearchBoolNumericExactEscapesAndTombstoneOptions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"Web Alpha",
		"vni":50001
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("web-beta"), json.RawMessage(`{
		"display_name":"Web Beta",
		"vni":50002
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("deleted"), json.RawMessage(`{
		"display_name":"Deleted Resource"
	}`))
	deleteTestResource(ctx, t, store, groupResourceSpec("deleted"))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "_system_owned:false AND _revision:0 AND display_name:Web\\ Alpha",
	})
	if err != nil {
		t.Fatalf("Search() bool/numeric/escaped exact error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-alpha")

	results, err = store.Search(ctx, SearchQueryOptions{
		Query:                  "marked_for_delete:true",
		IncludeMarkedForDelete: true,
	})
	if err != nil {
		t.Fatalf("Search() include tombstone bool error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/deleted")

	results, err = store.Search(ctx, SearchQueryOptions{
		Query:  "_system_owned:false",
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("Search() limit offset error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/web-beta")
}

func TestResourceStoreSearchPageReturnsTotalCursorAndSort(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("alpha"), json.RawMessage(`{
		"display_name":"Alpha"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("beta"), json.RawMessage(`{
		"display_name":"Beta"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("gamma"), json.RawMessage(`{
		"display_name":"Gamma"
	}`))

	page, err := store.SearchPage(ctx, SearchQueryOptions{
		Query:         "resource_type:Group",
		Limit:         2,
		SortBy:        "display_name",
		SortAscending: false,
	})
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if page.ResultCount != 3 {
		t.Fatalf("ResultCount = %d, want 3", page.ResultCount)
	}
	if page.Cursor != "2" {
		t.Fatalf("Cursor = %q, want 2", page.Cursor)
	}
	requireResourcePaths(t, page.Resources,
		"/infra/domains/default/groups/gamma",
		"/infra/domains/default/groups/beta",
	)

	_, err = store.SearchPage(ctx, SearchQueryOptions{
		Query:  "resource_type:Group",
		SortBy: "unsupported",
	})
	if !IsSearchQueryError(err) {
		t.Fatalf("SearchPage() unsupported sort error = %v, want search query error", err)
	}
}

func TestSearchQueryIncludesMarkedForDeleteUsesParsedQuerySemantics(t *testing.T) {
	t.Parallel()

	includes, err := SearchQueryIncludesMarkedForDelete("marked_for_delete:true AND resource_type:Group")
	if err != nil {
		t.Fatalf("SearchQueryIncludesMarkedForDelete() error = %v", err)
	}
	if !includes {
		t.Fatal("SearchQueryIncludesMarkedForDelete() = false, want true")
	}

	includes, err = SearchQueryIncludesMarkedForDelete("NOT marked_for_delete:true")
	if err != nil {
		t.Fatalf("SearchQueryIncludesMarkedForDelete() negated error = %v", err)
	}
	if includes {
		t.Fatal("SearchQueryIncludesMarkedForDelete() negated = true, want false")
	}

	includes, err = SearchQueryIncludesMarkedForDelete(
		"resource_type:Group AND (marked_for_delete:true OR display_name:Deleted*)",
	)
	if err != nil {
		t.Fatalf("SearchQueryIncludesMarkedForDelete() grouped positive error = %v", err)
	}
	if !includes {
		t.Fatal("SearchQueryIncludesMarkedForDelete() grouped positive = false, want true")
	}

	includes, err = SearchQueryIncludesMarkedForDelete(
		"resource_type:Group AND !(marked_for_delete:true OR display_name:Deleted*)",
	)
	if err != nil {
		t.Fatalf("SearchQueryIncludesMarkedForDelete() grouped negated error = %v", err)
	}
	if includes {
		t.Fatal("SearchQueryIncludesMarkedForDelete() grouped negated = true, want false")
	}

	_, err = SearchQueryIncludesMarkedForDelete("resource_type:Group AND")
	if !IsSearchQueryError(err) {
		t.Fatalf("IsSearchQueryError(%v) = false, want true", err)
	}
}

func TestResourceStoreSearchTextRangeUsesLexicalFieldComparison(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("alpha"), json.RawMessage(`{
		"display_name":"Alpha"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("beta"), json.RawMessage(`{
		"display_name":"Beta"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("gamma"), json.RawMessage(`{
		"display_name":"Gamma"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: "display_name:(>=Beta AND <Gamma)",
	})
	if err != nil {
		t.Fatalf("Search() text range error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/beta")
}

func TestCompileSearchQueryReturnsDeterministicErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		query string
		want  error
	}{
		{name: "unknown field", query: "unknown_field:value", want: errUnknownSearchField},
		{name: "lowercase boolean", query: "display_name:WebAlpha and resource_type:Group", want: errMalformedSearchQuery},
		{name: "unterminated phrase", query: `"payment gateway`, want: errMalformedSearchQuery},
		{name: "dangling escape", query: `display_name:Web\`, want: errMalformedSearchQuery},
		{name: "single ampersand", query: `display_name:Web & resource_type:Group`, want: errMalformedSearchQuery},
		{name: "dangling operator", query: "display_name:WebAlpha AND", want: errMalformedSearchQuery},
		{name: "invalid numeric range", query: "vni:>=not-a-number", want: errInvalidSearchFieldUse},
		{name: "bool range", query: "marked_for_delete:>false", want: errInvalidSearchFieldUse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := CompileSearchQuery(SearchQueryOptions{Query: tc.query})
			if !errors.Is(err, tc.want) {
				t.Fatalf("CompileSearchQuery(%q) error = %v, want %v", tc.query, err, tc.want)
			}
		})
	}
}

func TestParseSearchQueryReturnsSafeObjectAndCompileUsesPreparedSQL(t *testing.T) {
	t.Parallel()

	opts := SearchQueryOptions{
		Query: "display_name:WebAlpha AND resource_type:Group",
		Limit: 5,
	}
	parsed, err := ParseSearchQuery(opts.Query)
	if err != nil {
		t.Fatalf("ParseSearchQuery() error = %v", err)
	}
	rendered := strings.ToLower(fmt.Sprintf("%#v", parsed))
	for _, sqlFragment := range []string{"select ", "exists ", "search_fields", "resource_fts", "sf."} {
		if strings.Contains(rendered, sqlFragment) {
			t.Fatalf("safe query object contains SQL fragment %q: %#v", sqlFragment, parsed)
		}
	}

	preparedFromSafe, err := CompileSafeSearchQuery(opts, parsed)
	if err != nil {
		t.Fatalf("CompileSafeSearchQuery() error = %v", err)
	}
	preparedFromString, err := CompileSearchQuery(opts)
	if err != nil {
		t.Fatalf("CompileSearchQuery() error = %v", err)
	}
	if preparedFromSafe.SQL != preparedFromString.SQL {
		t.Fatalf(
			"CompileSafeSearchQuery() SQL mismatch\nsafe:\n%s\nstring:\n%s",
			preparedFromSafe.SQL,
			preparedFromString.SQL,
		)
	}
	if !reflect.DeepEqual(preparedFromSafe.Args, preparedFromString.Args) {
		t.Fatalf("CompileSafeSearchQuery() args = %#v, want %#v", preparedFromSafe.Args, preparedFromString.Args)
	}
}

func TestParseSearchQueryRejectsUnwhitelistedFields(t *testing.T) {
	t.Parallel()

	for _, query := range []string{
		"unknown_field:value",
		"display_name\\)\\ OR\\ 1\\=1\\ --:WebAlpha",
	} {
		_, err := ParseSearchQuery(query)
		if !errors.Is(err, errUnknownSearchField) {
			t.Fatalf("ParseSearchQuery(%q) error = %v, want %v", query, err, errUnknownSearchField)
		}
	}
}

func TestCompileSearchQueryUsesHardCodedRangeOperatorsAndBoundArgs(t *testing.T) {
	t.Parallel()

	prepared, err := CompileSearchQuery(SearchQueryOptions{Query: "vni:>=50001"})
	if err != nil {
		t.Fatalf("CompileSearchQuery() error = %v", err)
	}
	if !strings.Contains(prepared.SQL, "sf.number_value >= ?") {
		t.Fatalf("compiled SQL does not contain fixed range operator placeholder:\n%s", prepared.SQL)
	}
	if strings.Contains(prepared.SQL, "50001") || strings.Contains(prepared.SQL, "vni") {
		t.Fatalf("compiled SQL contains user range data instead of bound args:\n%s", prepared.SQL)
	}
	requireArgsContain(t, prepared.Args, "vni", 50001.0)

	boundedPrepared, err := CompileSearchQuery(SearchQueryOptions{Query: "vni:>50000 AND vni:<=50001"})
	if err != nil {
		t.Fatalf("CompileSearchQuery() bounded range error = %v", err)
	}
	if !strings.Contains(boundedPrepared.SQL, "sf.number_value > ?") ||
		!strings.Contains(boundedPrepared.SQL, "sf.number_value <= ?") {
		t.Fatalf("compiled SQL does not contain fixed bounded range operators:\n%s", boundedPrepared.SQL)
	}
	requireArgsContain(t, boundedPrepared.Args, "vni", 50000.0, 50001.0)

	_, err = ParseSearchQuery("vni:=>50001")
	if !errors.Is(err, errMalformedSearchQuery) {
		t.Fatalf("ParseSearchQuery() invalid range operator error = %v, want %v", err, errMalformedSearchQuery)
	}
}

func TestCompileSearchQueryRoutesTermsToFTSAndWildcardsToFallback(t *testing.T) {
	t.Parallel()

	ftsPrepared, err := CompileSearchQuery(SearchQueryOptions{Query: `"payment gateway" AND frontend`})
	if err != nil {
		t.Fatalf("CompileSearchQuery() FTS error = %v", err)
	}
	if !strings.Contains(ftsPrepared.SQL, "resource_fts MATCH ?") {
		t.Fatalf("compiled FTS SQL does not use FTS5 MATCH placeholder:\n%s", ftsPrepared.SQL)
	}
	requireArgsContain(t, ftsPrepared.Args, `"payment gateway"`, `"frontend"`)

	wildcardPrepared, err := CompileSearchQuery(SearchQueryOptions{Query: "*vm*"})
	if err != nil {
		t.Fatalf("CompileSearchQuery() wildcard error = %v", err)
	}
	if !strings.Contains(wildcardPrepared.SQL, "FROM search_fields AS sf") ||
		!strings.Contains(wildcardPrepared.SQL, "sf.text_value LIKE ? ESCAPE '\\'") {
		t.Fatalf("compiled wildcard SQL does not use LIKE fallback placeholder:\n%s", wildcardPrepared.SQL)
	}
	requireArgsContain(t, wildcardPrepared.Args, "%vm%")
}

func TestCompileSafeSearchQueryRejectsMalformedInternalQuery(t *testing.T) {
	t.Parallel()

	_, err := CompileSafeSearchQuery(SearchQueryOptions{}, SafeSearchQuery{
		root: searchRangeNode{op: searchRangeGreaterThan, value: "5"},
	})
	if !errors.Is(err, errMalformedSearchQuery) {
		t.Fatalf("CompileSafeSearchQuery() unfielded range error = %v, want %v", err, errMalformedSearchQuery)
	}

	vniField, err := newSearchFieldRef("vni")
	if err != nil {
		t.Fatalf("newSearchFieldRef() error = %v", err)
	}
	_, err = CompileSafeSearchQuery(SearchQueryOptions{}, SafeSearchQuery{
		root: searchRangeNode{field: vniField, op: searchRangeOperator(99), value: "5"},
	})
	if !errors.Is(err, errMalformedSearchQuery) {
		t.Fatalf("CompileSafeSearchQuery() invalid range operator error = %v, want %v", err, errMalformedSearchQuery)
	}

	resourceTypeField, err := newSearchFieldRef("resource_type")
	if err != nil {
		t.Fatalf("newSearchFieldRef() resource_type error = %v", err)
	}
	_, err = CompileSafeSearchQuery(SearchQueryOptions{}, SafeSearchQuery{
		root: searchBoolNode{
			op:    searchBoolOperator(99),
			left:  searchTermNode{field: resourceTypeField, value: "Group"},
			right: searchTermNode{field: resourceTypeField, value: "IPSet"},
		},
	})
	if !errors.Is(err, errMalformedSearchQuery) {
		t.Fatalf("CompileSafeSearchQuery() invalid bool operator error = %v, want %v", err, errMalformedSearchQuery)
	}
}

func TestCompileSearchQueryUsesPlaceholdersForUserValues(t *testing.T) {
	t.Parallel()

	injection := `WebAlpha;DROP_TABLE_resources`
	prepared, err := CompileSearchQuery(SearchQueryOptions{
		Query: "display_name:" + injection,
	})
	if err != nil {
		t.Fatalf("CompileSearchQuery() error = %v", err)
	}
	if strings.Contains(prepared.SQL, injection) {
		t.Fatalf("compiled SQL contains user value:\n%s", prepared.SQL)
	}
	if !strings.Contains(prepared.SQL, "sf.field_name = ?") || !strings.Contains(prepared.SQL, "sf.text_value = ?") {
		t.Fatalf("compiled SQL does not use expected placeholders:\n%s", prepared.SQL)
	}
	requireArgsContain(t, prepared.Args, "display_name", strings.ToLower(injection))
}

func TestResourceStoreSearchBoundValuesPreventSQLInjectionMatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: `display_name:WebAlpha;DROP_TABLE_resources`,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	requireResourcePaths(t, results)
}

func TestResourceStoreSearchTreatsSQLAndLikeMetacharactersAsData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openResourceStoreTestDB(t)
	store := newSearchTestStore(db)

	createTestResource(ctx, t, store, groupResourceSpec("web-alpha"), json.RawMessage(`{
		"display_name":"WebAlpha"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("literal-like"), json.RawMessage(`{
		"display_name":"App%VM_1"
	}`))
	createTestResource(ctx, t, store, groupResourceSpec("expanded-like"), json.RawMessage(`{
		"display_name":"AppXVMY1"
	}`))

	results, err := store.Search(ctx, SearchQueryOptions{
		Query: `display_name:WebAlpha'\ OR\ 1\=1\ --`,
	})
	if err != nil {
		t.Fatalf("Search() escaped SQL-looking value error = %v", err)
	}
	requireResourcePaths(t, results)

	results, err = store.Search(ctx, SearchQueryOptions{
		Query: `display_name:App%VM_?`,
	})
	if err != nil {
		t.Fatalf("Search() wildcard with LIKE metacharacters error = %v", err)
	}
	requireResourcePaths(t, results, "/infra/domains/default/groups/literal-like")
}

func newSearchTestStore(sqlDB *sql.DB) ResourceStore {
	return NewResourceStore(sqlDB, ResourceStoreOptions{
		Clock: clock.NewFakeClock(time.Date(2026, 5, 17, 10, 30, 0, 0, time.UTC)),
	})
}

func requireArgsContain(t *testing.T, args []any, want ...any) {
	t.Helper()

	for _, expected := range want {
		if !slices.Contains(args, expected) {
			t.Fatalf("args %#v do not contain %#v", args, expected)
		}
	}
}
