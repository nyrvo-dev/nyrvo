- `nyrvo ci replay --json` serializes a job with no steps as `"steps": []`
  instead of `"steps": null`. The steps slice is part of the machine contract,
  and a consumer should not have to treat `null` and `[]` as the same thing:
  "no steps" is a fact, not an unknown.