package docker

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Services returns the images running on this machine, deduplicated.
//
// Only the image and the published host ports are read, and that is the point
// of the function rather than an omission in it. `docker ps` reports the whole
// machine, not this project: it also prints container names carrying compose
// project names, and labels carrying the absolute paths of the user's unrelated
// repositories. A snapshot is a file people paste into bug reports, and the
// image alone answers the only question a rule asks of it.
//
// A container that publishes nothing is still recorded. A workflow's services
// publish no ports either — the job reaches them by hostname — so absence of
// ports is normal and says nothing about whether the service is there.
func Services(ctx context.Context, run runFunc) ([]snapshot.Service, error) {
	out, err := run(ctx, "docker", "ps", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}

	portsByImage := make(map[string]map[string]struct{})
	for _, line := range strings.Split(out, "\n") {
		var container struct {
			Image string
			Ports string
		}
		if json.Unmarshal([]byte(line), &container) != nil || container.Image == "" {
			continue
		}

		ports := portsByImage[container.Image]
		if ports == nil {
			ports = make(map[string]struct{})
			portsByImage[container.Image] = ports
		}
		for _, mapping := range strings.Split(container.Ports, ",") {
			host, _, published := strings.Cut(strings.TrimSpace(mapping), "->")
			if !published {
				continue
			}
			colon := strings.LastIndexByte(host, ':')
			if colon < 0 {
				continue
			}
			port := host[colon+1:]
			if _, err := strconv.Atoi(port); err == nil {
				ports[port] = struct{}{}
			}
		}
	}

	services := make([]snapshot.Service, 0, len(portsByImage))
	for image, portSet := range portsByImage {
		// nil rather than an empty slice: the field is omitempty, so a snapshot
		// written and read back would otherwise not equal the one in memory.
		var ports []string
		for port := range portSet {
			ports = append(ports, port)
		}
		sort.Slice(ports, func(i, j int) bool {
			left, _ := strconv.Atoi(ports[i])
			right, _ := strconv.Atoi(ports[j])
			return left < right
		})
		services = append(services, snapshot.Service{Image: image, Ports: ports})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Image < services[j].Image })
	return services, nil
}
