// Package skills implements portable SKILL.md bundles without extracting them on the host.
package skills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	MaxZIPBytes         = 16 << 20
	MaxExpandedBytes    = 32 << 20
	MaxPackageBytes     = 8 << 20
	MaxFiles            = 1024
	MaxSkills           = 100
	MaxInstructionBytes = 32 << 10
	MaxResourceBytes    = 48 << 10
)

var skillName = regexp.MustCompile("^[a-z0-9]+(-[a-z0-9]+)*$")

type Info struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Enabled       bool     `json:"enabled"`
	ManualOnly    bool     `json:"manual_only"`
	UserInvocable bool     `json:"user_invocable"`
	Compatibility string   `json:"compatibility,omitempty"`
	Warnings      []string `json:"warnings"`
	Files         []string `json:"files"`
	Revision      string   `json:"revision"`
}

type Package struct {
	Info        Info              `json:"info"`
	Body        string            `json:"body"`
	Files       map[string][]byte `json:"files"`
	Executables []string          `json:"executables,omitempty"`
}

// ValidPath applies the same portable path rules during import, restore and resource lookup.
func ValidPath(name string) bool {
	if name == "" || len(name) > 512 || !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." || strings.TrimSpace(part) != part || strings.HasSuffix(part, ".") {
			return false
		}
	}
	return true
}

