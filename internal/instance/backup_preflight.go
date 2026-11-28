package instance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/buildinfo"
)

func (m *Manager) VerifyBackup(ctx context.Context, archive []byte, name string) (map[string]any, error) {
	if m.db == nil {
		return nil, fmt.Errorf("backup verification requires SQL storage")
	}
	decoded, err := backup.DecodeInstanceZIP(archive)
	if err != nil {
		return nil, err
	}
	impact, err := backup.PreviewRestore(ctx, m.db, decoded)
	if err != nil {
		return nil, err
	}
	explicit := strings.TrimSpace(name) != ""
	if !explicit {
		name = decoded.Manifest.InstanceName
	}
	infos, next := m.List()
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("实例%d", next)
	}
	name, err = validateName(name)
	if err != nil {
		return nil, err
	}
	conflicts := []string{}
	baseName := name
	for suffix := 2; ; suffix++ {
		exists := false
		for _, info := range infos {
			if strings.EqualFold(info.Name, name) {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
		if explicit {
			conflicts = append(conflicts, "instance name already exists")
			break
		}
		tail := fmt.Sprintf(" (%d)", suffix)
		base := []rune(baseName)
		if len(base) > 32-len([]rune(tail)) {
			base = base[:32-len([]rune(tail))]
		}
		name = string(base) + tail
	}
	metadata := "available"
	if decoded.Manifest.DatabaseSchemaVersion == 0 || decoded.Manifest.DatabaseMinimumCompatibleVersion == 0 || decoded.Manifest.ApplicationVersion == "" {
		metadata = "unknown: legacy backup omits version metadata; archive members and target schema were validated"
	}
	return map[string]any{
		"success": len(conflicts) == 0, "valid": true, "format_version": decoded.Manifest.FormatVersion,
		"backup":   map[string]any{"database_schema_version": decoded.Manifest.DatabaseSchemaVersion, "database_minimum_compatible_version": decoded.Manifest.DatabaseMinimumCompatibleVersion, "application_version": decoded.Manifest.ApplicationVersion, "metadata_status": metadata},
		"target":   map[string]any{"storage": m.storageStatus(ctx), "application_version": buildinfo.DisplayVersion()},
		"instance": map[string]any{"name": name, "enabled": false, "id": "allocated during restore"},
		"catalog":  impact, "conflicts": conflicts, "ownership": "not-provable: instance name is not an instance identity",
		"credentials":               "independent credentials are excluded; existing shared credentials are preserved",
		"application_compatibility": "archive structure and database capabilities determine compatibility; application version is informational",
	}, nil
}

func (m *Manager) handleBackupVerify(w http.ResponseWriter, r *http.Request) {
	archive, err := io.ReadAll(http.MaxBytesReader(w, r.Body, backup.MaxInstanceZIPBytes))
	if err != nil {
		writeError(w, err)
		return
	}
	plan, err := m.VerifyBackup(r.Context(), archive, "")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, plan)
}
