# Contributing

Bug reports, documentation improvements, and patches are welcome. Please follow
our [code of conduct](code-of-conduct.md). Report suspected vulnerabilities
privately to a contact in [SECURITY_CONTACTS](SECURITY_CONTACTS).

## Workflow

1. Check existing issues before opening a bug report or feature request. Describe
   the expected behavior, actual behavior, and steps to reproduce.
2. Fork the repository and create a focused branch from `main`.
3. Make the change and add regression tests for changed behavior.
4. Open a pull request against `main`, linking the issue and explaining the change
   and validation. Include compatibility or migration effects where applicable.
5. Address review feedback. The reviewers and approvers listed in [OWNERS](OWNERS)
   review changes; maintainers apply priority and area labels during triage.

Contributions are licensed under the repository's [Apache 2.0 license](LICENSE).
No separate CLA process is currently configured in this repository.

## Development and validation

Use the Go version in `go.mod`. Before submitting Go changes, run:

```sh
make lint-fix
make test
```

After changing API types or Kubebuilder markers, regenerate artifacts with
`make manifests generate`. Include the resulting changes in the PR. Do not edit
CRDs, RBAC generated from markers, deepcopy files, or `PROJECT` by hand. Use
`kubebuilder create api` and `kubebuilder create webhook` for scaffolding and
preserve the scaffold markers.

Follow the existing package layout, Kubernetes API conventions, and structured
logging style. Reconciliation must be idempotent. Use finalizers for external
resource cleanup and owner references for Kubernetes-owned objects. Avoid logging
credentials or bootstrap data.

Run `make test-e2e` only against a dedicated disposable Kind cluster. It builds
and deploys the controller and must never target a development or production
cluster. Changes requiring libvirt must also describe which host configuration
and VM lifecycle paths were exercised.

The repository uses beads (`bd`) for local task tracking; run `bd ready` and
`bd prime` to begin. See [AGENTS.md](AGENTS.md) for detailed project conventions.
