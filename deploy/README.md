# Deploy

This directory contains the Kustomize manifests of the maintainer's own
installation of `limend`. It is not an install path. This repository has
no base and no example overlay. To install `limend` yourself, follow
[`docs/install.md`](../docs/install.md).

| Directory | Contents |
|---|---|
| `prod/` | The maintainer's own installation, not an install path: the Deployment, the Service, the ServiceAccount, the PersistentVolumeClaim of the ledger, the PodDisruptionBudget, two NetworkPolicies, the Ingress, and the ConfigMap that Kustomize makes from `limend.env`. The `deploy` job of `.github/workflows/release.yml` applies it only if the repository variable `LIMEN_RELEASE_DEPLOY` is set. A fork does not set this variable, so a fork deploys nothing. Each manifest in `prod/` is safe to apply again. |
| `bootstrap/` | The resources that the release never applies for that installation: the identity that the `deploy` job uses. An operator applies it one time. Its [README](bootstrap/README.md) gives the steps. |
