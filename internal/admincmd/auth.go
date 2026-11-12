package admincmd

import (
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"os"
	"slices"
	"strings"
	"unicode"
)

const AdminQQIDsEnv = "ADMIN_QQ_IDS"

// IsPlatformAdmin applies the existing QQ-only administrator namespace. Native
// transports cannot inherit QQ privileges from an equal numeric user ID.
func IsPlatformAdmin(platform, callerUserID string, scopes ...*runtimescope.Scope) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "", "qq", "onebot", "aiocqhttp", "astrbot":
		return IsAdmin(callerUserID, scopes...)
	default:
		return false
	}
}

// IsAdmin checks whether the given caller user ID is configured as an administrator
// in the ADMIN_QQ_IDS environment variable for the given runtime scope.
// An empty list means nobody has admin permission.
func IsAdmin(callerUserID string, scopes ...*runtimescope.Scope) bool {
	scope := runtimescope.First(scopes)
	callerUserID = strings.TrimSpace(callerUserID)
	if callerUserID == "" {
		return false
	}
	rawIDs := ""
	if scope != nil {
		rawIDs = scope.Getenv(AdminQQIDsEnv)
	} else {
		rawIDs = os.Getenv(AdminQQIDsEnv)
	}
	configuredIDs := strings.FieldsFunc(rawIDs, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	})
	return slices.Contains(configuredIDs, callerUserID)
}
