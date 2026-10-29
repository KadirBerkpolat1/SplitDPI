# SplitDPI

SplitDPI gets Discord, YouTube, Roblox and other blocked apps working again on
Linux when your ISP blocks them with deep packet inspection (DPI). It finds a
[zapret2](https://github.com/bol-van/zapret2) strategy that works on your ISP,
runs it on demand or as a service, and removes every trace with one command.

**Status:** v0.1.0, the first public release. Tested on a real Türk Telekom
connection and in Debian, Fedora and Alpine VMs; see
[the release checklist](docs/release-checklist.md).

[Türkçe](README.tr.md)

If you use keift/zapret today, SplitDPI replaces it. It runs zapret2 instead of
the end-of-life zapret v1, tries ISP presets before a full blockcheck2 scan,
leaves your DNS and firewall configuration alone, and works on immutable
distros and with SELinux enforcing.

## Supported systems

| Distro | Init | Service mode | On demand | Tested |
|---|---|---|---|---|
| Debian | systemd | yes | yes | 13.7 in a VM: service mode across reboot, revert (2026-10-29) |
| Ubuntu LTS, Linux Mint | systemd | yes | yes | not tested yet |
| Fedora (Workstation, Cloud) | systemd | yes | yes | 44 Cloud in a VM, SELinux enforcing: service mode across reboot, revert (2026-10-29) |
| Fedora Atomic (Silverblue, Bazzite) | systemd | yes | yes | not tested yet |
| CachyOS | systemd | yes | yes | Türk Telekom connection: `scan` (presets and `--site`), `probe` on Discord, Roblox, Pastebin and more, `revert` (2026-10-29) |
| Arch, EndeavourOS | systemd | yes | yes | not tested yet |
| openSUSE Tumbleweed | systemd | yes | yes | not tested yet |
| Alpine | OpenRC | yes | yes | 3.24.3 in a VM: service mode across reboot, revert (2026-10-29) |
| Gentoo | OpenRC | yes | yes | not tested yet |
| Void (glibc and musl) | runit | yes | yes | not tested yet |
| Artix, antiX, dinit, s6 or sysvinit distros | other | no: run on demand | yes | not tested yet |

SplitDPI needs an x86_64 or arm64 kernel 5.4 or newer with nftables, plus the
`nft` and `curl` commands. `splitdpi doctor` checks all of this and prints the
install command for your package manager. NixOS users should look at
zapret2-flake or zapret2-nix instead.

## Quick start

```sh
curl -fsSLO https://raw.githubusercontent.com/KadirBerkpolat1/SplitDPI/main/install.sh
sudo sh install.sh        # verifies and installs /usr/local/bin/splitdpi
sudo splitdpi             # opens the setup wizard
```

The wizard checks your system, finds your ISP, asks which apps should work,
tests strategies, and then runs SplitDPI now or at every boot. Every screen
shows the command that does the same thing, so you can script it.

## Commands

| Command | What it does |
|---|---|
| `splitdpi` | Opens the terminal UI. |
| `splitdpi doctor` | Checks the system and lists anything that blocks SplitDPI. |
| `splitdpi engine install [--from FILE]` | Installs the pinned zapret2 release after checking its SHA-256. |
| `splitdpi detect` | Looks up your ISP and lists its preset strategies. |
| `splitdpi scan [--site DOMAIN]` | Tries the presets, then blockcheck2, and saves the strategy that works; `--site` runs only blockcheck2, on that one site. |
| `splitdpi probe` | Tests whether each enabled app's sites and your own sites are reachable. |
| `splitdpi run` | Runs in this terminal; Ctrl-C stops it and removes its rules. |
| `splitdpi enable` / `disable` | Installs and starts the service, or stops and removes it. |
| `splitdpi stop` / `status` / `logs [-f]` | Stops it, shows its state, or shows its log. |
| `splitdpi apps list\|enable ID\|disable ID` | Chooses which apps get the bypass. |
| `splitdpi sites list\|add DOMAIN\|remove DOMAIN` | Manages your own blocked sites, which the bypass always handles. |
| `splitdpi strategy list\|set ID` | Lists the preset strategies or picks one. |
| `splitdpi exclude list\|add DOMAIN\|remove DOMAIN` | Manages sites the bypass never touches. |
| `splitdpi presets list\|update` | Shows or refreshes the preset data. |
| `splitdpi dns on\|off` | Sends DNS through a built-in DNS-over-HTTPS forwarder. |
| `splitdpi revert [--purge]` | Removes everything SplitDPI added. |
| `splitdpi uninstall [--yes]` | Removes SplitDPI itself, its files and its program. |

All commands except `--help`, `version` and `presets list` need root. Add
`--lang tr` or `--lang en` to pick a language; otherwise SplitDPI
follows `LANG`.

## Your own sites and strategy

If a site you need is blocked and no app profile covers it, add it. SplitDPI
then handles it from the first packet, and `probe` and `scan` test it too:

```sh
sudo splitdpi sites add pastebin.com
```

`splitdpi strategy list` shows every preset strategy and marks the one in use;
`sudo splitdpi strategy set ID` picks another without a scan. The terminal UI
does both: the Apps screen lists your sites, and `t` opens the strategy list.
Restart SplitDPI to apply either change.

To skip the presets and search for a strategy for one site, run
`sudo splitdpi scan --site pastebin.com` or choose "Scan a site with
blockcheck2" in the setup wizard. blockcheck2 tests that site over IPv4, and
over IPv6 too if this machine has it. blockcheck2 results are re-tested on your
apps and sites before one is saved: SplitDPI keeps the simplest one that opens
all of them, preferring those that work on both IPv4 and IPv6, and adds the
site to your sites. This takes longer than the preset trials.

## A site broke

Banks, government sites and some shops refuse modified traffic. Exclude the
site and restart SplitDPI:

```sh
sudo splitdpi exclude add https://www.example-bank.com/
```

SplitDPI ships exclusion lists for common Turkish sites already.

## How it works, and what to expect

SplitDPI keeps its firewall rules in its own nftables table, `inet splitdpi`.
It never flushes your ruleset and never edits firewall, DNS or network
configuration files. Its rules fail open: if nfqws2 stops, traffic passes
unmodified.

- **Learning new sites.** Apps you enable are handled from the first packet.
  Other sites are learned: the first visits (three by default) to a newly
  blocked site fail, and later visits work. If a site fails, retry it.
- **Telegram.** Only Telegram's web and API domains are covered. The apps'
  MTProto connections to raw IP addresses are not.
- **Discord voice.** The voice preset is applied but not verified on a real
  call yet. `scan` says so; report whether it works for you.
- **DNS.** Some ISPs poison DNS answers. `doctor` and `probe` detect it.
  `splitdpi dns on` sends port 53 through a DNS-over-HTTPS forwarder inside
  SplitDPI; it changes no resolver settings and disappears with the table.
  While it is on, VPN split DNS and local hostnames may stop resolving.

## Revert guarantee

`sudo splitdpi revert` stops SplitDPI, removes its service, deletes its table
and its runtime files, and then checks that nothing is left. It prints
`clean`, or lists what remains. `--purge` also removes its settings and data.
Revert never reads the configuration, so a broken config cannot block it. If
SplitDPI is killed, its queue rules stay in place but let all traffic through
until you run `revert` or start it again.

## Removing SplitDPI

`sudo splitdpi uninstall` lists what it would remove and changes nothing.
`sudo splitdpi uninstall --yes` does what `revert --purge` does, then deletes
the log and `/usr/local/bin/splitdpi`, and lists anything it could not remove.
## Where presets come from

The ISP strategies started from
[SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey) by Çağrı
Taşkın (MIT). Other MIT-licensed collections, such as
[erdometo/zapret-turkey-configs](https://github.com/erdometo/zapret-turkey-configs),
are used to cross-check them. Each ISP file records in its `reported` field
when its strategies were last seen working on a real connection. When `scan`
saves a working strategy, it prints a link that opens a prefilled issue in
[SplitDPI-presets](https://github.com/KadirBerkpolat1/SplitDPI-presets) with
your AS number, ISP, country, the strategy and the versions. Nothing is sent
until you submit the issue, and it never contains your IP address or your own
sites.

## Reporting a strategy

Open an issue with the "Strategy report" template. Include the output of
`splitdpi doctor`, your ISP's AS number, the preset or strategy, and
`splitdpi probe`.

## Disclaimer

SplitDPI changes how your computer sends some network traffic. Whether using
such a tool is allowed depends on where you are; check the rules that apply to
you. You use SplitDPI at your own responsibility, and it comes without
warranty.

## Credits

- [zapret2](https://github.com/bol-van/zapret2) by bol-van (MIT): the engine.
  SplitDPI downloads it; it does not redistribute it.
- [SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey) by Çağrı
  Taşkın (MIT): ISP strategies, the exclusion list and Roblox hosts.
- [Flowseal/zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube)
  (MIT): Discord and YouTube hosts and the Discord voice settings.
- [dmfrpro/zapret2-flake](https://github.com/dmfrpro/zapret2-flake) (MIT): the
  extra Discord voice ports.
- alimali54/zapret-win-turkey: the origin of the Turkish zapret2 strategies.

See [NOTICE](NOTICE) for licenses. SplitDPI is MIT-licensed.
