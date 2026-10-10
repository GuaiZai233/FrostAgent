package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *GroupStore) updateSQLGroupName(name string) error {
	b := s.sql
	_, err := b.db.SQL.ExecContext(context.Background(), b.db.Bind(`INSERT INTO group_profiles
		(instance_id, platform, group_id, group_name, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (instance_id, platform, group_id) DO UPDATE SET
		group_name = excluded.group_name, updated_at = excluded.updated_at
		WHERE group_profiles.group_name <> excluded.group_name`),
		b.instanceID, b.platform, b.groupID, name, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GroupStore) updateSQLMemberFields(userID string, role GroupRole, preferredName string, aliases []string, updateRole bool) error {
	b := s.sql
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cleanAliases := make([]string, 0, len(aliases))
	seen := make(map[string]bool, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" && !seen[alias] {
			seen[alias] = true
			cleanAliases = append(cleanAliases, alias)
		}
	}
	encodedAliases, err := json.Marshal(cleanAliases)
	if err != nil {
		return err
	}
	update := `preferred_name = excluded.preferred_name, aliases_json = excluded.aliases_json,
		updated_at = excluded.updated_at`
	if updateRole {
		update = `role = excluded.role, updated_at = excluded.updated_at`
	}
	tx, err := b.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, b.db.Bind(`INSERT INTO group_profiles
		(instance_id, platform, group_id, group_name, updated_at) VALUES (?, ?, ?, '', ?)
		ON CONFLICT (instance_id, platform, group_id) DO UPDATE SET updated_at = excluded.updated_at`),
		b.instanceID, b.platform, b.groupID, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, b.db.Bind(`INSERT INTO group_members
		(instance_id, platform, group_id, user_id, nickname, card, role, preferred_name,
		 aliases_json, source, created_at, updated_at, last_spoke_at)
		VALUES (?, ?, ?, ?, '', '', ?, ?, ?, '', ?, ?, '')
		ON CONFLICT (instance_id, platform, group_id, user_id) DO UPDATE SET `+update),
		b.instanceID, b.platform, b.groupID, userID, role, strings.TrimSpace(preferredName),
		string(encodedAliases), now, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GroupStore) observeSQLMember(userID, nickname, card, role, source string) (*MemberProfile, error) {
	b := s.sql
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := b.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, b.db.Bind(`INSERT INTO group_profiles
		(instance_id, platform, group_id, group_name, updated_at) VALUES (?, ?, ?, '', ?)
		ON CONFLICT (instance_id, platform, group_id) DO UPDATE SET updated_at = excluded.updated_at`),
		b.instanceID, b.platform, b.groupID, now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, b.db.Bind(`INSERT INTO group_members
		(instance_id, platform, group_id, user_id, nickname, card, role, preferred_name,
		 aliases_json, source, created_at, updated_at, last_spoke_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', '[]', ?, ?, ?, ?)
		ON CONFLICT (instance_id, platform, group_id, user_id) DO UPDATE SET
		nickname = CASE WHEN excluded.nickname <> '' THEN excluded.nickname ELSE group_members.nickname END,
		card = CASE WHEN excluded.card <> '' THEN excluded.card ELSE group_members.card END,
		role = CASE WHEN excluded.role <> ? THEN excluded.role ELSE group_members.role END,
		source = CASE WHEN group_members.source = '' THEN excluded.source ELSE group_members.source END,
		updated_at = excluded.updated_at, last_spoke_at = excluded.last_spoke_at`),
		b.instanceID, b.platform, b.groupID, userID, SanitizeProfileText(nickname),
		SanitizeProfileText(card), NormalizeGroupRole(role), source, now, now, now, GroupRoleUnknown); err != nil {
		return nil, err
	}
	member := &MemberProfile{}
	var aliases, created, updated, lastSpoke string
	err = tx.QueryRowContext(ctx, b.db.Bind(`SELECT user_id, nickname, card, role, preferred_name,
		aliases_json, source, created_at, updated_at, last_spoke_at FROM group_members
		WHERE instance_id = ? AND platform = ? AND group_id = ? AND user_id = ?`),
		b.instanceID, b.platform, b.groupID, userID).Scan(&member.UserID, &member.Nickname, &member.Card,
		&member.Role, &member.PreferredName, &aliases, &member.Source, &created, &updated, &lastSpoke)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(aliases), &member.Aliases); err != nil {
		return nil, err
	}
	if member.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, err
	}
	if member.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return nil, err
	}
	if member.LastSpokeAt, err = time.Parse(time.RFC3339Nano, lastSpoke); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return member, nil
}

