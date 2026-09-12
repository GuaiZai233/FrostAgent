package security

import (
	"context"
	"regexp"
	"strings"
)

// CalibratedClassifier is a deterministic, calibrated classifier capable of
// evaluating content risk, actor intent, confidence, and platform policy compliance
// across multiple languages without requiring external network dependencies.
type CalibratedClassifier struct{}

func NewCalibratedClassifier() *CalibratedClassifier {
	return &CalibratedClassifier{}
}

func (c *CalibratedClassifier) Classify(ctx context.Context, input ClassificationInput) (ClassificationResult, error) {
	text := input.Normalized
	if text == "" {
		text = input.Content
	}
	res := c.classifyText(text, input.Origin)
	if res.IsRisky() {
		return res, nil
	}
	if input.Content != "" && input.Content != text {
		rawRes := c.classifyText(input.Content, input.Origin)
		if rawRes.IsRisky() {
			return rawRes, nil
		}
	}
	return res, nil
}

func (c *CalibratedClassifier) classifyText(text string, origin WatchdogSource) ClassificationResult {
	textLower := strings.ToLower(text)

	// Check if this is a benign educational, explanatory, or analytical query
	isEducational := checkEducationalContext(textLower)

	// 1. Prompt Injection & Jailbreak (Multilingual)
	if match, detail := matchPromptInjection(textLower); match {
		if isEducational {
			return ClassificationResult{
				Category:   RiskCategoryPromptInjection,
				RiskLevel:  RiskLevelLow,
				Intent:     IntentBenign,
				Confidence: 0.40,
				Origin:     origin,
				Reason:     "educational or analytical query discussing prompt injection concepts",
				Details:    []string{detail},
			}
		}
		return ClassificationResult{
			Category:   RiskCategoryPromptInjection,
			RiskLevel:  RiskLevelHigh,
			Intent:     IntentMalicious,
			Confidence: 0.95,
			Origin:     origin,
			Reason:     "prompt injection or jailbreak instruction detected",
			Details:    []string{detail},
		}
	}

	// 2. Malicious Execution (Destructive system commands, reverse shells)
	if match, detail := matchMaliciousExecution(textLower); match {
		if isEducational {
			return ClassificationResult{
				Category:   RiskCategoryMaliciousExecution,
				RiskLevel:  RiskLevelLow,
				Intent:     IntentBenign,
				Confidence: 0.35,
				Origin:     origin,
				Reason:     "educational inquiry about destructive command semantics",
				Details:    []string{detail},
			}
		}
		return ClassificationResult{
			Category:   RiskCategoryMaliciousExecution,
			RiskLevel:  RiskLevelCritical,
			Intent:     IntentMalicious,
			Confidence: 0.98,
			Origin:     origin,
			Reason:     "destructive system command or shell execution payload detected",
			Details:    []string{detail},
		}
	}

	// 3. Data Exfiltration
	if match, detail := matchExfiltration(textLower); match {
		if isEducational {
			return ClassificationResult{
				Category:   RiskCategoryExfiltration,
				RiskLevel:  RiskLevelLow,
				Intent:     IntentAmbiguous,
				Confidence: 0.45,
				Origin:     origin,
				Reason:     "query referencing security credentials or exfiltration concepts",
				Details:    []string{detail},
			}
		}
		return ClassificationResult{
			Category:   RiskCategoryExfiltration,
			RiskLevel:  RiskLevelHigh,
			Intent:     IntentMalicious,
			Confidence: 0.92,
			Origin:     origin,
			Reason:     "credential exfiltration or key harvesting attempt",
			Details:    []string{detail},
		}
	}

	// 4. Platform Policy & Tencent-facing compliance
	if match, cat, detail := matchPlatformPolicy(textLower); match {
		return ClassificationResult{
			Category:   cat,
			RiskLevel:  RiskLevelHigh,
			Intent:     IntentMalicious,
			Confidence: 0.90,
			Origin:     origin,
			Reason:     "platform policy violation (" + string(cat) + ")",
			Details:    []string{detail},
		}
	}

	// 5. Ambiguous security phrases that are not explicitly malicious
	if match, detail := matchAmbiguousSecurity(textLower); match {
		return ClassificationResult{
			Category:   RiskCategoryPromptInjection,
			RiskLevel:  RiskLevelMedium,
			Intent:     IntentAmbiguous,
			Confidence: 0.50,
			Origin:     origin,
			Reason:     "ambiguous security-related terminology without clear malicious intent",
			Details:    []string{detail},
		}
	}

	return ClassificationResult{
		Category:   RiskCategoryNone,
		RiskLevel:  RiskLevelNone,
		Intent:     IntentBenign,
		Confidence: 0.99,
		Origin:     origin,
		Reason:     "content deemed benign",
	}
}

