package actionscat

import (
	"github.com/GuaiZai233/FrostAgent/internal/managementauth"
	"io"
	"net/http"
	"net/http/httptest"
)

func authenticatedActionRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	return r.WithContext(managementauth.WithLocalAdmin(r.Context()))
}
