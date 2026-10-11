//go:build ignore

// This fixture is compiled inside an ActionsCat checkout by the optional ingress integration test.
package main

import (
	"actionscat/internal/api"
	"actionscat/internal/domain"
	"actionscat/internal/matcher"
	"actionscat/internal/runner"
	"actionscat/internal/sandbox"
	"actionscat/internal/store"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	data, ready := os.Args[1], os.Args[2]
	db, err := store.OpenDB(filepath.Join(data, "fixture.db"))
	must(err)
	defer db.Close()
	st, fs := store.NewSQLiteStore(db), store.NewFileStore(data)
	ctx, now := context.Background(), time.Now().UTC()
	action := &domain.Action{ID: "act_fixture", Name: "fixture", Enabled: true, MaxConcurrency: 2, CreatedAt: now, UpdatedAt: now}
	must(st.CreateAction(ctx, action))
	version := &domain.ActionVersion{ID: "ver_fixture", ActionID: action.ID, VersionNumber: 1, SourceDigest: "fixture", SourcePath: "fixture", RuntimeSpec: domain.RuntimeSpec{Entrypoint: "entrypoint"}, CreatedAt: now}
	must(st.CreateVersion(ctx, version))
	digest, path, size, err := fs.SaveArtifactBundle(action.ID, version.ID, "build_fixture", map[string][]byte{"entrypoint": []byte("fixture")})
	must(err)
	build := &domain.ArtifactBuild{ID: "build_fixture", ActionID: action.ID, VersionID: version.ID, BuildNumber: 1, Status: domain.BuildStatusSucceeded, ArtifactDigest: digest, ArtifactPath: path, ArtifactSize: size, CreatedAt: now}
	must(st.CreateBuild(ctx, build))
	must(st.SetActiveBuild(ctx, action.ID, version.ID, build.ID))
	must(st.CreateMatcher(ctx, &domain.Matcher{ID: "matcher_fixture", ActionID: action.ID, Name: "bilibili", MatchType: domain.MatchTypeContains, Pattern: "bilibili.com", TargetField: "text", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	run := runner.NewRunner(st, fs, store.NewStateStore(data), sandbox.NewFakeBackend(), runner.Config{})
	handler := api.NewDispatchHandler(matcher.NewEngine(st, run))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/dispatch", api.DispatchAuthMiddleware("synthetic-dispatch-token", "", st), handler.HandleDispatch)
	router.GET("/test/runs", func(c *gin.Context) {
		runs, err := st.ListRuns(c.Request.Context(), action.ID, 10, 0)
		must(err)
		c.JSON(http.StatusOK, runs)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	must(os.WriteFile(ready, []byte("http://"+listener.Addr().String()), 0600))
	must(http.Serve(listener, router))
}
