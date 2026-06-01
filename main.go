package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/adiom-data/grpcmcp/grpcmcp"
	"github.com/mark3labs/mcp-go/server"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

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
	sseHostPort := flag.String("hostport", "", "host:port for HTTP server, STDIN if not set")
	transport := flag.String("transport", "http", "Transport to use when hostport is set: http or sse")
	descriptors := flag.String("descriptors", "", "Location of the descriptor")
	reflect := flag.Bool("reflect", false, "Use reflection to get descriptors")
	services := flag.String("services", "", "If set, a comma separated list of services to expose")
	bearer := flag.String("bearer", "", "Token to use in an Authorization bearer header")
	bearerEnv := flag.String("bearer-env", "", "Environment variable for token to use in an Authorization bearer header")
	baseURL := flag.String("url", "http://localhost:8090", "The url of the backend")
	useConnect := flag.Bool("connect", false, "Use connect protocol (instead of gRPC)")
	string64 := flag.Bool("string64", false, "Expose 64-bit protobuf integer fields as strings only in JSON schemas")

	flag.Parse()

	if *bearerEnv != "" {
		*bearer, _ = os.LookupEnv(*bearerEnv)
	}

	if *bearer != "" {
		http.Header(headers).Set("Authorization", "Bearer "+*bearer)
	}

	ctx := context.Background()

	if *descriptors == "" && !*reflect {
		fmt.Fprint(os.Stderr, "descriptors or reflect is required.\n")
		flag.Usage()
		os.Exit(-1)
	}

	descriptorSet, err := loadDescriptors(ctx, *descriptors, *reflect, *baseURL, http.Header(headers), *useConnect)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(-1)
	}
	serviceNames := parseServices(*services)

	srv, err := grpcmcp.NewServer(grpcmcp.Config{
		Headers:     grpcmcp.StaticHeaders(http.Header(headers)),
		ServerName:  *serverName,
		Version:     *serverVersion,
		Descriptors: descriptorSet,
		Services:    serviceNames,
		BaseURL:     *baseURL,
		UseConnect:  *useConnect,
		String64:    *string64,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(-1)
	}

	if *sseHostPort == "" {
		if err := server.ServeStdio(srv); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	} else {
		var err error
		switch *transport {
		case "http", "streamable-http":
			httpSrv := server.NewStreamableHTTPServer(srv)
			err = httpSrv.Start(*sseHostPort)
		case "sse":
			sseSrv := server.NewSSEServer(srv)
			err = sseSrv.Start(*sseHostPort)
		default:
			err = fmt.Errorf("unknown transport %q: expected http, streamable-http, or sse", *transport)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}
}

func loadDescriptors(ctx context.Context, descriptorsPath string, reflect bool, baseURL string, headers http.Header, useConnect bool) (*descriptorpb.FileDescriptorSet, error) {
	var descriptorSet *descriptorpb.FileDescriptorSet
	if reflect {
		var err error
		descriptorSet, err = grpcmcp.LoadDescriptorsFromReflection(ctx, baseURL, headers, useConnect)
		if err != nil {
			return nil, err
		}
	}
	if descriptorsPath != "" {
		return grpcmcp.LoadDescriptorsFromFile(descriptorsPath)
	}
	return descriptorSet, nil
}

func parseServices(services string) []protoreflect.FullName {
	if services == "" {
		return nil
	}
	parts := strings.Split(services, ",")
	result := make([]protoreflect.FullName, 0, len(parts))
	for _, service := range parts {
		service = strings.TrimSpace(service)
		if service != "" {
			result = append(result, protoreflect.FullName(service))
		}
	}
	return result
}
