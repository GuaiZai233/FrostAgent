package modelrouter

import (
	"errors"
	"strings"
)

type Workload string

const (
	WorkloadDialogue      Workload = "dialogue"
	WorkloadSubagent      Workload = "subagent"
	WorkloadVision        Workload = "vision"
	WorkloadReflection    Workload = "reflection"
	WorkloadMemoryExtract Workload = "memory_extract"
	WorkloadGroupCompact  Workload = "group_compact"
)

var Workloads = []Workload{
	WorkloadDialogue,
	WorkloadSubagent,
	WorkloadVision,
	WorkloadReflection,
	WorkloadMemoryExtract,
	WorkloadGroupCompact,
}

type BindingMode string

const (
	BindingInherit        BindingMode = "inherit"
	BindingModel          BindingMode = "model"
	BindingDisabled       BindingMode = "disabled"
	BindingFollowDialogue BindingMode = "follow_dialogue"
)

var ErrDisabled = errors.New("model workload is disabled")

type Endpoint struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	Enabled     bool   `json:"enabled"`
}

type Model struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	EndpointID    string   `json:"endpoint_id"`
	UpstreamModel string   `json:"upstream_model"`
	Enabled       bool     `json:"enabled"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

type Binding struct {
	Mode    BindingMode `json:"mode"`
	ModelID string      `json:"model_id,omitempty"`
}

type GroupOverride struct {
	Platform string               `json:"platform"`
	GroupID  string               `json:"group_id"`
	Bindings map[Workload]Binding `json:"bindings,omitempty"`
}

type Configuration struct {
	Version        int                  `json:"version"`
	Revision       int64                `json:"revision"`
	Endpoints      []Endpoint           `json:"endpoints"`
	Models         []Model              `json:"models"`
	GlobalBindings map[Workload]Binding `json:"global_bindings"`
	GroupOverrides []GroupOverride      `json:"group_overrides"`
}

type Scope struct {
	Platform string
	GroupID  string
}

func (s Scope) Normalized() Scope {
	return Scope{
		Platform: strings.ToLower(strings.TrimSpace(s.Platform)),
		GroupID:  strings.TrimSpace(s.GroupID),
	}
}

type Target struct {
	EndpointID          string
	EndpointDisplayName string
	ModelID             string
	ModelDisplayName    string
	BaseURL             string
	APIKey              string
	UpstreamModel       string
}

type EffectiveBinding struct {
	Workload       Workload
	Binding        Binding
	Inherited      bool
	RuntimeApplied bool
	Target         *Target
}

func RuntimeApplied(Workload) bool {
	return true
}
