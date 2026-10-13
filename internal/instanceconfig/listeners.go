package instanceconfig

// Default listener addresses can be overridden at link time for container builds.
// Settings saved in SQL always take precedence over these defaults.
var (
	DefaultListenAddr   = "127.0.0.1:8080"
	DefaultWSListenAddr = "127.0.0.1:1234"
)
