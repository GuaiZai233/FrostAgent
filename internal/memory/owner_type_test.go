package memory

import "testing"

func TestNormalizeOwnerType(t *testing.T) {
	cases := []struct {
		in   OwnerType
		want OwnerType
	}{
		{OwnerUser, OwnerUser},
		{OwnerGroup, OwnerGroup},
		{"", OwnerUser},                       // 零值兼容
		{"unknown", OwnerUser},                // 未知值兜底
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
