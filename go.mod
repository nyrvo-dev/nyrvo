module github.com/nyrvo-dev/nyrvo

// Go 1.25 is the minimum supported version: it is what contributors and
// downstream packagers must have to build Nyrvo. Development and CI run on
// 1.26 (CI still verifies the 1.25 floor), so the toolchain is deliberately
// not pinned here.
go 1.25

require gopkg.in/yaml.v3 v3.0.1
