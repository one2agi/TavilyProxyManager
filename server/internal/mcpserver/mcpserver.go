package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"tavily-proxy/server/internal/services"
)

type Dependencies struct {
	MasterKey  *services.MasterKeyService
	Proxy      *services.TavilyProxy
	Stats      *services.StatsService
	Stateless  bool
	SessionTTL time.Duration
}

func NewHandler(deps Dependencies) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tavily-proxy-mcp",
		Version: "0.1.0",
	}, nil)

	addProxyTool(server, deps.Proxy, &mcp.Tool{
		Name:        "tavily-search",
		Description: "Execute a search query using Tavily Search (via Tavily Proxy Pool). Returns ranked results and optional answer/raw_content/images/usage.",
		InputSchema: tavilySearchInputSchema,
	}, http.MethodPost, "/search")
	addProxyTool(server, deps.Proxy, &mcp.Tool{
		Name:        "tavily-extract",
		Description: "Extract structured content from URLs (via Tavily Proxy Pool)",
		InputSchema: tavilyExtractInputSchema,
	}, http.MethodPost, "/extract")
	addProxyTool(server, deps.Proxy, &mcp.Tool{
		Name:        "tavily-crawl",
		Description: "Crawl a website starting from a root URL (via Tavily Proxy Pool)",
		InputSchema: tavilyCrawlInputSchema,
	}, http.MethodPost, "/crawl")
	addProxyTool(server, deps.Proxy, &mcp.Tool{
		Name:        "tavily-map",
		Description: "Map a website's URL structure (via Tavily Proxy Pool)",
		InputSchema: tavilyMapInputSchema,
	}, http.MethodPost, "/map")
	addUsageTool(server, deps.Stats, &mcp.Tool{
		Name:        "tavily-usage",
		Description: "Get aggregated usage/quota info from local key statistics",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	})

	addResearchTool(server, deps.Proxy, &mcp.Tool{
		Name:        "tavily-research",
		Description: "Execute a comprehensive research task via Tavily Research API. Internally creates a task and polls until completion or 5-minute timeout. Returns a structured report with sources.",
		InputSchema: tavilyResearchInputSchema,
	})

	base := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:      deps.Stateless,
		SessionTimeout: deps.SessionTTL,
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := parseBearerToken(r.Header.Get("Authorization"))
		if !deps.MasterKey.Authenticate(token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		base.ServeHTTP(w, r)
	})
}

func addProxyTool(server *mcp.Server, proxy *services.TavilyProxy, tool *mcp.Tool, method, path string) {
	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var body []byte
		if method == http.MethodPost {
			if len(req.Params.Arguments) > 0 {
				body = req.Params.Arguments
			} else {
				body = []byte("{}")
			}
		}

		headers := make(http.Header)
		headers.Set("User-Agent", "tavily-proxy-mcp")
		if method == http.MethodPost {
			headers.Set("Content-Type", "application/json")
		}

		resp, err := proxy.Do(ctx, services.ProxyRequest{
			Method:      method,
			Path:        path,
			Headers:     headers,
			Body:        body,
			ClientIP:    "mcp",
			ContentType: "application/json",
		})
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				},
				StructuredContent: map[string]any{"error": err.Error()},
			}, nil
		}

		text := string(resp.Body)

		var parsed any
		if err := json.Unmarshal(resp.Body, &parsed); err != nil {
			parsed = nil
		}

		var structured any
		if m, ok := parsed.(map[string]any); ok {
			structured = m
		} else {
			structured = map[string]any{"raw": text}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Upstream status %d: %s", resp.StatusCode, text)},
				},
				StructuredContent: structured,
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
			StructuredContent: structured,
		}, nil
	})
}

func addUsageTool(server *mcp.Server, stats *services.StatsService, tool *mcp.Tool) {
	server.AddTool(tool, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if stats == nil {
			const msg = "stats service unavailable"
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: msg},
				},
				StructuredContent: map[string]any{"error": msg},
			}, nil
		}

		s, err := stats.Get(ctx)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				},
				StructuredContent: map[string]any{"error": err.Error()},
			}, nil
		}

		payload := map[string]any{
			"key": map[string]any{
				"usage": s.TotalUsed,
				"limit": s.TotalQuota,
			},
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				},
				StructuredContent: map[string]any{"error": err.Error()},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(raw)},
			},
			StructuredContent: payload,
		}, nil
	})
}

var tavilySearchInputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": true,
	"required":             []string{"query"},
	"properties": map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "The search query to execute with Tavily.",
		},
		"auto_parameters": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Automatically configures search parameters based on the query. Explicit values override auto-selected ones. Note: include_answer/include_raw_content/max_results must be set manually. auto_parameters may set search_depth=advanced (2 credits); set search_depth=basic to avoid extra cost.",
		},
		"topic": map[string]any{
			"type":        "string",
			"enum":        []string{"general", "news", "finance"},
			"default":     "general",
			"description": "Search topic/category. Use news for real-time updates; general for broad searches.",
		},
		"search_depth": map[string]any{
			"type":        "string",
			"enum":        []string{"advanced", "basic", "fast", "ultra-fast"},
			"default":     "basic",
			"description": "Controls relevance vs latency and how results[].content is generated. basic: balanced, 1 summary per URL (1 credit). fast: lower latency, multiple snippets per URL (1 credit). ultra-fast: lowest latency, 1 summary per URL (1 credit). advanced: highest relevance, multiple snippets per URL (2 credits).",
		},
		"chunks_per_source": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"maximum":     3,
			"default":     3,
			"description": "Max number of relevant chunks (each up to ~500 chars) to return per source. Used with search_depth=advanced.",
		},
		"max_results": map[string]any{
			"type":        "integer",
			"minimum":     0,
			"maximum":     20,
			"default":     5,
			"description": "The maximum number of search results to return.",
		},
		"time_range": map[string]any{
			"type":        "string",
			"enum":        []string{"day", "week", "month", "year", "d", "w", "m", "y"},
			"default":     nil,
			"description": "Filter results by publish/updated time window back from now (day/week/month/year or d/w/m/y).",
		},
		"start_date": map[string]any{
			"type":        "string",
			"format":      "date",
			"default":     nil,
			"description": "Return results after this date (YYYY-MM-DD).",
		},
		"end_date": map[string]any{
			"type":        "string",
			"format":      "date",
			"default":     nil,
			"description": "Return results before this date (YYYY-MM-DD).",
		},
		"country": map[string]any{
			"type":        "string",
			"default":     nil,
			"description": "Boost results from a specific country (topic=general only). Use lowercase country names like 'united states'.",
		},
		"include_domains": map[string]any{
			"type":        "array",
			"default":     []any{},
			"items":       map[string]any{"type": "string"},
			"description": "A list of domains to specifically include in the search results (max 300).",
		},
		"exclude_domains": map[string]any{
			"type":        "array",
			"default":     []any{},
			"items":       map[string]any{"type": "string"},
			"description": "A list of domains to specifically exclude from the search results (max 150).",
		},
		"include_images": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Also perform an image search and include images in the response.",
		},
		"include_image_descriptions": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "When include_images is true, also add a descriptive text for each image.",
		},
		"include_answer": map[string]any{
			"description": "Include an LLM-generated answer to the query. true/basic: quick answer; advanced: more detailed.",
			"oneOf": []any{
				map[string]any{"type": "boolean"},
				map[string]any{"type": "string", "enum": []string{"basic", "advanced"}},
			},
			"default": false,
		},
		"include_raw_content": map[string]any{
			"description": "Include cleaned/parsed page content for each result. true/markdown: markdown; text: plain text (may increase latency).",
			"oneOf": []any{
				map[string]any{"type": "boolean"},
				map[string]any{"type": "string", "enum": []string{"markdown", "text"}},
			},
			"default": false,
		},
		"include_favicon": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Whether to include the favicon URL for each result.",
		},
		"include_usage": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Whether to include credit usage information in the response.",
		},
	},
}

var tavilyExtractInputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": true,
	"required":             []string{"urls"},
	"properties": map[string]any{
		"urls": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "URLs to extract content from.",
		},
		"extract_depth": map[string]any{
			"type":        "string",
			"enum":        []string{"basic", "advanced"},
			"default":     "basic",
			"description": "Depth of extraction.",
		},
		"format": map[string]any{
			"type":        "string",
			"enum":        []string{"markdown", "text"},
			"default":     "markdown",
			"description": "Output format for extracted content.",
		},
		"include_images": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include images.",
		},
		"include_image_descriptions": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include descriptions for extracted images.",
		},
		"include_favicon": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include favicon URL.",
		},
		"include_usage": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include credit usage information in the response.",
		},
		"include_domains": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Domains to include.",
		},
		"exclude_domains": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Domains to exclude.",
		},
		"country": map[string]any{
			"type":        "string",
			"description": "Prioritize content from a specific country (lowercase plain English).",
		},
	},
}

var tavilyMapInputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": true,
	"required":             []string{"url"},
	"properties": map[string]any{
		"url": map[string]any{
			"type":        "string",
			"description": "Root URL to begin mapping.",
		},
		"instructions": map[string]any{
			"type":        "string",
			"description": "Natural language instructions for the crawler.",
		},
		"max_depth": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"maximum":     5,
			"default":     1,
			"description": "Max depth of mapping.",
		},
		"max_breadth": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"default":     20,
			"description": "Max number of links to follow per level.",
		},
		"limit": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"default":     50,
			"description": "Total number of links to process.",
		},
		"select_paths": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to include specific paths.",
		},
		"select_domains": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to include specific domains.",
		},
		"exclude_paths": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to exclude paths.",
		},
		"exclude_domains": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to exclude domains.",
		},
		"allow_external": map[string]any{
			"type":        "boolean",
			"default":     true,
			"description": "Allow following external-domain links.",
		},
		"include_images": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include images discovered during mapping.",
		},
		"extract_depth": map[string]any{
			"type":        "string",
			"enum":        []string{"basic", "advanced"},
			"default":     "basic",
			"description": "Extraction depth for mapped pages.",
		},
		"format": map[string]any{
			"type":        "string",
			"enum":        []string{"markdown", "text"},
			"default":     "markdown",
			"description": "Format of extracted content.",
		},
		"include_favicon": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include favicon URL for each result.",
		},
		"include_usage": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include credit usage information in the response.",
		},
	},
}

var tavilyCrawlInputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": true,
	"required":             []string{"url"},
	"properties": map[string]any{
		"url": map[string]any{
			"type":        "string",
			"description": "Root URL to begin crawling.",
		},
		"instructions": map[string]any{
			"type":        "string",
			"description": "Natural language instructions for the crawler.",
		},
		"max_depth": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"maximum":     5,
			"default":     1,
			"description": "Max depth of crawl.",
		},
		"max_breadth": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"default":     20,
			"description": "Max number of links to follow per level.",
		},
		"limit": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"default":     50,
			"description": "Total number of pages to process.",
		},
		"select_paths": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to include specific paths.",
		},
		"select_domains": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to include specific domains.",
		},
		"exclude_paths": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to exclude paths.",
		},
		"exclude_domains": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Regex patterns to exclude domains.",
		},
		"allow_external": map[string]any{
			"type":        "boolean",
			"default":     true,
			"description": "Allow following external-domain links.",
		},
		"include_images": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include images discovered during crawling.",
		},
		"extract_depth": map[string]any{
			"type":        "string",
			"enum":        []string{"basic", "advanced"},
			"default":     "basic",
			"description": "Extraction depth for crawled pages.",
		},
		"format": map[string]any{
			"type":        "string",
			"enum":        []string{"markdown", "text"},
			"default":     "markdown",
			"description": "Format of extracted content.",
		},
		"include_favicon": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include favicon URL for each result.",
		},
		"include_usage": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Include credit usage information in the response.",
		},
	},
}

const (
	researchPollInterval = 2 * time.Second
	researchMaxWait      = 5 * time.Minute
)

