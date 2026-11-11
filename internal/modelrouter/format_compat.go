package modelrouter

// This release cannot edit new protocol choices. Its old UI omits the field,
// so a save preserves the existing choice instead of silently clearing it.
func preserveEndpointFormats(next *Configuration, previous Configuration) {
	formats := make(map[string]string, len(previous.Endpoints))
	for _, endpoint := range previous.Endpoints {
		formats[endpoint.ID] = endpoint.APIFormat
	}
	for i := range next.Endpoints {
		if format, exists := formats[next.Endpoints[i].ID]; exists {
			next.Endpoints[i].APIFormat = format
		}
	}
}
