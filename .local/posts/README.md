# Posts

Drafts, kept next to the code they quote so they cannot drift from it silently.

`.local/` is gitignored, so these stay out of the repository and off GitHub.
Moving them elsewhere is fine as long as it is two directories below the repo
root, or the relative links into `examples/book` stop resolving.

Every command output and code sample in these is copied from a program in
[`examples/book`](../../examples/book), and those run in CI. If a post makes a
claim the library stops honouring, the example that produced the claim goes red.

| Post                                                                            | Chapter | Example              |
| ------------------------------------------------------------------------------- | ------- | -------------------- |
| [Sixty-three lines before you record anything](01-sixty-three-lines-before-you-record-anything.md) | 7       | `ch07-setup`         |
| [Writing chapter 15 as code found a bug](02-the-rung-we-could-not-climb.md)      | 15      | `ch15-sampling`      |
| [Alert on the budget, not the threshold](03-alert-on-the-budget-not-the-threshold.md) | 11, 12  | `ch12-burn-alerts`   |

Post 2 is the one to lead with. It is the only one that is not really about this
library: it is about a library claiming something its tests never checked, which
is the book's own argument turned on the tools.

The book's code is at <https://resources.oreilly.com/examples/0636920722618>.

Read [EVIDENCE.md](EVIDENCE.md) before publishing any of these. It sets out which
claim the examples actually carry, which one is stronger but unwritten, and which
one nothing here supports yet.