func addResearchTool(server *mcp.Server, proxy *services.TavilyProxy, tool *mcp.Tool) {
	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, _ := json.Marshal(req.Params.Arguments)

		// 1. POST /research
		headers := make(http.Header)
		headers.Set("Content-Type", "application/json")

		resp, err := proxy.Do(ctx, services.ProxyRequest{
			Method:      http.MethodPost,
			Path:        "/research",
			Headers:     headers,
			Body:        body,
			ClientIP:    "mcp",
			ContentType: "application/json",
		})
		if err != nil {
			return toolError(err.Error()), nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return toolError(fmt.Sprintf("upstream status %d: %s", resp.StatusCode, string(resp.Body))), nil
		}

		// The research task is bound to the key that created it upstream;
		// subsequent polls must reuse the same key, otherwise Tavily returns
		// 404 "No research task found for this request ID".
		pinnedKeyID := resp.KeyID

		// 2. Extract task_id and initial status
		var initial struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		}
		if err := json.Unmarshal(resp.Body, &initial); err != nil {
			return toolError(fmt.Sprintf("decode create response: %v", err)), nil
		}
		if initial.RequestID == "" {
			return toolError("upstream returned empty request_id"), nil
		}
		if initial.Status == "completed" || initial.Status == "failed" {
			return toolResultBytes(resp.Body), nil
		}

		// 3. Poll GET /research/{id}
		deadline := time.Now().Add(researchMaxWait)
		pollPath := "/research/" + initial.RequestID
		for {
			timer := time.NewTimer(researchPollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return toolError(fmt.Sprintf("client canceled: %v", ctx.Err())), nil
			case <-timer.C:
			}
			if time.Now().After(deadline) {
				return toolError(fmt.Sprintf("research timeout after %s, last status: pending", researchMaxWait)), nil
			}

			getResp, err := proxy.Do(ctx, services.ProxyRequest{
				Method:    http.MethodGet,
				Path:      pollPath,
				Headers:   make(http.Header),
				ClientIP:  "mcp",
				KeyIDHint: pinnedKeyID,
			})
			if err != nil {
				// Network error: continue polling until deadline
				continue
			}
			if getResp.StatusCode < 200 || getResp.StatusCode >= 300 {
				// 5xx: continue polling. 4xx: abort.
				if getResp.StatusCode < 500 {
					return toolError(fmt.Sprintf("poll failed: %d %s", getResp.StatusCode, string(getResp.Body))), nil
				}
				continue
			}

			var status struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(getResp.Body, &status); err != nil {
				continue
			}
			switch status.Status {
			case "completed":
				return toolResultBytes(getResp.Body), nil
			case "failed":
				return toolError(fmt.Sprintf("research task failed: %s", string(getResp.Body))), nil
			}
			// status still "pending" or "running": continue
		}
	})
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

func toolResultBytes(body []byte) *mcp.CallToolResult {
	var parsed any
	_ = json.Unmarshal(body, &parsed)
	structured, _ := parsed.(map[string]any)
	if structured == nil {
		structured = map[string]any{"raw": string(body)}
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
		StructuredContent: structured,
	}
}

func parseBearerToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

var tavilyResearchInputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": true,
	"required":             []string{"input"},
	"properties": map[string]any{
		"input": map[string]any{
			"type":        "string",
			"description": "The research task or question to investigate.",
		},
		"model": map[string]any{
			"type":        "string",
			"enum":        []string{"mini", "pro", "auto"},
			"default":     "auto",
			"description": "mini: targeted/efficient; pro: comprehensive/multi-angle; auto: let Tavily pick.",
		},
		"stream": map[string]any{
			"type":        "boolean",
			"default":     false,
			"description": "Whether to stream results. Currently unused by proxy (always polled).",
		},
		"output_schema": map[string]any{
			"type":        "object",
			"description": "Optional JSON schema for structured output.",
		},
		"citation_format": map[string]any{
			"type":        "string",
			"enum":        []string{"numbered", "markdown", "json", "none"},
			"default":     "markdown",
		},
		"include_domains": map[string]any{
			"type":     "array",
			"items":    map[string]any{"type": "string"},
			"maxItems": 300,
		},
		"exclude_domains": map[string]any{
			"type":     "array",
			"items":    map[string]any{"type": "string"},
			"maxItems": 150,
		},
		"output_length": map[string]any{
			"type":        "string",
			"enum":        []string{"low", "medium", "high"},
			"default":     "medium",
		},
		"files": map[string]any{
			"type":        "object",
			"description": "Optional file references; opaque to proxy.",
		},
	},
}
