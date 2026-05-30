# JSON Schema Adapter

This package is an adapted copy of Buf's protobuf JSON Schema generator:

https://github.com/bufbuild/protoschema-plugins/blob/main/internal/protoschema/jsonschema/jsonschema.go

It is kept local because `grpcmcp` needs a single JSON Schema object for each
MCP tool input. The upstream generator is designed to emit a set of schemas
with `$id`, `$ref`, and optional bundled `$defs`.

## Local adaptations

Keep these changes when updating from upstream:

* Package name is `buf`, not `jsonschema`.
* `Generate(desc, opts...) map[string]interface{}` is the public API used by
  `main.go`.
* `Generate` returns only the root message schema and strips root `$schema`,
  `$id`, and `title` so the result stays suitable for
  `mcp.NewToolWithRawSchema`.
* Nested message fields are inlined instead of emitted as `$ref` references.
* Trailing protobuf comments are appended to descriptions, preserving the older
  local behavior.
* `WithStringOnly64BitIntegers` is a local option used by the `--string64` CLI
  flag. It emits 64-bit protobuf integer fields as string schemas only, avoiding
  precision ambiguity for JavaScript-based MCP clients while keeping the default
  upstream-compatible either-number-or-string behavior.
* `base64EncodedLength` includes a local fix for byte lengths divisible by 3:
  padding must be `(4 - (characters % 4)) % 4`.

## Updating

To refresh this file from upstream:

1. Copy the latest upstream `jsonschema.go` into this directory.
2. Re-apply the local adaptations above.
3. Run:

   ```sh
   gofmt -w buf/jsonschema.go buf/jsonschema_test.go
   go test ./...
   go vet ./...
   ```

The tests in `buf/jsonschema_test.go` cover the local base64 bound fix and the
64-bit integer schema modes.
