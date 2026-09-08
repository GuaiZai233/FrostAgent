package settings

// The multi-instance runtime exposes OneBot and AstrBot together. Keep legacy
// ENABLE_*_ADAPTER keys parseable in instanceconfig for old .env files, but do
// not advertise them as user-selectable Settings fields until adapter selection
// is implemented deliberately.
func init() {
	delete(knownEnvVars, "ENABLE_ONEBOT_ADAPTER")
	delete(knownEnvVars, "ENABLE_ASTRBOT_ADAPTER")
}
