# SplitDPI

SplitDPI gets Discord, YouTube, Roblox and other blocked apps working again on
Linux when your ISP blocks them with deep packet inspection (DPI). It finds a
[zapret2](https://github.com/bol-van/zapret2) strategy that works on your ISP,
runs it on demand or as a service, and removes every trace with one command.

**Status:** v0.2.5. v0.1.0 was tested on a real Türk Telekom connection and in
Debian, Fedora and Alpine VMs. The features added since v0.2.0 are
covered by automated tests and by CI on six distros, but were not retested on a
real connection or in VMs; see [the release checklist](docs/release-checklist.md).

[Türkçe](README.tr.md)

If you use keift/zapret today, SplitDPI replaces it. It runs zapret2 instead of
the end-of-life zapret v1, tries ISP presets before a full blockcheck2 scan,
leaves your DNS and firewall configuration alone, and works on immutable
distros and with SELinux enforcing. `splitdpi takeover` switches an existing
zapret or zapret2 install over to SplitDPI.

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
curl -fsSL https://raw.githubusercontent.com/KadirBerkpolat1/SplitDPI/main/install.sh | sudo sh
sudo splitdpi
```

The install script verifies the download's checksum and installs
`/usr/local/bin/splitdpi`. `sudo splitdpi` opens the terminal UI; press `w` to
start the setup. The setup checks your system, installs the zapret2 engine
itself, finds your ISP, asks which apps should work, tests strategies, and then
runs SplitDPI now or at every boot.

There are three ways to keep the bypass on. In the UI window (`r`, or
`splitdpi run`) it stops when that window closes. In the background for this
session (`b`, or `sudo splitdpi run --background`) it keeps running after the
terminal closes and stops at reboot; quitting the UI while it runs the bypass
offers to move it there. At every boot (`sudo splitdpi enable`) it runs as a
service. Settings → Run mode chooses which of the three `r` on the Status page
uses; it only saves the choice, and the CLI commands stay as they are.
`sudo splitdpi stop` stops any of them.

## The terminal UI

- **Top:** the name and version, and a status pill on the right that shows
  whether the bypass is running, stopped or failed. Clicking the pill starts
  or stops the bypass as the Status page does.
- **Left:** the menu: Status, Sites, Strategy, Scan, Logs and Settings. Use the
  arrow keys or click an entry; Enter or Tab opens the page, Esc goes back to
  the menu.
- **Right:** the chosen page. Settings lists every setting with its value,
  grouped as General (language, run mode), Bypass (scope, excluded sites,
  strategy), DNS, Network (interfaces to leave alone, provider lookup
  service), Maintenance (update presets, install the engine, check the
  system, switch from or back to zapret, uninstall) and Advanced (queue
  number). `/` searches the list; Esc clears the search.
- **Bottom:** the result of the last action; under it the page's actions as
  buttons with their keys (`?` Help and `q` Quit on the right), where a click
  does what the key does; and last the command that does the selected action,
  so anything you do in the UI can be scripted.

`?` lists every key. Before anything that changes the system, a dialog says what
will change and how to undo it; answer `y` or `n` (`e` or `h` in Turkish). On
the Linux console or with a non-UTF-8 locale the UI draws with plain ASCII. It
needs a terminal of at least 80x24.

## Commands

| Command | What it does |
|---|---|
| `splitdpi` | Opens the terminal UI. |
| `splitdpi doctor` | Checks the system and lists anything that blocks SplitDPI. |
| `splitdpi engine install [--from FILE]` / `engine status` | Installs the pinned zapret2 release after checking its SHA-256, or shows the installed one. |
| `splitdpi detect [--asn N] [--country CC]` | Looks up your ISP and lists its preset strategies. |
| `splitdpi scan [--site DOMAIN] [--dns keep\|auto\|PROVIDER] [--apps ID,ID] [--asn N] [--country CC] [--blockcheck-only\|--no-blockcheck] [-v]` | Finds a strategy that works and saves it; see [Finding a strategy](#finding-a-strategy). |
| `splitdpi probe [--apps ID,ID] [-v]` | Tests whether each enabled app's sites and your own sites are reachable, and says why one is not. |
| `splitdpi strategy list\|set ID` | Lists the preset strategies or picks one without a scan. |
| `splitdpi run` | Runs in this terminal; Ctrl-C stops it and removes its rules. |
| `splitdpi run --background` | Runs on after the terminal closes, until reboot; its output goes to `/var/log/splitdpi.log`. |
| `splitdpi enable` / `disable` | Installs and starts the service, or stops and removes it. |
| `splitdpi stop` / `status` / `logs [-f]` | Stops it, shows its state, or shows its log. |
| `splitdpi apps list\|enable ID\|disable ID` | Chooses which apps get the bypass. |
| `splitdpi sites list\|add DOMAIN\|remove DOMAIN\|import FILE` | Manages your own blocked sites, which the bypass always handles. |
| `splitdpi exclude list\|add DOMAIN\|remove DOMAIN` | Manages sites the bypass never touches. |
| `splitdpi takeover [--yes]` | Switches another zapret or zapret2 install over to SplitDPI. |
| `splitdpi dns [off\|auto\|cloudflare\|google\|quad9\|adguard] [--dot]` | Turns encrypted DNS on or off; without an argument, shows the current choice. |
| `splitdpi config [get KEY\|set KEY VALUE\|reset KEY]` | Lists the settings with their defaults, or shows, changes or resets one. |
| `splitdpi presets list\|update` | Shows or refreshes the preset data. |
| `splitdpi revert [--purge]` | Removes everything SplitDPI added. |
| `splitdpi uninstall [--yes]` | Removes SplitDPI itself, its files and its program. |
| `splitdpi version` | Prints the version. |

All commands except `--help`, `version` and `presets list` need root. Add
`--lang tr` or `--lang en` to pick a language; otherwise SplitDPI uses the
language chosen under Settings in the terminal UI, or else follows `LANG`.

`splitdpi config` covers the settings of the Settings page: `lang`,
`run_mode` (`window`, `background`, `boot`), `mode` (`broad` handles every
blocked site it learns plus your sites, `apps` only enabled apps and your
sites), `skip_ifaces` (interfaces never touched, as a comma list such as
`'wg*,tun*'`), `ipinfo_url` (the https service that finds your provider) and
`qnum` (the NFQUEUE number, 0-65535; no other program may use it). A running
SplitDPI picks up `mode`, `skip_ifaces` and `qnum` when it restarts. Encrypted
DNS stays under `splitdpi dns`.

## Your sites

If a site you need is blocked and no app profile covers it, add it. SplitDPI
then handles it from the first packet, and `probe` and `scan` test it too:

```sh
sudo splitdpi sites add pastebin.com
```

To add many at once, put them in a text file, one per line, and run
`sudo splitdpi sites import FILE`. Lines starting with `#` are comments, and
`*.example.com` or `.example.com` mean `example.com`; lines that are not a
domain are skipped and listed. The Sites page of the terminal UI turns apps on
and off and adds or removes sites. Restart SplitDPI to apply a change.

## Switching from zapret or zapret2

`sudo splitdpi takeover` looks for another zapret or zapret2 install, such as
the one keift's installer sets up: its directory under `/opt`, its service and
its running processes. It shows what switching would change, without changing
anything. `sudo splitdpi takeover --yes` then adds the sites from its
user list to your sites, stops its service and turns off its start on boot.
It never deletes or edits that install's files, and `sudo splitdpi revert`
turns its service back on. If such a tool is still running, `run` refuses to
start, because both would handle the same traffic.

## Finding a strategy

`sudo splitdpi scan` first tries your ISP's preset strategies on your apps and
sites, and runs blockcheck2 only if none works. It saves the strategy that
works.

- **One site.** To skip the presets and search for a strategy for one site,
  run `sudo splitdpi scan --site pastebin.com`, or choose "Scan a site with
  blockcheck2" on the Scan page. blockcheck2 tests that site over IPv4, and over
  IPv6 too if this machine has it. Its results are re-tested on your apps and
  sites before one is saved: SplitDPI keeps the simplest one that opens all of
  them, preferring those that work on both IPv4 and IPv6, and adds the site to
  your sites. This takes longer than the preset trials.
- **Poisoned DNS.** Before blockcheck2 runs, `scan` checks whether your ISP's
  DNS answers for its domains are poisoned. If they are, it asks whether to
  turn on encrypted DNS (automatic) and keep it on; the terminal UI offers
  Cloudflare, another provider, or no. When nobody can answer,
  pass `--dns auto` (or a provider) to turn it on, or `--dns keep` to go on
  without it.
- **Other options.** `--apps` tests only the given apps, `--asn` and `--country`
  skip the ISP lookup, `--blockcheck-only` skips the presets, `--no-blockcheck`
  stops after them, and `-v` shows the raw error of every failed test.
- **Why it failed.** `scan` and `probe` say in plain words why a site did not
  open (the connection was cut, it timed out, DNS or the secure connection
  failed, or something else answered) and what to try next. A domain that has
  no address of its own, by both your DNS and encrypted DNS (only its
  subdomains are used, it does not exist, or it points to this computer), is
  shown as not tested and does not count as a failure; `probe` ends with how
  many sites open, are blocked and were not tested. The terminal UI's Status
  page shows the same counts and up to three blocked sites; Sites lists every
  result.
- **Sharing the result.** When `scan` saves a strategy, it prints a link that
  opens a prefilled issue in
  [SplitDPI-presets](https://github.com/KadirBerkpolat1/SplitDPI-presets) with
  your AS number, ISP, country, the strategy and the versions. The terminal UI
  shows the same link, copies it with `c` and prints it again when you quit.
  Nothing is sent
  until you submit the issue, and it never contains your IP address or your own
  sites.

`splitdpi strategy list` shows every preset strategy and marks the one in use;
`sudo splitdpi strategy set ID` picks another without a scan. The Strategy page
of the terminal UI does the same. Restart SplitDPI to apply it.

## A site broke

Banks, government sites and some shops refuse modified traffic. Exclude the
site and restart SplitDPI:

```sh
sudo splitdpi exclude add https://www.example-bank.com/
```

In the terminal UI, press `e` on the Sites page.

SplitDPI ships exclusion lists for common Turkish sites already.

## Encrypted DNS

Some ISPs poison DNS answers; `doctor` and `probe` detect it. With encrypted
DNS on, SplitDPI sends port 53 traffic through a forwarder inside itself to one
of these providers:

| Choice | What it does |
|---|---|
| `auto` | Cloudflare, then Google, then Quad9: the first one that answers. |
| `cloudflare`, `google` | No filtering. |
| `quad9` | Also blocks known malware and phishing domains. |
| `adguard` | Also blocks ads and trackers. |

It uses DNS-over-HTTPS by default; `--dot` picks DNS-over-TLS, which uses port
853 and is blocked by some ISPs. `sudo splitdpi dns off` turns it off. The
choice applies on the next start, and Settings in the terminal UI offers the
same choices.

- **System settings stay as they are.** SplitDPI never changes system DNS
  settings: no resolver configuration file or service is edited, and the
  redirect disappears with the `inet splitdpi` table.
- **Local resolvers decide.** Queries to resolvers on this machine
  (`127.0.0.0/8`, `::1`) are never redirected, so programs keep using the
  systemd-resolved stub (127.0.0.53), dnscrypt-proxy or any other local
  resolver as before, with its own rules. Only DNS leaving the machine on port
  53 is redirected. A resolver that already encrypts DNS sends nothing on port
  53, so SplitDPI's DNS then only affects programs that query an outside server
  directly.
- **Already encrypted.** When dnscrypt-proxy, Stubby, NextDNS, AdGuard Home,
  Unbound or Knot Resolver forwarding over TLS, or systemd-resolved with
  DNS-over-TLS is in use, `doctor` says so, `splitdpi dns` and Settings say
  SplitDPI's DNS is not needed (turning it on is still allowed), and `scan`
  skips the poisoning question unless you pass `--dns`.
- **VPNs and local names.** DNS through VPN interfaces (`skip_ifaces` in
  config.toml) is left alone, so VPN split DNS keeps working. Local names go to
  your network's usual DNS server, never to the provider: single-label names,
  `.lan`, `.local`, `.home.arpa`, `.internal`, `.localdomain`, your search
  domains, and reverse lookups of private addresses.
- **What still changes.** With `quad9` or `adguard`, the domains they block do
  not resolve. Hotel, airport and other captive-portal Wi-Fi may not show its
  login page until you turn DNS off (`sudo splitdpi dns off`) and restart
  SplitDPI.

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

## Revert guarantee

`sudo splitdpi revert` stops SplitDPI, removes its service, deletes its table
and its runtime files, and then checks that nothing is left. It prints
`clean`, or lists what remains. `--purge` also removes its settings and data.
If `takeover` switched off another zapret install, revert turns its service
back on. Revert never reads the configuration, so a broken config cannot block
it. If SplitDPI is killed, its queue rules stay in place but let all traffic
through until you run `revert` or start it again.

## Removing SplitDPI

`sudo splitdpi uninstall` lists what it would remove and changes nothing.
`sudo splitdpi uninstall --yes` does what `revert --purge` does, then deletes
the log and `/usr/local/bin/splitdpi`, and lists anything it could not remove;
run it again once that is fixed. Settings in the terminal UI does the same
after showing the list. It cannot be undone: to use SplitDPI again, install it
again as in [Quick start](#quick-start).

## Where presets come from

The ISP strategies started from
[SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey) by Çağrı
Taşkın (MIT). Other MIT-licensed collections, such as
[erdometo/zapret-turkey-configs](https://github.com/erdometo/zapret-turkey-configs),
are used to cross-check them. Each ISP file records in its `reported` field
when its strategies were last seen working on a real connection; the links
`scan` prints feed these files.

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
