package fleetteams

import (
	"strings"
	"testing"
)

func TestI08_ParseMappingLine_OK(t *testing.T) {
	row, err := ParseMappingLine("42,aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee,Acme/Security")
	if err != nil {
		t.Fatal(err)
	}
	if row.FleetTeamID != 42 || row.OrgID == "" || row.TeamName == "" {
		t.Fatalf("bad row: %+v", row)
	}
}

func TestI08_ParseMappingLine_RejectsBad(t *testing.T) {
	for _, bad := range []string{"", "0,org,n", "x,org,n", "1,../org,n", "1,org,", "1,,n"} {
		if _, err := ParseMappingLine(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestI08_ParseMappingCSV(t *testing.T) {
	body := "# comment\n1,org-a,TeamA\n2,org-b,TeamB\n"
	rows, err := ParseMappingCSV(body)
	if err != nil || len(rows) != 2 {
		t.Fatalf("got %v %v", rows, err)
	}
	sql := SQLInsert(rows[0])
	if sql == "" || !strings.Contains(sql, "fleet_team_orgs") {
		t.Fatalf("sql: %s", sql)
	}
}

func TestJ08_ParseMappingCSV_MaxRows(t *testing.T) {
	// Production: CSV above MaxMappingCSVRows is rejected.
	var b strings.Builder
	for i := 0; i < MaxMappingCSVRows+1; i++ {
		b.WriteString(itoa(i+1) + ",org,n\n")
	}
	if _, err := ParseMappingCSV(b.String()); err == nil {
		t.Fatal("expected max rows error")
	}
}
func itoa(n int) string {
	if n == 0 { return "0" }
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
