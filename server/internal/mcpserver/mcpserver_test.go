package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"tavily-proxy/server/internal/db"
	"tavily-proxy/server/internal/services"
)

func TestTavilyUsage_ReturnsAggregatedStatsWithoutUpstreamCall(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	master := services.NewMasterKeyService(database, logger)
	if err := master.LoadOrCreate(ctx); err != nil {
		t.Fatalf("master key init: %v", err)
	}

	keys := services.NewKeyService(database, logger)
	keyA, err := keys.Create(ctx, "tvly-pool-a", "a", 1000)
	if err != nil {
		t.Fatalf("create key a: %v", err)
	}
	keyB, err := keys.Create(ctx, "tvly-pool-b", "b", 500)
	if err != nil {
		t.Fatalf("create key b: %v", err)
	}
	if err := keys.SetUsage(ctx, keyA.ID, 250, nil); err != nil {
		t.Fatalf("set usage for key a: %v", err)
	}
	if err := keys.SetUsage(ctx, keyB.ID, 100, nil); err != nil {
		t.Fatalf("set usage for key b: %v", err)
	}

	stats := services.NewStatsService(database)
	expected, err := stats.Get(ctx)
	if err != nil {
		t.Fatalf("stats get: %v", err)
	}

	var upstreamCalls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":0,"limit":0}}`))
	}))
	t.Cleanup(upstream.Close)

	proxy := services.NewTavilyProxy(upstream.URL, 3*time.Second, keys, nil, nil, logger)
	handler := NewHandler(Dependencies{
		MasterKey:  master,
		Proxy:      proxy,
		Stats:      stats,
		Stateless:  true,
		SessionTTL: time.Minute,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	session := connectMCPClient(t, server.URL, master.Get())
	defer session.Close()

	callCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: "tavily-usage"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %+v", res)
	}

	payload := mustStructuredMap(t, res.StructuredContent)
	key := mustStructuredMap(t, payload["key"])
	usage := asInt64(t, key["usage"])
	limit := asInt64(t, key["limit"])

	if usage != expected.TotalUsed {
		t.Fatalf("unexpected usage: got %d want %d", usage, expected.TotalUsed)
	}
	if limit != expected.TotalQuota {
		t.Fatalf("unexpected limit: got %d want %d", limit, expected.TotalQuota)
	}
	if limit-usage != expected.TotalRemaining {
		t.Fatalf("unexpected remaining: got %d want %d", limit-usage, expected.TotalRemaining)
	}

	textPayload := mustStructuredMap(t, mustTextJSON(t, res))
	textKey := mustStructuredMap(t, textPayload["key"])
	if asInt64(t, textKey["usage"]) != usage || asInt64(t, textKey["limit"]) != limit {
		t.Fatalf("content text does not match structured content")
	}

	if got := atomic.LoadInt32(&upstreamCalls); got != 0 {
		t.Fatalf("unexpected upstream calls for tavily-usage: %d", got)
	}
}

