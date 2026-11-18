# SplitDPI

SplitDPI, internet sağlayıcınız Discord, YouTube, Roblox gibi uygulamaları ve
siteleri derin paket incelemesiyle (DPI) engellediğinde bunları Linux'ta
yeniden çalıştırır. Engelin nasıl işlediğini öğrenmeden uygulamalarına
kavuşmak isteyenler içindir.

Sağlayıcınızda çalışan bir [zapret2](https://github.com/bol-van/zapret2)
stratejisi bulur, onu istediğinizde ya da her açılışta çalıştırır ve tek
komutla geride iz bırakmadan kaldırır. Güvenlik duvarı, DNS ve ağ yapılandırma
dosyalarınıza hiç dokunmaz.

**Durum:** son sürüm v0.2.7. Gerçek bir Türk Telekom bağlantısında (CachyOS)
ve Debian, Fedora, Alpine, Ubuntu sanal makinelerinde ya da konteynerlerinde
denendi; bkz. [Desteklenen sistemler](#desteklenen-sistemler) ve
[yayın kontrol listesi](docs/release-checklist.md).

[English](README.md)

## Hızlı başlangıç

```sh
curl -fsSL https://raw.githubusercontent.com/KadirBerkpolat1/SplitDPI/main/install.sh | sudo sh
sudo splitdpi
```

İlk satır sağlama toplamını doğrulayıp `/usr/local/bin/splitdpi`'yi kurar.
İkincisi terminal arayüzünü açar: Kurulum sayfasında kurulumu başlatın.
Kurulum sisteminizi denetler, eksik paketleri kurmayı önerir, zapret2
motorunu kurar, sağlayıcınızı bulur, hangi uygulamaların çalışması gerektiğini
sorar, stratejileri dener ve SplitDPI'yi başlatır.

- **Başka bir yere:** `sudo sh` yerine `sudo SPLITDPI_PREFIX=/opt/splitdpi sh`
  `/opt/splitdpi/bin/splitdpi`'yi kurar (varsayılan önek `/usr/local`).
- **Güncelleme:** kurulum satırını yeniden çalıştırın. Kurulu zapret2 motoru,
  `sudo splitdpi engine install` yeni sürümün sabitlediği motoru kurana dek
  kullanılmaya devam eder; `doctor` eski motoru bildirir. Eski motor dizinleri
  artık kullanılmadığında silinir.
- **Gereksinimler:** x86_64 ya da arm64, nftables destekli 5.4 veya daha yeni
  çekirdek, `nft` ve `curl` komutları, blockcheck2 için `nslookup` ya da
  `host`. Kurulum eksik paketleri kurmadan önce sorar; `sudo splitdpi deps
  install` aynısını yapar. NixOS'ta zapret2-flake ya da zapret2-nix kullanın.

## Günlük kullanım

`sudo splitdpi` terminal arayüzünü beş sayfayla açar: **Durum**, **Siteler**,
**Kurulum**, **Günlük** ve **Ayarlar**. Tab ile Shift+Tab ya da rakam
tuşları sayfa değiştirir; ok tuşları sayfa içinde gezer, Enter seçileni
yapar. Esc geri döner, `?` bütün tuşları gösterir, `q` çıkar.

- **Durum** engel aşmanın çalışıp çalışmadığını, sağlayıcınızı, hangi
  uygulamalar için çalıştığını ve açılıp açılmadıklarını gösterir. Burada
  başlatıp durdurur, **Açılışta başlasın**'ı açıp kapatırsınız. Yeniden
  başlatma isteyen bir değişiklikten sonra `R` yeniden başlatır; `d`
  ayrıntıları (yöntem, motor, süreç) gösterir.
- **Siteler** üç bölümdür: **Uygulamalar** (uygulama profillerini açıp
  kapatın, adreslerini düzenleyin), **Eklediğim siteler** (kendi eklediğiniz
  engelli siteler) ve **Dokunulmayan siteler** (engel aşmanın hiç dokunmadığı
  siteler; `x` birini kaldırır).
- **Kurulum** rehberli kurulumu ya da tek bir site için derin taramayı
  çalıştırır; mevcut yöntem çalışmayı bırakınca hazır yöntemlerden birini elle
  de seçebilirsiniz.
- **Günlük** SplitDPI'nin günlüğünü canlı gösterir.
- **Ayarlar**'da bir ana grup (Dil, Açılışta başlat, Şifreli DNS, Kapsam) ve
  geri kalanı için **Gelişmiş** grubu vardır: dokunulmayacak ağ arayüzleri,
  kuyruk numarası ve hazır ayarları güncelleme, sistemi denetleme, kaldırma
  gibi bakım işleri.

Sistemi değiştiren her işten önce bir pencere neyin değişeceğini ve nasıl
geri alınacağını söyler; `e` ya da `h` ile yanıtlayın (İngilizcede `y`/`n`).
Arayüz en az 80x24 ister; Linux konsolunda ya da UTF-8 olmayan yerel ayarda
düz ASCII ile çizer.

En çok kullanılan komutlar:

```sh
sudo splitdpi status                  # çalışıyor mu, hangi strateji ve motorla?
sudo splitdpi probe                   # uygulamalarım ve sitelerim açılıyor mu, açılmıyorsa neden
sudo splitdpi sites add pastebin.com  # engelli bir siteyi ekle
sudo splitdpi exclude add https://www.ornek-banka.com.tr/   # bir siteye dokunma
sudo splitdpi stop
sudo splitdpi run --background        # yeniden başlat, bilgisayar yeniden başlayana kadar
```

Çoğu değişiklik SplitDPI yeniden başlayınca geçerli olur. SplitDPI çalışırken
bir şey değiştiren komut, onun çalışma biçimine uyan yeniden başlatma komutunu
yazar; örneğin açılış servisi için
`sudo splitdpi stop && sudo splitdpi enable`.

`--help`, `version` ve `presets list` dışındaki her komut root ister. Dil
İngilizce, Türkçe ya da Rusça olabilir: `--lang en|tr|ru`, yoksa Ayarlar'da
seçilen, o da yoksa `LANG`.

## Uygulamalar

Uygulama profili, uygulamanın alan adlarının listesidir. İhtiyacınız olanları
Siteler sayfasında ya da `sudo splitdpi apps enable ID` ile açın; açık
uygulamalar ilk paketten itibaren işlenir.

| ID | Uygulama | Neyi kapsar |
|---|---|---|
| `discord` | Discord | Sohbet, web sitesi, ekler ve etkinlikler. Ses kanalları ve Go Live yayınları hariç. |
| `facebook` | Facebook / Messenger | Web sitesi ve Messenger. Aramalar hariç. |
| `grindr` | Grindr | Web sitesi. Yeni. |
| `hornet` | Hornet | Web sitesi. Yeni. |
| `instagram` | Instagram | Uygulama ve web sitesi. Aramalar hariç. |
| `linkedin` | LinkedIn | Web sitesi. |
| `pastebin` | Pastebin | Web sitesi. |
| `roblox` | Roblox | Web sitesi, giriş ve geliştirici siteleri. Oyun sunucuları (UDP) hariç. |
| `scruff` | Scruff | Web sitesi. Yeni. |
| `signal` | Signal | Uygulama bağlantısı ve güncellemeler. Aramalar hariç. |
| `telegram` | Telegram | Web, API ve Fragment gibi Telegram'ın kendi siteleri. Uygulamaların IP adreslerine doğrudan bağlantıları hariç. |
| `threads` | Threads | Web sitesi ve görselleri. Yeni. |
| `tiktok` | TikTok | Uygulama ve web sitesi. |
| `viber` | Viber | Yalnız web sitesi ve indirmeler. |
| `vpn-sites` | VPN sağlayıcı siteleri | NordVPN, Proton VPN, Mullvad, Surfshark, ExpressVPN, CyberGhost, Private Internet Access, PureVPN, hide.me ve Psiphon'un siteleri, hesapları ve indirmeleri. VPN bağlantılarının kendisi hariç. Yeni. |
| `wattpad` | Wattpad | Web sitesi. |
| `whatsapp` | WhatsApp | WhatsApp Web, medya ve web sitesi. Telefon uygulamasının sohbetleri ve aramaları hariç. |
| `wikileaks` | WikiLeaks | Web sitesi. Yeni. |
| `x` | X (Twitter) | Uygulama, web sitesi ve Periscope. |
| `youtube` | YouTube | Web sitesi, uygulama ve videolar. |

TLS ana makine adı taşımayan aramalar ve uygulama bağlantıları kapsanamaz;
Siteler sayfası bunu her uygulamanın yanında belirtir.

**Discord ses kanallarına ve Go Live yayınlarına bilerek dokunulmaz.** Bunlar
engellenen Discord alan adlarından geçmez ve Türkiye'de SplitDPI olmadan
çalışır; trafiklerini değiştirmek yalnızca aramaları bozabilir. Discord
profilinde ses ya da yayın adresi ve UDP kuralı yoktur; bir test bütün hazır
uygulamaları böyle tutar. Sesin kendisinin engellendiği yerde SplitDPI bunda
yardımcı olmaz.

**Uygulamaları düzenleme.** Bir uygulamayı değiştirin ya da kendinizinkini
ekleyin:

```sh
sudo splitdpi apps show discord                    # adresleri
sudo splitdpi apps remove-host discord klipy.com    # bir adres eksik
sudo splitdpi apps new steam Steam                  # yeni uygulama, açık
sudo splitdpi apps add-host steam store.steampowered.com
sudo splitdpi apps reset discord                    # dağıtıldığı haline döner
```

Düzenleme kendi kopyanızı `/etc/splitdpi/apps/ID.toml` olarak kaydeder; bu
kopya hazır uygulamanın yerine geçer ve `reset` edene kadar `presets update`
ona dokunmaz. Kendi uygulamanızda `reset` onu siler. Kendi uygulamalarınızı
`scan`, `probe` ve `doctor` alan adlarında dener.

## Ülkeler

SplitDPI ülkenizi biliyorsa kurulum, orada SNI ile engellenen uygulamaları
önceden seçer; SplitDPI'nin aşabildiği engel türü budur. Yalnızca Türkiye
gerçek bir bağlantıda denendi; diğer satırlar Ağustos–Ekim 2026 arasındaki
[OONI](https://explorer.ooni.org) ölçümlerine dayanır.

| Ülke | Önceden seçilen uygulamalar | SplitDPI ne yapabilir | Ne yapamaz |
|---|---|---|---|
| Türkiye | Discord, Roblox | Gerçek bir Türk Telekom hattında denendi. İki uygulama da DNS ve SNI ile engelli ([OONI](https://explorer.ooni.org/findings/267640924000)). | |
| Rusya | YouTube, Discord, WhatsApp, X, Viber | Kısmen, denenmedi: bu uygulamalar SNI ile engelli ([OONI](https://ooni.org/post/2024-russia-report)), ama çalışan stratejiler sağlayıcıya göre değişir ve sık bozulur. Düz DNS yanıtları değiştiriliyor, bu yüzden [şifreli DNS](#şifreli-dns)'i açın. | Telegram, Instagram ve Facebook IP adresiyle engelli. Mobil beyaz liste modu ([net4people](https://github.com/net4people/bbs/issues/516)) ve yurt dışı barındırmaya giden bağlantıların yaklaşık 16 KB'den sonra donması ([net4people](https://github.com/net4people/bbs/issues/490)). |
| Özbekistan | TikTok | Denenmedi. TikTok SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030202526.825336_UZ_webconnectivity_052c7879728aa664)). | |
| Pakistan | Signal | Denenmedi. Signal SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030220511.996290_PK_signal_5ac96b6fcef513f4)). | Signal aramaları. Güvenlik duvarı TCP akışlarını birleştirdiği için basit bölme stratejileri işe yaramayabilir. |
| Umman | Signal, Viber | Denenmedi. İkisi de SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030153230.174668_OM_signal_587cc9ac388c198c)). | Signal ve Viber aramaları. |
| Myanmar | X, Signal | Denenmedi. İkisi de birkaç sağlayıcıda SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030164419.537459_MM_webconnectivity_04a4276a4ab2ba2c)). | Facebook, Instagram, Messenger ve WhatsApp IP adresiyle engelli ([OONI](https://explorer.ooni.org/m/20261030185034.005475_MM_webconnectivity_feef379ddff0b397)). Signal aramaları. |
| Tanzanya | Discord, Signal | Denenmedi. İkisi de SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030224510.394340_TZ_signal_ff0333dd53e6a624)). | X ve Telegram'ın web sitesi IP adresiyle engelli ([OONI](https://explorer.ooni.org/m/20261030115612.829938_TZ_webconnectivity_595b8e6a4a66486b)). Signal aramaları. |
| Suriye | Discord | Denenmedi. Discord iki sağlayıcıda SNI ile engelli; ölçüm sayısı az ([OONI](https://explorer.ooni.org/m/20261013164632.158025_SY_webconnectivity_9e07ffe0537f7f05)). | |

İran ve Çin desteklenmiyor: oradaki engeller IP adreslerine ve izin listelerine
dayanır, SplitDPI bunları aşamaz.

## Siteler ve istisnalar

**Sitelerim.** Engelli bir sitenin uygulama profili yoksa onu ekleyin.
SplitDPI onu ilk paketten itibaren işler, `probe` ve `scan` da onu dener:

```sh
sudo splitdpi sites add pastebin.com
sudo splitdpi sites import DOSYA    # her satırda bir alan adı, # yorum başlatır
```

**Dokunulmayan siteler.** Bankalar, devlet siteleri ve bazı mağazalar
değiştirilmiş trafiği reddeder. Böyle bir siteyi istisna yapın ve SplitDPI'yi
yeniden başlatın:

```sh
sudo splitdpi exclude add https://www.ornek-banka.com.tr/
```

SplitDPI hazır istisna listeleriyle de gelir: genel bir liste (örneğin GitHub,
Google, Microsoft, Steam ve oyun başlatıcıları) ve Türkiye, Rusya ve
Özbekistan için bankalar, devlet portalları gibi yerli sitelerin listeleri.
Açtığınız bir uygulama ya da eklediğiniz bir site hazır listelerden her zaman
üstün gelir (YouTube'u açmak `youtube.com`'u listeden çıkarır); kendi
istisnalarınız her şeyden üstündür.

**Dosyaları elle düzenlemek.** Siteleriniz `/etc/splitdpi/hosts.txt`'te,
istisnalarınız `/etc/splitdpi/exclude.txt`'tedir. SplitDPI her satırı bir alan
adı olarak okur: büyük harf, boşluk, `*.` ya da baştaki nokta, sondaki
`# yorum` ve bayt sırası işareti (BOM) fark etmez; alan adı olmayan satır
yok sayılır ama dosyada kalır.

**Kapsam** (Ayarlar ya da `config set mode`). Varsayılan `broad`,
uygulamalarınızı ve sitelerinizi ilk paketten işler, diğer engelli siteleri
öğrenir: yeni engellenmiş bir siteye ilk ziyaretler (varsayılan üç) başarısız
olur, sonrakiler çalışır. `apps` yalnızca açık uygulamaları ve sitelerinizi
işler; bu kapsamda SplitDPI, işleyecek hiçbir şey bırakmayacak değişikliği
(adresi olan son uygulamayı kapatmak, son sitenizi silmek gibi) reddeder.

## Strateji bulma

`sudo splitdpi scan` ya da Kurulum sayfası önce sağlayıcınızın hazır
stratejilerini uygulamalarınızda ve sitelerinizde dener; hiçbiri çalışmazsa
blockcheck2'yi çalıştırır. Çalışan stratejiyi kaydeder.

- **Tek site:** `scan --site ALANADI` hazır ayarları atlayıp o site için
  blockcheck2 ile arar (IPv4, bu makinede varsa IPv6), sonuçları
  uygulamalarınızda ve sitelerinizde yeniden dener, hepsini açan en basitini
  saklar ve siteyi sitelerinize ekler. Daha uzun sürer.
- **DNS:** blockcheck2'den önce `scan`, sağlayıcınızın DNS'i bozup bozmadığına
  bakar ve şifreli DNS'i açmak isteyip istemediğinizi sorar. Terminal yoksa
  `--dns auto` (ya da bir sağlayıcı) veya `--dns keep` verin.
- **Paketler:** blockcheck2'nin paketleri eksikse `scan` kurmayı sorar;
  blockcheck2 başlayamazsa nedenini söyler.
- **Seçenekler:** `--apps` yalnız o uygulamaları dener, `--asn` ve `--country`
  sağlayıcı sorgusunu atlar, `--blockcheck-only` hazır ayarları atlar,
  `--no-blockcheck` onlardan sonra durur, `-v` ham hataları gösterir.
- **Elle:** `splitdpi strategy list` hazır stratejileri gösterir;
  `sudo splitdpi strategy set ID` taramasız birini seçer.
- **Paylaşın:** taramadan sonra SplitDPI,
  [SplitDPI-presets](https://github.com/KadirBerkpolat1/SplitDPI-presets)
  deposunda AS numaranız, sağlayıcınız, ülkeniz, strateji ve sürümlerle
  önceden doldurulmuş bir issue bağlantısı yazar. Siz gönderene kadar hiçbir
  şey gitmez; IP adresiniz ve siteleriniz içinde olmaz. Buradaki "Strategy
  report" issue şablonu da olur.

**Hazır ayarlar.** Sağlayıcı stratejileri ve uygulama profilleri SplitDPI'nin
içinde gelir. `sudo splitdpi presets update` son SplitDPI-presets yayınını
indirir ve sağlama dosyası tutmazsa hiçbir şeyi değiştirmez;
`splitdpi presets list` kullanılan sürümü gösterir. Hazır ayar yazanlar:
`splitdpi presets check DIZIN` [yayın kontrol listesinde](docs/release-checklist.md)
anlatılır.

## Şifreli DNS

Bazı sağlayıcılar DNS yanıtlarını bozar. Şifreli DNS açıkken SplitDPI, 53.
porttaki trafiği kendi içindeki bir ileticiden bir sağlayıcıya gönderir:

| Seçim | Ne yapar |
|---|---|
| `auto` | Sırayla Cloudflare, Google ve Quad9: ilk yanıt veren kullanılır. |
| `cloudflare`, `google` | Filtreleme yapmaz. |
| `quad9` | Bilinen zararlı yazılım ve oltalama alan adlarını da engeller. |
| `adguard` | Reklamları ve izleyicileri de engeller. |

`sudo splitdpi dns auto` onu DNS-over-HTTPS ile açar; DNS-over-TLS için
`--dot` ekleyin (853. port, bazı sağlayıcılar engeller). `dns off` kapatır,
yalnız `dns` seçimi gösterir. Bir sonraki başlatmada geçerli olur.

- **Bozma tespiti:** `doctor`, `probe` ve `scan` sisteminizin yanıtlarını
  şifreli DNS'inkilerle karşılaştırır. Bilinen bir engel sayfası adresi ya da
  şifreli DNS genel bir adres bulurken "böyle bir ad yok" veya boş yanıt,
  bozma sayılır. `probe` her alan adı için bir `dns` satırı yazar: `✓` aynı,
  `~` farklı, `✗` bozulmuş ya da başarısız, nedeniyle.
- **Sisteminizde hiçbir şey değişmez:** hiçbir çözümleyici ayarına
  dokunulmaz; yönlendirme SplitDPI'nin `inet splitdpi` tablosundadır. Bu
  makinedeki çözümleyicilere (`127.0.0.0/8`, `::1`), örneğin systemd-resolved
  sapına ya da dnscrypt-proxy'ye giden sorgular hiç yönlendirilmez.
- **IPv4 ve IPv6:** iletici `127.0.0.1:5453` ve `[::1]:5453`'te dinler. IPv6
  geri döngü adresi yoksa yalnız IPv4 DNS yönlendirilir, IPv6 DNS eskisi gibi
  şifresiz gider. `[::1]:5453` başka bir nedenle (örneğin port dolu)
  açılamazsa SplitDPI başlamaz.
- **Zaten şifreli:** dnscrypt-proxy, Stubby, NextDNS, AdGuard Home, TLS ile
  ileten Unbound ya da Knot Resolver veya DNS-over-TLS'li systemd-resolved
  kullanılıyorsa `doctor` ve Ayarlar SplitDPI'nin DNS'ine gerek olmadığını
  söyler, `scan` DNS'i sormaz.
- **VPN ve yerel adlar:** VPN arayüzlerinden (`skip_ifaces`) giden DNS'e
  dokunulmaz. Tek etiketli adlar, `.lan`, `.local`, `.home.arpa`, `.internal`,
  `.localdomain`, arama alan adlarınız ve özel adreslerin ters sorguları
  ağınızın her zamanki DNS sunucusuna gider.
- **Yan etkiler:** `quad9` ve `adguard` engelledikleri alan adlarını
  çözmez. Giriş sayfalı Wi-Fi ağları (otel, havalimanı) `dns off` yapıp
  SplitDPI'yi yeniden başlatana kadar giriş sayfasını göstermeyebilir.

## Çalışma biçimleri ve açılış servisi

| Biçim | Komut | Ne zaman durur |
|---|---|---|
| Bu pencerede | `sudo splitdpi run` | pencere kapanınca ya da Ctrl-C ile |
| Arka planda | `sudo splitdpi run --background` | `stop` ile ya da bilgisayar yeniden başlayınca |
| Her açılışta | `sudo splitdpi enable` | `disable`, `stop` ya da `revert` ile |

- **Arka plan** geçici systemd birimi `splitdpi-background.service` olarak
  (oturumu kapatmak onu durdurmaz), systemd yoksa ayrık bir süreç olarak
  çalışır. Çıktısı `/var/log/splitdpi.log`'a gider.
- **Açılış servisi:** `enable` servisi kurar ve başlatır; pencerede ya da arka
  planda çalışan engel aşma işi ona devreder, genellikle bir saniyeden kısa
  sürede. Servis başlamazsa `enable` onu kaldırır, önceki çalışmayı arka
  planda yeniden başlatır ve bunu söyler. Servis başlarken SplitDPI zaten
  çalışıyorsa servis 4 koduyla çıkar ve yeniden başlatılmaz.
- `enable` çalışma biçimini `boot` olarak kaydeder, `disable` ve `revert`
  `window`'a döndürür; `config set run_mode` yalnız seçimi kaydeder ve servis
  uymuyorsa uyarır. `status` SplitDPI'nin açılışta başlayıp başlamadığını
  söyler.
- Servis modu olmayan dağıtımlar (bkz.
  [Desteklenen sistemler](#desteklenen-sistemler)) SplitDPI'yi pencerede ya da
  arka planda çalıştırır.

## zapret ya da zapret2'den geçiş

SplitDPI, keift/zapret'in ve diğer zapret ya da zapret2 kurulumlarının yerini
alır. `sudo splitdpi takeover` geçişin neyi değiştireceğini hiçbir şeyi
değiştirmeden gösterir; `takeover --yes` o kurulumun kullanıcı sitelerini
sizinkilere ekler, servisini durdurur ve açılışta başlamasını kapatır.
Dosyalarını asla silmez ya da değiştirmez; `revert` servisini geri açar. Başka
bir zapret çalışırken `run` başlamayı reddeder.

## Sorun giderme

- **`sudo splitdpi doctor` ile başlayın.** Çekirdeği, araçları, NFQUEUE'yu,
  motoru, SELinux'u, çakışan araçları, init sistemini, IPv6'yı ve DNS
  bozmasını denetler; her sorunu düzelten komutu yazar.
- **Sonra `sudo splitdpi probe`.** Bir uygulamanın ya da sitenin neden
  açılmadığını (bağlantı kesildi, zaman aşımı, DNS ya da güvenli bağlantı
  başarısız, başka bir şey yanıt verdi) ve ne deneneceğini sade bir dille
  söyler. Kendi adresi olmayan alan adı başarısız değil, denenmedi diye
  görünür.
- **Site listede ama engel sayfası ya da sertifika hatası çıkıyor.**
  Genellikle zehirli bir DNS yanıtı hâlâ önbellektedir; engel aşma yanlış
  adresi düzeltemez. SplitDPI her başlatmada ve şifreli DNS açıkken site
  eklendiğinde systemd-resolved ve nscd önbelleklerini temizler; ama daha
  önce ya da şifreli DNS kapalıyken giren yanıtlar kalabilir.
  `resolvectl query --cache=no ALANADI` ile bakın, `resolvectl flush-caches`
  ile temizleyin ve [şifreli DNS](#şifreli-dns)'i açın. Diğer çözümleyiciler
  ve tarayıcılar kendi önbelleklerini tutar.
- **Bir site bozuldu** (banka ya da mağaza açılmıyor): `sudo splitdpi exclude
  add URL`, sonra yeniden başlatın.
- **Yeni bir site birkaç kez açılmıyor, sonra açılıyor:** `broad` kapsamda
  SplitDPI engelli siteleri başarısız ziyaretlerden öğrenir. Yeniden deneyin
  ya da siteyi sitelerinize ekleyin.
- **Artık hiçbir şey açılmıyor:** sağlayıcılar engellerini değiştirir.
  `sudo splitdpi scan` ya da `scan --site ALANADI` çalıştırın.
- **SplitDPI öldürüldü:** kuralları yerinde kalır ama `revert` çalıştırılana
  ya da yeniden başlatılana kadar bütün trafiği değiştirmeden geçirir.

## Desteklenen sistemler

| Dağıtım | Init | Açılış servisi | Denendi |
|---|---|---|---|
| Debian | systemd | evet | Sanal makinede 13.7: yeniden başlatma sonrası servis modu ve revert (2026-10-29) |
| Ubuntu LTS, Linux Mint | systemd | evet | Incus konteynerinde 26.04: entegrasyon testleri, yeniden başlatmalarda servis modu, çalışan engel aşmanın servise taşınması, servis başlayamayınca geri alma (2026-11-09) |
| Fedora (Workstation, Cloud) | systemd | evet | Sanal makinede 44 Cloud, SELinux zorlayıcı modda: yeniden başlatma sonrası servis modu ve revert (2026-10-29) |
| Fedora Atomic (Silverblue, Bazzite) | systemd | evet | henüz denenmedi |
| CachyOS | systemd | evet | Türk Telekom bağlantısında: `scan` (hazır ayarlar ve `--site`), Discord, Roblox, Pastebin ve diğerlerinde `probe`, `revert` (2026-10-29); arka plan çalışması açıkken `enable`, motorun v1.0.7'ye yükseltilmesi (2026-11-09); bakım derlemesiyle servisin yeniden başlatılması, IPv4 ve IPv6'da DNS iletici, Discord açılıyor (2026-11-17) |
| Arch, EndeavourOS | systemd | evet | henüz denenmedi |
| openSUSE Tumbleweed | systemd | evet | henüz denenmedi |
| Alpine | OpenRC | evet | Sanal makinede 3.24.3: yeniden başlatma sonrası servis modu ve revert (2026-10-29) |
| Gentoo | OpenRC | evet | henüz denenmedi |
| Void (glibc ve musl) | runit | evet | henüz denenmedi |
| Artix, antiX, dinit, s6 veya sysvinit dağıtımları | diğer | hayır | henüz denenmedi |

Her dağıtım SplitDPI'yi pencerede ya da arka planda çalıştırabilir.
Değiştirilemez (immutable) dağıtımlar ve SELinux zorlayıcı mod desteklenir.

## Komut başvurusu

| Komut | Ne yapar |
|---|---|
| `splitdpi` | Terminal arayüzünü açar. |
| `splitdpi status` | Çalışıp çalışmadığını ve nasıl, stratejiyi, motoru ve açılışta başlayıp başlamadığını gösterir. |
| `splitdpi probe [--apps ID,ID] [-v]` | Açık uygulamalar ve siteleriniz için DNS ve HTTPS'i dener. |
| `splitdpi doctor` | Sistemi denetler, sorunları çözümleriyle listeler. |
| `splitdpi scan [--site DOMAIN] [--dns keep\|auto\|PROVIDER] [--apps ID,ID] [--asn N] [--country CC] [--blockcheck-only\|--no-blockcheck] [-v]` | Çalışan bir strateji bulur ve kaydeder. |
| `splitdpi run [--background]` | Bu terminalde ya da yeniden başlatmaya kadar arka planda çalışır. |
| `splitdpi stop` | Engel aşmayı, nasıl çalışıyorsa durdurur. |
| `splitdpi enable` / `disable` | Açılış servisini kurup başlatır ya da kaldırır. |
| `splitdpi logs [-f]` | Günlüğü gösterir; `-f` izlemeyi sürdürür. |
| `splitdpi apps list\|enable ID\|disable ID` | Engel aşmanın hangi uygulamalara uygulanacağını seçer. |
| `splitdpi apps show ID\|new ID NAME\|add-host ID DOMAIN\|remove-host ID DOMAIN\|reset ID` | Bir uygulamayı gösterir, düzenler ya da kendinizinkini ekler. |
| `splitdpi sites list\|add DOMAIN\|remove DOMAIN\|import FILE` | Kendi engelli sitelerinizi yönetir. |
| `splitdpi exclude list\|add DOMAIN\|remove DOMAIN` | Engel aşmanın hiç dokunmadığı siteleri yönetir. |
| `splitdpi dns [off\|auto\|cloudflare\|google\|quad9\|adguard] [--dot]` | Şifreli DNS'i ayarlar ya da gösterir. |
| `splitdpi strategy list\|set ID` | Hazır stratejileri listeler ya da birini seçer. |
| `splitdpi detect [--asn N] [--country CC]` | Sağlayıcınızı ve onun hazır stratejilerini bulur. |
| `splitdpi config [get KEY\|set KEY VALUE\|reset KEY]` | `lang`, `run_mode`, `mode`, `skip_ifaces` (`'wg*,tun*'` gibi virgüllü liste), `ipinfo_url` ve `qnum`'u (NFQUEUE numarası, başka program kullanmamalı) gösterir ya da değiştirir. |
| `splitdpi presets list\|update` | Hazır ayar verisini gösterir ya da SHA-256'sını doğrulayıp günceller. |
| `splitdpi engine install [--from FILE]` | Sabitlenmiş zapret2 sürümünü SHA-256'sını doğrulayıp kurar. |
| `splitdpi deps [install [--yes]]` | Eksik paketleri listeler ya da kurar. |
| `splitdpi takeover [--yes]` | Başka bir zapret ya da zapret2 kurulumundan SplitDPI'ye geçer. |
| `splitdpi revert [--purge]` | SplitDPI'nin eklediği her şeyi kaldırır; `--purge` ayarları ve veriyi de. |
| `splitdpi uninstall [--yes]` | SplitDPI'nin kendisini kaldırır. |
| `splitdpi version` | Sürümü yazar. |

**2026-11-17'de kaldırılanlar:** `engine status` (`status` ve `doctor` motoru
gösterir), `dns on` (`dns auto` kullanın) ve `apps udp` (hiçbir uygulamanın
ses ya da yayın profili yok).

## Güvenlik notları

- **Kurulum betiği:** `install.sh` GitHub'dan yalnız https ile indirir (curl
  düz http'yi yönlendirmede de reddeder ve TLS 1.2 ya da yenisini ister) ve
  ikiliyi ancak yayının `sha256sums.txt` dosyasındaki satırıyla tutarsa kurar.
  Dizinler umask 022 ile oluşturulur. curl yoksa wget'e düşer; wget https'i
  zorlamaz.
- **İndirmeler:** zapret2 motoru, SHA-256'sı denetlenen sabit bir sürümdür;
  hazır ayar güncellemesi `presets.tar.gz.sha256` tutmazsa reddedilir.
- **Root servisi yalnız root'un değiştirebildiği bir ikiliyi çalıştırır:**
  uygunsa `enable`'ı çalıştıran ikiliyi, değilse `/usr/local/bin/splitdpi`'yi.
  İkili ve üstündeki her dizin, sembolik bağlantılar boyunca, root'a ait
  olmalı ve grubu ya da başkaları yazamamalı; ikili `/tmp`, `/var/tmp`,
  `/dev/shm` ya da bir ev dizini (`/home`, `/var/home`, `/root`,
  `/var/roothome`) altında olmamalı. Hata, sorunlu dosyayı adlandırır ve
  düzelten `sudo install` komutunu verir.
- **Hazır ayar alan adları doğrulanır:** hazır ya da sizin, her uygulama
  profilindeki her alan adı düz bir ana makine adı olmalıdır (harf, rakam,
  tire, en çok 253 bayt); buna uymayan profil bir uyarıyla atlanır. `scan`,
  root olarak çalışan blockcheck2'ye vermeden önce her alan adını yeniden
  denetler.
- **Güvenlik duvarı:** SplitDPI kurallarını kendi nftables tablosunda,
  `inet splitdpi`'de tutar ve kural setinizi asla temizlemez. Kuralları açık
  kalır: motor durursa trafik değiştirilmeden geçer.

## Kaldırma

`sudo splitdpi revert` SplitDPI'yi durdurur, servisini, tablosunu ve çalışma
dosyalarını kaldırır, sonra geride bir şey kalmadığını denetler; temiz
olduğunu söyler ya da kalanları listeler. `--purge` ayarları ve veriyi de
siler. `takeover`'ın kapattığı zapret kurulumunun servisi geri açılır. Bozuk
bir yapılandırma revert'i engelleyemez.

`sudo splitdpi uninstall` neyi kaldıracağını listeler, hiçbir şeyi
değiştirmez; `uninstall --yes`, `revert --purge`'ün yaptığını yapıp günlüğü,
`/usr/local/bin/splitdpi`'yi ve yalnız root'un değiştirebildiği durumda onu
çalıştırdığınız ikiliyi (örneğin `SPLITDPI_PREFIX` ile kurulanı) siler. Geride
bir şey kalırsa ikililer durur, böylece komutu yeniden çalıştırabilirsiniz.
Terminal arayüzünde Ayarlar aynısını yapar.

## Teşekkürler ve lisans

- bol-van'ın [zapret2](https://github.com/bol-van/zapret2)'si (MIT): motor.
  SplitDPI onu indirir, yeniden dağıtmaz.
- Çağrı Taşkın'ın [SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey)'i
  (MIT): sağlayıcı stratejileri, istisna listesi ve Roblox adresleri;
  [erdometo/zapret-turkey-configs](https://github.com/erdometo/zapret-turkey-configs)
  (MIT) bunları karşılaştırmak için.
- [Flowseal/zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube)
  (MIT): Discord ve YouTube adresleri ve Rusya istisna listesi.
- [dmfrpro/zapret2-flake](https://github.com/dmfrpro/zapret2-flake) (MIT):
  yerleşik blob yedek yolu.
- [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community)
  (MIT): Instagram, Facebook, WhatsApp, X, Signal, Viber, LinkedIn, TikTok ve
  Pastebin adresleri ile eklenen Telegram, YouTube ve Discord adresleri.
- [1andrevich/Re-filter-lists](https://github.com/1andrevich/Re-filter-lists)
  (MIT): bir TikTok adresi.
- [itdoginfo/allow-domains](https://github.com/itdoginfo/allow-domains)
  (lisans yok; yalnız alan adları, kod ya da veri dosyası kopyalanmadı): VPN
  sağlayıcı ve Threads adresleri ile Telegram, X, TikTok, WhatsApp, Roblox ve
  Facebook'a eklenen adresler.
- [OONI](https://explorer.ooni.org) ve
  [EngelliWeb](https://ifade.org.tr/engelliweb/): yeni uygulamalardan hangisinin
  Türkiye'de engelli olduğu.
- alimali54/zapret-win-turkey: Türkiye zapret2 stratejilerinin kökeni.

Lisanslar için [NOTICE](NOTICE). SplitDPI MIT lisanslıdır.

SplitDPI bilgisayarınızın bazı ağ trafiğini nasıl gönderdiğini değiştirir.
Bunun serbest olup olmadığı bulunduğunuz yere bağlıdır; size uygulanan
kurallara bakın. SplitDPI'yi kendi sorumluluğunuzda, hiçbir garanti olmadan
kullanırsınız.
