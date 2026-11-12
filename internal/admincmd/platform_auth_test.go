package admincmd

import "testing"

func TestQQAdministratorGrantDoesNotCrossPlatforms(t *testing.T) {
	scope := newTestScope(t, map[string]string{AdminQQIDsEnv: "synthetic-admin"})
	defer scope.Cancel()
	for _, platform := range []string{"", "qq", "onebot", "aiocqhttp", "astrbot"} {
		if !IsPlatformAdmin(platform, "synthetic-admin", scope) {
			t.Fatalf("QQ grant lost for %s", platform)
		}
	}
	for _, platform := range []string{"telegram", "discord", "mock"} {
		if IsPlatformAdmin(platform, "synthetic-admin", scope) {
			t.Fatalf("QQ grant escaped into %s", platform)
		}
	}
}
