package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/memory"
)

// MemoryReadScopes binds memory access to the current conversation and its
// adapter-authenticated sender. Model arguments never select an owner or group.
func (r RunContext) MemoryReadScopes() []memory.SearchScope {
	groupID := r.RouteScope.GroupID
	if groupID == "" && strings.HasPrefix(r.Owner, "group:") {
		groupID = strings.TrimPrefix(r.Owner, "group:")
	}
	if groupID == "" {
		if r.OwnerType == memory.OwnerGroup || strings.TrimSpace(r.Owner) == "" || r.Owner == "group:" {
			return nil
		}
		return []memory.SearchScope{{Type: memory.ScopePrivate, Owner: memory.CanonicalOwner(r.Owner)}}
	}
	scopes := []memory.SearchScope{{Type: memory.ScopeGroup, Platform: r.RouteScope.Platform, GroupID: groupID}}
	// Timer turns have no sending user. Quoted authors and mentioned users do
	// not grant access; only the identity supplied by the adapter can do so.
	if r.Awareness || strings.TrimSpace(r.ActorUserID) == "" || strings.TrimSpace(r.ActorPlatform) == "" {
		return scopes
	}
	if memory.CanonicalPlatform(r.ActorPlatform) != memory.CanonicalPlatform(r.RouteScope.Platform) {
		return scopes
	}
	owner, _ := memory.OwnerForPlatformPrivate(r.ActorPlatform, strings.TrimSpace(r.ActorUserID))
	return append(scopes, memory.SearchScope{Type: memory.ScopePrivate, Owner: owner})
}

// SearchMemoryScope searches one already-authorized scope. Scope filtering
// happens before callers apply a recall limit, including without Embedding.
func (e *Engine) SearchMemoryScope(ctx context.Context, scope memory.SearchScope, query string, tags []string) ([]memory.MemoryEntry, error) {
	if e.MemoryGateway == nil {
		return nil, fmt.Errorf("记忆功能未启用")
	}
	var entries []memory.MemoryEntry
	var err error
	if e.MemoryEmbeddings != nil {
		if query == "" {
			query = strings.Join(tags, ", ")
		}
		entries, err = e.MemoryEmbeddings.Search(ctx, scope, query, 20)
	} else if scope.Type == memory.ScopeGroup {
		if e.GroupManager == nil {
			return nil, fmt.Errorf("群记忆功能未启用")
		}
		group, openErr := e.GroupManager.GetGroupStoreForPlatform(scope.Platform, scope.GroupID)
		if openErr != nil {
			return nil, openErr
		}
		if query != "" || len(tags) == 0 {
			entries, err = group.Search(query, 0)
		} else {
			entries, err = group.SearchByTags(tags, 0)
		}
	} else {
		if e.MemoryReader == nil || scope.Owner == "" {
			return nil, fmt.Errorf("无法读取用户记忆")
		}
		if query != "" || len(tags) == 0 {
			entries, err = e.MemoryReader.Recall(ctx, query)
		} else {
			entries, err = e.MemoryReader.SearchByTags(ctx, tags)
		}
	}
	if err != nil {
		return nil, err
	}
	if scope.Type == memory.ScopeGroup {
		return e.MemoryGateway.FilterGroup(entries), nil
	}
	entries = e.MemoryGateway.FilterPrivate(entries, scope.Owner)
	for i := range entries {
		entries[i].ScopeType = memory.ScopePrivate
		entries[i].GroupID = ""
	}
	return entries, nil
}

func (e *Engine) ListMemoryScope(ctx context.Context, scope memory.SearchScope) ([]memory.MemoryEntry, error) {
	if scope.Type != memory.ScopeGroup {
		if e.MemoryReader == nil || e.MemoryGateway == nil || scope.Owner == "" {
			return nil, fmt.Errorf("无法读取用户记忆")
		}
		entries, err := e.MemoryReader.Recall(ctx, "")
		if err != nil {
			return nil, err
		}
		entries = e.MemoryGateway.FilterPrivate(entries, scope.Owner)
		for i := range entries {
			entries[i].ScopeType = memory.ScopePrivate
			entries[i].GroupID = ""
		}
		return entries, nil
	}
	if e.GroupManager == nil || e.MemoryGateway == nil {
		return nil, fmt.Errorf("群记忆功能未启用")
	}
	group, err := e.GroupManager.GetGroupStoreForPlatform(scope.Platform, scope.GroupID)
	if err != nil {
		return nil, err
	}
	entries, err := group.ListAll()
	if err != nil {
		return nil, err
	}
	return e.MemoryGateway.FilterGroup(entries), nil
}

func (e *Engine) RecordMemoryScopeRecall(scope memory.SearchScope, entries []memory.MemoryEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if scope.Type != memory.ScopeGroup {
		return e.MemoryReader.RecordRecall(entries)
	}
	group, err := e.GroupManager.GetGroupStoreForPlatform(scope.Platform, scope.GroupID)
	if err != nil {
		return err
	}
	return group.RecordRecall(entries)
}
