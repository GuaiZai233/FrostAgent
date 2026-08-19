package frontend

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler 返回一个 http.Handler 用于提供嵌入的前端单页应用（SPA）静态资源。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("frontend: embedded dist directory not found: " + err.Error())
	}
	fileFS := http.FS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// 1. 根路径 "/" 或空路径：返回 index.html
		if path == "/" || path == "" {
			serveFile(fileFS, w, r, "index.html")
			return
		}

		// 2. SPA 路由回退：不带文件后缀的路径（如 /settings, /logs, /memory 等），返回 index.html
		if !hasExtension(path) {
			serveFile(fileFS, w, r, "index.html")
			return
		}

		// 3. 静态资源（有后缀，如 /main.js, /styles.css, /favicon.ico）：直接由 FileServer 处理
		http.FileServer(fileFS).ServeHTTP(w, r)
	})
}

// serveFile 读取并向浏览器输出单个文件
func serveFile(fsys http.FileSystem, w http.ResponseWriter, r *http.Request, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
}

// hasExtension 判断路径最后一段是否包含 "." （借此区分是静态资源还是 SPA 网页路由）
func hasExtension(path string) bool {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	return strings.Contains(last, ".")
}
