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

## Before the first public release

- [x] Turkish copy reviewed (`internal/i18n/locales/tr.toml`, `README.tr.md`).
- [x] The SplitDPI repository made public; README status line updated.
      (2026-10-29, published as a fresh single-commit history.)
- [x] The `SplitDPI-presets` repository created and seeded with
      `scripts/export-presets.sh <checkout>`, then its first release tagged.
      Its workflows download the latest SplitDPI release, so this comes after
      the first non-prerelease SplitDPI release is public.
      (2026-10-29: release `2026-10-29`; `splitdpi presets update` fetched it,
      and `scripts/import-presets.sh 2026-10-29` left `internal/presets/data`
      unchanged.)

## v0.2.0

Tick these on the release commit, with the date.

- [ ] `go vet ./...` passes.
- [ ] `go test -count=1 ./...` passes.
- [ ] `sudo -E SPLITDPI_BIN=/tmp/splitdpi go test -tags integration ./test/integration/...`
      passes after `go build -o /tmp/splitdpi ./cmd/splitdpi`.
- [ ] `splitdpi presets check internal/presets/data` exits 0.
- [ ] CI is green, all 9 jobs: `test`, `integration`, `build` and the six
      `distros` jobs (Debian 13, Fedora, Arch, openSUSE Tumbleweed, Alpine,
      Void glibc).

By Berk's decision, the real-connection and VM retests under
[On real systems](#on-real-systems) were skipped for v0.2.0. The v0.1.0
results there still hold for what they covered; the features new in v0.2.0
(the terminal UI, `sites import`, `takeover`, encrypted DNS providers and
DNS-over-TLS, the DNS check in `scan`, plain-language errors, the share link
and `uninstall`) are covered only by automated tests and CI.
