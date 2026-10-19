# Contributing to AXV

The [cofy-x contribution guidelines](https://github.com/cofy-x/.github/blob/main/CONTRIBUTING.md) and [Code of Conduct](CODE_OF_CONDUCT.md) apply. This page adds AXV's project-specific workflow; use [Support](SUPPORT.md) for questions and [Security](SECURITY.md) for private vulnerability reports.

## Choose a change

Read the [project status](README.md#project-status) and [product direction](docs/product.md) before proposing a feature. Search existing [Issues](https://github.com/cofy-x/axv/issues) and [pull requests](https://github.com/cofy-x/axv/pulls). Discuss substantial changes to product boundaries, storage, authentication or agent protocols in an issue before implementation so the intended behavior can be reviewed.

Follow [AGENTS.md](AGENTS.md), then use the [documentation map](docs/README.md#read-by-task) to find the affected owner. These instructions apply to human and agent-assisted contributions. Follow the organization's branch and Conventional Commit guidance.

## Develop and verify

Use the [pinned toolchain and development setup](README.md#start-developing). The [development guide](docs/development.md) owns build commands, the required [verification matrix](docs/development.md#verification), and [test isolation](docs/development.md#test-isolation-and-diagnostics).

Run the checks for the change's category and summarize the commands and results in the pull request. Report unavailable checks and failures explicitly. Add or update regression coverage when behavior needs it; a small reversible change does not need a new test solely to repeat its implementation. Update facts in their [owning document](docs/README.md#content-ownership) and link to the owner elsewhere.

## Open a pull request

Explain the problem, resulting behavior and relevant limitations. Link related issues and include validation evidence; UI changes should include the applicable visual and interaction review. Use sanitized examples and [redact sensitive information](SUPPORT.md#sharing-diagnostics).

Keep generated executables, dependencies, databases, profiles, uploads, screenshots and browser reports out of commits. The [storage baseline policy](docs/storage.md#desktop-database-baseline) governs unreleased data changes; document effects on existing AXV-owned data without changing host projects or provider-owned history.

Contributions are licensed under AXV's [MIT License](LICENSE). Keep required upstream notices for any third-party material; [release preparation](docs/releasing.md) covers distribution notices.
