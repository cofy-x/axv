# AXV support

Use [GitHub Issues](https://github.com/cofy-x/axv/issues) for bug reports, feature requests and usage questions. Discussions are not enabled for AXV. This selects the issue-based support path in the [cofy-x support guide](https://github.com/cofy-x/.github/blob/main/SUPPORT.md).

Start with [project status](README.md#project-status), [installation and startup](docs/startup.md) and [Using AXV](docs/usage.md). AXV is experimental and has no stable release; the status page distinguishes implemented features, experimental workflows and native platform validation.

## Choose an issue

Search existing issues first, then use the appropriate [issue form](https://github.com/cofy-x/axv/issues/new/choose):

- **Bug Report:** include the commit or version, host OS and architecture, browser and relevant component versions, exact steps, expected result and actual result. For VM issues, identify Lima or Firecracker and whether the image is the default base or a custom bundle.
- **Feature Request:** describe the problem, intended workflow, proposed behavior and alternatives. Check the [product direction](docs/product.md) for existing decisions.
- **Usage Question:** explain what you are trying to do, the instructions you followed and where you became stuck.

Suspected vulnerabilities belong in the private channel described in [SECURITY.md](SECURITY.md). Community conduct concerns follow the [Code of Conduct](CODE_OF_CONDUCT.md), including its private incident-reporting channel.

## Sharing diagnostics

Share a short relevant excerpt instead of an entire profile, database or log directory. `axv version`, `axv daemon status` and `axv daemon logs` can help identify the build and service failure; inspect their output before posting.

Remove API tokens, passwords, cookies, authorization headers, SSH keys, private hostnames and sensitive URLs. Replace usernames and private project paths with consistent placeholders. Remove document contents, terminal commands/output and coding-agent prompts or history that are unrelated to the reproduction. Check screenshots and browser traces for the same information. Do not attach provider authentication files, SQLite databases, complete AXV profiles or private VM images.

Use a fresh development or test profile and synthetic project data when possible. The [development guide](docs/development.md#test-isolation-and-diagnostics) owns isolated verification commands. A support request does not require deleting working data or resetting a profile.
