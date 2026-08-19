- Released binaries report their version instead of `v0.5.0+dirty`. Go stamps a
  binary as dirty when `git status` is not clean, and the release workflow built
  into an untracked `dist/` inside the repository, which was enough to trigger
  it — so the v0.5.0 archives each claimed to be a modified build of the tag
  they were cut from. The build now happens outside the working tree. The
  release step is also re-runnable now: `gh release create` refuses to run
  twice, which defeated the `workflow_dispatch` retry that exists for exactly
  this situation, so an existing release is updated in place instead.
