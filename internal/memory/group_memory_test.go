package memory

import (
	"os"
	"testing"
	"time"
)

func TestSafeGroupKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"123456", "123456"},
		{"group:123456", "123456"},
		{"qq:group:123456", "123456"},
		{"onebot:group:123456", "123456"},
		{"astrbot:group:123456", "123456"},
		{"group:abc/def\\ghi:jkl*mno?pqr\"stu<vwx>yz|123", "abc_def_ghi_jkl_mno_pqr_stu_vwx_yz_123"},
		{"", "unknown_group"},
		{"   ", "unknown_group"},
	}

	for _, tt := range tests {
		got := SafeGroupKey(tt.input)
		if got != tt.expected {
			t.Errorf("SafeGroupKey(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestResolveCallingNamePriority(t *testing.T) {
	tests := []struct {
		name     string
		member   MemberProfile
		expected string
	}{
		{
			name: "preferred name has highest priority",
			member: MemberProfile{
				UserID:        "user_test_1",
				Nickname:      "NickOne",
				Card:          "CardOne",
				PreferredName: "PrefOne",
			},
			expected: "PrefOne",
		},
		{
			name: "nickname takes precedence when preferred name is empty",
			member: MemberProfile{
				UserID:        "user_test_2",
				Nickname:      "NickTwo",
				Card:          "CardTwo",
				PreferredName: "",
			},
			expected: "NickTwo",
		},
		{
			name: "card is strictly disambiguation and never used as calling name",
			member: MemberProfile{
				UserID:        "user_test_3",
				Nickname:      "",
				Card:          "CardThree",
				PreferredName: "",
			},
			expected: "群友",
		},
		{
			name: "fallback to neutral address when all are empty",
			member: MemberProfile{
				UserID:        "user_test_4",
				Nickname:      "",
				Card:          "",
				PreferredName: "",
			},
			expected: "群友",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.member.ResolveCallingName()
			if got != tt.expected {
				t.Errorf("ResolveCallingName() = %q; want %q", got, tt.expected)
			}
		})
	}
}

func TestGroupStore_MemberObservationAndRoleSync(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_group_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gs, err := NewGroupStore(tempDir, "test_group_101")
	if err != nil {
		t.Fatalf("NewGroupStore failed: %v", err)
	}

	// First observation
	m1, err := gs.ObserveMember("test_u_1", "UserAlpha", "CardAlpha", "member", "onebot")
	if err != nil {
		t.Fatalf("ObserveMember failed: %v", err)
	}
	if m1.Nickname != "UserAlpha" || m1.Role != GroupRoleMember {
		t.Errorf("m1 mismatch: %+v", m1)
	}

	// Update role to admin and change nickname
	m2, err := gs.ObserveMember("test_u_1", "UserAlphaRenamed", "CardAlpha", "admin", "onebot")
	if err != nil {
		t.Fatalf("ObserveMember 2 failed: %v", err)
	}
	if m2.Nickname != "UserAlphaRenamed" || m2.Role != GroupRoleAdmin {
		t.Errorf("m2 mismatch: %+v", m2)
	}

	// Set preferred name and aliases
	err = gs.UpdateMemberPreferredName("test_u_1", "AlphaChief", []string{"Chief", "Boss"})
	if err != nil {
		t.Fatalf("UpdateMemberPreferredName failed: %v", err)
	}

	prof, err := gs.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	m3 := prof.GetMember("test_u_1")
	if m3 == nil {
		t.Fatalf("GetMember failed")
	}
	if m3.PreferredName != "AlphaChief" || len(m3.Aliases) != 2 {
		t.Errorf("m3 profile mismatch: %+v", m3)
	}
	if m3.ResolveCallingName() != "AlphaChief" {
		t.Errorf("m3 calling name = %q; want AlphaChief", m3.ResolveCallingName())
	}

	// Update group name
	if err := gs.UpdateGroupName("Frost Test Group"); err != nil {
		t.Fatalf("UpdateGroupName failed: %v", err)
	}
	prof, _ = gs.GetProfile()
	if prof.GroupName != "Frost Test Group" {
		t.Errorf("GroupName = %q; want 'Frost Test Group'", prof.GroupName)
	}

	// Reopen group store from disk to verify persistence
	gsReopened, err := NewGroupStore(tempDir, "test_group_101")
	if err != nil {
		t.Fatalf("reopening GroupStore failed: %v", err)
	}
	reopenedProf, err := gsReopened.GetProfile()
	if err != nil {
		t.Fatalf("reopened GetProfile failed: %v", err)
	}
	if reopenedProf.GroupName != "Frost Test Group" {
		t.Errorf("reopened GroupName = %q; want 'Frost Test Group'", reopenedProf.GroupName)
	}
	reopenedMember := reopenedProf.GetMember("test_u_1")
	if reopenedMember == nil || reopenedMember.PreferredName != "AlphaChief" {
		t.Errorf("reopened member profile mismatch: %+v", reopenedMember)
	}
}

func TestGroupStore_MemoryCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_group_mem_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gs, err := NewGroupStore(tempDir, "test_group_202")
	if err != nil {
		t.Fatalf("NewGroupStore failed: %v", err)
	}

	now := time.Now().UTC()
	entries := []MemoryEntry{
		{
			ID:        "gmem_1",
			Owner:     "test_user_a",
			Content:   "test_user_a likes programming in Go",
			Tags:      []string{"programming", "golang"},
			Source:    "extract",
			CreatedAt: now,
			UpdatedAt: now,
			ScopeType: ScopeGroup,
			GroupID:   "test_group_202",
		},
		{
			ID:        "gmem_2",
			Owner:     GroupOwnerExplicit,
			Content:   "Group rule: no advertising allowed",
			Tags:      []string{"rule", "regulation"},
			Source:    "manual",
			CreatedAt: now,
			UpdatedAt: now,
			ScopeType: ScopeGroup,
			GroupID:   "test_group_202",
		},
		{
			ID:        "gmem_3",
			Owner:     GroupOwnerExplicit,
			Content:   "test_user_a says test_user_b is arriving tomorrow",
			Tags:      []string{"test_user_b", "schedule"},
			Source:    "extract",
			CreatedAt: now,
			UpdatedAt: now,
			ScopeType: ScopeGroup,
			GroupID:   "test_group_202",
		},
	}

	for _, e := range entries {
		if err := gs.Save(e); err != nil {
			t.Fatalf("Save(%s) failed: %v", e.ID, err)
		}
	}

	all, err := gs.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAll len = %d; want 3", len(all))
	}

	byUserA, err := gs.ListByOwner("test_user_a")
	if err != nil {
		t.Fatalf("ListByOwner(test_user_a) failed: %v", err)
	}
	if len(byUserA) != 1 || byUserA[0].ID != "gmem_1" {
		t.Errorf("ListByOwner(test_user_a) mismatch: %+v", byUserA)
	}

	byGroup, err := gs.ListByOwner(GroupOwnerExplicit)
	if err != nil {
		t.Fatalf("ListByOwner(group) failed: %v", err)
	}
	if len(byGroup) != 2 {
		t.Errorf("ListByOwner(group) len = %d; want 2", len(byGroup))
	}

	// Search
	searchRes, err := gs.Search("rule", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(searchRes) != 1 || searchRes[0].ID != "gmem_2" {
		t.Errorf("Search(rule) mismatch: %+v", searchRes)
	}

	// Tag search
	tagRes, err := gs.SearchByTags([]string{"golang"}, 10)
	if err != nil {
		t.Fatalf("SearchByTags failed: %v", err)
	}
	if len(tagRes) != 1 || tagRes[0].ID != "gmem_1" {
		t.Errorf("SearchByTags(golang) mismatch: %+v", tagRes)
	}

	// Delete
	if err := gs.Delete("gmem_1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	allAfterDelete, _ := gs.ListAll()
	if len(allAfterDelete) != 2 {
		t.Errorf("ListAll len after delete = %d; want 2", len(allAfterDelete))
	}
}

func TestGroupManager_Lifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_group_mgr_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gm := NewGroupManager(tempDir, nil)

	// Create/Get group 1
	g1, err := gm.GetGroupStore("test_grp_1")
	if err != nil {
		t.Fatalf("GetGroupStore 1 failed: %v", err)
	}
	_ = g1.UpdateGroupName("Alpha Group")
	_, _ = g1.ObserveMember("u1", "User1", "", "member", "onebot")

	// Create/Get group 2
	g2, err := gm.GetGroupStore("test_grp_2")
	if err != nil {
		t.Fatalf("GetGroupStore 2 failed: %v", err)
	}
	_ = g2.UpdateGroupName("Beta Group")

	groups, err := gm.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups failed: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("ListGroups len = %d; want 2", len(groups))
	}
}
