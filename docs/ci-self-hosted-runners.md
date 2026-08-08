# Self-hosted CI runners

Multica CI uses self-hosted GitHub Actions runners to avoid paid GitHub-hosted runner minutes.

Required labels:

- General Linux: `self-hosted`, `linux`, `x64`
- Docker Linux amd64: `self-hosted`, `linux`, `x64`, `docker`
- Docker Linux arm64: `self-hosted`, `linux`, `arm64`, `docker`
- macOS installer tests: `self-hosted`, `macos`, `arm64`, `xcode`
- Windows installer/runtime tests: `self-hosted`, `windows`, `x64`

Operational notes:

- Linux runners should have Docker, Go, Node 22, pnpm, Helm, and buildx available.
- Docker release jobs use local buildx cache under `/var/cache/github-actions/buildx` instead of GitHub Actions cache storage.
- Windows and macOS jobs will queue until matching self-hosted runners exist; do not switch them back to GitHub-hosted labels.
- Release/publish jobs remain human-gated by tags and repository conditions.
