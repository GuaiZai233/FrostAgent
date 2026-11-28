package modelrouter

import (
	"errors"
	"fmt"
	"strings"
)

// ErrOutcomeUnknown requires the caller to query active state, never to retry
// automatically. External credential compensation may have failed.
var ErrOutcomeUnknown = errors.New("model update outcome unknown")

type Mutation struct {
	Operation  string
	EndpointID string
	Endpoint   Endpoint
	Workload   Workload
	Binding    Binding
	Secret     string
}

// ApplyDirect reads the latest state and commits a single mutation under the
// same locks used by Web publication and backup import. The editor's draft and
// staged credentials are left intact at their old revision.
func (m *Manager) ApplyDirect(change Mutation) (Configuration, error) {
	if m.db == nil {
		return Configuration{}, fmt.Errorf("direct updates require SQL storage")
	}
	m.db.CatalogMutationMu.Lock()
	defer m.db.CatalogMutationMu.Unlock()
	m.db.ModelRouterMu.Lock()
	defer m.db.ModelRouterMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	secrets, err := newSQLSecretBackend(m.db, m.instanceID)
	if err != nil {
		return Configuration{}, err
	}
	writer := &Manager{db: m.db, instanceID: m.instanceID, secrets: secrets, active: defaultConfiguration()}
	if err = writer.loadSQL(); err != nil {
		return Configuration{}, err
	}
	cfg := cloneConfiguration(writer.active)
	if strings.HasPrefix(change.Operation, "route-") {
		if m.instanceID == "" {
			return Configuration{}, errors.New("routes require an instance")
		}
		valid := false
		for _, w := range Workloads {
			valid = valid || change.Workload == w
		}
		if !valid {
			return Configuration{}, errors.New("unknown workload")
		}
		switch change.Operation {
		case "route-set":
			cfg.GlobalBindings[change.Workload] = change.Binding
		case "route-unset":
			cfg.GlobalBindings[change.Workload] = defaultConfiguration().GlobalBindings[change.Workload]
		default:
			return Configuration{}, errors.New("unknown route mutation")
		}
	} else {
		if m.instanceID != "" {
			return Configuration{}, errors.New("endpoints require global scope")
		}
		index := -1
		for n, e := range cfg.Endpoints {
			if e.ID == change.EndpointID {
				index = n
			}
		}
		switch change.Operation {
		case "endpoint-add":
			if change.Endpoint.ID == "" {
				return Configuration{}, errors.New("endpoint ID is required")
			}
			for _, e := range cfg.Endpoints {
				if e.ID == change.Endpoint.ID {
					return Configuration{}, errors.New("endpoint already exists")
				}
			}
			cfg.Endpoints = append(cfg.Endpoints, change.Endpoint)
		case "endpoint-update":
			if index < 0 {
				return Configuration{}, errors.New("endpoint does not exist")
			}
			if change.Endpoint.ID != "" && change.Endpoint.ID != change.EndpointID {
				return Configuration{}, errors.New("endpoint ID cannot change")
			}
			change.Endpoint.ID = change.EndpointID
			cfg.Endpoints[index] = change.Endpoint
		case "endpoint-remove":
			if index < 0 {
				return Configuration{}, errors.New("endpoint does not exist")
			}
			for _, model := range cfg.Models {
				if model.EndpointID == change.EndpointID {
					return Configuration{}, fmt.Errorf("endpoint is referenced by model %s; remove its catalog entry first", model.ID)
				}
			}
			cfg.Endpoints = append(cfg.Endpoints[:index], cfg.Endpoints[index+1:]...)
		case "secret-set", "secret-unset":
			if index < 0 {
				return Configuration{}, errors.New("endpoint does not exist")
			}
			endpoint := cfg.Endpoints[index]
			if !persistentSecretSource(endpoint.APIKeySource) {
				return Configuration{}, errors.New("credential source does not support direct writes")
			}
			if change.Operation == "secret-set" && strings.TrimSpace(change.Secret) == "" {
				return Configuration{}, errors.New("secret is empty")
			}
			secrets.Stage(endpoint, change.Secret, change.Operation == "secret-unset")
		default:
			return Configuration{}, errors.New("unknown endpoint mutation")
		}
	}
	normalizeConfiguration(&cfg)
	if err = validateStoredConfiguration(cfg); err != nil {
		return Configuration{}, err
	}
	if err = validateSQLSources(cfg); err != nil {
		return Configuration{}, err
	}
	if err = PersistentRefs(cfg.Endpoints); err != nil {
		return Configuration{}, err
	}
	if m.instanceID == "" {
		if err = checkCatalogReferences(m.db, cfg); err != nil {
			return Configuration{}, err
		}
	}
	cfg.Revision = writer.active.Revision + 1
	rollback := func() error { return nil }
	if m.instanceID == "" {
		rollback, err = secrets.apply(writer.active, cfg, false)
		if err != nil {
			return Configuration{}, err
		}
	}
	if err = writer.writeSQL(cfg); err != nil {
		if undoErr := rollback(); undoErr != nil {
			return Configuration{}, fmt.Errorf("%w: credential rollback failed", ErrOutcomeUnknown)
		}
		return Configuration{}, err
	}
	// Reload configured flags from the committed credential store. An error
	// here is not a failed write: publication has already committed.
	if err = writer.loadSQL(); err != nil {
		return Configuration{}, fmt.Errorf("%w: committed state could not be read", ErrOutcomeUnknown)
	}
	m.active = cloneConfiguration(writer.active)
	return cloneConfiguration(writer.active), nil
}
