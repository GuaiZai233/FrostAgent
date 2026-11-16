package memory

import (
	"slices"
	"testing"
)

func TestNormalizeOwnerType(t *testing.T) {
	cases := []struct {
		in   OwnerType
		want OwnerType
	}{
		{OwnerUser, OwnerUser},
		{OwnerGroup, OwnerGroup},
		{"", OwnerUser},        // 零值兼容
		{"unknown", OwnerUser}, // 未知值兜底
	}
	for _, c := range cases {
		if got := NormalizeOwnerType(c.in); got != c.want {
			t.Errorf("NormalizeOwnerType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOwnerForPrivate(t *testing.T) {
	owner, kind := OwnerForPrivate("123456")
	if owner != "123456" || kind != OwnerUser {
		t.Errorf("got (%q,%q), want (\"123456\",user)", owner, kind)
	}

	owner, kind = OwnerForPrivate("")
	if owner != "" || kind != OwnerUser {
		t.Errorf("empty userID: got (%q,%q), want (\"\",user)", owner, kind)
	}
}

func TestOwnerForGroup(t *testing.T) {
	owner, kind := OwnerForGroup(789012)
	if owner != "group:789012" || kind != OwnerGroup {
		t.Errorf("got (%q,%q), want (\"group:789012\",group)", owner, kind)
	}

	// 非正群号兜底
	owner, kind = OwnerForGroup(0)
	if owner != "" || kind != OwnerGroup {
		t.Errorf("zero groupID: got (%q,%q), want (\"\",group)", owner, kind)
	}
	owner, kind = OwnerForGroup(-1)
	if owner != "" || kind != OwnerGroup {
		t.Errorf("neg groupID: got (%q,%q), want (\"\",group)", owner, kind)
	}
}

func TestMemoryEntryZeroValueOwnerType(t *testing.T) {
	// 直接构造不带 OwnerType 的 entry（模拟老 brain.json）
	e := MemoryEntry{ID: "mem_x", Owner: "alice"}
	if NormalizeOwnerType(e.OwnerType) != OwnerUser {
		t.Errorf("expected zero value normalized to OwnerUser, got %q", e.OwnerType)
	}
	// 模拟 store.Save 的默认填充
	if e.OwnerType == "" {
		e.OwnerType = OwnerUser
	}
	if e.OwnerType != OwnerUser {
		t.Errorf("expected OwnerUser after default fill, got %q", e.OwnerType)
	}
}

func TestOwnerForPlatform(t *testing.T) {
	// OneBot / QQ 平台默认
	o1, t1 := OwnerForPlatformPrivate("onebot", "test_user_1")
	if o1 != "test_user_1" || t1 != OwnerUser {
		t.Errorf("got (%q, %q), want (\"test_user_1\", user)", o1, t1)
	}

	o2, t2 := OwnerForPlatformGroup("onebot", "test_grp_1")
	if o2 != "group:test_grp_1" || t2 != OwnerGroup {
		t.Errorf("got (%q, %q), want (\"group:test_grp_1\", group)", o2, t2)
	}

	o2qq, t2qq := OwnerForPlatformGroup("qq", "test_grp_1")
	if o2qq != "group:test_grp_1" || t2qq != OwnerGroup {
		t.Errorf("qq group: got (%q, %q), want (\"group:test_grp_1\", group)", o2qq, t2qq)
	}

	o2qqPriv, t2qqPriv := OwnerForPlatformPrivate("qq", "test_user_1")
	if o2qqPriv != "test_user_1" || t2qqPriv != OwnerUser {
		t.Errorf("qq private: got (%q, %q), want (\"test_user_1\", user)", o2qqPriv, t2qqPriv)
	}

	// 其他外部多平台前缀
	o3, t3 := OwnerForPlatformPrivate("telegram", "user_abc")
	if o3 != "telegram:user:user_abc" || t3 != OwnerUser {
		t.Errorf("got (%q, %q), want (\"telegram:user:user_abc\", user)", o3, t3)
	}

	o4, t4 := OwnerForPlatformGroup("telegram", "grp_xyz")
	if o4 != "telegram:group:grp_xyz" || t4 != OwnerGroup {
		t.Errorf("got (%q, %q), want (\"telegram:group:grp_xyz\", group)", o4, t4)
	}
}

func TestCanonicalAndAliases(t *testing.T) {
	if !IsQQPlatform("qq") || !IsQQPlatform("onebot") || !IsQQPlatform("") {
		t.Errorf("expected QQ platform checks to be true")
	}
	if IsQQPlatform("telegram") {
		t.Errorf("expected non-QQ platform to be false")
	}

	if CanonicalPlatform("qq") != "qq" || CanonicalPlatform("onebot") != "qq" {
		t.Errorf("expected QQ platform normalized to qq")
	}
	if CanonicalPlatform("telegram") != "telegram" {
		t.Errorf("expected telegram to stay telegram")
	}

	// CanonicalOwner
	if CanonicalOwner("qq:user:test_user_a") != "test_user_a" {
		t.Errorf("got %q", CanonicalOwner("qq:user:test_user_a"))
	}
	if CanonicalOwner("qq:group:test_grp_a") != "group:test_grp_a" {
		t.Errorf("got %q", CanonicalOwner("qq:group:test_grp_a"))
	}
	if CanonicalOwner("onebot:user:test_user_a") != "test_user_a" {
		t.Errorf("got %q", CanonicalOwner("onebot:user:test_user_a"))
	}

	// OwnersMatch
	if !OwnersMatch("test_user_a", "qq:user:test_user_a") {
		t.Errorf("expected OwnersMatch to be true across qq and canonical")
	}
	if !OwnersMatch("group:test_grp_a", "qq:group:test_grp_a") {
		t.Errorf("expected OwnersMatch to be true for group across qq and canonical")
	}
	if !OwnersMatch("group:test_grp_a", "onebot:group:test_grp_a") {
		t.Errorf("expected OwnersMatch to be true for group across onebot and canonical")
	}

	// CanonicalSessionKey & SessionKeyAliases
	if CanonicalSessionKey("qq:group:test_grp_a") != "group:test_grp_a" {
		t.Errorf("got %q", CanonicalSessionKey("qq:group:test_grp_a"))
	}
	if CanonicalSessionKey("qq:private:test_user_a") != "private:test_user_a" {
		t.Errorf("got %q", CanonicalSessionKey("qq:private:test_user_a"))
	}

	aliases := SessionKeyAliases("group:test_grp_a")
	if !slices.Contains(aliases, "qq:group:test_grp_a") {
		t.Errorf("expected aliases to contain qq alias: %v", aliases)
	}

	if CanonicalSessionID("qq", "group", "test_grp_a") != "group:test_grp_a" {
		t.Errorf("got %q", CanonicalSessionID("qq", "group", "test_grp_a"))
	}
	if CanonicalSessionID("qq", "private", "test_user_a") != "private:test_user_a" {
		t.Errorf("got %q", CanonicalSessionID("qq", "private", "test_user_a"))
	}
}
