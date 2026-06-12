package tools

// Registry implements core.ToolRegistry.
// It stores tool definitions mapped to their executors.
type Registry struct {
	tools     map[string]Tool
	executors map[string]func(args string) (string, error)
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:     make(map[string]Tool),
		executors: make(map[string]func(args string) (string, error)),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.name] = t
	r.executors[t.name] = t.execute
}

// GetTool returns the tool metadata by name.
func (r *Registry) GetTool(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// GetExecutor returns the executor function for a tool name.
func (r *Registry) GetExecutor(name string) (func(args string) (string, error), bool) {
	fn, ok := r.executors[name]
	return fn, ok
}

// ListTools returns all registered tool definitions.
func (r *Registry) ListTools() []Tool {
	list := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		list = append(list, t)
	}
	return list
}

// Execute invokes a tool by name with JSON arguments.
func (r *Registry) Execute(name, args string) (string, error) {
	if fn, ok := r.executors[name]; ok {
		return fn(args)
	}
	return "tool not found", nil
}