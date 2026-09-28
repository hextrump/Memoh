# antmemo: the Ant-Browser fork

The workspace image's built-in browser ("antmemo") is a fork of
[Ant-Browser](https://github.com/hextrump/ant-browser) (upstream:
`github.com/felinics/ant-browser`, our fork lives at
`github.com/hextrump/ant-browser`), driving a
[fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium)
core instead of plain Chrome/Chromium. This replaces the workspace's old
Chrome install outright — there is no fallback path, no toggle. If the
`ant-browser-builder` or `fingerprint-chromium-fetcher` Docker stages fail,
the workspace image build fails; it does not silently fall back to another
browser.

## Why

Plain Chrome/Chromium has no fingerprint spoofing, so it's trivially
identifiable as automation traffic by fingerprinting-aware sites.
fingerprint-chromium (an ungoogled-chromium fork, BSD-3-Clause) adds
`--fingerprint`, `--fingerprint-platform`, `--fingerprint-brand`, and
related flags. Ant-Browser adds profile/instance/proxy management and a
Launch API on top of a bare Chromium-family binary, which the workspace uses
to provision and drive that core.

## Vendoring

`third_party/ant-browser` is a git submodule pointing at our fork, pinned to
a specific commit (not tracking `master`). To sync a change from upstream
Ant-Browser or re-pin to a newer fork commit:

```sh
cd third_party/ant-browser
git fetch origin
git checkout <new-commit-or-tag>
cd ../..
git add third_party/ant-browser
git commit -m "chore: bump third_party/ant-browser pin to <ref>"
```

CI checks out submodules recursively; a bare `git clone` of the parent repo
needs `git submodule update --init --recursive` before the workspace image
can build.

## What we added on top of upstream

- `backend/internal/launchcode/provision_api.go`: `POST /api/provision`, an
  idempotent endpoint that upserts a browser core (by `CorePath`) and a
  profile (by `ProfileName`), then optionally launches it and waits for CDP
  to become ready — replacing what would otherwise be three separate calls
  (`/api/cores`, `/api/profiles`, `/api/launch`) that Memoh's bridge would
  have to orchestrate itself. `cmd/bridge/browser.go`'s
  `browserProvisionRequest`/`browserProvisionResponse` mirror this endpoint's
  request/response structs field-for-field; if the fork's structs change,
  update both sides together.

Upstream internals this depends on (`backend/internal/browser/core_dao.go`,
`profile_api*.go`, `proxy_binding.go`) are not a stable public API — re-check
them whenever re-pinning to a newer fork commit.

## How Memoh drives it

1. `docker/Dockerfile.workspace`'s `ant-browser-builder` stage builds the
   `ant-chrome` binary (Wails v2); `fingerprint-chromium-fetcher` downloads
   and unpacks the pinned fingerprint-chromium release. Both land in the
   `workspace` image at build time — nothing is downloaded at container
   startup.
2. `cmd/bridge/browser.go`'s `startWorkspaceBrowser` launches `ant-chrome` on
   the same Xvnc `:99` display the old Chrome used (Ant-Browser has no
   headless mode — see below), waits for its Launch API
   (`127.0.0.1:19876`) to come up, then calls `POST /api/provision`.
3. Memoh always dials CDP at a single fixed address,
   `127.0.0.1:19876` (`internal/agent/tool/browser.go`'s
   `browserCDPAddress`) — Ant-Browser's Launch API has a unified CDP
   reverse-proxy on `/` that forwards to whichever real debug port the
   underlying fingerprint-chromium process picked, so Memoh never needs to
   know that port.
4. Per-bot fingerprint/proxy overrides live in `workspace.browser.*` bot
   metadata (`internal/workspace/browser_preference.go`), merged with the
   global `[browser]` config section (`docs/configuration.md`) into a
   `MEMOH_BROWSER_PROVISION_JSON` env var that `startWorkspaceBrowser` reads.

## Known risks (accepted, not blockers)

- **License**: the upstream Ant-Browser repo ships no LICENSE file. This
  fork's use is covered by Memoh's own AGPLv3 license; this is a conscious,
  accepted risk, not an open item. Revisit if this code path is ever
  audited or open-sourced independently of Memoh.
- **Headed-only**: fingerprint-chromium's headless mode only swaps the user
  agent string — other automation tells remain. This is why `ant-chrome`
  always runs on a real Xvnc display; do not add a `--headless` path here to
  "save memory" without re-evaluating this tradeoff.
- **amd64 only**: fingerprint-chromium 148.0.7778.215 has no Linux arm64
  release. The `fingerprint-chromium-fetcher` and `ant-browser-builder`
  Docker stages both hard-fail (not silently skip) on non-amd64 builds.
- **New dependency surface**: the workspace image now needs GTK3 +
  WebKitGTK runtime libraries (Wails' dependencies) in addition to
  fingerprint-chromium's own libraries, increasing image size and the
  build's failure surface (front-end `npm ci`/`wails build` can fail
  independently of everything else in the image).
