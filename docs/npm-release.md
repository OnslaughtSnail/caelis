# npm Publish Guide

This repository publishes `@onslaughtsnail/caelis` from GitHub Actions.

## What CI does

On tag push `v*` (`v0.0.3` etc):

1. Build + upload binaries via GoReleaser.
2. Set npm package version from tag (`v0.0.3` -> `0.0.3`).
3. Publish `npm/` to npm registry with provenance.

Workflow: `.github/workflows/release.yml`

## One-time manual setup

1. npm login locally:
   ```bash
   npm login
   ```
2. Ensure package name is correct in `npm/package.json`.
3. Create or open package on npm (`@onslaughtsnail/caelis`).
4. In npm package settings -> **Trusted publishers** -> Add GitHub repository:
   - Owner: `OnslaughtSnail`
   - Repo: `caelis`
   - Workflow: `release.yml`
5. Confirm repository Actions are enabled in GitHub.

## Release operation

1. Merge to release branch (`main` or your release branch).
2. Create and push tag:
   ```bash
   git tag v0.0.3
   git push origin v0.0.3
   ```
3. Wait for GitHub Action `release` to finish.
4. Verify:
   ```bash
   npm view @onslaughtsnail/caelis version
   npm i -g @onslaughtsnail/caelis
   caelis -version
   ```

## Troubleshooting

- `npm publish` auth error:
  - Check npm Trusted Publisher binding (`owner/repo/workflow`) exactly matches.
- `postinstall` download error for users:
  - Confirm GitHub release has expected asset names:
    - `caelis_<version>_<os>_<arch>.tar.gz`
  - Supported OS for npm binary install: `darwin`, `linux` (x64/arm64)
- Need to skip download during local debugging:
  ```bash
  CAELIS_NPM_SKIP_DOWNLOAD=1 npm i
  ```
