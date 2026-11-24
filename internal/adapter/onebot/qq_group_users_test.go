package onebot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func qqMemberListResponse(count int) oneBotAPIResponse {
	members := make([]map[string]any, 0, count)
	for i := 1; i <= count; i++ {
		members = append(members, map[string]any{"user_id": 1000 + i, "nickname": fmt.Sprintf("member%d", i), "card": "", "role": "member"})
	}
	data, _ := json.Marshal(members)
	return oneBotAPIResponse{Status: "ok", Data: data}
}

func qqGroupUserCall(t *testing.T, list oneBotAPIResponse, info oneBotAPIResponse) (func(model.OneBotAction) (oneBotAPIResponse, error), *[]string) {
	t.Helper()
	var actions []string
	return func(action model.OneBotAction) (oneBotAPIResponse, error) {
		actions = append(actions, action.Action)
		params := action.Params.(map[string]any)
		if params["group_id"] != int64(42) {
			t.Errorf("wrong group: %+v", params)
		}
		switch action.Action {
		case "get_group_member_list":
			// OneBot v11 defines only group_id for this action.
			if len(params) != 1 {
				t.Errorf("unexpected member-list params: %+v", params)
			}
			return list, nil
		case "get_group_info":
			return info, nil
		}
		t.Errorf("unexpected action %s", action.Action)
		return oneBotAPIResponse{}, fmt.Errorf("unexpected")
	}, &actions
}

func TestQQGroupUserCount(t *testing.T) {
	call, actions := qqGroupUserCall(t, qqMemberListResponse(3), oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"group_id":42,"member_count":120}`)})
	result, err := runQQGroupUsers(42, tools.QQRequest{Action: "group_user_count"}, call)
	if err != nil || !strings.Contains(result.Content, `"group_user_count":120`) || len(*actions) != 1 {
		t.Fatalf("%+v %v %v", result, err, *actions)
	}

	// Gateways without member_count fall back to the member list.
	call, actions = qqGroupUserCall(t, qqMemberListResponse(3), oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"group_id":42}`)})
	result, err = runQQGroupUsers(42, tools.QQRequest{Action: "group_user_count"}, call)
	if err != nil || !strings.Contains(result.Content, `"group_user_count":3`) || len(*actions) != 2 {
		t.Fatalf("%+v %v %v", result, err, *actions)
	}
}

func TestQQGroupUserListLimit(t *testing.T) {
	call, _ := qqGroupUserCall(t, qqMemberListResponse(35), oneBotAPIResponse{})
	result, err := runQQGroupUsers(42, tools.QQRequest{Action: "group_user_list"}, call)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Count int `json:"group_user_count"`
		Users []qqGroupMember
	}
	if err := json.Unmarshal([]byte(result.Content), &page); err != nil || page.Count != 35 || len(page.Users) != 35 || page.Users[0].UserID != "1001" {
		t.Fatalf("%s %v", result.Content, err)
	}

	call, _ = qqGroupUserCall(t, qqMemberListResponse(36), oneBotAPIResponse{})
	if _, err := runQQGroupUsers(42, tools.QQRequest{Action: "group_user_list"}, call); err == nil || err.Error() != "当前群人数过多，请使用group_user_search搜索特定群友的昵称。" {
		t.Fatalf("err=%v", err)
	}
}

func TestQQGroupUserSearchRanksFuzzyMatchesAndReturnsQQNumber(t *testing.T) {
	data := `[
		{"user_id":"10005","nickname":"Frost Fox Fan","card":"","role":"member"},
		{"user_id":10002,"nickname":"someone","card":"FrostFox","role":"admin"},
		{"user_id":10001,"nickname":"frost","card":"","role":"owner"},
		{"user_id":10003,"nickname":"f-r-o-s-t","card":"","role":"member"},
		{"user_id":10004,"nickname":"unrelated","card":"","role":"member"},
		{"user_id":0,"nickname":"frost","card":"","role":"member"}
	]`
	call, _ := qqGroupUserCall(t, oneBotAPIResponse{Status: "ok", Data: json.RawMessage(data)}, oneBotAPIResponse{})
	result, err := runQQGroupUsers(42, tools.QQRequest{Action: "group_user_search", Arguments: tools.QQArguments{Query: " FROST "}}, call)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Total int `json:"total_matches"`
		Users []qqGroupMember
	}
	if err := json.Unmarshal([]byte(result.Content), &page); err != nil || page.Total != 4 {
		t.Fatalf("%s %v", result.Content, err)
	}
	var ids []string
	for _, user := range page.Users {
		ids = append(ids, user.UserID)
	}
	// exact, prefix, substring, then in-order subsequence; invalid IDs are dropped.
	if got := strings.Join(ids, ","); got != "10001,10002,10005,10003" {
		t.Fatalf("order=%s", got)
	}

	result, err = runQQGroupUsers(42, tools.QQRequest{Action: "group_user_search", Arguments: tools.QQArguments{Query: "frost", Limit: 2}}, call)
	if err != nil || !strings.Contains(result.Content, `"total_matches":4`) || strings.Count(result.Content, `"user_id"`) != 2 {
		t.Fatalf("%s %v", result.Content, err)
	}

	result, err = runQQGroupUsers(42, tools.QQRequest{Action: "group_user_search", Arguments: tools.QQArguments{Query: "nobody"}}, call)
	if err != nil || !strings.Contains(result.Content, `"total_matches":0`) {
		t.Fatalf("%s %v", result.Content, err)
	}
}

func TestQQGroupUserLookupPropagatesPlatformFailure(t *testing.T) {
	call := func(model.OneBotAction) (oneBotAPIResponse, error) {
		return oneBotAPIResponse{}, fmt.Errorf("platform failed")
	}
	for _, action := range []string{"group_user_list", "group_user_search"} {
		request := tools.QQRequest{Action: action, Arguments: tools.QQArguments{Query: "a"}}
		if _, err := runQQGroupUsers(42, request, call); err == nil {
			t.Fatalf("%s hid platform failure", action)
		}
	}
	bad := func(model.OneBotAction) (oneBotAPIResponse, error) {
		return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(`{"not":"a list"}`)}, nil
	}
	if _, err := runQQGroupUsers(42, tools.QQRequest{Action: "group_user_list"}, bad); err == nil {
		t.Fatal("malformed list accepted")
	}
	for _, data := range []string{`null`, ``} {
		null := func(model.OneBotAction) (oneBotAPIResponse, error) {
			return oneBotAPIResponse{Status: "ok", Data: json.RawMessage(data)}, nil
		}
		for _, action := range []string{"group_user_list", "group_user_search", "group_user_count"} {
			request := tools.QQRequest{Action: action, Arguments: tools.QQArguments{Query: "a"}}
			if result, err := runQQGroupUsers(42, request, null); err == nil {
				t.Fatalf("%s accepted data=%q: %s", action, data, result.Content)
			}
		}
	}
}
