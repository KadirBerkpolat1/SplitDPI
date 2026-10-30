# SplitDPI

SplitDPI, internet sağlayıcınız Discord, YouTube, Roblox gibi uygulamaları
derin paket incelemesiyle (DPI) engellediğinde bunları Linux'ta yeniden
çalıştırır. Sağlayıcınızda çalışan bir [zapret2](https://github.com/bol-van/zapret2)
stratejisi bulur, onu istediğinizde ya da servis olarak çalıştırır ve tek
komutla geride iz bırakmadan kaldırır.

**Durum:** v0.2.6. v0.1.0 gerçek bir Türk Telekom bağlantısında ve Debian,
Fedora, Alpine sanal makinelerinde denendi. v0.2.0'dan bu yana gelen
özellikler otomatik testlerle ve altı dağıtımda çalışan CI ile sınanıyor; gerçek
bir bağlantıda ya da sanal makinelerde yeniden denenmedi. Ayrıntılar
[yayın kontrol listesinde](docs/release-checklist.md).

[English](README.md)

Bugün keift/zapret kullanıyorsanız SplitDPI onun yerini alır. Ömrünü
tamamlamış zapret v1 yerine zapret2 çalıştırır, uzun bir blockcheck2
taramasından önce sağlayıcı hazır ayarlarını dener, DNS ve güvenlik duvarı
ayarlarınıza dokunmaz; değiştirilemez (immutable) dağıtımlarda ve SELinux
zorlayıcı modda da çalışır. Kurulu bir zapret ya da zapret2'den geçmek için
`splitdpi takeover` yeterli.

## Desteklenen sistemler

| Dağıtım | Init | Servis modu | İstendiğinde | Denendi |
|---|---|---|---|---|
| Debian | systemd | evet | evet | Sanal makinede 13.7: yeniden başlatma sonrası servis modu ve revert (2026-10-29) |
| Ubuntu LTS, Linux Mint | systemd | evet | evet | henüz denenmedi |
| Fedora (Workstation, Cloud) | systemd | evet | evet | Sanal makinede 44 Cloud, SELinux zorlayıcı modda: yeniden başlatma sonrası servis modu ve revert (2026-10-29) |
| Fedora Atomic (Silverblue, Bazzite) | systemd | evet | evet | henüz denenmedi |
| CachyOS | systemd | evet | evet | Türk Telekom bağlantısında: `scan` (hazır ayarlar ve `--site`), Discord, Roblox, Pastebin ve diğerlerinde `probe`, `revert` (2026-10-29) |
| Arch, EndeavourOS | systemd | evet | evet | henüz denenmedi |
| openSUSE Tumbleweed | systemd | evet | evet | henüz denenmedi |
| Alpine | OpenRC | evet | evet | Sanal makinede 3.24.3: yeniden başlatma sonrası servis modu ve revert (2026-10-29) |
| Gentoo | OpenRC | evet | evet | henüz denenmedi |
| Void (glibc ve musl) | runit | evet | evet | henüz denenmedi |
| Artix, antiX, dinit, s6 veya sysvinit dağıtımları | diğer | hayır: istendiğinde çalıştırın | evet | henüz denenmedi |

SplitDPI, nftables destekli 5.4 veya daha yeni bir x86_64 ya da arm64 çekirdek
ile `nft` ve `curl` komutlarını ister. `splitdpi doctor` bunların hepsini
denetler ve paket yöneticinize uygun kurulum komutunu yazar. NixOS
kullanıcıları zapret2-flake veya zapret2-nix'e bakmalı.

## Hızlı başlangıç

```sh
curl -fsSL https://raw.githubusercontent.com/KadirBerkpolat1/SplitDPI/main/install.sh | sudo sh
sudo splitdpi
```

Kurulum betiği indirdiği dosyanın sağlama toplamını doğrular ve programı
`/usr/local/bin/splitdpi` olarak kurar. `sudo splitdpi` terminal arayüzünü
açar; kurulumu başlatmak için `w` tuşuna basın. Kurulum sisteminizi denetler,
zapret2 motorunu kendisi kurar, sağlayıcınızı bulur, hangi uygulamaların
çalışması gerektiğini sorar, stratejileri dener ve SplitDPI'yi hemen ya da her
açılışta çalıştırır.

Engel aşmayı açık tutmanın üç yolu var. Arayüz penceresinde (`r` ya da
`splitdpi run`) çalışırsa pencere kapanınca durur. Bu oturum için arka planda
(`b` ya da `sudo splitdpi run --background`) çalışırsa terminal kapansa da
sürer, bilgisayar yeniden başlayınca kapanır; arayüz engel aşmayı kendisi
çalıştırırken çıkarsanız onu arka plana almayı önerir. Her açılışta
(`sudo splitdpi enable`) servis olarak çalışır. Durum sayfasındaki `r`
tuşunun bunlardan hangisini kullanacağı Ayarlar → Çalıştırma biçimi'nden
seçilir; seçmek yalnızca tercihi kaydeder, komutlar olduğu gibi kalır.
Hangisi olursa olsun `sudo splitdpi stop` durdurur.

## Terminal arayüzü

- **Üstte:** adı ve sürümü, sağda da engel aşmanın çalışıp çalışmadığını ya
  da hata verip vermediğini gösteren durum etiketi. Etikete tıklamak engel
  aşmayı Durum sayfasındaki gibi başlatır ya da durdurur.
- **Solda:** menü: Durum, Siteler, Strateji, Tarama, Günlük ve Ayarlar. Ok
  tuşlarıyla ya da fareyle tıklayarak seçin; Enter veya Tab sayfaya girer, Esc
  menüye döner.
- **Sağda:** seçilen sayfa. Ayarlar sayfası her ayarı değeriyle birlikte
  gruplar halinde listeler: Genel (dil, çalıştırma biçimi), Engel aşma
  (kapsam, hariç tutulan siteler, strateji), DNS, Ağ (dokunulmayacak
  arayüzler, sağlayıcıyı bulan servis), Bakım (hazır ayarları güncelleme,
  motoru kurma, sistemi denetleme, zapret'ten geçme ya da ona geri dönme,
  kaldırma) ve Gelişmiş (kuyruk numarası). `/` listede arar; Esc aramayı
  temizler.
- **Altta:** son işin sonucu; altında sayfanın işleri, tuşlarıyla birlikte
  düğmeler halinde (sağda `?` Yardım ve `q` Çık). Bir düğmeye tıklamak tuşuna
  basmakla aynı işi yapar. En altta seçili işi yapan komut durur; arayüzde
  yaptığınız her şeyi böylece bir betikle de yapabilirsiniz.

`?` tüm kısayolları listeler. Sistemi değiştiren her işten önce bir pencere
neyin değişeceğini ve nasıl geri alınacağını söyler; `e` (evet) ya da `h`
(hayır) ile yanıt verin, İngilizce arayüzde `y` ve `n`. Linux konsolunda ya da
UTF-8 olmayan bir yerel ayarda arayüz düz ASCII karakterlerle çizilir. En az
80x24 boyutunda bir terminal gerekir.

## Komutlar

| Komut | Ne yapar |
|---|---|
| `splitdpi` | Terminal arayüzünü açar. |
| `splitdpi doctor` | Sistemi denetler ve SplitDPI'yi engelleyen her şeyi listeler. |
| `splitdpi engine install [--from DOSYA]` / `engine status` | Sabitlenmiş zapret2 sürümünü SHA-256'sını doğrulayarak kurar ya da kurulu olanı gösterir. |
| `splitdpi detect [--asn N] [--country ÜLKE]` | Sağlayıcınızı bulur ve onun hazır stratejilerini listeler. |
| `splitdpi scan [--site ALAN] [--dns keep\|auto\|SAĞLAYICI] [--apps ID,ID] [--asn N] [--country ÜLKE] [--blockcheck-only\|--no-blockcheck] [-v]` | Çalışan bir strateji bulur ve kaydeder; bkz. [Strateji bulma](#strateji-bulma). |
| `splitdpi probe [--apps ID,ID] [-v]` | Açık uygulamaların sitelerine ve kendi sitelerinize erişilip erişilemediğini dener; erişilemeyenin nedenini söyler. |
| `splitdpi strategy list\|set ID` | Hazır stratejileri listeler ya da tarama yapmadan birini seçer. |
| `splitdpi run` | Bu terminalde çalışır; Ctrl-C durdurur ve kurallarını kaldırır. |
| `splitdpi run --background` | Terminal kapansa da bilgisayar yeniden başlayana kadar çalışır; çıktısı `/var/log/splitdpi.log` dosyasına yazılır. |
| `splitdpi enable` / `disable` | Servisi kurup başlatır ya da durdurup kaldırır. |
| `splitdpi stop` / `status` / `logs [-f]` | Durdurur, durumunu ya da günlüğünü gösterir. |
| `splitdpi apps list\|enable ID\|disable ID` | Engel aşmanın hangi uygulamalara uygulanacağını seçer. |
| `splitdpi sites list\|add ALAN\|remove ALAN\|import DOSYA` | Engel aşmanın her zaman uygulandığı kendi engelli sitelerinizi yönetir. |
| `splitdpi exclude list\|add ALAN\|remove ALAN` | Engel aşmanın hiç dokunmayacağı siteleri yönetir. |
| `splitdpi takeover [--yes]` | Kurulu başka bir zapret ya da zapret2'yi bırakıp SplitDPI'ye geçer. |
| `splitdpi dns [off\|auto\|cloudflare\|google\|quad9\|adguard] [--dot]` | Şifreli DNS'i açar ya da kapatır; argüman verilmezse geçerli seçimi gösterir. |
| `splitdpi config [get ANAHTAR\|set ANAHTAR DEĞER\|reset ANAHTAR]` | Ayarları varsayılanlarıyla listeler ya da birini gösterir, değiştirir veya varsayılana döndürür. |
| `splitdpi presets list\|update` | Hazır ayar verisini gösterir ya da günceller. |
| `splitdpi revert [--purge]` | SplitDPI'nin eklediği her şeyi kaldırır. |
| `splitdpi uninstall [--yes]` | SplitDPI'yi, dosyalarını ve programını kaldırır. |
| `splitdpi version` | Sürümü yazar. |

`--help`, `version` ve `presets list` dışındaki tüm komutlar root ister. Dili
seçmek için `--lang tr` ya da `--lang en` ekleyin; eklemezseniz SplitDPI
terminal arayüzünde Ayarlar'dan seçilen dili, o da yoksa `LANG` değişkenini
kullanır.

`splitdpi config`, Ayarlar sayfasındaki ayarları kapsar: `lang`, `run_mode`
(`window`, `background`, `boot`), `mode` (`broad` öğrendiği tüm engelli
sitelere ve sizin sitelerinize, `apps` yalnızca açık uygulamalara ve sizin
sitelerinize uygulanır), `skip_ifaces` (hiç dokunulmayacak arayüzler,
`'wg*,tun*'` gibi virgüllü liste), `ipinfo_url` (sağlayıcınızı bulan https
servisi) ve `qnum` (NFQUEUE numarası, 0-65535; başka bir program
kullanmamalı). Çalışan SplitDPI `mode`, `skip_ifaces` ve `qnum`
değişikliklerini yeniden başlayınca uygular. Şifreli DNS `splitdpi dns`
altında kalır.

## Kendi siteleriniz

İhtiyacınız olan bir site engelliyse ve hiçbir uygulama profili onu
kapsamıyorsa siteyi ekleyin. SplitDPI o siteyi ilk paketten itibaren işler;
`probe` ve `scan` da onu dener:

```sh
sudo splitdpi sites add pastebin.com
```

Çok sayıda siteyi bir kerede eklemek için onları her satıra bir tane gelecek
şekilde bir metin dosyasına yazın ve `sudo splitdpi sites import DOSYA`
komutunu çalıştırın. `#` ile başlayan satırlar açıklama sayılır;
`*.ornek.com` ve `.ornek.com`, `ornek.com` olarak eklenir. Alan adı olmayan
satırlar atlanır ve listelenir. Terminal arayüzünün Siteler sayfasında
uygulamaları açıp kapatabilir, site ekleyip çıkarabilirsiniz. Değişikliğin
geçerli olması için SplitDPI'yi yeniden başlatın.

## zapret ya da zapret2'den geçiş

`sudo splitdpi takeover`, keift'in kurulum betiğinin kurduğu gibi başka bir
zapret ya da zapret2 kurulumu arar: `/opt` altındaki dizinine, servisine ve
çalışan süreçlerine bakar. Geçişin neleri değiştireceğini gösterir ama hiçbir
şeyi değiştirmez. `sudo splitdpi takeover --yes` ise o kurulumun site
listesindeki siteleri sizin sitelerinize ekler, servisini durdurur ve
açılışta başlamasını kapatır. O kurulumun dosyalarını asla silmez ya da
değiştirmez; `sudo splitdpi revert` servisini yeniden açar. Böyle bir araç
hâlâ çalışıyorsa `run` başlamayı reddeder, çünkü ikisi aynı trafiği işlemeye
çalışır.

## Strateji bulma

`sudo splitdpi scan` önce sağlayıcınızın hazır stratejilerini uygulamalarınızda
ve sitelerinizde dener; hiçbiri çalışmazsa blockcheck2'yi çalıştırır. Çalışan
stratejiyi kaydeder.

- **Tek bir site.** Hazır ayarları atlayıp tek bir site için strateji aramak
  isterseniz `sudo splitdpi scan --site pastebin.com` komutunu çalıştırın ya da
  Tarama sayfasında "Tek siteyi blockcheck2 ile tara" seçeneğini seçin. blockcheck2 o
  siteyi IPv4 üzerinden, bilgisayarınızda IPv6 varsa IPv6 üzerinden de dener.
  Sonuçları, biri kaydedilmeden önce uygulamalarınızda ve sitelerinizde
  yeniden denenir: SplitDPI hepsini açan en basit stratejiyi, mümkünse hem
  IPv4 hem IPv6'da çalışanı kaydeder ve siteyi sitelerinize ekler. Bu, hazır
  ayarları denemekten daha uzun sürer.
- **Zehirlenmiş DNS.** blockcheck2 çalışmadan önce `scan`, sağlayıcınızın
  DNS'inin onun alan adları için zehirlenmiş yanıt verip vermediğini denetler.
  Öyleyse şifreli DNS'in (otomatik) açılıp açık kalmasını isteyip
  istemediğinizi sorar; terminal arayüzü Cloudflare'i, başka bir sağlayıcıyı
  ya da hayır seçeneğini sunar. Soruyu yanıtlayacak kimse yoksa açmak için
  `--dns auto` (ya da bir sağlayıcı), onsuz devam etmek için `--dns keep`
  verin.
- **Diğer seçenekler.** `--apps` yalnızca verilen uygulamaları dener, `--asn`
  ve `--country` sağlayıcı aramasını atlar, `--blockcheck-only` hazır ayarları
  atlar, `--no-blockcheck` onlardan sonra durur, `-v` ise başarısız her
  denemenin ham hatasını gösterir.
- **Neden açılmadı.** `scan` ve `probe` bir sitenin neden açılmadığını
  (bağlantı kesildi, zaman aşımına uğradı, DNS ya da güvenli bağlantı
  başarısız oldu, ya da siteden başka bir şey yanıt verdi) anlaşılır bir dille
  söyler ve ne denemeniz gerektiğini yazar. Hem sizin DNS'inize hem şifreli
  DNS'e göre kendi adresi olmayan bir alan adı (yalnızca alt alan adları
  kullanılıyor, hiç yok ya da bu bilgisayarı gösteriyor) test edilmedi olarak
  gösterilir ve başarısızlık sayılmaz; `probe` sonunda kaç sitenin açıldığını,
  kaçının engelli olduğunu ve kaçının test edilmediğini yazar. Terminal
  arayüzünün Durum sayfası aynı sayıları ve en fazla üç engelli siteyi
  gösterir; tüm sonuçlar Siteler sayfasındadır.
- **Sonucu paylaşma.** `scan` bir strateji kaydettiğinde,
  [SplitDPI-presets](https://github.com/KadirBerkpolat1/SplitDPI-presets)
  deposunda önceden doldurulmuş bir issue açan bir bağlantı yazar: AS
  numaranız, sağlayıcınız, ülkeniz, strateji ve sürümler. Terminal arayüzü de
  aynı bağlantıyı gösterir, `c` ile kopyalar ve çıkarken bir kez daha yazar.
  Siz issue'yu
  göndermedikçe hiçbir şey gönderilmez; içinde IP adresiniz ya da kendi
  siteleriniz hiçbir zaman yer almaz.

`splitdpi strategy list` tüm hazır stratejileri gösterir ve kullanılanı
işaretler; `sudo splitdpi strategy set ID` tarama yapmadan başka birini seçer.
Terminal arayüzünün Strateji sayfası da aynı işi görür. Değişikliğin geçerli
olması için SplitDPI'yi yeniden başlatın.

## Bir site bozuldu

Bankalar, devlet siteleri ve bazı alışveriş siteleri değiştirilmiş trafiği
reddeder. Siteyi hariç tutun ve SplitDPI'yi yeniden başlatın:

```sh
sudo splitdpi exclude add https://www.ornek-banka.com.tr/
```

Terminal arayüzünde Siteler sayfasında `e` tuşuna basın.

SplitDPI, yaygın Türk siteleri için hazır hariç tutma listeleriyle gelir.

## Şifreli DNS

Bazı sağlayıcılar DNS yanıtlarını zehirler; `doctor` ve `probe` bunu tespit
eder. Şifreli DNS açıkken SplitDPI, 53. porttaki trafiği kendi içindeki bir
ileticiden şu sağlayıcılardan birine gönderir:

| Seçim | Ne yapar |
|---|---|
| `auto` | Sırayla Cloudflare, Google ve Quad9 denenir; ilk yanıt veren kullanılır. |
| `cloudflare`, `google` | Filtreleme yapmaz. |
| `quad9` | Bilinen zararlı yazılım ve oltalama alan adlarını da engeller. |
| `adguard` | Reklamları ve izleyicileri de engeller. |

Varsayılan protokol DNS-over-HTTPS'tir; `--dot` DNS-over-TLS'i seçer. DoT 853
numaralı portu kullanır ve bazı sağlayıcılar bu portu engeller.
`sudo splitdpi dns off` şifreli DNS'i kapatır. Seçim bir sonraki başlatmada
geçerli olur; terminal arayüzünde Ayarlar sayfası aynı seçenekleri sunar.

- **Sistem ayarları olduğu gibi kalır.** SplitDPI sistemin DNS ayarlarını hiç
  değiştirmez: hiçbir çözümleyici yapılandırma dosyasına ya da servisine
  dokunmaz; yönlendirme `inet splitdpi` tablosuyla birlikte kaybolur.
- **Karar yerel çözümleyicinin.** Bu bilgisayardaki çözümleyicilere
  (`127.0.0.0/8`, `::1`) giden sorgular hiç yönlendirilmez; programlar
  systemd-resolved'ın yerel adresini (127.0.0.53), dnscrypt-proxy'yi ya da
  başka bir yerel çözümleyiciyi eskisi gibi, onun kurallarıyla kullanır.
  Yalnızca bilgisayardan 53. port üzerinden çıkan DNS yönlendirilir. DNS'i
  zaten şifreleyen bir çözümleyici 53. porttan bir şey göndermez; bu durumda
  SplitDPI'nin DNS'i yalnızca doğrudan dışarıdaki bir sunucuya soran programları
  etkiler.
- **Zaten şifreliyse.** dnscrypt-proxy, Stubby, NextDNS, AdGuard Home, TLS
  üzerinden yönlendiren Unbound ya da Knot Resolver veya DNS-over-TLS açık
  systemd-resolved kullanılıyorsa `doctor` bunu söyler, `splitdpi dns` ve
  Ayarlar SplitDPI'nin DNS'ine gerek olmadığını belirtir (yine de açılabilir),
  `scan` ise `--dns` verilmedikçe zehirlenme sorusunu atlar.
- **VPN'ler ve yerel adlar.** VPN arayüzlerinden (config.toml'daki
  `skip_ifaces`) geçen DNS'e dokunulmaz; böylece VPN'in ayrık DNS'i çalışmaya
  devam eder. Yerel ağ adları sağlayıcıya hiç gitmez, ağınızın her zamanki DNS
  sunucusuna sorulur: tek parçalı adlar, `.lan`, `.local`, `.home.arpa`,
  `.internal`, `.localdomain`, arama alan adlarınız ve özel adreslerin ters
  sorguları.
- **Yine de değişenler.** `quad9` ya da `adguard` seçiliyse engelledikleri alan
  adları çözülmez. Otel, havalimanı gibi giriş sayfası açan Wi-Fi ağlarında,
  giriş sayfası görünmezse DNS'i kapatın (`sudo splitdpi dns off`) ve
  SplitDPI'yi yeniden başlatın.

## Nasıl çalışır, ne beklemeli

SplitDPI güvenlik duvarı kurallarını kendi nftables tablosunda, `inet splitdpi`
içinde tutar. Kural setinizi asla temizlemez; güvenlik duvarı, DNS ya da ağ
yapılandırma dosyalarını değiştirmez. Kuralları açık kalacak şekilde çalışır:
nfqws2 durursa trafik değiştirilmeden geçer.

- **Yeni siteleri öğrenme.** Açtığınız uygulamalar ilk paketten itibaren
  işlenir. Diğer siteler öğrenilir: yeni engellenen bir siteye ilk ziyaretler
  (varsayılan üç) başarısız olur, sonrakiler çalışır. Bir site açılmazsa
  yeniden deneyin.
- **Telegram.** Yalnızca Telegram'ın web ve API alan adları kapsanır.
  Uygulamaların doğrudan IP adreslerine yaptığı MTProto bağlantıları kapsanmaz.
- **Aramalar ve uygulama protokolleri.** Instagram, Messenger, WhatsApp ve
  Signal'daki sesli ve görüntülü aramalar, Roblox oyun sunucuları ve WhatsApp
  ile Viber telefon uygulamalarının kendi bağlantıları TLS alan adı taşımaz; bu
  yüzden kapsanmaz. Siteler sayfası bunu her uygulamanın yanında belirtir.
- **Discord sesli görüşme.** Ses hazır ayarı uygulanır ama henüz gerçek bir
  görüşmede doğrulanmadı. `scan` bunu belirtir; sizde çalışıp çalışmadığını
  bildirin.

## Geri alma güvencesi

`sudo splitdpi revert`, SplitDPI'yi durdurur, servisini kaldırır, tablosunu ve
çalışma dosyalarını siler, sonra geride bir şey kalmadığını denetler. Sonunda
temiz olduğunu yazar ya da kalanları listeler. `--purge` ayarları ve verileri
de siler. `takeover` başka bir zapret kurulumunu kapattıysa geri alma onun
servisini yeniden açar. Geri alma yapılandırmayı hiç okumaz; bozuk bir ayar
dosyası onu engelleyemez. SplitDPI zorla sonlandırılırsa kuyruk kuralları
yerinde kalır ama siz `revert` çalıştırana ya da onu yeniden başlatana kadar
tüm trafiği geçirir.

## SplitDPI'yi kaldırma

`sudo splitdpi uninstall` neleri sileceğini listeler, hiçbir şeyi değiştirmez.
`sudo splitdpi uninstall --yes`, `revert --purge` ile aynı işi yapar, ardından
günlüğü ve `/usr/local/bin/splitdpi` dosyasını siler; silemediği bir şey
kalırsa onu listeler, sorunu giderdikten sonra komutu yeniden çalıştırın.
Terminal arayüzünde Ayarlar sayfası da listeyi gösterdikten sonra aynı işi
yapar. Bu işlem geri alınamaz: SplitDPI'yi yeniden kullanmak için
[Hızlı başlangıç](#hızlı-başlangıç) bölümündeki gibi yeniden kurun.

## Ülkeler

SplitDPI ülkenizi biliyorsa kurulum, orada SNI ile engellenen uygulamaları
önceden seçer; SplitDPI'nin aşabildiği engel türü budur. Yalnızca Türkiye
gerçek bir bağlantıda denendi; diğer satırlar Ağustos–Ekim 2026 arasındaki
[OONI](https://explorer.ooni.org) ölçümlerine dayanır.

| Ülke | Önceden seçilen uygulamalar | SplitDPI ne yapabilir | Ne yapamaz |
|---|---|---|---|
| Türkiye | Discord, Roblox | Gerçek bir Türk Telekom hattında denendi. İki uygulama da DNS ve SNI ile engelli ([OONI](https://explorer.ooni.org/findings/267640924000)). | |
| Rusya | YouTube, Discord, WhatsApp, X, Viber | Kısmen, denenmedi: bu uygulamalar SNI ile engelli ([OONI](https://ooni.org/post/2024-russia-report)), ama çalışan stratejiler sağlayıcıya göre değişir ve sık bozulur. Düz DNS yanıtları değiştiriliyor, bu yüzden [şifreli DNS](#şifreli-dns)'i açın. | Telegram, Instagram ve Facebook IP adresiyle engelli. Mobil beyaz liste modu ([net4people](https://github.com/net4people/bbs/issues/516)) ve yurt dışı barındırmaya giden bağlantıların yaklaşık 16 KB'den sonra donması ([net4people](https://github.com/net4people/bbs/issues/490)) aşılamaz. |
| Özbekistan | TikTok | Denenmedi. TikTok SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030202526.825336_UZ_webconnectivity_052c7879728aa664)). | |
| Pakistan | Signal | Denenmedi. Signal SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030220511.996290_PK_signal_5ac96b6fcef513f4)). | Signal aramaları. Güvenlik duvarı TCP akışlarını birleştirdiği için basit bölme stratejileri işe yaramayabilir. |
| Umman | Signal, Viber | Denenmedi. İkisi de SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030153230.174668_OM_signal_587cc9ac388c198c)). | Signal ve Viber aramaları. |
| Myanmar | X, Signal | Denenmedi. İkisi de birkaç sağlayıcıda SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030164419.537459_MM_webconnectivity_04a4276a4ab2ba2c)). | Facebook, Instagram, Messenger ve WhatsApp IP adresiyle engelli ([OONI](https://explorer.ooni.org/m/20261030185034.005475_MM_webconnectivity_feef379ddff0b397)). Signal aramaları. |
| Tanzanya | Discord, Signal | Denenmedi. İkisi de SNI ile engelli ([OONI](https://explorer.ooni.org/m/20261030224510.394340_TZ_signal_ff0333dd53e6a624)). | X ve Telegram'ın web sitesi IP adresiyle engelli ([OONI](https://explorer.ooni.org/m/20261030115612.829938_TZ_webconnectivity_595b8e6a4a66486b)). Signal aramaları. |
| Suriye | Discord | Denenmedi. Discord iki sağlayıcıda SNI ile engelli; ölçüm sayısı az ([OONI](https://explorer.ooni.org/m/20261013164632.158025_SY_webconnectivity_9e07ffe0537f7f05)). | |

Rusya, Türkiye ve Özbekistan için ayrıca SplitDPI'nin dokunmadığı yerli
sitelerin (bankalar, devlet portalları gibi) bir listesi gelir.

İran ve Çin desteklenmiyor: oradaki engeller IP adreslerine ve izin listelerine
dayanır, SplitDPI bunları aşamaz.

## Hazır ayarlar nereden geliyor

Sağlayıcı stratejilerinin çıkış noktası Çağrı Taşkın'ın
[SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey) projesidir
(MIT). [erdometo/zapret-turkey-configs](https://github.com/erdometo/zapret-turkey-configs)
gibi MIT lisanslı başka derlemeler de stratejileri karşılaştırmak için
kullanılır. Her sağlayıcı dosyası, stratejilerinin gerçek bir bağlantıda en son
ne zaman çalıştığını `reported` alanında tutar; `scan`'in yazdığı bağlantılarla
gelen bildirimler bu dosyaları besler.

## Strateji bildirme

"Strategy report" şablonuyla bir issue açın. `splitdpi doctor` çıktısını,
sağlayıcınızın AS numarasını, hazır ayarı ya da stratejiyi ve
`splitdpi probe` çıktısını ekleyin.

## Sorumluluk reddi

SplitDPI, bilgisayarınızın bazı ağ trafiğini gönderme biçimini değiştirir.
Böyle bir aracı kullanmanın serbest olup olmadığı bulunduğunuz yere göre
değişir; size uygulanan kuralları kontrol edin. SplitDPI'yi kendi
sorumluluğunuzda kullanırsınız ve hiçbir garanti verilmez.

## Teşekkürler

- [zapret2](https://github.com/bol-van/zapret2), bol-van (MIT): motor.
  SplitDPI onu indirir; yeniden dağıtmaz.
- [SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey), Çağrı
  Taşkın (MIT): sağlayıcı stratejileri, hariç tutma listesi ve Roblox adresleri.
- [Flowseal/zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube)
  (MIT): Discord ve YouTube adresleri, Discord ses ayarları ve Rusya hariç
  tutma listesi.
- [dmfrpro/zapret2-flake](https://github.com/dmfrpro/zapret2-flake) (MIT): ek
  Discord ses portları.
- [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community)
  (MIT): Instagram, Facebook, WhatsApp, X, Signal, Viber, LinkedIn, TikTok ve
  Pastebin adresleri ile Telegram, YouTube ve Discord'a eklenen adresler.
- [1andrevich/Re-filter-lists](https://github.com/1andrevich/Re-filter-lists)
  (MIT): bir TikTok adresi.
- alimali54/zapret-win-turkey: Türkçe zapret2 stratejilerinin kaynağı.

Lisanslar için [NOTICE](NOTICE) dosyasına bakın. SplitDPI MIT lisanslıdır.
