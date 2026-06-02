package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"github.com/adiom-data/grpcmcp/grpcmcp"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
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

func TestLoadDescriptorsFromFileBuildsServer(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	descriptors := loadReflectedDescriptors(t, backendURL)
	data, err := proto.Marshal(descriptors)
	if err != nil {
		t.Fatalf("marshal descriptors failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "descriptors.pb")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write descriptors failed: %v", err)
	}

	loaded, err := grpcmcp.LoadDescriptorsFromFile(path)
	if err != nil {
		t.Fatalf("LoadDescriptorsFromFile failed: %v", err)
	}
	srv := buildTestServerFromDescriptors(t, backendURL, loaded, []protoreflect.FullName{protoreflect.FullName(grpchealth.HealthV1ServiceName)})
	client := startInProcessClient(t, srv)

	assertHealthCheckToolCall(t, client)
}

func TestLoadDescriptorsPrefersDescriptorFileWhenReflectionAlsoSet(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	data, err := proto.Marshal(&descriptorpb.FileDescriptorSet{})
	if err != nil {
		t.Fatalf("marshal descriptors failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "descriptors.pb")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write descriptors failed: %v", err)
	}

	loaded, err := loadDescriptors(t.Context(), path, true, backendURL, nil, false)
	if err != nil {
		t.Fatalf("loadDescriptors failed: %v", err)
	}
	if got := len(loaded.GetFile()); got != 0 {
		t.Fatalf("loaded %d descriptor files, want descriptor file to override reflection", got)
	}
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

func TestStreamableHTTPToolInputSchemasCompileAndValidate(t *testing.T) {
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
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	var healthCheckSchema *jsonschemav6.Schema
	for _, tool := range tools.Tools {
		schema := compileToolInputSchema(t, tool)
		if tool.Name == healthCheckTool {
			healthCheckSchema = schema
		}
	}
	if healthCheckSchema == nil {
		t.Fatalf("expected tools list to include %q, got %+v", healthCheckTool, tools.Tools)
	}

	assertSchemaValid(t, healthCheckSchema, map[string]any{
		"service": grpchealth.HealthV1ServiceName,
	})
	assertSchemaValid(t, healthCheckSchema, map[string]any{})
	assertSchemaInvalid(t, healthCheckSchema, map[string]any{
		"service": 123,
	})
	assertSchemaInvalid(t, healthCheckSchema, map[string]any{
		"unknown": "value",
	})
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

func TestDynamicHeaderProviderReceivesInboundToolRequestHeaders(t *testing.T) {
	const token = "Bearer caller-token"

	backendURL := startReflectingHealthBackendWithAuth(t, token)
	descriptors, err := grpcmcp.LoadDescriptorsFromReflection(t.Context(), backendURL, nil, false)
	if err != nil {
		t.Fatalf("LoadDescriptorsFromReflection failed: %v", err)
	}
	srv, err := grpcmcp.NewServer(grpcmcp.Config{
		Headers: func(ctx context.Context, req mcp.CallToolRequest) (http.Header, error) {
			headers := make(http.Header)
			headers.Set("Authorization", req.Header.Get("Authorization"))
			return headers, nil
		},
		ServerName:  "test grpc mcp",
		Version:     "test",
		Descriptors: descriptors,
		Services:    []protoreflect.FullName{protoreflect.FullName(grpchealth.HealthV1ServiceName)},
		BaseURL:     backendURL,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	httpSrv := mcpserver.NewTestStreamableHTTPServer(srv)
	t.Cleanup(httpSrv.Close)

	trans, err := transport.NewStreamableHTTP(httpSrv.URL+"/mcp", transport.WithHTTPHeaders(map[string]string{
		"Authorization": token,
	}))
	if err != nil {
		t.Fatalf("NewStreamableHTTP failed: %v", err)
	}
	client := mcpclient.NewClient(trans)
	startAndInitializeClient(t, client)

	assertHealthCheckToolCall(t, client)
}

func TestNewServerUsesConfiguredHTTPClient(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	descriptors := loadReflectedDescriptors(t, backendURL)
	recordingClient := &recordingHTTPClient{client: http.DefaultClient}
	srv, err := grpcmcp.NewServer(grpcmcp.Config{
		Headers:     grpcmcp.StaticHeaders(nil),
		ServerName:  "test grpc mcp",
		Version:     "test",
		Descriptors: descriptors,
		Services:    []protoreflect.FullName{protoreflect.FullName(grpchealth.HealthV1ServiceName)},
		BaseURL:     backendURL,
		HTTPClient:  recordingClient,
		UseConnect:  true,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	client := startInProcessClient(t, srv)

	assertHealthCheckToolCall(t, client)
	if got := recordingClient.calls.Load(); got == 0 {
		t.Fatal("expected configured HTTP client to be used")
	}
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

type recordingHTTPClient struct {
	client *http.Client
	calls  atomic.Int64
}

func (c *recordingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.client.Do(req)
}

func compileToolInputSchema(t *testing.T, tool mcp.Tool) *jsonschemav6.Schema {
	t.Helper()

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal tool %q failed: %v", tool.Name, err)
	}
	var rawTool map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawTool); err != nil {
		t.Fatalf("unmarshal tool %q failed: %v", tool.Name, err)
	}
	rawSchema, ok := rawTool["inputSchema"]
	if !ok {
		t.Fatalf("tool %q did not include inputSchema: %s", tool.Name, data)
	}
	var schemaDoc any
	if err := json.Unmarshal(rawSchema, &schemaDoc); err != nil {
		t.Fatalf("tool %q inputSchema is not JSON: %v; schema=%s", tool.Name, err, rawSchema)
	}

	compiler := jsonschemav6.NewCompiler()
	schemaURL := tool.Name + ".schema.json"
	if err := compiler.AddResource(schemaURL, schemaDoc); err != nil {
		t.Fatalf("add inputSchema for tool %q failed: %v; schema=%s", tool.Name, err, rawSchema)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatalf("compile inputSchema for tool %q failed: %v; schema=%s", tool.Name, err, rawSchema)
	}
	return schema
}

func assertSchemaValid(t *testing.T, schema *jsonschemav6.Schema, value any) {
	t.Helper()

	if err := schema.Validate(value); err != nil {
		t.Fatalf("expected schema to accept %#v: %v", value, err)
	}
}

func assertSchemaInvalid(t *testing.T, schema *jsonschemav6.Schema, value any) {
	t.Helper()

	if err := schema.Validate(value); err == nil {
		t.Fatalf("expected schema to reject %#v", value)
	}
}

func TestServiceFilterLimitsReflectedTools(t *testing.T) {
	backendURL := startReflectingHealthBackend(t)
	descriptors, err := grpcmcp.LoadDescriptorsFromReflection(t.Context(), backendURL, nil, false)
	if err != nil {
		t.Fatalf("LoadDescriptorsFromReflection failed: %v", err)
	}
	srv, err := grpcmcp.NewServer(grpcmcp.Config{
		Headers:     grpcmcp.StaticHeaders(nil),
		ServerName:  "test grpc mcp",
		Version:     "test",
		Descriptors: descriptors,
		Services:    []protoreflect.FullName{"missing.Service"},
		BaseURL:     backendURL,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
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

func startReflectingHealthBackendWithAuth(t *testing.T, wantAuthorization string) string {
	t.Helper()

	mux := http.NewServeMux()
	health := grpchealth.NewStaticChecker(grpchealth.HealthV1ServiceName)
	reflector := grpcreflect.NewStaticReflector(grpchealth.HealthV1ServiceName)
	path, handler := grpchealth.NewHandler(health)
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != wantAuthorization {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	srv := httptest.NewUnstartedServer(h2c.NewHandler(mux, &http2.Server{}))
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

func buildReflectedTestServer(t *testing.T, backendURL string) *mcpserver.MCPServer {
	t.Helper()

	descriptors := loadReflectedDescriptors(t, backendURL)
	return buildTestServerFromDescriptors(t, backendURL, descriptors, []protoreflect.FullName{protoreflect.FullName(grpchealth.HealthV1ServiceName)})
}

func loadReflectedDescriptors(t *testing.T, backendURL string) *descriptorpb.FileDescriptorSet {
	t.Helper()

	descriptors, err := grpcmcp.LoadDescriptorsFromReflection(t.Context(), backendURL, nil, false)
	if err != nil {
		t.Fatalf("LoadDescriptorsFromReflection failed: %v", err)
	}
	return descriptors
}

func buildTestServerFromDescriptors(t *testing.T, backendURL string, descriptors *descriptorpb.FileDescriptorSet, services []protoreflect.FullName) *mcpserver.MCPServer {
	t.Helper()

	srv, err := grpcmcp.NewServer(grpcmcp.Config{
		Headers:     grpcmcp.StaticHeaders(nil),
		ServerName:  "test grpc mcp",
		Version:     "test",
		Descriptors: descriptors,
		Services:    services,
		BaseURL:     backendURL,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
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
