# Skrá — GitHub Setup Checklist

One-time steps to do **on GitHub** after pushing this repo to a remote `origin`, so CI, image publishing, releases, and Renovate all work. Nothing here changes the code; it's repository configuration.

## 1. Push the repo

```sh
git remote add origin git@github.com:<owner>/skra.git
git push -u origin main
```

CI (`.github/workflows/ci.yml`) runs on every push to `main` and on `v*` tags. The image name is derived from the repo (`ghcr.io/<owner>/skra`), so nothing to configure there.

## 2. Actions permissions (required for image push + releases)

The workflows use the built-in `GITHUB_TOKEN` with job-scoped `permissions:` (`packages: write` for GHCR, `contents: write` for releases). For those to take effect:

- **Settings → Actions → General → Workflow permissions**: leave the default or set **Read and write permissions**. If it is locked to read-only at the org/enterprise level, the image push and release upload will fail with a 403.

No custom repository **secrets** are required — `GITHUB_TOKEN` is provided automatically. (The only secrets Skrá needs are the `SKRA_*` runtime env vars **on the deployment host**, not in CI.)

## 3. Cut the first automated release

The `release` job only runs from the workflow present **at the tagged commit**, and the existing `v1.1.0` tag predates it. So for the first auto-release, tag a commit that includes the workflow:

```sh
git tag -a v1.1.1 -m "Skrá 1.1.1" && git push origin v1.1.1
# (or move v1.1.0: git tag -d v1.1.0 && git tag -a v1.1.0 -m "…" && git push -f origin v1.1.0)
```

Pushing the tag builds + pushes the image and creates a published GitHub Release with the cross-compiled binaries, `SHA256SUMS`, and a link to the image. See [Releases](01_skra-development-principles.md#releases).

## 4. Make the container image public (optional)

The first push creates the GHCR package as **private**. For anonymous `docker pull`:

- **Repo → Packages → skra → Package settings → Change visibility → Public**, and confirm it's linked to this repository.

Leave it private if pulls should require `docker login ghcr.io`.

## 5. Enable Renovate

Renovate only runs once enabled — `renovate.json` alone does nothing:

- Install the **Renovate GitHub App** (github.com/apps/renovate) on the repo, **or** add a self-hosted `renovatebot/github-action` workflow. Merge the onboarding PR it opens.
- It then keeps Go modules, GitHub Actions SHAs, the Dockerfile builder image, and the vendored asset versions up to date (see the [Dependency & asset updates](../README.md#dependency--asset-updates-renovate) section of the README).

## 6. Recommended hardening (optional)

- **Settings → Code security**: enable **Dependabot alerts** (vulnerability alerts complement Renovate's update PRs) and **secret scanning**.
- **Branch protection / ruleset** on `main`: require the `test` (and `docker`) checks to pass before merge.
- Consider adding **CodeQL** if you want static security analysis on top of `govulncheck`.
