# SplitDPI

SplitDPI gets Discord, YouTube, Roblox and other blocked apps and sites
working again on Linux when your internet provider blocks them with deep
packet inspection (DPI). It is meant for people who just want those apps back,
without learning how the blocking works.

It finds a [zapret2](https://github.com/bol-van/zapret2) strategy that works
on your provider, runs it when you want or at every boot, and removes every
trace with one command. It never edits your firewall, DNS or network
configuration files.

**Status:** latest release v0.2.7. Tested on a real Türk Telekom connection
(CachyOS) and in Debian, Fedora, Alpine and Ubuntu VMs or containers; see
[Supported systems](#supported-systems) and
[the release checklist](docs/release-checklist.md).

[Türkçe](README.tr.md)

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/KadirBerkpolat1/SplitDPI/main/install.sh | sudo sh
sudo splitdpi
```

The first line installs `/usr/local/bin/splitdpi` after checking its
checksum. The second opens the terminal UI: on the Setup page, start the
setup. It checks your system, offers to install missing packages, installs the
zapret2 engine, finds your provider, asks which apps should work, tests
strategies and starts SplitDPI.

- **Another place:** `sudo SPLITDPI_PREFIX=/opt/splitdpi sh` instead of
  `sudo sh` installs `/opt/splitdpi/bin/splitdpi` (default prefix
  `/usr/local`).
- **Upgrading:** run the install line again. The installed zapret2 engine
  stays in use until `sudo splitdpi engine install` installs the one the new
  version pins; `doctor` reports an older engine. Older engine directories are
  removed once nothing uses them.
- **Requirements:** x86_64 or arm64, kernel 5.4 or newer with nftables, the
  `nft` and `curl` commands, and `nslookup` or `host` for blockcheck2. Setup
  asks before installing missing packages; `sudo splitdpi deps install` does
  the same. On NixOS use zapret2-flake or zapret2-nix instead.

## Everyday use

`sudo splitdpi` opens the terminal UI with five pages: **Status**, **Sites**,
**Setup**, **Logs** and **Settings**. Tab and Shift+Tab or the number keys
switch pages; the arrow keys move inside a page and Enter acts. Esc goes back,
`?` lists every key, `q` quits.

- **Status** shows whether the bypass runs, your provider, the apps it handles
  and whether they open. Start or stop it here and turn **Start at boot** on
  or off. After a change that needs a restart, press `R` to restart; `d` shows
  the details (method, engine, process).
- **Sites** has three sections: **Apps** (turn app profiles on or off, edit
  their addresses), **Sites I added** (blocked sites you add) and **Sites left
  alone** (sites the bypass never touches; `x` removes one).
- **Setup** runs the guided setup, runs a deep scan for one site, or lets you
  pick a ready-made bypass method by hand when the current one stops working.
- **Logs** shows SplitDPI's log as it runs.
- **Settings** has a main group (Language, Start at boot, Encrypted DNS,
  Scope) and an **Advanced** group with the rest: interfaces to leave alone,
  the queue number, and maintenance such as updating presets, checking the
  system and uninstalling.

Before anything that changes the system, a dialog says what will change and
how to undo it; answer `y` or `n` (`e` or `h` in Turkish). The UI needs at
least 80x24 and uses plain ASCII on the Linux console or a non-UTF-8 locale.

The commands you will use most:

```sh
sudo splitdpi status                  # running? which strategy and engine?
sudo splitdpi probe                   # do my apps and sites open, and if not, why
sudo splitdpi sites add pastebin.com  # handle a blocked site
sudo splitdpi exclude add https://www.example-bank.com/   # leave a site alone
sudo splitdpi stop
sudo splitdpi run --background        # start again, until reboot
```

Most changes apply when SplitDPI restarts. A command that changes something
while SplitDPI runs prints the restart command for the way it runs, for
example `sudo splitdpi stop && sudo splitdpi enable` for the boot service.

Every command except `--help`, `version` and `presets list` needs root. The
language is English, Turkish or Russian: `--lang en|tr|ru`, else the one set
in Settings, else `LANG`.

## Apps

An app profile is a list of the app's domains. Enable the ones you need on the
Sites page or with `sudo splitdpi apps enable ID`; enabled apps are handled
from the first packet.

| ID | App | What is covered |
|---|---|---|
| `discord` | Discord | Chat, website, attachments and activities. Not voice channels or Go Live streams. |
| `facebook` | Facebook / Messenger | Website and Messenger. Not calls. |
| `grindr` | Grindr | Website. New. |
| `hornet` | Hornet | Website. New. |
| `instagram` | Instagram | App and website. Not calls. |
| `linkedin` | LinkedIn | Website. |
| `pastebin` | Pastebin | Website. |
| `roblox` | Roblox | Website, sign-in and developer sites. Not game servers (UDP). |
| `scruff` | Scruff | Website. New. |
| `signal` | Signal | App connection and updates. Not calls. |
| `telegram` | Telegram | Web, API and Telegram's own sites such as Fragment. Not the apps' direct connections to IP addresses. |
| `threads` | Threads | Website and its images. New. |
| `tiktok` | TikTok | App and website. |
| `viber` | Viber | Website and downloads only. |
| `vpn-sites` | VPN provider websites | Websites, accounts and downloads of NordVPN, Proton VPN, Mullvad, Surfshark, ExpressVPN, CyberGhost, Private Internet Access, PureVPN, hide.me and Psiphon. Not the VPN connections. New. |
| `wattpad` | Wattpad | Website. |
| `whatsapp` | WhatsApp | WhatsApp Web, media and website. Not the phone app's chats and calls. |
| `wikileaks` | WikiLeaks | Website. New. |
| `x` | X (Twitter) | App, website and Periscope. |
| `youtube` | YouTube | Website, app and video. |

Calls and app connections that carry no TLS hostname cannot be covered; the
Sites page notes this next to each app.

**Discord voice channels and Go Live streams are deliberately left alone.**
They do not use the Discord domains that are blocked, and in Türkiye they work
without SplitDPI, so changing their traffic could only break calls. The
Discord profile has no voice or stream addresses and no UDP rules, and a test
keeps every shipped app that way. Where voice itself is blocked, SplitDPI does
not help.

**Editing apps.** Change an app, or add one of your own:

```sh
sudo splitdpi apps show discord                    # its addresses
sudo splitdpi apps remove-host discord klipy.com    # one address fewer
sudo splitdpi apps new steam Steam                  # a new app, enabled
sudo splitdpi apps add-host steam store.steampowered.com
sudo splitdpi apps reset discord                    # back as shipped
```

An edit saves your copy as `/etc/splitdpi/apps/ID.toml`; it replaces the
shipped app, so `presets update` leaves it alone until you `reset` it. `reset`
deletes an app of your own. Your apps are tested at their domains by `scan`,
`probe` and `doctor`.

## Countries

When SplitDPI knows your country, setup preselects the apps blocked there by
SNI, the kind of block it can bypass. Only Türkiye was tested on a real
connection; the other rows come from [OONI](https://explorer.ooni.org)
measurements from August to October 2026.

| Country | Preselected apps | What SplitDPI can do | What it cannot do |
|---|---|---|---|
| Türkiye | Discord, Roblox | Tested on a real Türk Telekom line. Both apps are blocked by DNS and SNI ([OONI](https://explorer.ooni.org/findings/267640924000)). | |
| Russia | YouTube, Discord, WhatsApp, X, Viber | Partly, not tested: these apps are blocked by SNI ([OONI](https://ooni.org/post/2024-russia-report)), but working strategies differ by provider and break often. Plain DNS is rewritten, so turn on [encrypted DNS](#encrypted-dns). | Telegram, Instagram and Facebook are blocked by IP address. Mobile whitelist mode ([net4people](https://github.com/net4people/bbs/issues/516)) and the freeze after about 16 KB to foreign hosting ([net4people](https://github.com/net4people/bbs/issues/490)). |
| Uzbekistan | TikTok | Not tested. TikTok is blocked by SNI ([OONI](https://explorer.ooni.org/m/20261030202526.825336_UZ_webconnectivity_052c7879728aa664)). | |
| Pakistan | Signal | Not tested. Signal is blocked by SNI ([OONI](https://explorer.ooni.org/m/20261030220511.996290_PK_signal_5ac96b6fcef513f4)). | Signal calls. The firewall reassembles TCP, so simple split strategies may fail. |
| Oman | Signal, Viber | Not tested. Both are blocked by SNI ([OONI](https://explorer.ooni.org/m/20261030153230.174668_OM_signal_587cc9ac388c198c)). | Signal and Viber calls. |
| Myanmar | X, Signal | Not tested. Both are blocked by SNI on several providers ([OONI](https://explorer.ooni.org/m/20261030164419.537459_MM_webconnectivity_04a4276a4ab2ba2c)). | Facebook, Instagram, Messenger and WhatsApp are blocked by IP address ([OONI](https://explorer.ooni.org/m/20261030185034.005475_MM_webconnectivity_feef379ddff0b397)). Signal calls. |
| Tanzania | Discord, Signal | Not tested. Both are blocked by SNI ([OONI](https://explorer.ooni.org/m/20261030224510.394340_TZ_signal_ff0333dd53e6a624)). | X and Telegram's website are blocked by IP address ([OONI](https://explorer.ooni.org/m/20261030115612.829938_TZ_webconnectivity_595b8e6a4a66486b)). Signal calls. |
| Syria | Discord | Not tested. Discord is blocked by SNI on two providers, from few measurements ([OONI](https://explorer.ooni.org/m/20261013164632.158025_SY_webconnectivity_9e07ffe0537f7f05)). | |

Iran and China are not supported: blocking there is based on IP addresses and
allowlists, which SplitDPI cannot get past.

## Sites and exclusions

**My sites.** If a blocked site has no app profile, add it. SplitDPI then
handles it from the first packet, and `probe` and `scan` test it:

```sh
sudo splitdpi sites add pastebin.com
sudo splitdpi sites import FILE     # one domain per line, # starts a comment
```

**Never-touched sites.** Banks, government sites and some shops refuse
modified traffic. Exclude such a site, then restart SplitDPI:

```sh
sudo splitdpi exclude add https://www.example-bank.com/
```

SplitDPI also ships exclusion lists: a global one (for example GitHub, Google,
Microsoft, Steam and game launchers) and lists of domestic sites such as banks
and government portals for Türkiye, Russia and Uzbekistan. An app you enable
or a site you add always wins over the shipped lists (enabling YouTube takes
`youtube.com` off them); your own exclusions win over everything.

**Editing the files by hand.** Your sites are in `/etc/splitdpi/hosts.txt`,
your exclusions in `/etc/splitdpi/exclude.txt`. SplitDPI reads each line as a
domain: capitals, spaces, `*.` or a leading dot, a trailing `# comment` and a
byte order mark do not matter, and a line that is not a domain is ignored but
stays in the file.

**Scope** (Settings, or `config set mode`). `broad`, the default, handles your
apps and sites from the first packet and learns other blocked sites: the first
visits (three by default) to a newly blocked site fail, later ones work.
`apps` handles only enabled apps and your sites; in this scope SplitDPI refuses
any change that would leave it nothing to handle, such as disabling the last
app with addresses or removing your last site.

## Finding a strategy

`sudo splitdpi scan`, or the Setup page, first tries your provider's preset
strategies on your apps and sites and runs blockcheck2 only if none works. It
saves the strategy that works.

- **One site:** `scan --site DOMAIN` skips the presets and searches with
  blockcheck2 for that site (IPv4, and IPv6 if this machine has it), re-tests
  the results on your apps and sites, keeps the simplest one that opens them
  all and adds the site to your sites. It takes longer.
- **DNS:** before blockcheck2 runs, `scan` checks whether your provider
  tampers with DNS and asks whether to turn on encrypted DNS. Without a
  terminal, pass `--dns auto` (or a provider) or `--dns keep`.
- **Packages:** if blockcheck2's packages are missing, `scan` asks to install
  them; if blockcheck2 cannot start, it says why.
- **Options:** `--apps` tests only those apps, `--asn` and `--country` skip
  the provider lookup, `--blockcheck-only` skips the presets,
  `--no-blockcheck` stops after them, `-v` shows raw errors.
- **By hand:** `splitdpi strategy list` shows the preset strategies;
  `sudo splitdpi strategy set ID` picks one without a scan.
- **Share it:** after a scan, SplitDPI prints a link to a prefilled issue in
  [SplitDPI-presets](https://github.com/KadirBerkpolat1/SplitDPI-presets)
  with your AS number, provider, country, strategy and versions. Nothing is
  sent until you submit it, and it never contains your IP address or your
  sites. The "Strategy report" issue template here works too.

**Presets.** Provider strategies and app profiles ship inside SplitDPI.
`sudo splitdpi presets update` downloads the latest SplitDPI-presets release
and changes nothing unless its checksum file matches; `splitdpi presets list`
shows the version in use. Preset authors: `splitdpi presets check DIR` is
described in [the release checklist](docs/release-checklist.md).

## Encrypted DNS

Some providers tamper with DNS answers. With encrypted DNS on, SplitDPI sends
port 53 traffic through a forwarder inside itself to a provider:

| Choice | What it does |
|---|---|
| `auto` | Cloudflare, then Google, then Quad9: the first one that answers. |
| `cloudflare`, `google` | No filtering. |
| `quad9` | Also blocks known malware and phishing domains. |
| `adguard` | Also blocks ads and trackers. |

`sudo splitdpi dns auto` turns it on over DNS-over-HTTPS; add `--dot` for
DNS-over-TLS (port 853, blocked by some providers). `dns off` turns it off,
`dns` alone shows the choice. It applies on the next start.

- **Tampering detection:** `doctor`, `probe` and `scan` compare your system's
  answers with encrypted DNS. A known block-page address, or "no such name"
  or an empty answer while encrypted DNS finds a public address, counts as
  tampering. `probe` prints a `dns` line per domain: `✓` same, `~` different,
  `✗` tampered or failed, with the reason.
- **Nothing in your system changes:** no resolver configuration is edited;
  the redirect lives in SplitDPI's `inet splitdpi` table. Queries to
  resolvers on this machine (`127.0.0.0/8`, `::1`), such as the
  systemd-resolved stub or dnscrypt-proxy, are never redirected.
- **IPv4 and IPv6:** the forwarder listens on `127.0.0.1:5453` and
  `[::1]:5453`. Without an IPv6 loopback only IPv4 DNS is redirected, and IPv6
  DNS goes out unencrypted as before. If `[::1]:5453` cannot be opened for
  another reason, such as the port being taken, SplitDPI does not start.
- **Already encrypted:** with dnscrypt-proxy, Stubby, NextDNS, AdGuard Home,
  Unbound or Knot Resolver over TLS, or systemd-resolved with DNS-over-TLS,
  `doctor` and Settings say SplitDPI's DNS is not needed and `scan` does not
  ask about DNS.
- **VPNs and local names:** DNS through VPN interfaces (`skip_ifaces`) is left
  alone. Single-label names, `.lan`, `.local`, `.home.arpa`, `.internal`,
  `.localdomain`, your search domains and reverse lookups of private addresses
  go to your network's usual DNS server.
- **Side effects:** `quad9` and `adguard` do not resolve what they block.
  Captive-portal Wi-Fi (hotels, airports) may not show its login page until
  you run `dns off` and restart SplitDPI.

## Run modes and boot service

| Mode | Command | Stops when |
|---|---|---|
| In this window | `sudo splitdpi run` | the window closes or you press Ctrl-C |
| In the background | `sudo splitdpi run --background` | you run `stop`, or at reboot |
| At every boot | `sudo splitdpi enable` | you run `disable`, `stop` or `revert` |

- **Background** runs as the transient systemd unit
  `splitdpi-background.service` (logging out leaves it running), or without
  systemd as a detached process. Its output goes to `/var/log/splitdpi.log`.
- **Boot service:** `enable` installs and starts the service; a bypass running
  in a window or the background hands over to it, usually in under a second.
  If the service does not start, `enable` removes it, restarts the previous
  run in the background and says so. If SplitDPI already runs when the service
  starts, the service exits with code 4 and is not restarted.
- `enable` saves run mode `boot`, `disable` and `revert` set it back to
  `window`; `config set run_mode` only saves the choice and warns when the
  service does not match. `status` says whether SplitDPI starts at boot.
- Distros without service mode (see [Supported systems](#supported-systems))
  run SplitDPI in a window or the background.

## Switching from zapret or zapret2

SplitDPI replaces keift/zapret and other zapret or zapret2 installs.
`sudo splitdpi takeover` shows what switching would change without changing
anything; `takeover --yes` adds that install's user sites to yours, stops its
service and turns off its start at boot. It never deletes or edits its files,
and `revert` turns its service back on. While another zapret runs, `run`
refuses to start.

## Troubleshooting

- **Start with `sudo splitdpi doctor`.** It checks the kernel, tools,
  NFQUEUE, engine, SELinux, conflicting tools, init system, IPv6 and DNS
  tampering, and prints the command that fixes each problem.
- **Then `sudo splitdpi probe`.** It says in plain words why an app or site
  did not open (connection cut, timeout, DNS or secure connection failed,
  something else answered) and what to try next. A domain without an address
  of its own is shown as not tested, not as a failure.
- **The site is on the list but shows a block page or certificate error.**
  Usually a poisoned DNS answer is still cached; the bypass cannot fix a wrong
  address. SplitDPI clears the systemd-resolved and nscd caches at every start
  and when you add a site with encrypted DNS on, but answers cached earlier or
  while encrypted DNS was off can remain. Check with
  `resolvectl query --cache=no DOMAIN`, clear with `resolvectl flush-caches`,
  and turn on [encrypted DNS](#encrypted-dns). Other resolvers and browsers
  keep their own caches.
- **A site broke** (a bank or shop will not load): `sudo splitdpi exclude add
  URL`, then restart.
- **A new site fails a few times, then works:** in `broad` scope SplitDPI
  learns blocked sites from failed visits. Retry, or add it to your sites.
- **Nothing opens any more:** providers change their blocking. Run
  `sudo splitdpi scan`, or `scan --site DOMAIN`.
- **SplitDPI was killed:** its rules stay but let all traffic through
  unmodified until you run `revert` or start it again.

## Supported systems

| Distro | Init | Boot service | Tested |
|---|---|---|---|
| Debian | systemd | yes | 13.7 in a VM: service mode across reboot, revert (2026-10-29) |
| Ubuntu LTS, Linux Mint | systemd | yes | 26.04 in an Incus container: integration tests, service mode across reboot, moving a running bypass to the service, rollback when the service cannot start (2026-11-09) |
| Fedora (Workstation, Cloud) | systemd | yes | 44 Cloud in a VM, SELinux enforcing: service mode across reboot, revert (2026-10-29) |
| Fedora Atomic (Silverblue, Bazzite) | systemd | yes | not tested yet |
| CachyOS | systemd | yes | Türk Telekom connection: `scan` (presets and `--site`), `probe` on Discord, Roblox, Pastebin and more, `revert` (2026-10-29); `enable` while a background run was active, engine upgrade to v1.0.7 (2026-11-09); service restart with a maintenance build, DNS forwarder on IPv4 and IPv6, Discord opens (2026-11-17) |
| Arch, EndeavourOS | systemd | yes | not tested yet |
| openSUSE Tumbleweed | systemd | yes | not tested yet |
| Alpine | OpenRC | yes | 3.24.3 in a VM: service mode across reboot, revert (2026-10-29) |
| Gentoo | OpenRC | yes | not tested yet |
| Void (glibc and musl) | runit | yes | not tested yet |
| Artix, antiX, dinit, s6 or sysvinit distros | other | no | not tested yet |

Every distro can run SplitDPI in a window or the background. Immutable distros
and SELinux enforcing are supported.

## Command reference

| Command | What it does |
|---|---|
| `splitdpi` | Opens the terminal UI. |
| `splitdpi status` | Shows whether and how it runs, the strategy, the engine and whether it starts at boot. |
| `splitdpi probe [--apps ID,ID] [-v]` | Tests DNS and HTTPS for enabled apps and your sites. |
| `splitdpi doctor` | Checks the system and lists problems with fixes. |
| `splitdpi scan [--site DOMAIN] [--dns keep\|auto\|PROVIDER] [--apps ID,ID] [--asn N] [--country CC] [--blockcheck-only\|--no-blockcheck] [-v]` | Finds a working strategy and saves it. |
| `splitdpi run [--background]` | Runs in this terminal, or in the background until reboot. |
| `splitdpi stop` | Stops the bypass, however it runs. |
| `splitdpi enable` / `disable` | Installs and starts the boot service, or removes it. |
| `splitdpi logs [-f]` | Shows the log; `-f` keeps following it. |
| `splitdpi apps list\|enable ID\|disable ID` | Chooses which apps get the bypass. |
| `splitdpi apps show ID\|new ID NAME\|add-host ID DOMAIN\|remove-host ID DOMAIN\|reset ID` | Shows or edits an app, or adds your own. |
| `splitdpi sites list\|add DOMAIN\|remove DOMAIN\|import FILE` | Manages your own blocked sites. |
| `splitdpi exclude list\|add DOMAIN\|remove DOMAIN` | Manages sites the bypass never touches. |
| `splitdpi dns [off\|auto\|cloudflare\|google\|quad9\|adguard] [--dot]` | Sets or shows encrypted DNS. |
| `splitdpi strategy list\|set ID` | Lists preset strategies, or picks one. |
| `splitdpi detect [--asn N] [--country CC]` | Looks up your provider and its preset strategies. |
| `splitdpi config [get KEY\|set KEY VALUE\|reset KEY]` | Shows or changes `lang`, `run_mode`, `mode`, `skip_ifaces` (comma list such as `'wg*,tun*'`), `ipinfo_url` and `qnum` (NFQUEUE number, no other program may use it). |
| `splitdpi presets list\|update` | Shows the preset data, or updates it after checking its SHA-256. |
| `splitdpi engine install [--from FILE]` | Installs the pinned zapret2 release after checking its SHA-256. |
| `splitdpi deps [install [--yes]]` | Lists or installs missing packages. |
| `splitdpi takeover [--yes]` | Switches another zapret or zapret2 install to SplitDPI. |
| `splitdpi revert [--purge]` | Removes everything SplitDPI added; `--purge` also settings and data. |
| `splitdpi uninstall [--yes]` | Removes SplitDPI itself. |
| `splitdpi version` | Prints the version. |

**Removed on 2026-11-17:** `engine status` (`status` and `doctor` show the
engine), `dns on` (use `dns auto`) and `apps udp` (no app has a voice or
stream profile).

## Security notes

- **Installer:** `install.sh` downloads from GitHub over https only (curl
  refuses plain http, also on redirects, and needs TLS 1.2 or newer) and
  installs the binary only if it matches its line in the release's
  `sha256sums.txt`. Directories are created with umask 022. It needs curl:
  wget follows a redirect from https to plain http.
- **Downloads:** the zapret2 engine is a pinned release checked against its
  SHA-256; a preset update is refused unless its `presets.tar.gz.sha256`
  matches.
- **The root service runs only a binary that only root can change:** the one
  that ran `enable` if it qualifies, else `/usr/local/bin/splitdpi`. The
  binary and every directory above it, along symbolic links, must belong to
  root and not be writable by group or others, and it must not be under
  `/tmp`, `/var/tmp`, `/dev/shm` or a home directory (`/home`, `/var/home`,
  `/root`, `/var/roothome`). The error names the file at fault and gives the
  `sudo install` command to fix it.
- **Preset hostnames are validated:** every domain in an app profile, shipped
  or yours, must be a plain hostname (letters, digits, hyphens, at most 253
  bytes); a profile that breaks this is skipped with a warning. `scan` checks
  each domain again before passing it to blockcheck2, which runs as root.
- **Firewall:** SplitDPI keeps its rules in its own nftables table,
  `inet splitdpi`, and never flushes your ruleset. The rules fail open: if the
  engine stops, traffic passes unmodified.

## Uninstall

`sudo splitdpi revert` stops SplitDPI, removes its service, table and runtime
files, then checks that nothing is left and says so or lists what remains.
`--purge` also removes settings and data. A zapret install switched off by
`takeover` gets its service back. A broken configuration cannot block revert.

`sudo splitdpi uninstall` lists what it would remove and changes nothing;
`uninstall --yes` does what `revert --purge` does, then deletes the log,
`/usr/local/bin/splitdpi` and the binary you ran it with if only root can
change it (such as one installed with `SPLITDPI_PREFIX`). If something is left
over, the binaries stay so you can run it again. Settings in the terminal UI
does the same.

## Credits and licence

- [zapret2](https://github.com/bol-van/zapret2) by bol-van (MIT): the engine.
  SplitDPI downloads it; it does not redistribute it.
- [SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey) by Çağrı
  Taşkın (MIT): provider strategies, the exclusion list and Roblox hosts;
  [erdometo/zapret-turkey-configs](https://github.com/erdometo/zapret-turkey-configs)
  (MIT) to cross-check them.
- [Flowseal/zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube)
  (MIT): Discord and YouTube hosts and the Russian exclusion list.
- [dmfrpro/zapret2-flake](https://github.com/dmfrpro/zapret2-flake) (MIT): the
  built-in-blob fallback.
- [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community)
  (MIT): hosts for Instagram, Facebook, WhatsApp, X, Signal, Viber, LinkedIn,
  TikTok and Pastebin, and added Telegram, YouTube and Discord hosts.
- [1andrevich/Re-filter-lists](https://github.com/1andrevich/Re-filter-lists)
  (MIT): one TikTok host.
- [itdoginfo/allow-domains](https://github.com/itdoginfo/allow-domains)
  (no licence; domain names only, no code or data files copied): VPN provider
  and Threads hosts, and hosts added to Telegram, X, TikTok, WhatsApp, Roblox
  and Facebook.
- [OONI](https://explorer.ooni.org) and
  [EngelliWeb](https://ifade.org.tr/engelliweb/): which new apps are blocked
  in Türkiye.
- alimali54/zapret-win-turkey: the origin of the Turkish zapret2 strategies.

See [NOTICE](NOTICE) for licences. SplitDPI is MIT-licensed.

SplitDPI changes how your computer sends some network traffic. Whether that is
allowed depends on where you are; check the rules that apply to you. You use
SplitDPI at your own responsibility, without warranty.
