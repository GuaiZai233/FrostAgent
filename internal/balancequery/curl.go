package balancequery

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

type requestSpec struct {
	method  string
	url     *url.URL
	headers http.Header
	body    string
}

// splitCurl parses quoting only. It never invokes a shell or expands variables.
func splitCurl(raw string) ([]string, error) {
	if len(raw) > 65536 || strings.ContainsRune(raw, 0) {
		return nil, errors.New("curl 配置过长或含有非法字符")
	}
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	runes := []rune(raw)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '\\' && quote != '\'' {
			if i+1 >= len(runes) {
				return nil, errors.New("curl 转义不完整")
			}
			next := runes[i+1]
			if next == '\r' && i+2 < len(runes) && runes[i+2] == '\n' {
				i += 2
				continue
			}
			if next == '\n' {
				i++
				continue
			}
			if quote == '"' && next != '"' && next != '\\' && next != '$' && next != '`' {
				word.WriteRune(ch)
				started = true
				continue
			}
			i++
			word.WriteRune(next)
			started = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
			started = true
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			started = true
			continue
		}
		if unicode.IsSpace(ch) {
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		if strings.ContainsRune(";|`<>$", ch) {
			return nil, errors.New("curl 不支持 shell 命令、变量或重定向")
		}
		word.WriteRune(ch)
		started = true
	}
	if quote != 0 {
		return nil, errors.New("curl 引号未闭合")
	}
	if started {
		words = append(words, word.String())
	}
	if len(words) > 256 {
		return nil, errors.New("curl 参数过多")
	}
	return words, nil
}

func parseCurl(raw string) (requestSpec, error) {
	spec := requestSpec{headers: make(http.Header)}
	words, err := splitCurl(raw)
	if err != nil {
		return spec, err
	}
	if len(words) < 2 || (words[0] != "curl" && words[0] != "curl.exe") {
		return spec, errors.New("请填写以 curl 开头的查询请求")
	}
	var target string
	var data []string
	forceGet := false
	for i := 1; i < len(words); i++ {
		arg := words[i]
		option, value, attached := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") {
			option = arg
			attached = false
		}
		if len(arg) > 2 && (strings.HasPrefix(arg, "-H") || strings.HasPrefix(arg, "-X") || strings.HasPrefix(arg, "-d")) {
			option, value, attached = arg[:2], arg[2:], true
		}
		switch option {
		case "-s", "--silent", "-S", "--show-error":
			continue
		case "-G", "--get":
			forceGet = true
			continue
		case "-X", "--request", "-H", "--header", "-d", "--data", "--data-raw", "--data-binary", "--url":
			if !attached {
				if i+1 >= len(words) {
					return spec, errors.New("curl 参数缺少值")
				}
				i++
				value = words[i]
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return spec, errors.New("curl 仅支持 --url、-X、-H、-d、--data-raw、--data-binary、-G 和静默选项")
			}
			if target != "" {
				return spec, errors.New("curl 只能配置一个查询端点")
			}
			target = arg
			continue
		}
		switch option {
		case "-X", "--request":
			spec.method = strings.ToUpper(value)
		case "--url":
			if target != "" {
				return spec, errors.New("curl 只能配置一个查询端点")
			}
			target = value
		case "-H", "--header":
			name, headerValue, ok := strings.Cut(value, ":")
			name = strings.TrimSpace(name)
			if !ok || !validHeaderName(name) || strings.ContainsAny(headerValue, "\r\n\x00") {
				return spec, errors.New("curl 请求头格式无效")
			}
			switch strings.ToLower(name) {
			case "host", "connection", "proxy-authorization", "proxy-connection", "content-length", "transfer-encoding", "trailer", "upgrade", "te", "accept-encoding":
				return spec, errors.New("curl 不支持修改连接、代理或传输控制请求头")
			}
			spec.headers.Add(name, strings.TrimSpace(headerValue))
		default:
			if strings.HasPrefix(value, "@") {
				return spec, errors.New("curl 不支持读取文件")
			}
			data = append(data, value)
		}
	}
	if !strings.Contains(raw, KeyPlaceholder) {
		return spec, errors.New("请使用 {{API_KEY}} 占位，API key 在独立字段中填写")
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return spec, errors.New("查询端点必须是无内嵌账号和片段的 HTTP/HTTPS URL")
	}
	if strings.Contains(u.Host, KeyPlaceholder) || strings.Contains(u.Path, KeyPlaceholder) {
		return spec, errors.New("API key 占位仅支持请求头、查询参数和请求体")
	}
	spec.url = u
	if forceGet {
		if len(data) > 0 {
			if u.RawQuery != "" {
				u.RawQuery += "&"
			}
			u.RawQuery += strings.Join(data, "&")
		}
	} else {
		spec.body = strings.Join(data, "&")
	}
	if spec.method == "" {
		spec.method = http.MethodGet
		if len(data) > 0 && !forceGet {
			spec.method = http.MethodPost
		}
	}
	if spec.method != http.MethodGet && spec.method != http.MethodPost {
		return spec, errors.New("额度查询仅支持 GET 和 POST")
	}
	if len(data) > 0 && !forceGet && spec.headers.Get("Content-Type") == "" {
		spec.headers.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return spec, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, ch := range name {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", ch) {
			continue
		}
		return false
	}
	return true
}
