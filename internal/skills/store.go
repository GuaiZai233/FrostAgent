package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"FrostAgent/internal/storage"
)

type Store struct {
	db         *storage.DB
	instanceID string
	state      *storage.SkillState
}

func NewStore(db *storage.DB, instanceID string) *Store {
	return &Store{db: db, instanceID: instanceID, state: db.SkillState(instanceID)}
}

func (s *Store) List() ([]Info, error) {
	rows, err := s.db.SQL.Query(s.db.Bind("SELECT info_json, enabled FROM skills WHERE instance_id = ? ORDER BY name"), s.instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Info{}
	for rows.Next() {
		var encoded string
		var enabled int
		if err := rows.Scan(&encoded, &enabled); err != nil {
			return nil, err
		}
		var info Info
		if err := json.Unmarshal([]byte(encoded), &info); err != nil {
			return nil, err
		}
		info.Enabled = enabled == 1
		result = append(result, info)
	}
	return result, rows.Err()
}

func (s *Store) Get(name string) (Package, error) {
	var encoded string
	var enabled int
	err := s.db.SQL.QueryRow(s.db.Bind("SELECT package_json, enabled FROM skills WHERE instance_id = ? AND name = ?"), s.instanceID, name).Scan(&encoded, &enabled)
	if err != nil {
		return Package{}, err
	}
	var pkg Package
	if err := json.Unmarshal([]byte(encoded), &pkg); err != nil {
		return Package{}, err
	}
	pkg.Info.Enabled = enabled == 1
	return pkg, nil
}

func (s *Store) ImportZIP(data []byte) ([]Info, error) {
	packages, err := ParseZIP(data)
	if err != nil {
		return nil, err
	}
	if err := s.ImportPackages(packages); err != nil {
		return nil, err
	}
	result := make([]Info, 0, len(packages))
	for _, pkg := range packages {
		result = append(result, pkg.Info)
	}
	return result, nil
}

// ImportPackages validates all packages before the transaction; same-name imports
// are rejected rather than replacing enabled instructions mid-session.
func (s *Store) ImportPackages(packages []Package) error {
	validated, err := ValidatePackages(packages)
	if err != nil {
		return err
	}
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	s.state.Generation++ // Invalidate old snapshots even if a write outcome is uncertain.
	tx, err := s.db.SQL.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current, total int
	if err := tx.QueryRow(s.db.Bind("SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM skills WHERE instance_id = ?"), s.instanceID).Scan(&current, &total); err != nil {
		return err
	}
	for _, pkg := range validated {
		for _, data := range pkg.Files {
			total += len(data)
		}
	}
	if total > MaxExpandedBytes {
		return fmt.Errorf("每个实例技能总大小最大为 32 MiB")
	}
	if current+len(validated) > MaxSkills {
		return fmt.Errorf("每个实例最多 %d 个技能", MaxSkills)
	}
	for _, pkg := range validated {
		var exists int
		if err := tx.QueryRow(s.db.Bind("SELECT COUNT(*) FROM skills WHERE instance_id = ? AND name = ?"), s.instanceID, pkg.Info.Name).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return fmt.Errorf("技能 %s 已存在，请先删除或使用不同名称", pkg.Info.Name)
		}
		enabled := 0
		if pkg.Info.Enabled {
			enabled = 1
		}
		encoded, err := json.Marshal(pkg)
		if err != nil {
			return err
		}
		info, err := json.Marshal(pkg.Info)
		if err != nil {
			return err
		}
		size := 0
		for _, data := range pkg.Files {
			size += len(data)
		}
		if _, err := tx.Exec(s.db.Bind("INSERT INTO skills(instance_id, name, enabled, info_json, body, package_json, size_bytes) VALUES (?, ?, ?, ?, ?, ?, ?)"), s.instanceID, pkg.Info.Name, enabled, string(info), pkg.Body, string(encoded), size); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ValidatePackages(packages []Package) ([]Package, error) {
	if len(packages) > MaxSkills {
		return nil, fmt.Errorf("技能数量超出限制")
	}
	names := map[string]bool{}
	total := 0
	result := make([]Package, 0, len(packages))
	for _, original := range packages {
		pkg, err := ParsePackage(original.Files, original.Info.Name, original.Executables...)
		if err != nil {
			return nil, err
		}
		if names[pkg.Info.Name] {
			return nil, fmt.Errorf("技能名称重复: %s", pkg.Info.Name)
		}
		names[pkg.Info.Name] = true
		for _, data := range pkg.Files {
			total += len(data)
		}
		if total > MaxExpandedBytes {
			return nil, fmt.Errorf("技能总大小超过 32 MiB")
		}
		pkg.Info.Enabled = original.Info.Enabled
		result = append(result, pkg)
	}
	return result, nil
}

func (s *Store) SetEnabled(name string, enabled bool) error {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	s.state.Generation++
	value := 0
	if enabled {
		value = 1
	}
	result, err := s.db.SQL.Exec(s.db.Bind("UPDATE skills SET enabled = ? WHERE instance_id = ? AND name = ?"), value, s.instanceID, name)
	return changed(result, err)
}
func (s *Store) Delete(name string) error {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	s.state.Generation++
	result, err := s.db.SQL.Exec(s.db.Bind("DELETE FROM skills WHERE instance_id = ? AND name = ?"), s.instanceID, name)
	return changed(result, err)
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Export() ([]Package, error) {
	infos, err := s.List()
	if err != nil {
		return nil, err
	}
	result := make([]Package, 0, len(infos))
	for _, info := range infos {
		pkg, err := s.Get(info.Name)
		if err != nil {
			return nil, err
		}
		result = append(result, pkg)
	}
	return result, nil
}

func (s *Store) Enabled(name string) (Package, error) {
	pkg, err := s.Get(name)
	if err != nil {
		return pkg, err
	}
	if !pkg.Info.Enabled {
		return pkg, errors.New("技能已禁用")
	}
	return pkg, nil
}

// Instructions reads only the small metadata/body columns, leaving assets out of prompt assembly.
func (s *Store) Instructions(name string) (Package, error) {
	var pkg Package
	var encoded string
	var enabled int
	err := s.db.SQL.QueryRow(s.db.Bind("SELECT info_json, body, enabled FROM skills WHERE instance_id = ? AND name = ?"), s.instanceID, name).Scan(&encoded, &pkg.Body, &enabled)
	if err != nil {
		return pkg, err
	}
	if err := json.Unmarshal([]byte(encoded), &pkg.Info); err != nil {
		return pkg, err
	}
	pkg.Info.Enabled = enabled == 1
	if !pkg.Info.Enabled {
		return pkg, errors.New("技能已禁用")
	}
	return pkg, nil
}