func ParseZIP(data []byte) ([]Package, error) {
	if len(data) == 0 || len(data) > MaxZIPBytes {
		return nil, fmt.Errorf("ZIP 最大为 16 MiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("无效 ZIP: %w", err)
	}
	if len(reader.File) > MaxFiles {
		return nil, fmt.Errorf("ZIP 文件数量超过 %d", MaxFiles)
	}
	files := map[string][]byte{}
	executables := map[string]bool{}
	seen := map[string]bool{}
	total := 0
	for _, member := range reader.File {
		name := strings.TrimSuffix(member.Name, "/")
		if !ValidPath(name) {
			return nil, fmt.Errorf("不安全的 ZIP 路径 %q", member.Name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, fmt.Errorf("ZIP 路径重复 %q", name)
		}
		seen[key] = true
		mode := member.Mode()
		if kind := mode.Type(); kind != 0 && kind != fs.ModeDir {
			return nil, fmt.Errorf("ZIP 不允许链接或特殊文件 %q", name)
		}
		if member.FileInfo().IsDir() {
			continue
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf("ZIP 不允许链接或特殊文件 %q", name)
		}
		remaining := MaxExpandedBytes - total
		if member.UncompressedSize64 > uint64(remaining) {
			return nil, fmt.Errorf("ZIP 解压后最大为 32 MiB")
		}
		stream, err := member.Open()
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(io.LimitReader(stream, int64(remaining)+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(content) > remaining {
			return nil, fmt.Errorf("ZIP 解压后最大为 32 MiB")
		}
		total += len(content)
		files[name] = content
		executables[name] = mode.Perm()&0111 != 0
	}
	var roots []string
	for name := range files {
		if path.Base(name) == "SKILL.md" {
			roots = append(roots, strings.TrimSuffix(name, "SKILL.md"))
		}
	}
	sort.Strings(roots)
	if len(roots) == 0 {
		return nil, fmt.Errorf("ZIP 中未找到 SKILL.md")
	}
	if len(roots) > MaxSkills {
		return nil, fmt.Errorf("一次最多导入 %d 个技能", MaxSkills)
	}
	result := make([]Package, 0, len(roots))
	names := map[string]bool{}
	for i, root := range roots {
		for _, other := range roots[i+1:] {
			if strings.HasPrefix(other, root) {
				return nil, fmt.Errorf("技能目录嵌套，无法确定资源归属: %q", other)
			}
		}
		bundled := map[string][]byte{}
		var executableNames []string
		for name, content := range files {
			if strings.HasPrefix(name, root) {
				relative := strings.TrimPrefix(name, root)
				bundled[relative] = content
				if executables[name] {
					executableNames = append(executableNames, relative)
				}
			}
		}
		fallback := path.Base(strings.TrimSuffix(root, "/"))
		if root == "" {
			fallback = ""
		}
		pkg, err := ParsePackage(bundled, fallback, executableNames...)
		if err != nil {
			return nil, fmt.Errorf("%sSKILL.md: %w", root, err)
		}
		if names[pkg.Info.Name] {
			return nil, fmt.Errorf("技能名称重复: %s", pkg.Info.Name)
		}
		names[pkg.Info.Name] = true
		result = append(result, pkg)
	}
	return result, nil
}

// ParsePackage derives metadata from the original files; backup metadata is never trusted.
func ParsePackage(files map[string][]byte, fallback string, executables ...string) (Package, error) {
	pkg := Package{Files: files, Info: Info{UserInvocable: true, Warnings: []string{}, Files: []string{}}}
	if len(files) == 0 || len(files) > MaxFiles {
		return pkg, fmt.Errorf("技能文件数量无效")
	}
	total := 0
	seen := map[string]bool{}
	for name, data := range files {
		if !ValidPath(name) || seen[strings.ToLower(name)] {
			return pkg, fmt.Errorf("资源路径无效或重复: %q", name)
		}
		seen[strings.ToLower(name)] = true
		total += len(data)
		pkg.Info.Files = append(pkg.Info.Files, name)
	}
	if total > MaxPackageBytes {
		return pkg, fmt.Errorf("单个技能最大为 8 MiB")
	}
	sort.Strings(pkg.Info.Files)
	for _, name := range pkg.Info.Files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[strings.ToLower(parent)] {
				return pkg, fmt.Errorf("资源文件与目录冲突: %q", parent)
			}
		}
	}
	executableSeen := map[string]bool{}
	for _, name := range executables {
		if _, exists := files[name]; !ValidPath(name) || !exists || executableSeen[name] {
			return pkg, fmt.Errorf("可执行资源路径无效或重复: %q", name)
		}
		executableSeen[name] = true
		pkg.Executables = append(pkg.Executables, name)
	}
	sort.Strings(pkg.Executables)
	original, ok := files["SKILL.md"]
	if !ok || len(original) > MaxInstructionBytes || !utf8.Valid(original) {
		return pkg, fmt.Errorf("SKILL.md 必须为 UTF-8 文本且不超过 32 KiB")
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(original), "\r\n", "\n"), "\ufeff")
	metadata := map[string]any{}
	body := text
	if strings.HasPrefix(text, "---\n") {
		end := strings.Index(text[4:], "\n---\n")
		// A frontmatter-only file is valid YAML but has no usable instructions.
		if end < 0 {
			return pkg, fmt.Errorf("YAML frontmatter 缺少结束标记")
		}
		if err := yaml.Unmarshal([]byte(text[4:4+end]), &metadata); err != nil {
			return pkg, fmt.Errorf("YAML frontmatter 无效: %w", err)
		}
		body = text[4+end+5:]
	}
	stringField := func(key string) (string, error) {
		value, exists := metadata[key]
		if !exists {
			return "", nil
		}
		s, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%s 必须是字符串", key)
		}
		return strings.TrimSpace(s), nil
	}
	var err error
	if pkg.Info.Name, err = stringField("name"); err != nil {
		return pkg, err
	}
	if pkg.Info.Name == "" {
		pkg.Info.Name = fallback
	}
	if !skillName.MatchString(pkg.Info.Name) || len(pkg.Info.Name) > 64 {
		return pkg, fmt.Errorf("name 必须为最多 64 字符的小写字母、数字和连字符")
	}
	if pkg.Info.Description, err = stringField("description"); err != nil {
		return pkg, err
	}
	if pkg.Info.Description == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" {
				pkg.Info.Description = strings.TrimSpace(line)
				break
			}
		}
	}
	if utf8.RuneCountInString(pkg.Info.Description) > 1024 || pkg.Info.Description == "" {
		return pkg, fmt.Errorf("description 必须为 1–1024 字符")
	}
	if pkg.Info.Compatibility, err = stringField("compatibility"); err != nil {
		return pkg, err
	}
	if utf8.RuneCountInString(pkg.Info.Compatibility) > 500 {
		return pkg, fmt.Errorf("compatibility 最多 500 字符")
	}
	for _, item := range []struct {
		key  string
		dest *bool
	}{{"disable-model-invocation", &pkg.Info.ManualOnly}, {"user-invocable", &pkg.Info.UserInvocable}} {
		if value, exists := metadata[item.key]; exists {
			b, ok := value.(bool)
			if !ok {
				return pkg, fmt.Errorf("%s 必须是布尔值", item.key)
			}
			*item.dest = b
		}
	}
	for _, key := range []string{"allowed-tools", "context", "agent", "model", "hooks", "mcp_servers", "paths", "arguments"} {
		if _, exists := metadata[key]; exists {
			pkg.Info.Warnings = append(pkg.Info.Warnings, key+" 为 harness 专属配置；保留原文件，FrostAgent 不执行该配置。")
		}
	}
	if strings.Contains(body, "!`") {
		pkg.Info.Warnings = append(pkg.Info.Warnings, "动态 shell 插值不会执行；需要时通过 run_skill 在沙箱执行命令。")
	}
	pkg.Body = strings.TrimSpace(body)
	if pkg.Body == "" {
		return pkg, fmt.Errorf("技能正文不能为空")
	}
	encoded, err := json.Marshal(struct {
		Files       map[string][]byte `json:"files"`
		Executables []string          `json:"executables,omitempty"`
	}{pkg.Files, pkg.Executables})
	if err != nil {
		return pkg, err
	}
	digest := sha256.Sum256(encoded)
	pkg.Info.Revision = hex.EncodeToString(digest[:])
	return pkg, nil
}
