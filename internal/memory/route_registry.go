package memory

import (
	"FrostAgent/internal/core"
	"strings"
)

// RememberRoute keeps the latest runtime route for an owner in memory. It is
// intentionally not persisted with memories; a restart may forget it.
func (s *Store) RememberRoute(owner string, route core.RouteContext) {
	if s == nil {
		return
	}
	owner = strings.TrimSpace(owner)
	route.Platform = strings.ToLower(strings.TrimSpace(route.Platform))
	route.GroupID = strings.TrimSpace(route.GroupID)
	if owner == "" || (route.Platform == "" && route.GroupID == "") {
		return
	}

	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	if s.routes == nil {
		s.routes = make(map[string]core.RouteContext)
	}
	s.routes[owner] = route
}

// RouteForOwner returns a remembered route, falling back to the established
// owner string formats for group memories created before this process started.
func (s *Store) RouteForOwner(owner string) core.RouteContext {
	owner = strings.TrimSpace(owner)
	if s != nil {
		s.routeMu.RLock()
		route, ok := s.routes[owner]
		s.routeMu.RUnlock()
		if ok {
			return route
		}
	}

	if groupID, ok := strings.CutPrefix(owner, "group:"); ok {
		return core.RouteContext{Platform: "onebot", GroupID: strings.TrimSpace(groupID)}
	}
	const marker = ":group:"
	if index := strings.Index(owner, marker); index > 0 {
		return core.RouteContext{
			Platform: strings.ToLower(strings.TrimSpace(owner[:index])),
			GroupID:  strings.TrimSpace(owner[index+len(marker):]),
		}
	}
	return core.RouteContext{}
}
