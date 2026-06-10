package tools

type Tool struct {
	name        string
	description string
	parameter   any //用于大模型生成json schema
	execute     func(args string) (string, error)
}

func (t Tool) Name() string {
	return t.name // 返回字段里的值
}

func (t Tool) Description() string {
	return t.description
}

func (t Tool) Parameters() map[string]any {
	if params, ok := t.parameter.(map[string]any); ok {
		return params
	}
	return nil
}

func (t Tool) Execute(args string) (string, error) {
	return t.execute(args)
}
