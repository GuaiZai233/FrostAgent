package settings

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
)

func TestChatHistorySettingValidation(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		if err := validateUpdate(chathistory.PrivateEnabledKey, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"", "fixture-group", "qq:group:fixture-group,telegram:group:fixture-group"} {
		if err := validateUpdate(chathistory.GroupWhitelistKey, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{chathistory.PrivateEnabledKey, "yes"}, {chathistory.GroupWhitelistKey, "qq:private:fixture-user"}, {chathistory.GroupWhitelistKey, "bad value"}} {
		if err := validateUpdate(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted setting %#v", pair)
		}
	}
	for _, key := range []string{chathistory.PrivateEnabledKey, chathistory.GroupWhitelistKey} {
		if _, ok := knownEnvVars[key]; !ok {
			t.Fatalf("setting %s not discoverable", key)
		}
		if resetHints[key] == "" {
			t.Fatalf("missing default hint for %s", key)
		}
	}
}
