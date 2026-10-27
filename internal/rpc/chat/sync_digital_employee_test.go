package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openimsdk/chat/pkg/common/config"
	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
)

func TestFetchDigitalEmployeeSnapshotUsesConfiguredBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != digitalEmployeeListPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"sysId":"openIM"}` {
			t.Fatalf("unexpected request body %s", body)
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"list":[{"employeeCode":"RO-001","employeeName":"智衡","status":"1"}]}}`))
	}))
	defer server.Close()

	employees, err := fetchDigitalEmployeeSnapshot(context.Background(), server.Client(), config.DigitalEmployee{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("fetch snapshot: %v", err)
	}
	if len(employees) != 1 || employees[0].EmployeeCode != "RO-001" {
		t.Fatalf("employees = %#v", employees)
	}
}

func TestPlanDigitalEmployeeSnapshotPartitionsActiveAndInactive(t *testing.T) {
	plan, err := planDigitalEmployeeSnapshot([]digitalEmployee{
		{EmployeeCode: "RO-001", EmployeeName: "智衡", Status: "1"},
		{EmployeeCode: "RS-001", EmployeeName: "观途", Status: "2"},
	})
	if err != nil {
		t.Fatalf("plan snapshot: %v", err)
	}
	if len(plan.Active) != 1 || plan.Active[0].EmployeeCode != "RO-001" {
		t.Fatalf("active employees = %#v", plan.Active)
	}
	if len(plan.Inactive) != 1 || plan.Inactive[0].EmployeeCode != "RS-001" {
		t.Fatalf("inactive employees = %#v", plan.Inactive)
	}
}

func TestPlanDigitalEmployeeSnapshotRejectsDuplicateCodes(t *testing.T) {
	_, err := planDigitalEmployeeSnapshot([]digitalEmployee{
		{EmployeeCode: "RO-001", EmployeeName: "智衡", Status: "1"},
		{EmployeeCode: "RO-001", EmployeeName: "智衡", Status: "2"},
	})
	if err == nil {
		t.Fatal("expected duplicate employeeCode to be rejected")
	}
}

func TestPlanDigitalEmployeeSnapshotRejectsInvalidStatus(t *testing.T) {
	_, err := planDigitalEmployeeSnapshot([]digitalEmployee{
		{EmployeeCode: "RO-001", EmployeeName: "智衡", Status: "3"},
	})
	if err == nil {
		t.Fatal("expected invalid status to be rejected")
	}
}

func TestDigitalEmployeeConfigDefaultsUsesVirtualDepartment(t *testing.T) {
	cfg := digitalEmployeeConfigDefaults(config.DigitalEmployee{Enable: true})
	if cfg.DepartmentID != "OU=数字员工,OU=ZXBXUsers,DC=qa,DC=bx" {
		t.Fatalf("department ID = %q", cfg.DepartmentID)
	}
	if cfg.DepartmentName != "数字员工" {
		t.Fatalf("department name = %q", cfg.DepartmentName)
	}
}

func TestBuildCredentialDigitalEmployee(t *testing.T) {
	if got := BuildCredentialDigitalEmployee("RO-001"); got != "de:RO-001" {
		t.Fatalf("credential = %q, want de:RO-001", got)
	}
}

func TestMergeDigitalEmployeeExPreservesDigitalTwinConfig(t *testing.T) {
	ex, err := mergeDigitalEmployeeEx(`{"openim_digital_twin":{"enabled":true},"legacy":"keep"}`, &chatdb.DepartmentMember{
		Username: "RO-001",
		Email:    "12345678901@aiehr.com",
		Phone:    "12345678901",
		Position: "数字员工",
	})
	if err != nil {
		t.Fatalf("merge ex: %v", err)
	}
	if ex != `{"email":"12345678901@aiehr.com","legacy":"keep","openim_digital_twin":{"enabled":true},"phone":"12345678901","position":"数字员工","username":"RO-001"}` {
		t.Fatalf("unexpected merged ex: %s", ex)
	}
}
