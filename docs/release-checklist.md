# Release checklist

Run all of this before every release. Record the date, the commit and the result
next to each step; a release ships only when every box is ticked or the gap is
written down here.

## Automated

- [x] CI is green on the release commit: `test`, `integration` (netns),
      `build` and every `distros` job.
- [x] Locally: `go vet ./... && go test ./...` and
      `sudo -E SPLITDPI_BIN=/tmp/splitdpi go test -tags integration ./test/integration/...`
      after `go build -o /tmp/splitdpi ./cmd/splitdpi`. (v0.1.0, 2026-10-29)
- [x] `splitdpi presets check internal/presets/data` exits 0. (v0.1.0, 2026-10-29)

## On real systems

These need real hardware or VMs and a real ISP; CI cannot do them.

- [x] On at least one real ISP per seeded country (TR now): `sites add` for the
      user's blocked sites → `scan` → `probe` exits 0 → `revert` prints clean.
      (v0.1.0, 2026-10-29, Türk Telekom AS9121 on CachyOS: preset scan chose
      `tt-multidisorder`; `scan --site discord.com` re-tested 15 blockcheck2
      results and saved `multidisorder:pos=2:seqovl=1`; both opened Discord,
      Roblox, Pastebin and Pornhub. Raw blockcheck2 output is
      `internal/scan/testdata/bc-real-tr.log`.)
- [x] Service mode in VMs: `enable` → reboot → `status` shows running in service
      mode → `revert` prints clean, and the service file and `inet splitdpi`
      table are gone. (v0.1.0, 2026-10-29)
  - Debian 13.7 (systemd)
  - Fedora Cloud 44 (systemd, SELinux enforcing; `doctor` shows 0 denials in
    `/var/log/audit/audit.log`)
  - Alpine 3.24.3 (OpenRC)
- [ ] Fedora Silverblue or Bazzite: `install.sh`, `enable`, reboot, `revert`.
      Not tested yet.
- [ ] Void (runit): `enable`, reboot, `revert`. Not tested yet.

## Known gaps

- **Discord voice** uses a substitute fake blob and has not been tested on a
  real call. In Türkiye voice currently works without UDP desync, so the UDP
  profile is not needed there today.
- **Presets import**: once the presets repo has a release, run
  `scripts/import-presets.sh <tag>` and confirm it replaces
  `internal/presets/data` and `go test ./internal/presets` still passes.

## Before the first public release

- [x] Turkish copy reviewed (`internal/i18n/locales/tr.toml`, `README.tr.md`).
- [x] The SplitDPI repository made public; README status line updated.
      (2026-10-29, published as a fresh single-commit history.)
- [ ] The `SplitDPI-presets` repository created and seeded with
      `scripts/export-presets.sh <checkout>`, then its first release tagged.
      Its workflows download the latest SplitDPI release, so this comes after
      the first non-prerelease SplitDPI release is public.
