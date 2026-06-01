package grpcmcp

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

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
