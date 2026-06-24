package frontend

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

type localeConfig struct {
	code    string   // 对应的目录名，如 "zh-Hans", "en-US"
	aliases []string // 浏览器 Accept-Language 的匹配别名（全小写）
}

var locales = []localeConfig{
	{code: "zh-Hans", aliases: []string{"zh-hans", "zh-cn", "zh-sg", "zh"}},
	{code: "en-US", aliases: []string{"en-us", "en-gb", "en"}},
}

const defaultLocale = "zh-Hans"

//go:embed all:dist
var distFS embed.FS

// Handler 返回一个 http.Handler 用于提供嵌入的前端静态资源。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("frontend: embedded dist directory not found: " + err.Error())
	}
	fileFS := http.FS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// 1. 根路径 "/"：根据浏览器的 Accept-Language 重定向到 /zh-Hans/ 或 /en-US/
		if path == "/" {
			locale := detectLocale(r)
			http.Redirect(w, r, "/"+locale+"/", http.StatusFound)
			return
		}

		// 2. SPA 路由回退：不带文件后缀的路径（如 /zh-Hans/settings），返回对应语言的 index.html
		if !hasExtension(path) {
			index := resolveLocaleIndex(path)
			serveFile(fileFS, w, r, index)
			return
		}

		// 3. 静态资源（有后缀，如 /zh-Hans/main.js）：URL 与实际目录一致，直接由 FileServer 处理
		http.FileServer(fileFS).ServeHTTP(w, r)
	})
}

// detectLocale 根据 Accept-Language 请求头匹配最佳语言目录
func detectLocale(r *http.Request) string {
	header := r.Header.Get("Accept-Language")
	if header == "" {
		return defaultLocale
	}
	for _, tag := range parseAcceptLanguage(header) {
		for _, loc := range locales {
			for _, alias := range loc.aliases {
				if tag == alias {
					return loc.code
				}
			}
		}
	}
	return defaultLocale
}

// parseAcceptLanguage 解析并提取化低小写的语言标签
func parseAcceptLanguage(header string) []string {
	var tags []string
	for _, seg := range strings.Split(header, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if idx := strings.IndexByte(seg, ';'); idx >= 0 {
			seg = strings.TrimSpace(seg[:idx])
		}
		tags = append(tags, strings.ToLower(seg))
	}
	return tags
}

// resolveLocaleIndex 将当前的 SPA 路由映射到正确的 index.html
func resolveLocaleIndex(path string) string {
	for _, loc := range locales {
		if path == "/"+loc.code || strings.HasPrefix(path, "/"+loc.code+"/") {
			return "/" + loc.code + "/index.html"
		}
	}
	// 如果碰到了无法识别的路径前缀，默认回退到默认语言的 index.html
	return "/" + defaultLocale + "/index.html"
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
