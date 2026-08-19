- Every release now carries prebuilt binaries. Pushing a `v*` tag runs a
  release workflow that cross-compiles Nyrvo for macOS, Linux and Windows
  (amd64 and arm64), packages each binary with the LICENSE and the README, and
  attaches the archives and a SHA256SUMS to a GitHub Release. People without a
  Go toolchain — the JavaScript, Python and PHP developers most likely to hit
  environment drift — can download a binary instead of being bounced by the
  first `go install` in the README.
