# engine

The learning engine behind Cribrum's predictor. Two packages exist today, the
numerical core the rest of the engine is built on (specs/004-engine.md):

| Package | What it holds |
|---|---|
| [`kernel`](kernel/) | The covariance functions over feature points: the additive kernel, whose static term keeps what was learned about an action's risk while the person's tolerance drifts, and the separable product kernel the paper compares against. Standard library only. |
| [`gp`](gp/) | The Laplace Gaussian-process probit classifier: an immutable fitted posterior, the approval probability it predicts, and the log marginal likelihood. |

Both are ports of the research implementation,
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib)
(`go/kernel`, `go/gp`, Apache-2.0), and reproduce its golden fixtures, which
the paper's Python reference wrote ([`testdata/fixtures`](testdata/fixtures/README.md)).
The largest deviation from the reference is about 1e-15 for the kernel
matrices, the fitted mode, its gradient, the log marginal likelihood, and
the predictions; the tests hold it below 1e-12.

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
