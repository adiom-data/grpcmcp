package buf

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestBase64EncodedLength(t *testing.T) {
	tests := []struct {
		inputSize uint64
		wantMin   uint64
		wantPad   uint64
	}{
		{inputSize: 0, wantMin: 0, wantPad: 0},
		{inputSize: 1, wantMin: 2, wantPad: 2},
		{inputSize: 2, wantMin: 3, wantPad: 1},
		{inputSize: 3, wantMin: 4, wantPad: 0},
		{inputSize: 4, wantMin: 6, wantPad: 2},
		{inputSize: 5, wantMin: 7, wantPad: 1},
		{inputSize: 6, wantMin: 8, wantPad: 0},
	}

	for _, tt := range tests {
		gotMin, gotPad := base64EncodedLength(tt.inputSize)
		if gotMin != tt.wantMin || gotPad != tt.wantPad {
			t.Fatalf("base64EncodedLength(%d) = (%d, %d), want (%d, %d)", tt.inputSize, gotMin, gotPad, tt.wantMin, tt.wantPad)
		}
	}
}

func TestInt64AndUint64SchemasDoNotEmitUnsafeIntegerBounds(t *testing.T) {
	desc := testMessageDescriptor(t, []*descriptorpb.FieldDescriptorProto{
		{
			Name:   proto.String("int64_value"),
			Number: proto.Int32(1),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum(),
		},
		{
			Name:   proto.String("uint64_value"),
			Number: proto.Int32(2),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_UINT64.Enum(),
		},
	})

	schema := Generate(desc)
	assertNoUnsafeIntegerBounds(t, schema)

	properties := schemaMap(t, schema["properties"], "properties")
	int64Schema := schemaMap(t, properties["int64_value"], "properties.int64_value")
	uint64Schema := schemaMap(t, properties["uint64_value"], "properties.uint64_value")

	assertAnyOfContains(t, int64Schema, "properties.int64_value", map[string]any{
		"type": jsInteger,
	})
	assertAnyOfContains(t, int64Schema, "properties.int64_value", map[string]any{
		"type":    jsString,
		"pattern": "^-?[0-9]+$",
	})
	assertAnyOfContains(t, uint64Schema, "properties.uint64_value", map[string]any{
		"type":    jsInteger,
		"minimum": 0,
	})
	assertAnyOfContains(t, uint64Schema, "properties.uint64_value", map[string]any{
		"type":    jsString,
		"pattern": "^[0-9]+$",
	})
}

func TestStringOnly64BitIntegerSchemas(t *testing.T) {
	desc := testMessageDescriptor(t, []*descriptorpb.FieldDescriptorProto{
		{
			Name:   proto.String("int64_value"),
			Number: proto.Int32(1),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum(),
		},
		{
			Name:   proto.String("uint64_value"),
			Number: proto.Int32(2),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_UINT64.Enum(),
		},
	})

	schema := Generate(desc, WithStringOnly64BitIntegers())
	assertNoUnsafeIntegerBounds(t, schema)

	properties := schemaMap(t, schema["properties"], "properties")
	assertStringSchema(t, properties["int64_value"], "properties.int64_value", "^-?[0-9]+$")
	assertStringSchema(t, properties["uint64_value"], "properties.uint64_value", "^[0-9]+$")
}

func testMessageDescriptor(t *testing.T, fields []*descriptorpb.FieldDescriptorProto) protoreflect.MessageDescriptor {
	t.Helper()

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("test.proto"),
		Package: proto.String("test"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:  proto.String("NumericBounds"),
				Field: fields,
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewFile failed: %v", err)
	}
	return file.Messages().ByName("NumericBounds")
}

func assertNoUnsafeIntegerBounds(t *testing.T, value any) {
	t.Helper()
	assertNoUnsafeIntegerBoundsAt(t, value, "$")
}

func assertNoUnsafeIntegerBoundsAt(t *testing.T, value any, path string) {
	t.Helper()

	switch value := value.(type) {
	case map[string]any:
		for key, nested := range value {
			nestedPath := path + "." + key
			if strings.Contains(strings.ToLower(key), "maximum") ||
				strings.Contains(strings.ToLower(key), "minimum") {
				assertSafeIntegerBound(t, nested, nestedPath)
			}
			assertNoUnsafeIntegerBoundsAt(t, nested, nestedPath)
		}
	case []any:
		for i, nested := range value {
			assertNoUnsafeIntegerBoundsAt(t, nested, fmt.Sprintf("%s[%d]", path, i))
		}
	case []map[string]any:
		for i, nested := range value {
			assertNoUnsafeIntegerBoundsAt(t, nested, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

func assertSafeIntegerBound(t *testing.T, value any, path string) {
	t.Helper()

	switch value := value.(type) {
	case int:
		assertSafeSignedIntegerBound(t, int64(value), path)
	case int32:
		assertSafeSignedIntegerBound(t, int64(value), path)
	case int64:
		assertSafeSignedIntegerBound(t, value, path)
	case uint:
		assertSafeUnsignedIntegerBound(t, uint64(value), path)
	case uint32:
		assertSafeUnsignedIntegerBound(t, uint64(value), path)
	case uint64:
		assertSafeUnsignedIntegerBound(t, value, path)
	case float32:
		assertSafeFloatIntegerBound(t, float64(value), path)
	case float64:
		assertSafeFloatIntegerBound(t, value, path)
	}
}

func assertSafeSignedIntegerBound(t *testing.T, value int64, path string) {
	t.Helper()

	if value < jsMinInt || value > jsMaxInt {
		t.Fatalf("%s emitted unsafe integer bound %d", path, value)
	}
}

func assertSafeUnsignedIntegerBound(t *testing.T, value uint64, path string) {
	t.Helper()

	if value > jsMaxUint {
		t.Fatalf("%s emitted unsafe integer bound %d", path, value)
	}
}

func assertSafeFloatIntegerBound(t *testing.T, value float64, path string) {
	t.Helper()

	if value < float64(jsMinInt) || value > float64(jsMaxInt) || math.Trunc(value) != value {
		t.Fatalf("%s emitted unsafe integer bound %g", path, value)
	}
}

func schemaMap(t *testing.T, value any, path string) map[string]any {
	t.Helper()

	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %T, want map[string]any", path, value)
	}
	return result
}

func assertStringSchema(t *testing.T, value any, path string, pattern string) {
	t.Helper()

	schema := schemaMap(t, value, path)
	if schema["type"] != jsString {
		t.Fatalf("%s.type = %v, want %q", path, schema["type"], jsString)
	}
	if schema["pattern"] != pattern {
		t.Fatalf("%s.pattern = %v, want %q", path, schema["pattern"], pattern)
	}
	if schema["default"] != "0" {
		t.Fatalf("%s.default = %v, want %q", path, schema["default"], "0")
	}
	if _, ok := schema["anyOf"]; ok {
		t.Fatalf("%s should not include anyOf in string-only mode: %#v", path, schema)
	}
}

func assertAnyOfContains(t *testing.T, schema map[string]any, path string, want map[string]any) {
	t.Helper()

	anyOf, ok := schema["anyOf"].([]map[string]any)
	if !ok {
		t.Fatalf("%s.anyOf = %T, want []map[string]any", path, schema["anyOf"])
	}
	for _, candidate := range anyOf {
		if schemaContains(candidate, want) {
			return
		}
	}
	t.Fatalf("%s.anyOf = %#v, want entry containing %#v", path, anyOf, want)
}

func schemaContains(candidate, want map[string]any) bool {
	for key, wantValue := range want {
		if candidate[key] != wantValue {
			return false
		}
	}
	return true
}