// Educational context patterns
var educationalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(what\s+(does|is|are)|can\s+you\s+(explain|describe)|why\s+(is|are|do)|how\s+(does|do|to\s+prevent)|tell\s+me\s+about|meaning\s+of)`),
	regexp.MustCompile(`(?i)(解释|说明|为什么|是什么|如何(防范|防御|避免)|科普|概念|原理)`),
}

func checkEducationalContext(s string) bool {
	for _, p := range educationalPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

// Multilingual Prompt Injection & Jailbreak patterns
var promptInjectionPatterns = []struct {
	pattern *regexp.Regexp
	desc    string
}{
	// English
	{regexp.MustCompile(`(?i)ignore\s+((all|any|the)\s+)?(previous|prior)`), "en_ignore_previous"},
	{regexp.MustCompile(`(?i)ignore\s+((all|any|the|previous|prior)\s+)*(instructions|prompts|rules|guidelines)`), "en_ignore_instructions"},
	{regexp.MustCompile(`(?i)disregard\s+((all|any|the|previous|prior)\s+)*(instructions|prompts|rules|guidelines)`), "en_disregard_instructions"},
	{regexp.MustCompile(`(?i)(disable|bypass)\s+(the\s+)?(safety|watchdog|policy|lock|guardrails)`), "en_bypass_safety"},
	{regexp.MustCompile(`(?i)(system\s+override|jailbreak|dan\s+mode|developer\s+mode\s+enabled)`), "en_jailbreak"},
	// Chinese
	{regexp.MustCompile(`(?i)(忽略|无视|丢弃)(所有|全部)?(之前|先前|既有)?(的)?(指令|要求|提示词|限制|规则)`), "zh_ignore_instructions"},
	{regexp.MustCompile(`(?i)(绕过|关闭|禁用)(安全|审查|看门狗|watchdog|策略|封禁|过滤)`), "zh_bypass_safety"},
	{regexp.MustCompile(`(?i)(进入越狱模式|开启开发者模式|解除所有道德限制)`), "zh_jailbreak"},
	// Italian
	{regexp.MustCompile(`(?i)ignora\s+(tutte\s+le|ogni)\s+istruzioni\s+precedent`), "it_ignore_instructions"},
	{regexp.MustCompile(`(?i)(bypassa|disabilita)\s+(la\s+)?(sicurezza|watchdog|policy)`), "it_bypass_safety"},
	// Russian
	{regexp.MustCompile(`(?i)(игнорируй|проигнорируй)(те)?\s+(все|любые)\s+(предыдущие|ранее\s+данные)\s+(инструкции|указания)`), "ru_ignore_instructions"},
	{regexp.MustCompile(`(?i)(обойти|отключи(ть)?)\s+(защиту|безопасность|политику|вотчдог)`), "ru_bypass_safety"},
	// Japanese
	{regexp.MustCompile(`(?i)(以前の|これまでの|すべての)(指示|プロンプト|ルール)を(無視|破弃|破棄)`), "ja_ignore_instructions"},
	{regexp.MustCompile(`(?i)(セキュリティ|安全制限|watchdog|看门狗)を(無効化|バイパス|解除)`), "ja_bypass_safety"},
	// Spanish
	{regexp.MustCompile(`(?i)ignora\s+(todas\s+las|cualquier)\s+instrucciones\s+anteriores`), "es_ignore_instructions"},
	{regexp.MustCompile(`(?i)(omite|salta|desactiva)\s+(la\s+)?(seguridad|politica|watchdog)`), "es_bypass_safety"},
	// French
	{regexp.MustCompile(`(?i)ignorez?\s+(toutes\s+les|les)\s+instructions\s+pr[eé]c[eé]dentes`), "fr_ignore_instructions"},
	// German
	{regexp.MustCompile(`(?i)ignoriere?\s+(alle|die)\s+vorherigen\s+anweisungen`), "de_ignore_instructions"},
}

func matchPromptInjection(s string) (bool, string) {
	for _, entry := range promptInjectionPatterns {
		if entry.pattern.MatchString(s) {
			return true, entry.desc
		}
	}
	return false, ""
}

// Destructive execution & shell payloads
var maliciousExecutionPatterns = []struct {
	pattern *regexp.Regexp
	desc    string
}{
	{regexp.MustCompile(`(?i)\b(rm\s+-rf\s+[/~*]|del\s+/[fsq]\s+[a-z]:\\|format\s+[a-z]:)`), "destructive_deletion"},
	{regexp.MustCompile(`(?i)\b(curl|wget)\s+[^\n|]{0,512}\|\s*(sh|bash|powershell|zsh|dash)\b`), "pipe_to_shell"},
	{regexp.MustCompile(`(?i)\b(nc|ncat|netcat)\s+-[el]\s+/bin/(ba)?sh\b`), "reverse_shell_nc"},
	{regexp.MustCompile(`(?i)/dev/tcp/[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/[0-9]+`), "reverse_shell_dev_tcp"},
	{regexp.MustCompile(`(?i):(){ :\|:& };:`), "fork_bomb"},
}

func matchMaliciousExecution(s string) (bool, string) {
	for _, entry := range maliciousExecutionPatterns {
		if entry.pattern.MatchString(s) {
			return true, entry.desc
		}
	}
	return false, ""
}

// Exfiltration patterns
var exfiltrationPatterns = []struct {
	pattern *regexp.Regexp
	desc    string
}{
	{regexp.MustCompile(`(?i)(exfiltrat(e|ion)|steal|harvest)\s+(all\s+)?(api|access|session|private)\s*keys?`), "exfiltrate_keys"},
	{regexp.MustCompile(`(?i)(dump|leak|print)\s+(the\s+)?(system\s+prompt|environment\s+variables|\.env\s+file)`), "leak_system_prompt"},
	{regexp.MustCompile(`(?i)(窃取|偷取|窃听|外发|外传|盗取)(全部|所有)?(密钥|token|api_?key|会话|系统提示词)`), "zh_exfiltrate_keys"},
}

func matchExfiltration(s string) (bool, string) {
	for _, entry := range exfiltrationPatterns {
		if entry.pattern.MatchString(s) {
			return true, entry.desc
		}
	}
	return false, ""
}

// Platform policy & Tencent compliance patterns
var platformPolicyPatterns = []struct {
	pattern  *regexp.Regexp
	category RiskCategory
	desc     string
}{
	{regexp.MustCompile(`(?i)\b(child\s*porn(?:ography)?|pedophilia|paedophilia|csam)\b`), RiskCategoryVulgarity, "child_exploitation"},
	{regexp.MustCompile(`(?i)(制造|合成)(炸药|毒品|爆炸物|炭疽|沙林)`), RiskCategoryViolence, "weapons_contraband"},
	{regexp.MustCompile(`(?i)(代开|办理|出售)(假发票|假证件|高仿枪支)`), RiskCategoryFraud, "illicit_services"},
	{regexp.MustCompile(`(?i)(境外特务|煽动颠覆|分裂国家)`), RiskCategoryPolitical, "political_extremism"},
}

func matchPlatformPolicy(s string) (bool, RiskCategory, string) {
	for _, entry := range platformPolicyPatterns {
		if entry.pattern.MatchString(s) {
			return true, entry.category, entry.desc
		}
	}
	return false, RiskCategoryNone, ""
}

// Ambiguous security patterns
var ambiguousSecurityPatterns = []struct {
	pattern *regexp.Regexp
	desc    string
}{
	{regexp.MustCompile(`(?i)\b(firewall\s+bypass|security\s+audit|pentest|port\s+scan)\b`), "ambiguous_security_term"},
	{regexp.MustCompile(`(?i)\b(exploit|payload|injection\s+vector)\b`), "ambiguous_exploit_term"},
}

func matchAmbiguousSecurity(s string) (bool, string) {
	for _, entry := range ambiguousSecurityPatterns {
		if entry.pattern.MatchString(s) {
			return true, entry.desc
		}
	}
	return false, ""
}
