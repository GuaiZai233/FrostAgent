package memory

import (
	"slices"
	"testing"
)

func TestRetiredPlatformsDoNotAliasQQ(t *testing.T) {
	for _, platform := range []string{"aiocqhttp", "astrbot"} {
		if IsQQPlatform(platform) || CanonicalPlatform(platform) == "qq" {
			t.Fatalf("retired platform aliases QQ: %s", platform)
		}
		for _, kind := range []string{"user", "group"} {
			owner := platform + ":" + kind + ":synthetic-id"
			qqOwner := "synthetic-id"
			if kind == "group" {
				qqOwner = "group:synthetic-id"
			}
			if CanonicalOwner(owner) != owner || OwnersMatch(owner, qqOwner) || slices.Contains(OwnerAliases(qqOwner), owner) {
				t.Fatalf("retired owner aliases QQ: %s", owner)
			}
		}
		for _, kind := range []string{"private", "group"} {
			id := platform + ":" + kind + ":synthetic-id"
			if CanonicalSessionKey(id) != id || slices.Contains(SessionKeyAliases(kind+":synthetic-id"), id) {
				t.Fatalf("retired session aliases QQ: %s", id)
			}
		}
		group := platform + ":group:synthetic-id"
		if CanonicalGroupID(group) != group || SafeGroupKey(group) == SafeGroupKey("synthetic-id") {
			t.Fatalf("retired group aliases QQ: %s", group)
		}
	}
}
