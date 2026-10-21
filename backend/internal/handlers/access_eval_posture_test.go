package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"go.uber.org/zap"
)

// Regression tests for issue #202: when the access-eval request carries no
// device id, the request cannot be attributed to a device. The previous
// implementation allowed the request if ANY enrolled device was compliant,
// which let a user on a non-compliant machine bypass posture by suppressing
// the device-identity cookie. Every enrolled device must now be compliant.

func postureHandler(states map[string]*fleet.SecurityState) *Handler {
	return &Handler{
		fleet: &fakeFleet{
			getHostSecurityStateFn: func(_ context.Context, hostID string) (*fleet.SecurityState, error) {
				if s, ok := states[hostID]; ok {
					return s, nil
				}
				return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
			},
		},
		logger: zap.NewNop(),
	}
}

func compliant() *fleet.SecurityState {
	return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}
}

func nonCompliant() *fleet.SecurityState {
	return &fleet.SecurityState{FirewallEnabled: false, DiskEncrypted: true}
}

func TestEvaluateDevicesPosture_AllDevicesMustBeCompliant(t *testing.T) {
	h := postureHandler(map[string]*fleet.SecurityState{
		"host-good": compliant(),
		"host-bad":  nonCompliant(),
	})

	// The regression case: one clean device and one dirty device. The old
	// "any compliant" logic allowed this request.
	reasons := evaluateDevicesPosture(context.Background(), h, appPolicy{}, []string{"host-good", "host-bad"})
	if len(reasons) == 0 {
		t.Fatal("expected a deny reason when one of the enrolled devices is non-compliant, got none")
	}
	if !strings.Contains(strings.Join(reasons, "|"), "host-bad") {
		t.Fatalf("expected the reason to name the offending device host-bad, got %v", reasons)
	}
	if strings.Contains(strings.Join(reasons, "|"), "host-good") {
		t.Fatalf("expected no reason for the compliant device host-good, got %v", reasons)
	}
}

func TestEvaluateDevicesPosture_OrderDoesNotMatter(t *testing.T) {
	h := postureHandler(map[string]*fleet.SecurityState{
		"host-good": compliant(),
		"host-bad":  nonCompliant(),
	})

	// The dirty device first: the old logic scanned for "any compliant" and
	// would still have allowed the request.
	reasons := evaluateDevicesPosture(context.Background(), h, appPolicy{}, []string{"host-bad", "host-good"})
	if len(reasons) == 0 {
		t.Fatal("expected a deny reason regardless of device order, got none")
	}
}

func TestEvaluateDevicesPosture_AllCompliantIsAllowed(t *testing.T) {
	h := postureHandler(map[string]*fleet.SecurityState{
		"host-a": compliant(),
		"host-b": compliant(),
	})

	if reasons := evaluateDevicesPosture(context.Background(), h, appPolicy{}, []string{"host-a", "host-b"}); len(reasons) != 0 {
		t.Fatalf("expected no deny reasons when every device is compliant, got %v", reasons)
	}
}

func TestEvaluateDevicesPosture_SingleDeviceBehaviourUnchanged(t *testing.T) {
	// When the SPI supplies a device id, deviceIDs holds exactly that one
	// device, so the single-device behaviour must be unchanged.
	h := postureHandler(map[string]*fleet.SecurityState{
		"host-bad": nonCompliant(),
	})

	reasons := evaluateDevicesPosture(context.Background(), h, appPolicy{}, []string{"host-bad"})
	if len(reasons) == 0 {
		t.Fatal("expected a deny reason for the single non-compliant device, got none")
	}

	hGood := postureHandler(map[string]*fleet.SecurityState{"host-good": compliant()})
	if reasons := evaluateDevicesPosture(context.Background(), hGood, appPolicy{}, []string{"host-good"}); len(reasons) != 0 {
		t.Fatalf("expected no deny reasons for a single compliant device, got %v", reasons)
	}
}

func TestEvaluateDevicesPosture_FleetFailureFailsClosed(t *testing.T) {
	h := &Handler{
		fleet: &fakeFleet{
			getHostSecurityStateFn: func(_ context.Context, _ string) (*fleet.SecurityState, error) {
				return nil, errors.New("fleet unreachable")
			},
		},
		logger: zap.NewNop(),
	}

	reasons := evaluateDevicesPosture(context.Background(), h, appPolicy{}, []string{"host-1"})
	if len(reasons) == 0 {
		t.Fatal("expected a deny reason when the posture lookup fails, got none")
	}
}
