package skills

import "FrostAgent/internal/storage"

// Snapshot contains metadata and only the requested instruction bodies, never
// resource bytes. Its token is valid only for the originating instance state.
type Snapshot struct {
	Infos        []Info
	Instructions map[string]Package
	state        *storage.SkillState
	generation   uint64
}

// Snapshot reads one consistent skill state. Writers always acquire the shared
// state lock before SQL, including imports through another Store handle.
func (s *Store) Snapshot(names ...string) (Snapshot, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()
	infos, err := s.List()
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{
		Infos: infos, Instructions: map[string]Package{},
		state: s.state, generation: s.state.Generation,
	}
	requested := make(map[string]bool, len(names))
	for _, name := range names {
		requested[name] = true
	}
	for _, info := range infos {
		if info.Enabled && requested[info.Name] {
			pkg, err := s.Instructions(info.Name)
			if err != nil {
				return Snapshot{}, err
			}
			result.Instructions[info.Name] = pkg
		}
	}
	return result, nil
}

// WithSnapshot applies a short in-memory operation only while this snapshot is
// current. The callback must not access SQL or call Store methods: it runs under
// the state read lock. A false result asks the caller to read a fresh snapshot.
func (s *Store) WithSnapshot(snapshot Snapshot, apply func() error) (bool, error) {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()
	if snapshot.state != s.state || snapshot.generation != s.state.Generation {
		return false, nil
	}
	return true, apply()
}
