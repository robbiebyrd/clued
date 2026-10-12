# Historical design documents

`specs/` holds the design documents (brainstorm output) behind each feature,
from the original Node plugin (September 2026) to the mind-palace plan
storage (October 2026). They describe the intent at the time; the current
behaviour is documented in the top-level `README.md` and
`mind-palace/README.md`.

`plans/` holds the implementation plans of the retired TypeScript
implementation. They are kept for history only: the code they describe was
ported to Go (see `docs/plans/0003-7lv` and `0004-b1m`), and the
`guard-superpowers-plans` hook blocks new writes here. Current plans live in
`docs/plans/` and are managed by `mind-palace`; current stories in
`docs/stories/`.
