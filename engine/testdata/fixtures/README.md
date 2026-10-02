# Golden fixtures

These files hold the numbers the engine's kernel and Gaussian-process code
must reproduce. They are copied unchanged from the research implementation,
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib)
(`go/testdata/fixtures`, last changed in commit `7819dcd`, release tag
`v3`), where `go/testdata/gen/export_fixtures.py` writes them from the
paper's Python reference (`experiment/kernel.py`, `experiment/gp.py`):

```
PYTHONPATH=. uv run python go/testdata/gen/export_fixtures.py
```

The generator draws feature matrices from a seeded NumPy generator and
writes the arrays verbatim, so a test re-runs only the deterministic kernel
and Gaussian-process arithmetic and never NumPy's random numbers.

| File | What it pins | Kernel |
|---|---|---|
| `kernel_full.json` | the kernel matrix over 24 points | product, sigma2 1.6, lengthscales 1.1 and 1.2, lambda 200 |
| `kernel_additive.json` | the kernel matrix over the same points | additive, scales 1.6, 1.0, 0.6, lengthscales 1.1 and 1.2, lambda 90 |
| `laplace_fit.json`, `laplace_fit_additive.json` | the Laplace mode, its gradient, and the log marginal likelihood for 0/1 answers | product, additive |
| `predict.json`, `predict_additive.json` | the latent mean, variance, and approval probability at query points | product, additive |
| `logcdf.json` | the log normal distribution function and density from z = -40 to 8, against scipy's `log_ndtr` | none |

The product-kernel fixtures use lambda 200, the reference implementation's
dataclass default when they were exported; the tests read every
hyperparameter from the fixture, never from the engine's defaults.

SHA-256 of the files as copied:

```
457e4c3ffc5532febfd7d06faff654b19414d8ec5ba46b55401f14fbf4aabc83  kernel_full.json
b7ec69e07b0c0680380cc1f739fb9ac92d30bca2f62e600562281b5694393ff4  kernel_additive.json
613f805c41922e84985a74ab6d0724f1e66c795dea7789c2d79878e6db38c4c4  laplace_fit.json
6c291c7410230b9ddf909fcb51cb4ce4558323fb75265bdcc7b117d8d08f16c7  laplace_fit_additive.json
49f7bd3056a24df17ddc35c791bdfb66447b6b7b3bd762afca99ef1c5b7eb7c3  predict.json
3482b3506bf5bcf8a42ae2cdae04758df713f1f91361db906e889774fe1757d3  predict_additive.json
b5af27a230966455511be0bcf50994b0f8c2eb57f04761a055ff967108df9352  logcdf.json
```

A change here is a change to the reference: regenerate in the research
repository, copy the files, and update the hashes in the same commit.
