# grpcmcp

A simple MCP server that will proxy to a grpc backend based on a provided descriptors file or using reflection.

## Quick Start

1. Install the binary: `go install .` or `go install github.com/adiom-data/grpcmcp` Ensure the go bin directory is in your PATH.

2. In a terminal, run the example grpc server `go run example/main.go`. This will start a grpc health service on port 8090 with server reflection enabled. Note that this runs on the default port that grpcmcp will connect to.

3. **SSE Transport** In another terminal, run `grpcmcp --hostport=localhost:3000 --reflect`. Specifying `hostport` will use SSE. The SSE endpoint will be served at `http://localhost:3000/sse`.

3. **STDIN Transport** Set up the MCP config. e.g.
```
"grpcmcp": {
    "command": "grpcmcp",
    "args": ["--reflect"]
}
```

## Options / Features

`grpcmcp --help` for a full list of options.

* `hostport` string - When set, use SSE, and this serves as the server host:port.

* `descriptors` string - Specify file location of the protobuf definitions generated from `buf build -o protos.pb` or `protoc --descriptor_set_out=protos.pb` instead of using gRPC reflection.

* `reflect` - If set, use reflection to retrieve gRPC endpoints instead of descriptor file.

* `url` string - Specify the url of the backend server.

* `services` string - Comma separated list of fully qualified gRPC service names to filter.

* `bearer` string - Token to attach in an `Authorization: Bearer` header.

* `bearer-env` string - Environment variable for token to attach in an `Authorization: Bearer` header. Overrides `bearer`.

* `header` string (repeatable) - Headers to add in `Key: Value` format.

## Help

Join our Discord at https://discord.gg/hDjx3DehwG