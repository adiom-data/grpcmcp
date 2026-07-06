package grpcmcp

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestNewServerMapsIdempotencyLevelToToolAnnotations(t *testing.T) {
	method := func(name string, level *descriptorpb.MethodOptions_IdempotencyLevel) *descriptorpb.MethodDescriptorProto {
		m := &descriptorpb.MethodDescriptorProto{
			Name:       proto.String(name),
			InputType:  proto.String(".annotations.v1.Empty"),
			OutputType: proto.String(".annotations.v1.Empty"),
		}
		if level != nil {
			m.Options = &descriptorpb.MethodOptions{IdempotencyLevel: level}
		}
		return m
	}
	file := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("annotations_test.proto"),
		Package:     proto.String("annotations.v1"),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Empty")}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("ThingService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				method("GetThing", descriptorpb.MethodOptions_NO_SIDE_EFFECTS.Enum()),
				method("PutThing", descriptorpb.MethodOptions_IDEMPOTENT.Enum()),
				method("DeleteThing", nil),
			},
		}},
	}

	srv, err := NewServer(Config{
		ServerName:  "test",
		Version:     "0.0.1",
		BaseURL:     "http://localhost:1",
		Descriptors: &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}},
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	tools := srv.ListTools()

	annotations := func(name string) mcp.ToolAnnotation {
		tool, ok := tools["annotations_v1_ThingService__"+name]
		if !ok {
			t.Fatalf("missing tool for method %s, got %d tools", name, len(tools))
		}
		return tool.Tool.Annotations
	}

	get := annotations("GetThing")
	if get.ReadOnlyHint == nil || !*get.ReadOnlyHint {
		t.Fatal("NO_SIDE_EFFECTS method must have readOnlyHint=true")
	}
	if get.IdempotentHint == nil || !*get.IdempotentHint {
		t.Fatal("NO_SIDE_EFFECTS method must have idempotentHint=true")
	}

	put := annotations("PutThing")
	if put.ReadOnlyHint != nil {
		t.Fatalf("IDEMPOTENT method must not set readOnlyHint, got %v", *put.ReadOnlyHint)
	}
	if put.IdempotentHint == nil || !*put.IdempotentHint {
		t.Fatal("IDEMPOTENT method must have idempotentHint=true")
	}

	del := annotations("DeleteThing")
	if del.ReadOnlyHint != nil || del.IdempotentHint != nil {
		t.Fatal("method without idempotency_level must get no hints")
	}
}

func TestTopSortReturnsErrorForMissingDependency(t *testing.T) {
	desc := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test.proto"),
		Dependency: []string{"missing.proto"},
	}

	var sorted []*descriptorpb.FileDescriptorProto
	err := topSort(desc, map[string]*descriptorpb.FileDescriptorProto{
		desc.GetName(): desc,
	}, map[string]struct{}{}, &sorted)
	if err == nil {
		t.Fatal("expected missing dependency error")
	}
	if !strings.Contains(err.Error(), `depends on missing descriptor "missing.proto"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
