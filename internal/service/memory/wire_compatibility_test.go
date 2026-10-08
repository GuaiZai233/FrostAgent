package memsvc

import (
	v1 "FrostAgent/gen/proto/frostagent/v1"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestProtobufWireBackwardCompatibility(t *testing.T) {
	// 1. ListMemoriesRequest: tag 1 = owner, tag 2 = pagination, tag 3 = scope, tag 4 = group_id
	listReq := &v1.ListMemoriesRequest{
		Owner: "mock_user_1",
		Pagination: &v1.Pagination{
			PageSize:  20,
			PageToken: "token_1",
		},
		Scope:   "group",
		GroupId: "mock_group_100",
	}
	data, err := proto.Marshal(listReq)
	if err != nil {
		t.Fatalf("marshal ListMemoriesRequest failed: %v", err)
	}
	var unmarshaledList v1.ListMemoriesRequest
	if err := proto.Unmarshal(data, &unmarshaledList); err != nil {
		t.Fatalf("unmarshal ListMemoriesRequest failed: %v", err)
	}
	if unmarshaledList.GetOwner() != "mock_user_1" ||
		unmarshaledList.GetPagination().GetPageSize() != 20 ||
		unmarshaledList.GetPagination().GetPageToken() != "token_1" ||
		unmarshaledList.GetScope() != "group" ||
		unmarshaledList.GetGroupId() != "mock_group_100" {
		t.Errorf("ListMemoriesRequest roundtrip mismatch: %+v", &unmarshaledList)
	}

	// 2. UpdateMemoryRequest: tag 1 = id, tag 2 = content, tag 3 = tags, tag 4 = visibility, tag 5 = scope, tag 6 = group_id
	updateReq := &v1.UpdateMemoryRequest{
		Id:         "mem_123",
		Content:    "updated content",
		Tags:       []string{"t1", "t2"},
		Visibility: "private",
		Scope:      "group",
		GroupId:    "mock_group_100",
	}
	updateData, err := proto.Marshal(updateReq)
	if err != nil {
		t.Fatalf("marshal UpdateMemoryRequest failed: %v", err)
	}
	var unmarshaledUpdate v1.UpdateMemoryRequest
	if err := proto.Unmarshal(updateData, &unmarshaledUpdate); err != nil {
		t.Fatalf("unmarshal UpdateMemoryRequest failed: %v", err)
	}
	if unmarshaledUpdate.GetId() != "mem_123" ||
		unmarshaledUpdate.GetContent() != "updated content" ||
		len(unmarshaledUpdate.GetTags()) != 2 ||
		unmarshaledUpdate.GetVisibility() != "private" ||
		unmarshaledUpdate.GetScope() != "group" ||
		unmarshaledUpdate.GetGroupId() != "mock_group_100" {
		t.Errorf("UpdateMemoryRequest roundtrip mismatch: %+v", &unmarshaledUpdate)
	}

	// 3. DeleteMemoryRequest: tag 1 = id, tag 2 = scope, tag 3 = group_id
	delReq := &v1.DeleteMemoryRequest{
		Id:      "mem_123",
		Scope:   "group",
		GroupId: "mock_group_100",
	}
	delData, err := proto.Marshal(delReq)
	if err != nil {
		t.Fatalf("marshal DeleteMemoryRequest failed: %v", err)
	}
	var unmarshaledDel v1.DeleteMemoryRequest
	if err := proto.Unmarshal(delData, &unmarshaledDel); err != nil {
		t.Fatalf("unmarshal DeleteMemoryRequest failed: %v", err)
	}
	if unmarshaledDel.GetId() != "mem_123" ||
		unmarshaledDel.GetScope() != "group" ||
		unmarshaledDel.GetGroupId() != "mock_group_100" {
		t.Errorf("DeleteMemoryRequest roundtrip mismatch: %+v", &unmarshaledDel)
	}

	// 4. ImportMemoriesRequest: tag 1 = json_content, tag 2 = overwrite, tag 3 = scope, tag 4 = group_id
	importReq := &v1.ImportMemoriesRequest{
		JsonContent: "{}",
		Overwrite:   true,
		Scope:       "group",
		GroupId:     "mock_group_100",
	}
	importData, err := proto.Marshal(importReq)
	if err != nil {
		t.Fatalf("marshal ImportMemoriesRequest failed: %v", err)
	}
	var unmarshaledImport v1.ImportMemoriesRequest
	if err := proto.Unmarshal(importData, &unmarshaledImport); err != nil {
		t.Fatalf("unmarshal ImportMemoriesRequest failed: %v", err)
	}
	if unmarshaledImport.GetJsonContent() != "{}" ||
		!unmarshaledImport.GetOverwrite() ||
		unmarshaledImport.GetScope() != "group" ||
		unmarshaledImport.GetGroupId() != "mock_group_100" {
		t.Errorf("ImportMemoriesRequest roundtrip mismatch: %+v", &unmarshaledImport)
	}
}
