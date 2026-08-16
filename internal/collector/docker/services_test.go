package docker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func servicesFakeRun(t *testing.T, out string, err error) runFunc {
	t.Helper()
	return func(_ context.Context, name string, args ...string) (string, error) {
		t.Helper()
		if name != "docker" || !reflect.DeepEqual(args, []string{"ps", "--format", "{{json .}}"}) {
			t.Fatalf("command = %q %q, want docker ps --format {{json .}}", name, args)
		}
		return out, err
	}
}

func TestServicesSingleContainer(t *testing.T) {
	out := `{"Image":"postgres:16","Ports":"0.0.0.0:5432->5432/tcp"}`
	want := []snapshot.Service{{Image: "postgres:16", Ports: []string{"5432"}}}

	got, err := Services(context.Background(), servicesFakeRun(t, out, nil))
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Services() = %+v, want %+v", got, want)
	}
}

func TestServicesMailpitDoesNotLeakMachineDetails(t *testing.T) {
	out := `{"Command":"\"/mailpit\"","ID":"139559426b40","Image":"axllent/mailpit:latest","Labels":"com.docker.compose.project.config_files=/Users/someone/Projects/thing/docker-compose.yml,com.docker.compose.project=thing,...","Names":"thing-mailpit-1","Ports":"0.0.0.0:1025->1025/tcp, [::]:1025->1025/tcp, 0.0.0.0:8025->8025/tcp, [::]:8025->8025/tcp, 1110/tcp","State":"running","Status":"Up 3 hours (healthy)"}`
	want := []snapshot.Service{{Image: "axllent/mailpit:latest", Ports: []string{"1025", "8025"}}}

	got, err := Services(context.Background(), servicesFakeRun(t, out, nil))
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Services() = %+v, want %+v", got, want)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, private := range []string{"thing-mailpit-1", "thing", "/Users/someone/Projects/thing/docker-compose.yml"} {
		if strings.Contains(string(encoded), private) {
			t.Errorf("serialized services contain private machine detail %q: %s", private, encoded)
		}
	}
}

func TestServicesDeduplicatesIPv4AndIPv6Port(t *testing.T) {
	out := `{"Image":"redis:7","Ports":"0.0.0.0:6379->6379/tcp, [::]:6379->6379/tcp"}`
	want := []snapshot.Service{{Image: "redis:7", Ports: []string{"6379"}}}

	got, err := Services(context.Background(), servicesFakeRun(t, out, nil))
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Services() = %+v, want %+v", got, want)
	}
}

func TestServicesDropsExposedOnlyPort(t *testing.T) {
	out := `{"Image":"mail:latest","Ports":"1110/tcp"}`
	want := []snapshot.Service{{Image: "mail:latest", Ports: []string{}}}

	got, err := Services(context.Background(), servicesFakeRun(t, out, nil))
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Services() = %+v, want %+v", got, want)
	}
}

func TestServicesMergesDuplicateImagesAndSortsPortsNumerically(t *testing.T) {
	out := strings.Join([]string{
		`{"Image":"postgres:16","Ports":"0.0.0.0:10000->10000/tcp, 0.0.0.0:80->80/tcp"}`,
		`{"Image":"postgres:16","Ports":"[::]:80->80/tcp, 0.0.0.0:9->9/tcp"}`,
	}, "\n")
	want := []snapshot.Service{{Image: "postgres:16", Ports: []string{"9", "80", "10000"}}}

	got, err := Services(context.Background(), servicesFakeRun(t, out, nil))
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Services() = %+v, want %+v", got, want)
	}
}

func TestServicesSkipsMalformedLine(t *testing.T) {
	out := strings.Join([]string{
		`{"Image":"redis:7","Ports":"0.0.0.0:6379->6379/tcp"}`,
		`warning: unexpected output`,
		`{"Image":"postgres:16","Ports":"0.0.0.0:5432->5432/tcp"}`,
	}, "\n")
	want := []snapshot.Service{
		{Image: "postgres:16", Ports: []string{"5432"}},
		{Image: "redis:7", Ports: []string{"6379"}},
	}

	got, err := Services(context.Background(), servicesFakeRun(t, out, nil))
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Services() = %+v, want %+v", got, want)
	}
}

func TestServicesEmptyOutput(t *testing.T) {
	got, err := Services(context.Background(), servicesFakeRun(t, "", nil))
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Services() = %#v, want non-nil empty slice", got)
	}
}

func TestServicesReturnsCommandError(t *testing.T) {
	wantErr := errors.New("docker daemon unavailable")
	got, err := Services(context.Background(), servicesFakeRun(t, "", wantErr))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Services() error = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Errorf("Services() = %+v, want nil after command failure", got)
	}
}
