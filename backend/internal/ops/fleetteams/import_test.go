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
	sql, args := SQLInsert(rows[0])
	if sql == "" || !strings.Contains(sql, "fleet_team_orgs") {
		t.Fatalf("sql: %s", sql)
	}
	// The statement must be PARAMETERIZED: the values are bound, never
	// interpolated into the SQL text (issue #162).
	if !strings.Contains(sql, "$1") || !strings.Contains(sql, "$2") || !strings.Contains(sql, "$3") {
		t.Errorf("expected placeholders in the statement, got: %s", sql)
	}
	if strings.Contains(sql, "org-a") || strings.Contains(sql, "TeamA") {
		t.Errorf("values must not be inlined into the SQL text, got: %s", sql)
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 bound arguments, got %d", len(args))
	}
	if args[0] != rows[0].FleetTeamID || args[1] != rows[0].OrgID || args[2] != rows[0].TeamName {
		t.Errorf("unexpected bound arguments: %v", args)
	}
}

// TestI08_SQLInsertDoesNotInlineQuotes proves a value containing a quote is
// passed as a bound argument, not spliced into the SQL text.
func TestI08_SQLInsertDoesNotInlineQuotes(t *testing.T) {
	row := MappingRow{FleetTeamID: 7, OrgID: "org'; DROP TABLE fleet_team_orgs; --", TeamName: "O'Brien"}
	sql, args := SQLInsert(row)
	if strings.Contains(sql, "DROP TABLE") {
		t.Errorf("the value must not appear in the SQL text, got: %s", sql)
	}
	if args[1] != row.OrgID {
		t.Errorf("expected the raw value to be bound, got %v", args[1])
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

func TestK08_ParseMappingCSV_RejectsDuplicateTeamID(t *testing.T) {
	_, err := ParseMappingCSV("1,org-a,A\n1,org-b,B\n")
	if err == nil {
		t.Fatal("expected duplicate fleet_team_id error")
	}
}
