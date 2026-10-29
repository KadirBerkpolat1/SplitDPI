# SplitDPI

SplitDPI, internet sağlayıcınız Discord, YouTube, Roblox gibi uygulamaları
derin paket incelemesiyle (DPI) engellediğinde bunları Linux'ta yeniden
çalıştırır. Sağlayıcınızda çalışan bir [zapret2](https://github.com/bol-van/zapret2)
stratejisi bulur, onu istediğinizde ya da servis olarak çalıştırır ve tek
komutla geride iz bırakmadan kaldırır.

**Durum:** v0.1.0, ilk herkese açık sürüm. Gerçek bir Türk Telekom
bağlantısında ve Debian, Fedora, Alpine sanal makinelerinde denendi; bkz.
[yayın kontrol listesi](docs/release-checklist.md).

[English](README.md)

Bugün keift/zapret kullanıyorsanız SplitDPI onun yerini alır. Ömrünü
tamamlamış zapret v1 yerine zapret2 çalıştırır, uzun bir blockcheck2
taramasından önce sağlayıcı hazır ayarlarını dener, DNS ve güvenlik duvarı
ayarlarınıza dokunmaz; değiştirilemez (immutable) dağıtımlarda ve SELinux
zorlayıcı modda da çalışır.

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
curl -fsSLO https://raw.githubusercontent.com/KadirBerkpolat1/SplitDPI/main/install.sh
sudo sh install.sh        # doğrular ve /usr/local/bin/splitdpi olarak kurar
sudo splitdpi             # kurulum sihirbazını açar
```

Sihirbaz sisteminizi denetler, sağlayıcınızı bulur, hangi uygulamaların
çalışması gerektiğini sorar, stratejileri dener ve SplitDPI'yi hemen ya da her
açılışta çalıştırır. Her ekran aynı işi yapan komutu da gösterir; isterseniz
betik yazabilirsiniz.

## Komutlar

| Komut | Ne yapar |
|---|---|
| `splitdpi` | Terminal arayüzünü açar. |
| `splitdpi doctor` | Sistemi denetler ve SplitDPI'yi engelleyen her şeyi listeler. |
| `splitdpi engine install [--from DOSYA]` | Sabitlenmiş zapret2 sürümünü SHA-256'sını doğrulayarak kurar. |
| `splitdpi detect` | Sağlayıcınızı bulur ve onun hazır stratejilerini listeler. |
| `splitdpi scan [--site ALAN]` | Önce hazır ayarları, sonra blockcheck2'yi dener ve çalışan stratejiyi kaydeder; `--site` yalnızca blockcheck2'yi, o tek sitede çalıştırır. |
| `splitdpi probe` | Açık uygulamaların sitelerine ve kendi sitelerinize erişilip erişilemediğini dener. |
| `splitdpi run` | Bu terminalde çalışır; Ctrl-C durdurur ve kurallarını kaldırır. |
| `splitdpi enable` / `disable` | Servisi kurup başlatır ya da durdurup kaldırır. |
| `splitdpi stop` / `status` / `logs [-f]` | Durdurur, durumunu ya da günlüğünü gösterir. |
| `splitdpi apps list\|enable ID\|disable ID` | Engel aşmanın hangi uygulamalara uygulanacağını seçer. |
| `splitdpi sites list\|add ALAN\|remove ALAN` | Engel aşmanın her zaman uygulanacağı kendi engelli sitelerinizi yönetir. |
| `splitdpi strategy list\|set ID` | Hazır stratejileri listeler ya da birini seçer. |
| `splitdpi exclude list\|add ALAN\|remove ALAN` | Engel aşmanın hiç dokunmayacağı siteleri yönetir. |
| `splitdpi presets list\|update` | Hazır ayar verisini gösterir ya da günceller. |
| `splitdpi dns on\|off` | DNS'i yerleşik DNS-over-HTTPS ileticisinden geçirir. |
| `splitdpi revert [--purge]` | SplitDPI'nin eklediği her şeyi kaldırır. |
| `splitdpi uninstall [--yes]` | SplitDPI'yi, dosyalarını ve programını kaldırır. |

`--help`, `version` ve `presets list` dışındaki tüm komutlar root ister. Dili
seçmek için `--lang tr` ya da `--lang en` ekleyin; eklemezseniz
SplitDPI `LANG` değişkenine uyar.

## Kendi siteleriniz ve strateji

İhtiyacınız olan bir site engelliyse ve hiçbir uygulama profili onu
kapsamıyorsa siteyi ekleyin. SplitDPI o siteyi ilk paketten itibaren işler;
`probe` ve `scan` da onu dener:

```sh
sudo splitdpi sites add pastebin.com
```

`splitdpi strategy list` tüm hazır stratejileri gösterir ve kullanılanı
işaretler; `sudo splitdpi strategy set ID` tarama yapmadan başka birini seçer.
İkisini de terminal arayüzünden yapabilirsiniz: Uygulamalar ekranı
sitelerinizi listeler, `t` tuşu strateji listesini açar. Değişikliğin geçerli
olması için SplitDPI'yi yeniden başlatın.

Hazır ayarları atlayıp tek bir site için strateji aramak isterseniz
`sudo splitdpi scan --site pastebin.com` komutunu çalıştırın ya da kurulum
sihirbazında "Bir siteyi blockcheck2 ile tara" seçeneğini seçin. blockcheck2 o
siteyi IPv4 üzerinden, bilgisayarınızda IPv6 varsa IPv6 üzerinden de dener.
blockcheck2'nin sonuçları, biri kaydedilmeden önce uygulamalarınızda ve
sitelerinizde yeniden denenir: SplitDPI hepsini açan en basit stratejiyi,
mümkünse hem IPv4 hem IPv6'da çalışanı kaydeder ve siteyi sitelerinize ekler.
Bu, hazır ayarları denemekten daha uzun sürer.

## Bir site bozuldu

Bankalar, devlet siteleri ve bazı alışveriş siteleri değiştirilmiş trafiği
reddeder. Siteyi hariç tutun ve SplitDPI'yi yeniden başlatın:

```sh
sudo splitdpi exclude add https://www.ornek-banka.com.tr/
```

SplitDPI, yaygın Türk siteleri için hazır hariç tutma listeleriyle gelir.

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
- **Discord sesli görüşme.** Ses hazır ayarı uygulanır ama henüz gerçek bir
  görüşmede doğrulanmadı. `scan` bunu belirtir; sizde çalışıp çalışmadığını
  bildirin.
- **DNS.** Bazı sağlayıcılar DNS yanıtlarını zehirler. `doctor` ve `probe`
  bunu tespit eder. `splitdpi dns on`, 53. porttaki trafiği SplitDPI içindeki
  bir DNS-over-HTTPS ileticisine yönlendirir; hiçbir çözümleyici ayarını
  değiştirmez ve tabloyla birlikte kaybolur. Açıkken VPN'in ayrık DNS'i ve
  yerel ağ adları çözülemeyebilir.

## Geri alma güvencesi

`sudo splitdpi revert`, SplitDPI'yi durdurur, servisini kaldırır, tablosunu ve
çalışma dosyalarını siler, sonra geride bir şey kalmadığını denetler. Sonunda
temiz olduğunu yazar ya da kalanları listeler. `--purge` ayarları ve verileri
de siler. Geri alma yapılandırmayı hiç okumaz; bozuk bir ayar dosyası onu
engelleyemez. SplitDPI zorla sonlandırılırsa kuyruk kuralları yerinde kalır
ama siz `revert` çalıştırana ya da onu yeniden başlatana kadar tüm trafiği
geçirir.

## SplitDPI'yi kaldırma

`sudo splitdpi uninstall` neleri sileceğini listeler, hiçbir şeyi değiştirmez.
`sudo splitdpi uninstall --yes`, `revert --purge` ile aynı işi yapar, ardından
günlüğü ve `/usr/local/bin/splitdpi` dosyasını siler; silemediği bir şey
kalırsa onu listeler.

## Hazır ayarlar nereden geliyor

Sağlayıcı stratejilerinin çıkış noktası Çağrı Taşkın'ın
[SplitWire-Turkey](https://github.com/cagritaskn/SplitWire-Turkey) projesidir
(MIT). [erdometo/zapret-turkey-configs](https://github.com/erdometo/zapret-turkey-configs)
gibi MIT lisanslı başka derlemeler de stratejileri karşılaştırmak için
kullanılır. Her sağlayıcı dosyası, stratejilerinin gerçek bir bağlantıda en son
ne zaman çalıştığını `reported` alanında tutar. `scan` çalışan bir stratejiyi
kaydettiğinde, [SplitDPI-presets](https://github.com/KadirBerkpolat1/SplitDPI-presets)
deposunda önceden doldurulmuş bir issue açan bir bağlantı yazar: AS numaranız,
sağlayıcınız, ülkeniz, strateji ve sürümler. Siz issue'yu göndermedikçe hiçbir
şey gönderilmez; içinde IP adresiniz ya da kendi siteleriniz hiçbir zaman
yer almaz.

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
  (MIT): Discord ve YouTube adresleri ile Discord ses ayarları.
- [dmfrpro/zapret2-flake](https://github.com/dmfrpro/zapret2-flake) (MIT): ek
  Discord ses portları.
- alimali54/zapret-win-turkey: Türkçe zapret2 stratejilerinin kaynağı.

Lisanslar için [NOTICE](NOTICE) dosyasına bakın. SplitDPI MIT lisanslıdır.