func (s *GroupStore) loadSQLProfile() (*GroupProfile, error) {
	b := s.sql
	ctx := context.Background()
	profile := &GroupProfile{GroupID: s.groupID, Members: map[string]*MemberProfile{}, UpdatedAt: time.Now()}
	var updatedAt string
	err := b.db.SQL.QueryRowContext(ctx, b.db.Bind(`SELECT group_name, updated_at FROM group_profiles
		WHERE instance_id = ? AND platform = ? AND group_id = ?`), b.instanceID, b.platform, b.groupID).
		Scan(&profile.GroupName, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return profile, nil
	}
	if err != nil {
		return nil, err
	}
	profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := b.db.SQL.QueryContext(ctx, b.db.Bind(`SELECT user_id, nickname, card, role,
		preferred_name, aliases_json, source, created_at, updated_at, last_spoke_at
		FROM group_members WHERE instance_id = ? AND platform = ? AND group_id = ?`),
		b.instanceID, b.platform, b.groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		member := &MemberProfile{}
		var role GroupRole
		var aliases, created, updated, lastSpoke string
		if err := rows.Scan(&member.UserID, &member.Nickname, &member.Card, &role,
			&member.PreferredName, &aliases, &member.Source, &created, &updated, &lastSpoke); err != nil {
			return nil, err
		}
		member.Role = role
		if err := json.Unmarshal([]byte(aliases), &member.Aliases); err != nil {
			return nil, err
		}
		if member.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if member.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, err
		}
		if lastSpoke != "" {
			if member.LastSpokeAt, err = time.Parse(time.RFC3339Nano, lastSpoke); err != nil {
				return nil, err
			}
		}
		profile.Members[member.UserID] = member
	}
	return profile, rows.Err()
}

func (s *GroupStore) saveSQLProfile(profile *GroupProfile) error {
	b := s.sql
	ctx := context.Background()
	tx, err := b.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.saveSQLProfileTx(ctx, tx, profile); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GroupStore) saveSQLProfileTx(ctx context.Context, tx *sql.Tx, profile *GroupProfile) error {
	b := s.sql
	profile.UpdatedAt = time.Now().UTC()
	if _, err := tx.ExecContext(ctx, b.db.Bind(`INSERT INTO group_profiles
		(instance_id, platform, group_id, group_name, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (instance_id, platform, group_id) DO UPDATE SET
		group_name = excluded.group_name, updated_at = excluded.updated_at`),
		b.instanceID, b.platform, b.groupID, profile.GroupName, profile.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, b.db.Bind(`DELETE FROM group_members
		WHERE instance_id = ? AND platform = ? AND group_id = ?`), b.instanceID, b.platform, b.groupID); err != nil {
		return err
	}
	for userID, member := range profile.Members {
		if member == nil {
			continue
		}
		aliases, err := json.Marshal(member.Aliases)
		if err != nil {
			return err
		}
		lastSpoke := ""
		if !member.LastSpokeAt.IsZero() {
			lastSpoke = member.LastSpokeAt.UTC().Format(time.RFC3339Nano)
		}
		if _, err := tx.ExecContext(ctx, b.db.Bind(`INSERT INTO group_members
			(instance_id, platform, group_id, user_id, nickname, card, role, preferred_name,
			aliases_json, source, created_at, updated_at, last_spoke_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), b.instanceID, b.platform, b.groupID,
			userID, member.Nickname, member.Card, member.Role, member.PreferredName, string(aliases),
			member.Source, member.CreatedAt.UTC().Format(time.RFC3339Nano),
			member.UpdatedAt.UTC().Format(time.RFC3339Nano), lastSpoke); err != nil {
			return err
		}
	}
	return nil
}