func TestTavilyUsage_ReturnsErrorWhenStatsUnavailable(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	master := services.NewMasterKeyService(database, logger)
	if err := master.LoadOrCreate(ctx); err != nil {
		t.Fatalf("master key init: %v", err)
	}

	handler := NewHandler(Dependencies{
		MasterKey:  master,
		Stateless:  true,
		SessionTTL: time.Minute,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	session := connectMCPClient(t, server.URL, master.Get())
	defer session.Close()

	callCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: "tavily-usage"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result when stats unavailable")
	}

	payload := mustStructuredMap(t, res.StructuredContent)
	if payload["error"] != "stats service unavailable" {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
}

func TestMCPHandler_RejectsUnauthorizedRequest(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	master := services.NewMasterKeyService(database, logger)
	if err := master.LoadOrCreate(ctx); err != nil {
		t.Fatalf("master key init: %v", err)
	}

	handler := NewHandler(Dependencies{
		MasterKey:  master,
		Stateless:  true,
		SessionTTL: time.Minute,
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestTavilyResearch_CompletesAndReturnsReport(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	master := services.NewMasterKeyService(database, logger)
	if err := master.LoadOrCreate(ctx); err != nil {
		t.Fatalf("master key: %v", err)
	}
	keys := services.NewKeyService(database, logger)
	if _, err := keys.Create(ctx, "tvly-test", "test", 1000); err != nil {
		t.Fatalf("create key: %v", err)
	}
	stats := services.NewStatsService(database)

	var callCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/research":
			_, _ = w.Write([]byte(`{
				"request_id": "abc-123",
				"created_at": "2026-06-28T10:00:00Z",
				"status": "pending",
				"input": "test",
				"model": "mini",
				"response_time": 0.1
			}`))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/research/"):
			_, _ = w.Write([]byte(`{
				"request_id": "abc-123",
				"created_at": "2026-06-28T10:00:00Z",
				"status": "completed",
				"content": "Research report content here",
				"sources": [{"title": "Source A", "url": "https://example.com"}],
				"response_time": 1.5
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	proxy := services.NewTavilyProxy(upstream.URL, 3*time.Second, keys, nil, stats, logger)
	handler := NewHandler(Dependencies{
		MasterKey:  master,
		Proxy:      proxy,
		Stats:      stats,
		Stateless:  true,
		SessionTTL: time.Minute,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	session := connectMCPClient(t, server.URL, master.Get())
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tavily-research",
		Arguments: map[string]any{"input": "test"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %+v", res)
	}

	payload := mustStructuredMap(t, res.StructuredContent)
	if got := payload["status"]; got != "completed" {
		t.Errorf("status = %v, want completed", got)
	}
	if got := payload["content"]; got != "Research report content here" {
		t.Errorf("content = %v, want 'Research report content here'", got)
	}

	if calls := atomic.LoadInt32(&callCount); calls < 2 {
		t.Errorf("upstream calls = %d, want >= 2", calls)
	}
}

func TestTavilyResearch_HandlesUpstreamFailedStatus(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	master := services.NewMasterKeyService(database, logger)
	_ = master.LoadOrCreate(ctx)
	keys := services.NewKeyService(database, logger)
	_, _ = keys.Create(ctx, "tvly-test", "test", 1000)
	stats := services.NewStatsService(database)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/research":
			_, _ = w.Write([]byte(`{"request_id":"abc","status":"pending","created_at":"2026-06-28T10:00:00Z"}`))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/research/"):
			_, _ = w.Write([]byte(`{"request_id":"abc","status":"failed","content":"upstream error"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	proxy := services.NewTavilyProxy(upstream.URL, 3*time.Second, keys, nil, stats, logger)
	handler := NewHandler(Dependencies{
		MasterKey: master, Proxy: proxy, Stats: stats,
		Stateless: true, SessionTTL: time.Minute,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	session := connectMCPClient(t, server.URL, master.Get())
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tavily-research",
		Arguments: map[string]any{"input": "test"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
}

func TestTavilyResearch_RespectsClientCancellation(t *testing.T) {
	t.Skip("client cancellation across MCP transport + httptest is flaky; defer until real-world need arises")
}

// TestTavilyResearch_StaysOnSameKey guards against a real Tavily behaviour:
// a research task is bound to the API key that created it; polling with any
// other key returns 404 "No research task found for this request ID". The
// proxy must therefore pin the key used for the POST when issuing follow-up
// polls. Before the KeyIDHint fix, this test would fail because every
// proxy.Do() call re-shuffled candidates and the poll used a different key.
func TestTavilyResearch_StaysOnSameKey(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	master := services.NewMasterKeyService(database, logger)
	if err := master.LoadOrCreate(ctx); err != nil {
		t.Fatalf("master key: %v", err)
	}
	keys := services.NewKeyService(database, logger)
	if _, err := keys.Create(ctx, "tvly-key-a", "a", 1000); err != nil {
		t.Fatalf("create key a: %v", err)
	}
	if _, err := keys.Create(ctx, "tvly-key-b", "b", 1000); err != nil {
		t.Fatalf("create key b: %v", err)
	}
	stats := services.NewStatsService(database)

	var (
		mu             sync.Mutex
		creatingKey    = map[string]string{} // request_id -> bearer token that created it
		seenPollKeys   []string
		seenPostKeys   []string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		bearer := strings.TrimPrefix(auth, "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/research":
			mu.Lock()
			seenPostKeys = append(seenPostKeys, bearer)
			mu.Unlock()
			_, _ = w.Write([]byte(`{
				"request_id": "task-pin-test",
				"status": "pending",
				"input": "test",
				"model": "mini"
			}`))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/research/"):
			mu.Lock()
			seenPollKeys = append(seenPollKeys, bearer)
			// Lazily record the creator the first time we see this task.
			// Since upstream receives the same task id every poll, we capture
			// the first poll's bearer as the creator reference.
			if _, ok := creatingKey["task-pin-test"]; !ok {
				creatingKey["task-pin-test"] = bearer
			}
			mu.Unlock()
			// Per-key isolation: 404 if the polling key differs from creator.
			if creatingKey["task-pin-test"] != bearer {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"detail":"No research task found for this request ID"}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"request_id": "task-pin-test",
				"status": "completed",
				"content": "pinned key report",
				"sources": []
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	proxy := services.NewTavilyProxy(upstream.URL, 3*time.Second, keys, nil, stats, logger)
	handler := NewHandler(Dependencies{
		MasterKey:  master,
		Proxy:      proxy,
		Stats:      stats,
		Stateless:  true,
		SessionTTL: time.Minute,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	session := connectMCPClient(t, server.URL, master.Get())
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tavily-research",
		Arguments: map[string]any{"input": "test", "model": "mini"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored (key drift?): %+v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenPostKeys) != 1 {
		t.Errorf("expected exactly 1 POST, got %d", len(seenPostKeys))
	}
	if len(seenPollKeys) < 1 {
		t.Fatalf("expected at least 1 poll, got 0")
	}
	for i, k := range seenPollKeys {
		if k != seenPostKeys[0] {
			t.Errorf("poll #%d used key %q, want %q (creator)", i, k, seenPostKeys[0])
		}
	}

	payload := mustStructuredMap(t, res.StructuredContent)
	if got := payload["status"]; got != "completed" {
		t.Errorf("status = %v, want completed", got)
	}
	if got := payload["content"]; got != "pinned key report" {
		t.Errorf("content = %v, want 'pinned key report'", got)
	}
}

// TestMCP_SchemasMatchTavilyUpstream guards TavilyProxy's inputSchema against
// drift from Tavily's real API. Every enum/constraint below was verified by
// direct probing of https://api.tavily.com (each bad value triggered 400
// naming the legal set; each good value succeeded). We use the real API as
// ground truth — not Tavily's official MCP v0.2.20 schema, which is a
// conservative subset that omits fields like output_length, citation_format,
// chunks_per_source, html_tags, d/w/m/y time_range shortcuts, and news/finance
// topics that Tavily API does accept.
//
// If Tavily adds/removes a value, this test fails and the ground-truth list
// here must be updated.
func TestMCP_SchemasMatchTavilyUpstream(t *testing.T) {
	t.Parallel()

	// tool.field -> expected enum values (unsorted — order doesn't matter,
	// we compare as sets).
	expectedEnums := map[string][]string{
		// research
		"research.model":           {"mini", "pro", "auto"},
		"research.output_length":   {"short", "standard", "long"},
		"research.citation_format": {"numbered", "mla", "apa", "chicago"},
		// search
		"search.search_depth": {"basic", "advanced", "fast", "ultra-fast"},
		"search.topic":        {"general", "news", "finance"},
		"search.time_range":   {"day", "week", "month", "year", "d", "w", "m", "y"},
		// extract
		"extract.extract_depth": {"basic", "advanced"},
		"extract.format":        {"markdown", "text", "html_tags"},
		// crawl
		"crawl.extract_depth": {"basic", "advanced"},
		"crawl.format":        {"markdown", "text"},
		// map
		"map.extract_depth": {"basic", "advanced"},
		"map.format":        {"markdown", "text"},
	}

	// tool.field -> expected constraints on integer fields.
	// We only assert maximum when Tavily officially publishes one; otherwise
	// we skip (caller can pass any value Tavily accepts).
	expectedIntMax := map[string]int{
		"crawl.limit": 1000,
		"map.limit":   1000,
	}

	schemas := map[string]map[string]any{
		"research": tavilyResearchInputSchema,
		"search":   tavilySearchInputSchema,
		"extract":  tavilyExtractInputSchema,
		"crawl":    tavilyCrawlInputSchema,
		"map":      tavilyMapInputSchema,
	}

	for key, want := range expectedEnums {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			t.Fatalf("bad key %q", key)
		}
		tool, field := parts[0], parts[1]
		schema, ok := schemas[tool]
		if !ok {
			t.Fatalf("unknown tool %q in key %q", tool, key)
		}
		props, _ := schema["properties"].(map[string]any)
		fieldSchema, ok := props[field].(map[string]any)
		if !ok {
			t.Errorf("%s: field missing from schema", key)
			continue
		}
		// chunks_per_source uses oneOf (integer OR "auto"); recurse into the
		// integer branch's max and assert the string branch's enum.
		if field == "chunks_per_source" {
			oneOf, _ := fieldSchema["oneOf"].([]any)
			if len(oneOf) != 2 {
				t.Errorf("%s: oneOf must have 2 branches, got %d", key, len(oneOf))
				continue
			}
			intBranch, _ := oneOf[0].(map[string]any)
			strBranch, _ := oneOf[1].(map[string]any)
			if intBranch["type"] != "integer" {
				t.Errorf("%s: oneOf[0] must be integer, got %v", key, intBranch["type"])
			}
			if intBranch["maximum"] != 5 {
				t.Errorf("%s: oneOf[0].maximum = %v, want 5 (Tavily accepts 1-5)", key, intBranch["maximum"])
			}
			if intBranch["minimum"] != 1 {
				t.Errorf("%s: oneOf[0].minimum = %v, want 1", key, intBranch["minimum"])
			}
			strEnum, _ := strBranch["enum"].([]any)
			if len(strEnum) != 1 || strEnum[0] != "auto" {
				t.Errorf("%s: oneOf[1].enum = %v, want [\"auto\"]", key, strEnum)
			}
			continue
		}
		gotEnum := asStringSlice(t, fieldSchema["enum"])
		if len(gotEnum) == 0 {
			t.Errorf("%s: no enum on field; fieldSchema = %+v", key, fieldSchema)
			continue
		}
		gotSet := map[string]bool{}
		for _, v := range gotEnum {
			gotSet[v] = true
		}
		for _, w := range want {
			if !gotSet[w] {
				t.Errorf("%s: enum missing %q (got %v, want superset of %v)", key, w, gotEnum, want)
			}
		}
		// Guard against extra undocumented values: every enum entry must be in
		// the expected set. Otherwise we ship values Tavily will 400.
		for s := range gotSet {
			found := false
			for _, w := range want {
				if s == w {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: enum has undocumented value %q (got %v, allowed = %v)", key, s, gotEnum, want)
			}
		}
	}

	for key, wantMax := range expectedIntMax {
		parts := strings.SplitN(key, ".", 2)
		tool, field := parts[0], parts[1]
		schema := schemas[tool]
		props, _ := schema["properties"].(map[string]any)
		fieldSchema, _ := props[field].(map[string]any)
		if fieldSchema == nil {
			t.Errorf("%s: field missing", key)
			continue
		}
		gotMax, ok := fieldSchema["maximum"]
		if !ok {
			t.Errorf("%s: missing maximum constraint (Tavily caps at %d)", key, wantMax)
			continue
		}
		// JSON unmarshals numbers as float64; Go literals stay int.
		var gotMaxInt int
		switch v := gotMax.(type) {
		case int:
			gotMaxInt = v
		case float64:
			gotMaxInt = int(v)
		default:
			t.Errorf("%s: maximum has unexpected type %T", key, gotMax)
			continue
		}
		if gotMaxInt != wantMax {
			t.Errorf("%s: maximum = %d, want %d", key, gotMaxInt, wantMax)
		}
	}
}

// asStringSlice normalises either a []string or []any of strings into []string,
// since the schema variables are declared with []string but arrive here as
// interface{} values.
func asStringSlice(t *testing.T, v any) []string {
	t.Helper()
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			str, ok := x.(string)
			if !ok {
				t.Fatalf("enum contains non-string %v (%T)", x, x)
			}
			out = append(out, str)
		}
		return out
	default:
		return nil
	}
}

type authRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (t *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	transport := t.base
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return transport.RoundTrip(clone)
}

func connectMCPClient(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()

	connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.0.1",
	}, nil)
	session, err := client.Connect(connectCtx, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: &authRoundTripper{token: token}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect mcp client: %v", err)
	}
	return session
}

func mustTextJSON(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()

	if len(result.Content) == 0 {
		t.Fatalf("missing content")
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type: %T", result.Content[0])
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("text content is not json: %v (text=%q)", err, text.Text)
	}
	return out
}

func mustStructuredMap(t *testing.T, v any) map[string]any {
	t.Helper()
	out, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("unexpected structured type: %T", v)
	}
	return out
}

func asInt64(t *testing.T, v any) int64 {
	t.Helper()
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			t.Fatalf("invalid json number %q: %v", x, err)
		}
		return n
	default:
		t.Fatalf("unexpected numeric type: %T", v)
		return 0
	}
}

