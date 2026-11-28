package instance

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	settingssvc "github.com/GuaiZai233/FrostAgent/internal/service/settings"
)

// Apply or roll back settings before Connect serializes the result. Rewriting
// an encoded HTTP body would corrupt compressed responses and gRPC framing.
func (m *Manager) globalSettingsInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := request.Spec().Procedure
			method := procedure[strings.LastIndex(procedure, "/")+1:]
			if !strings.HasPrefix(method, "Update") && !strings.HasPrefix(method, "Delete") {
				return next(ctx, request)
			}
			m.settingsWriteMu.Lock()
			defer m.settingsWriteMu.Unlock()
			before := m.global.Snapshot()
			response, err := next(ctx, request)
			if !equalSettings(before, m.global.Snapshot()) {
				if applyErr := m.applyGlobalSettings(); applyErr != nil {
					rollback := m.restoreGlobalSnapshot(before)
					if rollback == nil {
						rollback = m.applyGlobalSettings()
					}
					code := connect.CodeAborted
					if rollback != nil || response != nil && response.Header().Get(settingssvc.WriteStateHeader) == "unknown" {
						code = connect.CodeInternal
					}
					return nil, connect.NewError(code, errors.Join(applyErr, rollback))
				}
			}
			return response, err
		}
	})
}

func (m *Manager) globalSettingsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
