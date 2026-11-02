# engine

The learning engine behind Limen's predictor: the in-process
`limen.Service` that predicts, keeps the ledger, fits each person's model
off the hot path, explains its predictions, and holds an allow to the
cover rule (specs/004-engine.md, specs/009-explanations.md,
specs/011-consent.md). The engine package is the service; its
subpackages are the parts it is built from:

| Package | What it holds |
|---|---|
| [`kernel`](kernel/) | The covariance functions over feature points: the additive kernel, whose static term keeps what was learned about an action's risk while the person's tolerance drifts, and the separable product kernel the paper compares against. Standard library only. |
| [`gp`](gp/) | The Laplace Gaussian-process probit classifier: an immutable fitted posterior, the approval probability it predicts, the log marginal likelihood, and the split of the mean into each answer's term and the kernel's parts. |
| [`featurize`](featurize/) | The open-set featurizer `features/1`: an action's tool and context feature vectors, the shell reader, and the label of each action from a fixed vocabulary. Its reading for consent: the scope of an action from the shell table, the holds and the claims, its families, and its `labels/2` label. |
| [`store`](store/) | The store interface, the memory store, the directory store [`store/file`](store/file/), and the contract tests every store passes ([`store/storetest`](store/storetest/)). |

`kernel` and `gp` are ports of the research implementation,
[github.com/changkun/trustcalib at tag `v3`](https://github.com/changkun/trustcalib/tree/v3/go)
([`go/kernel`](https://github.com/changkun/trustcalib/tree/v3/go/kernel),
[`go/gp`](https://github.com/changkun/trustcalib/tree/v3/go/gp),
Apache-2.0; the Go code is no longer on that repository's main branch),
and reproduce its golden fixtures, which the paper's Python reference
wrote ([`testdata/fixtures`](testdata/fixtures/README.md)). The largest
deviation from the reference is about 1e-15 for the kernel matrices, the
fitted mode, its gradient, the log marginal likelihood, and the
predictions; the tests hold it below 1e-12.

## Cost

Measured with `go test -bench . -benchmem -run xxx ./engine/gp` on an Apple
M5 Pro, with the additive kernel, 11 tool features, 9 context features, and
512 answers, the default cap:

| Operation | Time | Allocated |
|---|---|---|
| `Posterior.Predict`, one action | 96 µs | 4 KiB in 1 allocation |
| `Fit`, 512 answers | 74 ms | 28 MB in 1,609 allocations |

A prediction is a kernel evaluation and a multiply-add per answer for the
mean and a triangular solve for the variance, well inside the hot path's
budget. A fit is a Cholesky factorization per Newton step and runs off the
hot path: predictions read the current posterior while the next one is
fitted, and the caller swaps a pointer to publish it.
