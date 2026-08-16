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
		ports := make([]string, 0, len(portSet))
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
