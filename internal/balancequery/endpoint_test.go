package balancequery

import "testing"

func TestSameEndpoint(t *testing.T) {
	const deepseek = "curl https://api.deepseek.com/user/balance -H 'Authorization: Bearer {{API_KEY}}'"
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical templates", deepseek, deepseek, true},
		{"header-only change keeps endpoint", deepseek, "curl https://api.deepseek.com/user/balance -H 'Authorization: Bearer {{API_KEY}}' -H 'Accept: application/json'", true},
		{"host change", deepseek, "curl https://other.example/user/balance -H 'Authorization: Bearer {{API_KEY}}'", false},
		{"path change on same host", deepseek, "curl https://api.deepseek.com/user/other -H 'Authorization: Bearer {{API_KEY}}'", false},
		{"referer header naming old host does not hide a host change", deepseek,
			"curl -H 'Referer: https://api.deepseek.com' https://other.example/user/balance -H 'Authorization: Bearer {{API_KEY}}'", false},
		{"--url target is compared", deepseek, "curl --url https://other.example/user/balance -H 'Authorization: Bearer {{API_KEY}}'", false},
		{"unparseable template is never the same", deepseek, "curl -H 'Authorization: Bearer {{API_KEY}}'", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameEndpoint(tc.a, tc.b); got != tc.want {
				t.Fatalf("SameEndpoint() = %v, want %v", got, tc.want)
			}
		})
	}
}
