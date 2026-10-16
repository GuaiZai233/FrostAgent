package storage

import "sync"

// SkillState coordinates skill snapshots and writes across all Store handles for
// one instance. Acquire Mu before starting SQL work or locking a session.
type SkillState struct {
	Mu         sync.RWMutex
	Generation uint64
}

// SkillState keeps the same coordinator for the lifetime of this DB, including
// runtime replacement and instance deletion while old Store handles still exist.
func (d *DB) SkillState(instanceID string) *SkillState {
	state, _ := d.skillStates.LoadOrStore(instanceID, &SkillState{})
	return state.(*SkillState)
}
