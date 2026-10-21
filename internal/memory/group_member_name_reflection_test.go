package memory

import (
	"FrostAgent/internal/core"
	"context"
	"strings"
	"testing"
	"time"
)

type memberNameReflectProvider struct {
	response string
	onChat   func(core.ChatRequest)
}

func (p *memberNameReflectProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	if p.onChat != nil {
		p.onChat(req)
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: p.response}}, nil
}

func TestGroupReflectionUpdatesOnlyObservedMemberName(t *testing.T) {
	base := t.TempDir()
	group, err := NewGroupStore(base, "group-one")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewGroupStore(base, "group-two")
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*GroupStore{group, other} {
		if _, err := store.ObserveMember("user-one", "Nickname", "CardOnlySecret", "member", "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := group.Save(MemoryEntry{ID: "evidence-one", Owner: "user-one", Content: "这位成员希望被称为小狐", Source: SourceManual, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	provider := &memberNameReflectProvider{
		response: `{"topics":[],"member_names":[{"user_id":"user-one","preferred_name":"小狐","source_ids":["evidence-one"]},{"user_id":"unobserved","preferred_name":"伪造","source_ids":["evidence-one"]}]}`,
		onChat: func(req core.ChatRequest) {
			prompt := req.Messages[0].Content.(string)
			if !strings.Contains(prompt, `"user_id":"user-one"`) || strings.Contains(prompt, "CardOnlySecret") {
				t.Errorf("reflection member references are missing or include a group card: %s", prompt)
			}
		},
	}
	reflector := NewReflector(NewStore(base+"/private.json"), group.CatalogStore(), provider, "test-model", Config{})
	if err := reflector.ReflectGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	profile, err := group.GetProfile()
	if err != nil || profile.Members["user-one"].PreferredName != "小狐" || profile.Members["unobserved"] != nil {
		t.Fatalf("unexpected reflected profile: %#v, %v", profile, err)
	}
	otherProfile, err := other.GetProfile()
	if err != nil || otherProfile.Members["user-one"].PreferredName != "" {
		t.Fatalf("reflection changed another group: %#v, %v", otherProfile, err)
	}
}

func TestGroupReflectionDoesNotOverwriteConcurrentMemberEdit(t *testing.T) {
	base := t.TempDir()
	group, err := NewGroupStore(base, "group-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := group.ObserveMember("user-one", "Nickname", "", "member", "test"); err != nil {
		t.Fatal(err)
	}
	if err := group.Save(MemoryEntry{ID: "evidence-one", Owner: "user-one", Content: "旧称呼小狐", Source: SourceManual, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	provider := &memberNameReflectProvider{
		response: `{"topics":[],"member_names":[{"user_id":"user-one","preferred_name":"小狐","source_ids":["evidence-one"]}]}`,
		onChat: func(core.ChatRequest) {
			if err := group.UpdateMemberPreferredName("user-one", "人工新称呼", []string{"旧别名"}); err != nil {
				t.Fatal(err)
			}
		},
	}
	reflector := NewReflector(NewStore(base+"/private.json"), group.CatalogStore(), provider, "test-model", Config{})
	if err := reflector.ReflectGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	profile, err := group.GetProfile()
	if err != nil || profile.Members["user-one"].PreferredName != "人工新称呼" || len(profile.Members["user-one"].Aliases) != 1 {
		t.Fatalf("concurrent edit was overwritten: %#v, %v", profile, err)
	}
}

func TestGroupReflectionDoesNotUseChangedOrDeletedEvidence(t *testing.T) {
	for _, operation := range []string{"edit", "delete"} {
		t.Run(operation, func(t *testing.T) {
			base := t.TempDir()
			group, err := NewGroupStore(base, "group-one")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := group.ObserveMember("user-one", "Nickname", "", "member", "test"); err != nil {
				t.Fatal(err)
			}
			if err := group.Save(MemoryEntry{ID: "evidence-one", Owner: "user-one", Content: "叫我小狐", Source: SourceManual, CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			provider := &memberNameReflectProvider{
				response: `{"topics":[],"member_names":[{"user_id":"user-one","preferred_name":"小狐","source_ids":["evidence-one"]}]}`,
				onChat: func(core.ChatRequest) {
					if operation == "delete" {
						if err := group.Delete("evidence-one"); err != nil {
							t.Fatal(err)
						}
						return
					}
					entries, err := group.ListAll()
					if err != nil || len(entries) != 1 {
						t.Fatalf("load evidence: %#v, %v", entries, err)
					}
					entry := entries[0]
					entry.Content = "现在不要叫我小狐"
					if err := group.UpdateEntry(entry); err != nil {
						t.Fatal(err)
					}
				},
			}
			reflector := NewReflector(NewStore(base+"/private.json"), group.CatalogStore(), provider, "test-model", Config{})
			if err := reflector.ReflectGroup(context.Background(), group); err != nil {
				t.Fatal(err)
			}
			profile, err := group.GetProfile()
			if err != nil || profile.Members["user-one"].PreferredName != "" {
				t.Fatalf("stale evidence changed name: %#v, %v", profile, err)
			}
		})
	}
}

func TestGroupReflectionRejectsUnsupportedMemberNames(t *testing.T) {
	base := t.TempDir()
	group, err := NewGroupStore(base, "group-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := group.ObserveMember("user-one", "Nickname", "", "member", "test"); err != nil {
		t.Fatal(err)
	}
	if err := group.Save(MemoryEntry{ID: "evidence-one", Owner: "user-one", Content: "一个事实", Source: SourceManual, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	provider := &memberNameReflectProvider{response: `{"topics":[],"outdated_ids":["evidence-one"],"member_names":[{"user_id":"user-one","preferred_name":"无证据","source_ids":["other-group-id"]},{"user_id":"user-one","preferred_name":"清洗\n截断","source_ids":["evidence-one"]},{"user_id":"user-one","preferred_name":"过时称呼","source_ids":["evidence-one"]}]}`}
	reflector := NewReflector(NewStore(base+"/private.json"), group.CatalogStore(), provider, "test-model", Config{})
	if err := reflector.ReflectGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	profile, err := group.GetProfile()
	if err != nil || profile.Members["user-one"].PreferredName != "" {
		t.Fatalf("unsupported name was applied: %#v, %v", profile, err)
	}
}
