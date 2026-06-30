---
name: release
description: Cut a Skrá release — bump const Version in main.go, commit, and tag vX.Y.Z for minor releases (tags trigger the Docker build in CI).
disable-model-invocation: true
---

Release convention: patch bumps are committed untagged; minor bumps get an annotated tag `vX.Y.Z`, and pushing the tag makes CI build and publish the Docker image.

## Steps

1. **Preflight.** Working tree must be clean and on `main`. Run the full gate locally; abort on any failure:
   ```sh
   gofmt -l . && go vet ./... && go test ./... && CGO_ENABLED=0 go build -trimpath -o skra .
   ```
2. **Decide the bump.** Read the current `const Version` in `main.go`. If the user gave a version or bump type in the arguments, use it; otherwise summarize the commits since the last release (`git log $(git describe --tags --abbrev=0)..HEAD --oneline`) and ask: patch (fixes, docs, internal) or minor (user-visible features).
3. **Bump.** Edit `const Version` in `main.go` to the new `X.Y.Z` (minor bump resets patch to 0).
4. **Commit.** Message style matches history, e.g. `Release X.Y.Z: <one-line summary>`.
5. **Tag (minor only).** `git tag -a vX.Y.Z -m "Release X.Y.Z"`.
6. **Confirm, then push.** Show the user the commit, the tag (if any), and what pushing will trigger (tag push → Docker image build). Push only after explicit confirmation: `git push` and, for minor releases, `git push origin vX.Y.Z`.

Never push without confirmation, and never tag a patch release.
