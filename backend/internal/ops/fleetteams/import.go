// Package fleetteams provides operator helpers for fleet_team_orgs bulk import.
package fleetteams

import (
	"fmt"
	"strconv"
	"strings"
)

// MappingRow is one fleet_team_orgs backfill record (operator tooling).
type MappingRow struct {
	FleetTeamID int
	OrgID       string
	TeamName    string
}

// ParseMappingLine parses "fleet_team_id,org_id,team_name" (CSV-ish, no quotes).
// Rejects empty fields, non-positive team IDs, and path-like team names.
func ParseMappingLine(line string) (MappingRow, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return MappingRow{}, fmt.Errorf("empty or comment line")
	}
	parts := strings.Split(line, ",")
	if len(parts) != 3 {
		return MappingRow{}, fmt.Errorf("want fleet_team_id,org_id,team_name")
	}
	idStr := strings.TrimSpace(parts[0])
	orgID := strings.TrimSpace(parts[1])
	name := strings.TrimSpace(parts[2])
	if idStr == "" || orgID == "" || name == "" {
		return MappingRow{}, fmt.Errorf("fields must be non-empty")
	}
	if len(idStr) > 9 {
		return MappingRow{}, fmt.Errorf("fleet_team_id too long")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return MappingRow{}, fmt.Errorf("fleet_team_id must be a positive integer")
	}
	if strings.ContainsAny(orgID, "/\\") || strings.Contains(orgID, "..") {
		return MappingRow{}, fmt.Errorf("org_id must not contain path separators")
	}
	if strings.ContainsAny(name, "\x00\n\r") {
		return MappingRow{}, fmt.Errorf("team_name must not contain control characters")
	}
	return MappingRow{FleetTeamID: id, OrgID: orgID, TeamName: name}, nil
}

// ParseMappingCSV parses multi-line CSV of mapping rows (skips blank/# comments).
func ParseMappingCSV(body string) ([]MappingRow, error) {
	var out []MappingRow
	for i, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		row, err := ParseMappingLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no mapping rows")
	}
	return out, nil
}

// SQLInsert returns a parameterized INSERT template comment for operators.
func SQLInsert(row MappingRow) string {
	return fmt.Sprintf(
		"INSERT INTO fleet_team_orgs (fleet_team_id, org_id, team_name) VALUES (%d, '%s', '%s') ON CONFLICT (fleet_team_id) DO UPDATE SET org_id = EXCLUDED.org_id, team_name = EXCLUDED.team_name;",
		row.FleetTeamID,
		strings.ReplaceAll(row.OrgID, "'", "''"),
		strings.ReplaceAll(row.TeamName, "'", "''"),
	)
}
