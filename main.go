package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/adiom-data/grpcmcp/buf"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

var protojsonMarshaller = protojson.MarshalOptions{UseProtoNames: true}
var protojsonUnmarshaller = protojson.UnmarshalOptions{DiscardUnknown: true}

func toolHandler(c *connect.Client[dynamicpb.Message, dynamicpb.Message], desc protoreflect.MessageDescriptor, headers http.Header) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msg := dynamicpb.NewMessage(desc)
		b, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if err := protojsonUnmarshaller.Unmarshal(b, msg); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		req := connect.NewRequest(msg)
		if len(headers) > 0 {
			for k, v := range headers {
				if len(v) == 1 {
					req.Header().Set(k, v[0])
				} else {
					req.Header().Del(k)
					for _, v2 := range v {
						req.Header().Add(k, v2)
					}
				}
			}
		}
		resp, err := c.CallUnary(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := protojsonMarshaller.Marshal(resp.Msg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(res)), nil
	}
}

func insecureClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
}

var responseInitializer = connect.WithResponseInitializer(func(spec connect.Spec, message any) error {
	if m, ok := message.(*dynamicpb.Message); ok {
		desc := spec.Schema.(protoreflect.MethodDescriptor)
		*m = *dynamicpb.NewMessage(desc.Output())
	}
	return nil
})

func topSort(d *descriptorpb.FileDescriptorProto, all map[string]*descriptorpb.FileDescriptorProto, seen map[string]struct{}, ds *[]*descriptorpb.FileDescriptorProto) {
	if _, found := seen[d.GetName()]; found {
		return
	}
	seen[d.GetName()] = struct{}{}
	for _, dep := range d.GetDependency() {
		v, found := all[dep]
		if !found {
			panic("not found: " + dep)
		}
		topSort(v, all, seen, ds)
	}
	*ds = append(*ds, d)
}

type headerFlags http.Header

func (s *headerFlags) String() string {
	return fmt.Sprintf("%v", *s)
}

func (s *headerFlags) Set(value string) error {
	k, v, ok := strings.Cut(value, ":")
	if !ok {
		return fmt.Errorf("err invalid header format: expecting `key: value`")
	}
	h := http.Header(*s)
	h.Add(k, strings.TrimLeft(v, " "))
	return nil
}

func main() {
	headers := make(headerFlags)
	flag.Var(&headers, "header", "Headers to add to the backend request (Header: Value). Can apply multiple times.")
	serverName := flag.String("name", "gRPC MCP Server", "Name of MCP Server")
	serverVersion := flag.String("version", "1.0.0", "Version of MCP Server")
	sseHostPort := flag.String("hostport", "", "host:port for SSE server, STDIN if not set")
	descriptors := flag.String("descriptors", "", "Location of the descriptor")
	reflect := flag.Bool("reflect", false, "Use reflection to get descriptors")
	services := flag.String("services", "", "If set, a comma separated list of services to expose")
	bearer := flag.String("bearer", "", "Token to use in an Authorization bearer header")
	bearerEnv := flag.String("bearer-env", "", "Environment variable for token to use in an Authorization bearer header")
	baseURL := flag.String("url", "http://localhost:8090", "The url of the backend")
	useConnect := flag.Bool("connect", false, "Use connect protocol (instead of gRPC)")

	flag.Parse()

	if *bearerEnv != "" {
		*bearer, _ = os.LookupEnv(*bearerEnv)
	}

	if *bearer != "" {
		http.Header(headers).Set("Authorization", "Bearer "+*bearer)
	}

	servicesMap := map[string]struct{}{}
	if len(*services) > 0 {
		servicesSplit := strings.Split(*services, ",")
		for _, s := range servicesSplit {
			servicesMap[s] = struct{}{}
		}
	}

	ctx := context.Background()

	if *descriptors == "" && !*reflect {
		fmt.Fprint(os.Stderr, "descriptors or reflect is required.\n")
		flag.Usage()
		os.Exit(-1)
	}

	httpClient := http.DefaultClient
	if strings.HasPrefix(*baseURL, "http://") {
		httpClient = insecureClient()
	}
	var connectOpts []connect.ClientOption
	connectOpts = append(connectOpts, responseInitializer)
	if !*useConnect {
		connectOpts = append(connectOpts, connect.WithGRPC())
	}

	srv := server.NewMCPServer(*serverName, *serverVersion)

	var fds descriptorpb.FileDescriptorSet

	if *reflect {
		all := map[string]*descriptorpb.FileDescriptorProto{}
		client := grpcreflect.NewClient(httpClient, *baseURL, connectOpts...)
		stream := client.NewStream(ctx, grpcreflect.WithRequestHeaders(http.Header(headers)))
		names, err := stream.ListServices()
		if err != nil {
			panic(err)
		}
		for _, name := range names {
			fileDescriptors, err := stream.FileContainingSymbol(name)
			if err != nil {
				panic(err)
			}
			for _, d := range fileDescriptors {
				all[d.GetName()] = d
			}
		}
		_, err = stream.Close()
		if err != nil {
			panic(err)
		}
		var ds []*descriptorpb.FileDescriptorProto
		seen := map[string]struct{}{}
		for _, d := range all {
			topSort(d, all, seen, &ds)
		}
		fds.File = ds
	}

	if *descriptors != "" {
		b, err := os.ReadFile(*descriptors)
		if err != nil {
			panic(err)
		}
		if err := proto.Unmarshal(b, &fds); err != nil {
			panic(err)
		}
	}

	reg := new(protoregistry.Files)
	for _, f := range fds.GetFile() {
		desc, err := protodesc.NewFile(f, reg)
		if err != nil {
			panic(err)
		}
		if _, err := reg.FindFileByPath(desc.Path()); err != nil {
			reg.RegisterFile(desc)
		}
		services := desc.Services()
		for i := range services.Len() {
			s := services.Get(i)
			if len(servicesMap) > 0 {
				if _, found := servicesMap[string(s.FullName())]; !found {
					continue
				}
			}
			methods := s.Methods()
			for j := range methods.Len() {
				m := methods.Get(j)
				if m.IsStreamingClient() || m.IsStreamingServer() {
					// Currently don't support streaming
					continue
				}
				input := buf.Generate(m.Input())
				j, err := json.Marshal(input)
				if err != nil {
					panic(err)
				}
				var rawJson json.RawMessage
				if err := rawJson.UnmarshalJSON(j); err != nil {
					panic(err)
				}
				src := desc.SourceLocations().ByDescriptor(m)
				var descriptions []string
				if src.LeadingComments != "" {
					descriptions = append(descriptions, strings.TrimSpace(src.LeadingComments))
				}
				if src.TrailingComments != "" {
					descriptions = append(descriptions, strings.TrimSpace(src.TrailingComments))
				}
				procedure := fmt.Sprintf("/%v/%v", s.FullName(), m.Name())
				description := strings.Join(descriptions, " | ")
				c := connect.NewClient[dynamicpb.Message, dynamicpb.Message](httpClient, *baseURL+procedure, connect.WithSchema(m), connect.WithClientOptions(connectOpts...))

				name := strings.ReplaceAll(fmt.Sprintf("%v__%v", s.FullName(), m.Name()), ".", "_")
				srv.AddTool(mcp.NewToolWithRawSchema(name, description, rawJson), toolHandler(c, m.Input(), http.Header(headers)))
			}
		}
	}

	if *sseHostPort == "" {
		if err := server.ServeStdio(srv); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	} else {
		sseSrv := server.NewSSEServer(srv)
		if err := sseSrv.Start(*sseHostPort); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}
}
