package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const healthCheckTool = "grpc_health_v1_Health__Check"
const healthWatchTool = "grpc_health_v1_Health__Watch"

func TestBuildMCPServerFromReflectionExposesUnaryToolsAndSkipsStreaming(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	srv := buildReflectedTestServer(t, backendURL)

	tools := srv.ListTools()
	if _, ok := tools[healthCheckTool]; !ok {
		t.Fatalf("expected unary health check tool %q, got tools %v", healthCheckTool, toolNames(tools))
	}
	if _, ok := tools[healthWatchTool]; ok {
		t.Fatalf("streaming health watch tool %q should not be exposed", healthWatchTool)
	}
}

func TestReflectedUnaryToolCallsBackend(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	srv := buildReflectedTestServer(t, backendURL)
	client := startInProcessClient(t, srv)

	assertHealthCheckToolCall(t, client)
}

func TestStreamableHTTPTransportListsAndCallsReflectedTools(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	srv := buildReflectedTestServer(t, backendURL)
	httpSrv := mcpserver.NewTestStreamableHTTPServer(srv)
	t.Cleanup(httpSrv.Close)

	trans, err := transport.NewStreamableHTTP(httpSrv.URL + "/mcp")
	if err != nil {
		t.Fatalf("NewStreamableHTTP failed: %v", err)
	}
	client := mcpclient.NewClient(trans)
	startAndInitializeClient(t, client)

	tools, err := client.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if !hasTool(tools.Tools, healthCheckTool) {
		t.Fatalf("expected Streamable HTTP tools list to include %q, got %+v", healthCheckTool, tools.Tools)
	}
	if hasTool(tools.Tools, healthWatchTool) {
		t.Fatalf("streaming tool %q should not be listed over Streamable HTTP", healthWatchTool)
	}

	assertHealthCheckToolCall(t, client)
}

func TestLegacySSETransportListsAndCallsReflectedTools(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	srv := buildReflectedTestServer(t, backendURL)
	sseSrv := mcpserver.NewTestServer(srv)
	t.Cleanup(sseSrv.Close)

	client, err := mcpclient.NewSSEMCPClient(sseSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewSSEMCPClient failed: %v", err)
	}
	startAndInitializeClient(t, client)

	tools, err := client.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if !hasTool(tools.Tools, healthCheckTool) {
		t.Fatalf("expected SSE tools list to include %q, got %+v", healthCheckTool, tools.Tools)
	}
	if hasTool(tools.Tools, healthWatchTool) {
		t.Fatalf("streaming tool %q should not be listed over SSE", healthWatchTool)
	}

	assertHealthCheckToolCall(t, client)
}

func assertHealthCheckToolCall(t *testing.T, client *mcpclient.Client) {
	t.Helper()

	req := mcp.CallToolRequest{}
	req.Params.Name = healthCheckTool
	req.Params.Arguments = map[string]any{
		"service": grpchealth.HealthV1ServiceName,
	}

	result, err := client.CallTool(t.Context(), req)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		text, _ := toolResultText(result)
		t.Fatalf("CallTool returned tool error: %s", text)
	}

	text, err := toolResultText(result)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("response was not JSON: %v; text=%q", err, text)
	}
	if response.Status != "SERVING" {
		t.Fatalf("expected SERVING status, got %q in %s", response.Status, text)
	}
}

func TestServiceFilterLimitsReflectedTools(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	srv, err := buildMCPServer(t.Context(), grpcMCPConfig{
		Headers:    http.Header{},
		ServerName: "test grpc mcp",
		Version:    "test",
		Reflect:    true,
		Services:   "missing.Service",
		BaseURL:    backendURL,
	})
	if err != nil {
		t.Fatalf("buildMCPServer failed: %v", err)
	}
	if tools := srv.ListTools(); len(tools) != 0 {
		t.Fatalf("expected service filter to hide all tools, got %v", toolNames(tools))
	}
}

func startReflectingHealthBackend(t *testing.T) string {
	t.Helper()

	mux := http.NewServeMux()
	health := grpchealth.NewStaticChecker(grpchealth.HealthV1ServiceName)
	reflector := grpcreflect.NewStaticReflector(grpchealth.HealthV1ServiceName)
	path, handler := grpchealth.NewHandler(health)
	mux.Handle(path, handler)
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	srv := httptest.NewUnstartedServer(h2c.NewHandler(mux, &http2.Server{}))
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

func buildReflectedTestServer(t *testing.T, backendURL string) *mcpserver.MCPServer {
	t.Helper()

	srv, err := buildMCPServer(t.Context(), grpcMCPConfig{
		Headers:    http.Header{},
		ServerName: "test grpc mcp",
		Version:    "test",
		Reflect:    true,
		Services:   grpchealth.HealthV1ServiceName,
		BaseURL:    backendURL,
	})
	if err != nil {
		t.Fatalf("buildMCPServer failed: %v", err)
	}
	return srv
}

func startInProcessClient(t *testing.T, srv *mcpserver.MCPServer) *mcpclient.Client {
	t.Helper()

	client, err := mcpclient.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("NewInProcessClient failed: %v", err)
	}
	startAndInitializeClient(t, client)
	return client
}

func startAndInitializeClient(t *testing.T, client *mcpclient.Client) {
	t.Helper()

	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("client.Close failed: %v", err)
		}
	})
	if err := client.Start(t.Context()); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "test",
	}
	if _, err := client.Initialize(t.Context(), initReq); err != nil {
		t.Fatalf("client.Initialize failed: %v", err)
	}
}

func toolResultText(result *mcp.CallToolResult) (string, error) {
	var b strings.Builder
	for _, content := range result.Content {
		text, ok := content.(mcp.TextContent)
		if !ok {
			return "", fmt.Errorf("unsupported content type: %T", content)
		}
		b.WriteString(text.Text)
	}
	return b.String(), nil
}

func toolNames(tools map[string]*mcpserver.ServerTool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}

func hasTool(tools []mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
