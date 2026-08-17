// Package docker observes the container tooling available on this machine.
//
// "Docker is not installed" and "Docker is installed but the daemon is not
// answering" are different facts, and the second is a common reason a
// compose-backed test suite passes in CI and fails on a laptop. The snapshot
// models that difference: an absent section means no Docker, a present section
// with DaemonRunning false means a CLI with no daemon. Collapsing the two
// would throw away the more useful of the two answers.
package docker

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Docker fills the snapshot's Docker section.
type Docker struct {
	// run executes an external tool and returns its trimmed stdout. It
	// defaults to collector.Run, which spawns the binary directly with an
	// argument vector and a timeout, never through a shell. Tests override it
	// with a fake because the daemon state of the machine running them is
	// unknowable: a test whose result depends on whether the developer's
	// daemon happens to be running is worse than no test.
	run runFunc
}

// runFunc is the command executor Docker invokes. collector.Run satisfies it;
// tests substitute a fake.
type runFunc func(ctx context.Context, name string, args ...string) (string, error)

// Compile-time check that Docker satisfies the collector contract.
var _ collector.Collector = (*Docker)(nil)

// Name identifies this collector in progress output and errors.
func (d *Docker) Name() string { return "docker" }

// Collect fills snap.Docker with the client, server, and compose versions.
//
// Only one condition fails the capture: docker not being installed at all, in
// which case the section is left absent and the error wraps ErrUnavailable.
// Everything else degrades to an empty field instead of a failure, so a
// machine with a working CLI but a down daemon — the very state this collector
// exists to distinguish — records itself rather than erroring out.
func (d *Docker) Collect(ctx context.Context, snap *snapshot.Snapshot) error {
	run := d.run
	if run == nil {
		run = collector.Run
	}

	clientVersion, clientTimedOut, err := collectClientVersion(ctx, run)
	// The outer context is asked first: a cancelled capture must abort rather
	// than fall through and record a Docker section whose client version was
	// never actually read.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if clientTimedOut {
		snap.MarkUnmeasured("docker", "client_version")
	}

	// The section is attached to the snapshot only once collection succeeds: a
	// collector that returns an error must not leave a half-filled section
	// behind, which would read as an observation Nyrvo never completed.
	docker := &snapshot.Docker{ClientVersion: clientVersion}

	// ServerVersion is only knowable when the daemon answers, and the server
	// probe failing is the expected, interesting case — CLI present, daemon
	// down — so it degrades to an empty field rather than an error. Only the
	// outer context proves the caller cancelled; an inner probe deadline means
	// the tool was too slow to answer and should degrade like any other failure.
	serverVersion, serr := run(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	switch {
	case serr == nil:
		docker.ServerVersion = normalizeVersion(serverVersion)
	case ctx.Err() != nil:
		return ctx.Err()
	case collector.IsTimeout(serr):
		// A probe that ran out of time proves nothing about the daemon. Both
		// fields below would otherwise state that it is down, and DaemonRunning
		// is the more damaging of the two: it is a bool, so there is no value
		// that means "not known" and the false is indistinguishable from an
		// observation.
		snap.MarkUnmeasured("docker", "server_version")
		snap.MarkUnmeasured("docker", "daemon_running")
	}
	docker.DaemonRunning = docker.ServerVersion != ""

	composeVersion, composeTimedOut, cerr := collectComposeVersion(ctx, run)
	switch {
	case cerr == nil:
		docker.ComposeVersion = composeVersion
	case ctx.Err() != nil:
		return ctx.Err()
	}
	if composeTimedOut {
		// The case that produced a published untruth: windows-latest carries
		// compose, a cold probe ran out of time, and the daily feed recorded the
		// runner as having none.
		snap.MarkUnmeasured("docker", "compose_version")
	}

	// Running containers are only askable of a daemon that answers. Listing them
	// is not attempted otherwise, so a machine with the CLI and no daemon
	// reports no services because none could be observed — never because the
	// question was answered "none".
	if docker.DaemonRunning {
		services, serr := Services(ctx, run)
		if serr == nil {
			snap.Services = append(snap.Services, services...)
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	snap.Docker = docker
	return nil
}

// collectClientVersion discovers the CLI's own version. It prefers the
// machine-readable --format probe; when that fails — a down daemon still makes
// `docker version` exit non-zero, or output cannot be parsed — it falls back to
// `docker --version`, which needs no daemon. Only a missing docker binary
// (ErrUnavailable) is a hard failure.
// timedOut is true when the fallback also ran out of time, which is the one
// reason an empty version here says nothing about the client.
func collectClientVersion(ctx context.Context, run runFunc) (version string, timedOut bool, err error) {
	out, err := run(ctx, "docker", "version", "--format", "{{.Client.Version}}")
	if err == nil {
		if v := normalizeVersion(out); v != "" {
			return v, false, nil
		}
	}
	if errors.Is(err, collector.ErrUnavailable) {
		return "", false, fmt.Errorf("docker: %w", err)
	}

	// A probe deadline falls through to the fallback rather than giving up. The
	// first probe contacts the daemon and the fallback does not, so a daemon too
	// slow to answer is exactly the situation where the cheaper question is
	// worth asking.
	legacy, lerr := run(ctx, "docker", "--version")
	if lerr == nil {
		return normalizeVersion(legacy), false, nil
	}
	if errors.Is(lerr, collector.ErrUnavailable) {
		return "", false, fmt.Errorf("docker: %w", lerr)
	}
	// Both probes answered without a usable version and without saying the
	// binary is missing; degrade to an empty client version rather than fail.
	// Only the fallback's verdict is reported: it is the probe that does not
	// need the daemon, so its running out of time is the one that leaves the
	// client genuinely unknown rather than merely unreachable.
	return "", collector.IsTimeout(lerr), nil
}

// collectComposeVersion discovers the compose plugin, or the legacy
// docker-compose binary when the plugin is not installed. Compose is optional —
// a machine can have docker without any compose — so an absent or failing
// compose never fails the capture.
// timedOut is true when a probe ran out of time, which leaves compose unknown
// rather than absent.
func collectComposeVersion(ctx context.Context, run runFunc) (version string, timedOut bool, err error) {
	out, err := run(ctx, "docker", "compose", "version", "--short")
	if err == nil {
		return normalizeVersion(out), false, nil
	}
	// docker compose is the plugin form; docker-compose is the legacy
	// standalone binary. Try the other before concluding compose is absent,
	// including when the first probe merely ran out of time.
	legacy, lerr := run(ctx, "docker-compose", "--version")
	if lerr == nil {
		return normalizeVersion(legacy), false, nil
	}
	// Either probe running out of time is enough. The legacy binary being
	// absent does not settle whether the plugin exists, so a plugin probe that
	// never answered leaves the question open however loudly the fallback
	// reports "not found".
	return "", collector.IsTimeout(err) || collector.IsTimeout(lerr), nil
}

// versionRE matches a dotted-digit version such as "29.4.0" or "5.1.2". Two
// components are required so a lone digit inside a build hash ("9d7ad9f") is
// never mistaken for a version.
var versionRE = regexp.MustCompile(`\d+(\.\d+)+`)

// normalizeVersion reduces any version-shaped string to bare dotted digits:
// "v5.1.2", "Docker version 29.4.0, build 9d7ad9f", and "Docker Compose version
// v5.1.2" all become "29.4.0"-style values. Input without a version shape
// yields an empty string rather than a wrong one.
func normalizeVersion(s string) string {
	return versionRE.FindString(s)
}
