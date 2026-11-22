<div align="center">

# Gowa

Modular and high-performance WhatsApp bot written in Go.

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/)
[![Library](https://img.shields.io/badge/Library-whatsmeow-25D366?style=flat-square&logo=whatsapp&logoColor=white)](https://github.com/tulir/whatsmeow)
[![Database](https://img.shields.io/badge/Database-SQLite%20(Pure%20Go)-003B57?style=flat-square&logo=sqlite&logoColor=white)](https://modernc.org/sqlite)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20macOS%20%7C%20Windows-555555?style=flat-square&logo=linux&logoColor=white)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-blue?style=flat-square)](LICENSE)

</div>

---

## Daftar Isi

- [Tentang Proyek](#tentang-proyek)
- [Fitur Utama](#fitur-utama)
- [Struktur Direktori](#struktur-direktori)
- [Daftar Perintah](#daftar-perintah)
- [Panduan Instalasi](#panduan-instalasi)
- [Konfigurasi](#konfigurasi)
- [Pengembangan](#pengembangan)
- [Lisensi](#lisensi)

---

## Tentang Proyek

**Gowa** adalah bot WhatsApp berbasis Go yang dirancang dengan fokus pada efisiensi resource, stabilitas eksekusi di grup dengan volume chat tinggi, serta kemudahan modularitas kode. Menggunakan library [whatsmeow](https://github.com/tulir/whatsmeow) sebagai antarmuka protokol WhatsApp dan driver SQLite murni (`modernc.org/sqlite`) yang tidak memerlukan toolchain CGO.

Respon perintah menggunakan format teks minimalis tanpa dekorasi berlebihan, memudahkan pembacaan pesan dan meminimalkan ukuran payload jaringan.

---

## Fitur Utama

- **Autentikasi Pairing Code**: Mendukung login akun menggunakan kode pairing 8-digit ke nomor telepon tanpa memerlukan pemindaian QR code.
- **Arsitektur Modular**: Perintah diorganisir dalam paket-paket terpisah (`cmd/<kategori>/`) dan didaftarkan otomatis melalui mekanisme `init()`.
- **Manajemen Beban & Concurrency**:
  - Filter prefix berbasis byte check untuk mengabaikan chat non-perintah secara instan.
  - Bounded worker pool untuk membatasi jumlah goroutine konkruen saat lalu lintas pesan tinggi.
  - Pre-warming cache informasi grup dan daftar anggota saat inisialisasi awal.
- **Penyimpanan SQLite Pure-Go**: Berjalan tanpa dependensi CGO dengan konfigurasi Write-Ahead Logging (WAL) dan in-memory page cache.
- **Mode Operasi (Self / Public)**: Bot dapat diatur untuk melayani publik atau hanya merespon nomor pemilik (owner).
- **Eksekusi Perintah Shell**: Mendukung eksekusi perintah terminal sistem secara langsung oleh owner terotorisasi.
- **Diagnostik Sistem**: Menyediakan pemantauan real-time status memori proses (RSS, Go Alloc, GC) dan informasi perangkat server.

---

## Struktur Direktori

```text
.
├── cmd/                          # Modul perintah aplikasi
│   ├── general/                  # Perintah umum (getpp, menu, ping)
│   ├── owner/                    # Perintah khusus owner (exec, mode)
│   ├── system/                   # Perintah diagnosa sistem (os, rss)
│   └── loader.go                 # Loader registrasi paket perintah
├── internal/                     # Komponen internal bot
│   ├── bot/                      # Engine bot, event handler, dan worker pool
│   ├── command/                  # Struct context, cache, dan command registry
│   ├── storage/                  # Konfigurasi JSON dan database SQLite
│   ├── instagram/                # Scraper Instagram: media dan profil akun
│   ├── pinterest/                # Scraper Pinterest: pin dan pencarian
│   ├── tiktok/                   # Scraper TikTok: media dan profil akun
│   ├── xscraper/                 # Penghubung CLI x-scraper-node untuk media X
│   └── util/                     # Logger dan diagnostik sistem
├── plugins/                      # Plugin Go yang dimuat otomatis saat start
│   ├── hello.go                  # Contoh plugin
│   ├── instagram.go              # Perintah .ig (unduh video/foto Instagram)
│   ├── igstalk.go                # Perintah .igstalk (informasi akun Instagram)
│   ├── tiktokdl.go               # Perintah .tiktok (unduh video/foto/audio)
│   ├── tiktokstalk.go            # Perintah .ttstalk (informasi akun TikTok)
│   ├── pin.go                    # Perintah .pin (unduh media) dan .pins (cari pin)
│   └── xdl.go                    # Perintah .x (unduh video/foto X/Twitter)
├── config.json                   # Berkas konfigurasi utama
├── go.mod                        # Definisi modul Go
├── go.sum                        # Checksum dependensi
└── main.go                       # Entry point aplikasi
```

---

## Daftar Perintah

| Kategori | Perintah | Alias | Akses | Deskripsi |
| :--- | :--- | :--- | :---: | :--- |
| **General** | `.getpp` | `.pp`, `.avatar` | Publik | Mengambil foto profil WhatsApp via reply, tag, nomor, atau profil sendiri |
| **General** | `.menu` | `.help` | Publik | Menampilkan daftar seluruh perintah yang tersedia |
| **General** | `.ping` | `.p` | Publik | Menampilkan latensi dan durasi aktif bot |
| **General** | Reaksi `❌` | `delete_reaction` | Publik / Admin | Menghapus pesan bot atau pesan grup (admin/owner) via reaksi emoji |
| **System** | `.os` | `.infoserver`, `.server` | Publik | Menampilkan spesifikasi hardware, OS, CPU load, dan uptime |
| **System** | `.rss` | `.ram`, `.mem` | Publik | Menampilkan alokasi memori runtime Go dan RSS proses |
| **Owner** | `.self/public` | `.self`, `.public` | Owner | Mengubah mode operasi bot (Self / Public) |
| **Owner** | `$ <command>` | - | Owner | Menjalankan perintah shell bash pada server |
| **Owner** | `.addthumb <nama>` | `.addthumbnail`, `.addthumbail` | Owner | Menyimpan metadata thumbnail link WhatsApp ke database |
| **Owner** | `.addfavicon <nama>` | `.addfav`, `.addfavico` | Owner | Menyimpan metadata favicon link WhatsApp ke database |
| **Owner** | `.getfile <path>` | `.gf`, `.ambilfile`, `.sendfile` | Owner | Mengirim file dari server bot sebagai dokumen |
| **Owner** | `.getplugins <command>` | `.gp` | Owner | Mengambil source plugin; tambahkan `-cat` untuk menampilkan langsung |
| **Owner** | `.addplugins <nama.go>` | `.addplugin`, `.newplugin` | Owner | Menyimpan source plugin dari reply teks/dokumen lalu memuatnya langsung |
| **Owner** | `.delplugins <command>` | `.delplugin`, `.deleteplugin`, `.removeplugin` | Owner | Menghapus berkas plugin dan melepas perintahnya dari registry |
| **Owner** | `.lock <nama bot>` | `.unlock` | Owner | Mengunci bot sementara; hanya perintah lock yang diproses saat terkunci |
| **Plugin** | `.hello` | `.hi`, `.halo` | Publik | Contoh perintah yang dimuat dari berkas plugin |
| **Download** | `.tiktok <link>` | `.tt`, `.ttdl` | Publik | Mengunduh video/foto TikTok; tambahkan `audio` untuk trek suara (`audio/mpeg`) |
| **Stalk** | `.ttstalk <username>` | `.stalktt`, `.ttinfo` | Publik | Menampilkan informasi publik akun TikTok (username, ID, statistik, bio) |
| **Download** | `.ig <link>` | `.igdl`, `.instagram` | Publik | Mengunduh video, foto, atau carousel dari link Instagram |
| **Stalk** | `.igstalk <username>` | `.igs`, `.iginfo` | Publik | Menampilkan informasi publik akun Instagram (nama, ID, verifikasi, statistik) |
| **Search** | `.igsearch <nama akun> [jumlah]` | `.igfind`, `.igsc` | Publik | Mencari postingan dari akun Instagram; bila nama akun tidak ditemukan, pencarian hashtag dipakai sebagai cadangan; hasil lebih dari satu media dikirim sebagai album |
| **Download** | `.facebook <link>` | `.fb`, `.fbdl` | Publik | Mengunduh video (HD/SD) atau foto/album dari link Facebook, `fb.watch`, atau kode share |
| **Download** | `.x <link>` | `.xdl`, `.twitter`, `.twdl` | Publik | Mengunduh video atau foto dari link X (Twitter); tambahkan `video`/`image` untuk memfilter media |
| **Download** | `.pin <link atau ID>` | `.pindl`, `.pinterest` | Publik | Mengunduh foto, GIF, video, atau carousel dari link Pinterest |
| **Search** | `.pins <kata kunci>` | `.pinsearch`, `.pinsc` | Publik | Mencari pin Pinterest berdasarkan kata kunci |
| **Experimental** | `.cfc <nomor> <teks>` | `.createfakechat`, `.fakechat` | Publik | Mengirim bundle group-history eksperimental (target harus anggota grup; sebaiknya baru join) |

### Thumbnail & Favicon Link

Thumbnail dan favicon untuk preview tautan disimpan sebagai metadata media WhatsApp, bukan sebagai berkas gambar. Paket `internal/thumb` mengunggah gambar apa pun sebagai media `thumbnail-link` (`whatsmeow.MediaLinkThumbnail`) dan mengembalikan field yang dibutuhkan pesan `ExtendedTextMessage`: `thumbnailDirectPath`, `thumbnailSHA256`, `thumbnailEncSHA256`, `mediaKey`, `mediaKeyTimestamp`, `thumbnailWidth`, `thumbnailHeight`, dan `mimetype`. Untuk thumbnail (bukan favicon) turut dibuat `JPEGThumbnail` berukuran lebar maksimal 120px kualitas 40 memakai `image/jpeg` standard library.

Perintah `.addthumb` dan `.addfavicon` menerima sumber dari salah satu jalur berikut:

1. **Reply gambar atau stiker** — media diunduh lalu diunggah ulang.
2. **Gambar dengan caption** — gambar pada pesan perintah itu sendiri.
3. **URL gambar** — diunduh dengan batas 10 MB dan diverifikasi sebagai gambar.
4. **Reply pesan link-preview** — metadata diambil langsung dari proto `ExtendedTextMessage` yang di-reply (favicon dari `faviconMmsMetadata`) sehingga tidak perlu upload ulang.

Nama disimpan unik per `(nama, jenis)`. Flag `-private` hanya berlaku untuk thumbnail dan mengeluarkannya dari pool acak; favicon selalu `random`. Waktu kedaluwarsa dibaca dari parameter `oe` (hex unix) pada URL media WhatsApp, dan `.addthumb` menolak metadata yang sudah kedaluwarsa.

Data disimpan pada tabel `thumbnails` di database bot (`db_path`) lewat `internal/storage`:

```go
store, _ := storage.OpenThumbStore(cfg.DBPath)
row, _ := store.Get("nama", thumb.KindThumbnail)   // metadata tertentu
row, _ = store.Random(thumb.KindThumbnail)         // acak, melewati yang expired
rows, _ := store.List(thumb.KindFavicon)
```

Field `metadata` berisi JSON `thumb.Meta`, siap dipakai untuk membangun `ExtendedTextMessage` (thumbnail) atau `faviconMmsMetadata` (`MMSThumbnailMetadata`).

`.menu` memakai mekanisme ini seperti `sendThumbnail` di base zapo-js: menu dikirim sebagai `ExtendedTextMessage` berisi tautan `https://wa.me/6283175676190`, judul bot, deskripsi jumlah perintah, isi menu di bawah URL, thumbnail `random` dari database, dan favicon `random` bila tersedia. Bila database belum punya thumbnail, menu otomatis dikirim sebagai teks biasa.

### Scraper Instagram

Logika pengambilan data Instagram berada di paket `internal/instagram` dan hanya memakai standard library Go.

Instagram mengunci hampir semua jalur publik: halaman profil (`/<username>/`) dan `api/v1/users/web_profile_info` selalu dialihkan ke halaman login, `?__a=1` membalas `201` kosong, dan `api/v1/users/<id>/info/` membalas error tanpa sesi. Yang masih disajikan tanpa login adalah **HTML hasil render sisi server untuk perayap**, jadi paket ini memakai `User-Agent` Googlebot:

1. **Post** `https://www.instagram.com/p/<kode>/` memuat blok `<script type="application/json" data-sjs>` berisi objek media lengkap (`xig_polaris_media.if_not_gated_logged_out`): `video_versions` (mp4 CDN tanpa watermark), `image_versions2` (semua kandidat resolusi), `carousel_media` untuk post multi-media, `caption`, `like_count`, `comment_count`, dan `taken_at`. Resistensi dipilih yang terbesar, lalu diunduh dengan `Referer: https://www.instagram.com/`.
2. **Profil** `https://www.instagram.com/<username>/embed/` memuat `contextJSON` (string JSON berlapis) berisi `username`, `full_name`, `verified`, `profile_pic_url`, `followers_count`, `posts_count`, dan `owner_id` (ID numerik akun).

**Pencarian** (`.igsearch`) memakai cookie `sessionid` dari `ig_sessionid`, karena Instagram tidak menyajikan hasil pencarian kepada pengunjung tanpa sesi. Alurnya meniru halaman profil akun (`/<username>/`) untuk mengambil postingan akun, dan meniru halaman `/explore/tags/<hashtag>/` sebagai cadangan:

1. Buka halaman tag dengan cookie sesi untuk mengambil token `lsd`, `csrf_token`, `fb_dtsg`, `hsi`, dan `claim` dari data bootstrap HTML. Token `claim` hanya ada untuk pengunjung yang login, jadi ketiadaannya dibalas `ErrSearchLoginRequired`.
2. Kirim `POST https://www.instagram.com/api/graphql` dengan `doc_id` query `PolarisKeywordSearchExplorePageRelayQuery`, variabel `{"query":"#<hashtag>"}`, serta header `X-IG-WWW-Claim`, `X-CSRFToken`, `X-FB-LSD`, dan `Origin`/`Sec-Fetch-*` agar diterima seperti permintaan browser. Permintaan tanpa header tersebut dibalas `error 1357054`.
3. Urai media dari `data.xdt_fbsearch__top_serp_graphql.edges[].node.items[]` memakai parser yang sama dengan `Fetch`, lalu unduh lewat `DownloadBytes`.

Tanpa `ig_sessionid` yang valid, perintah membalas pesan bahwa pencarian butuh login; `.ig` dan `.igstalk` tetap berjalan tanpa sesi.

Keterbatasan yang tidak bisa ditembus tanpa akun: bio, jumlah mengikuti, dan tautan bio tidak muncul di halaman embed maupun halaman post. Avatar juga hanya tersedia sebagai varian bertanda tangan `s150x150`; mengganti ukuran pada URL dibalas `403`, jadi foto profil yang dikirim beresolusi 150×150. Post privat atau yang sudah dihapus dibalas `ErrPostUnavailable`, sedangkan akun yang tidak ada dibalas `ErrUserNotFound`.

---

## Panduan Instalasi

### Prasyarat

- Go versi 1.21 atau yang lebih baru.
- Sistem operasi Linux, macOS, atau Windows.
- Akses terminal atau shell.
- Node.js v14+ dan folder `x-scraper-node` (khusus perintah `.x`, lihat [Scraper X](#scraper-x)).

### 1. Kloning Repositori

```bash
git clone https://github.com/jochris/Gowa.git
cd Gowa
```

### 2. Unduh Dependensi & Kompilasi

```bash
go mod tidy
go build -o gowa .
```

### 3. Tautkan Akun WhatsApp

Jalankan binary dengan menyertakan nomor telepon yang ingin digunakan (gunakan format kode negara tanpa simbol `+`):

```bash
./gowa -phone 6281234567890
```

Terminal akan menampilkan kode pairing 8 digit. Masukkan kode tersebut pada aplikasi WhatsApp:
1. Buka **Pengaturan** WhatsApp.
2. Pilih **Perangkat Tertaut** > **Tautkan Perangkat**.
3. Pilih opsi **Tautkan dengan nomor telepon saja**.
4. Masukkan kode 8 digit yang muncul di terminal.

Setelah berhasil terhubung, sesi akan tersimpan secara otomatis di `data/gowa.db`.

### 4. Menjalankan di Latar Belakang

Untuk menjalankan aplikasi secara terus-menerus sebagai background service pada Linux:

```bash
nohup ./gowa > gowa.log 2>&1 &
```

---

## Konfigurasi

Aplikasi membaca konfigurasi dari berkas `config.json`. Jika berkas belum tersedia, konfigurasi default akan dibuat secara otomatis saat pertama kali dijalankan.

`config.json` tidak ikut di-commit karena berisi nomor owner dan `ig_sessionid`. Setelah kloning, salin contohnya lalu isi nilainya:

```bash
cp config.example.json config.json
```

Contoh `config.json`:

```json
{
  "bot_name": "Gowa",
  "prefix": ".",
  "db_path": "data/gowa.db",
  "phone_number": "6281234567890",
  "log_level": "INFO",
  "is_public": true,
  "owner_numbers": [
    "6281234567890"
  ]
}
```

### Parameter Konfigurasi

| Parameter | Tipe Data | Default | Keterangan |
| :--- | :--- | :--- | :--- |
| `bot_name` | String | `"Gowa"` | Nama identitas bot |
| `prefix` | String | `"."` | Karakter awalan untuk pemanggilan perintah |
| `db_path` | String | `"data/gowa.db"` | Path penyimpanan berkas database SQLite |
| `phone_number` | String | `""` | Nomor telepon untuk inisialisasi pairing code |
| `log_level` | String | `"INFO"` | Tingkat logging terminal (`DEBUG`, `INFO`, `WARN`, `ERROR`) |
| `is_public` | Boolean | `true` | `true` untuk merespon semua user, `false` untuk mode owner-only |
| `owner_numbers` | Array | `[]` | Daftar nomor telepon yang memiliki hak akses perintah owner |
| `plugin_dir` | String | `"plugins"` | Direktori berisi berkas plugin yang dimuat otomatis |
| `ig_sessionid` | String | `""` | Nilai cookie `sessionid` akun Instagram; wajib diisi agar `.igsearch` bisa mencari, kosongkan bila tidak dipakai |

---

## Pengembangan

### Menambahkan Perintah Baru

Untuk menambahkan perintah baru, buat berkas Go pada direktori kategori yang sesuai (`cmd/<kategori>/`).

Contoh pembuatan `cmd/general/uptime.go`:

```go
package general

import (
	"fmt"
	"time"

	"gowa/internal/command"
)

var startTime = time.Now()

func init() {
	command.Register(&command.Command{
		Name:    "uptime",
		Cmd:     "uptime",
		Categ:   "general",
		Desc:    "Menampilkan durasi aktif aplikasi",
		Alias:   []string{"up"},
		Execute: handleUptime,
	})
}

func handleUptime(ctx *command.Context) error {
	duration := time.Since(startTime).Truncate(time.Second)
	return ctx.Reply(fmt.Sprintf("Uptime: %s", duration.String()))
}
```

Jika menambahkan folder kategori baru (misalnya `cmd/tools/`), tambahkan impor modul pada [`cmd/loader.go`](cmd/loader.go):

```go
import (
	_ "gowa/cmd/general"
	_ "gowa/cmd/owner"
	_ "gowa/cmd/system"
	_ "gowa/cmd/tools"
)
```

Perintah baru akan terdaftar secara otomatis ke dalam registry dan tampil pada daftar `.menu`.

### Plugin (Auto Load)

Selain perintah bawaan, bot memuat otomatis setiap berkas `.go` di direktori `plugin_dir` (default `plugins/`) saat start. Berkas plugin diinterpretasi dengan [yaegi](https://github.com/traefik/yaegi), jadi tidak perlu kompilasi ulang binary untuk menambah perintah.

Contoh `plugins/hello.go`:

```go
package main

import (
	"fmt"

	"gowa/internal/command"
	"gowa/internal/plugin"
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "hello",
		Cmd:   "hello",
		Categ: "plugin",
		Desc:  "Menjawab sapaan",
		Alias: []string{"hi", "halo"},
	}, handleHello)
}

func handleHello(ctx *command.Context) error {
	return ctx.Reply(fmt.Sprintf("Halo %s!", ctx.Sender.User))
}
```

Ketentuan plugin:

- Satu berkas = satu plugin, wajib berupa `package main`.
- Perintah didaftarkan lewat `plugin.Register` (biasanya di `init()`).
- `Name`, `Categ`, dan `Desc` opsional; `Categ` default `plugin` sehingga muncul sebagai kategori tersendiri pada `.menu`/`.help`.
- Perintah atau alias yang namanya sudah dipakai perintah bawaan/plugin lain akan ditolak agar tidak menimpa.
- Handler memakai `*command.Context` yang sama dengan perintah bawaan (`Reply`, `Send`, `Args`, `IsOwner`, dan seterusnya).

Karena `.menu` membaca langsung dari registry, perintah dari plugin otomatis muncul tanpa perlu menulis daftar perintah secara manual.

### Scraper TikTok

Logika pengambilan data TikTok berada di paket `internal/tiktok` dan hanya memakai standard library Go (`net/http`, `encoding/json`, `regexp`). Halaman `embed/v2` yang dipakai proyek JS acuannya diblokir WAF TikTok, begitu pula API mobile (`aweme/v1/feed` membalas `429 ratelimit triggered` atau feed rekomendasi karena tidak membawa tanda tangan `X-Argus`). Jalur yang dipakai sekarang:

1. **Halaman embed resmi** `https://www.tiktok.com/embed/<id>` sebagai sumber utama. Halaman ini memuat state `__FRONTITY_CONNECT_STATE__` berisi data lengkap: URL video tanpa watermark, `videoMeta` (durasi, lebar, tinggi), musik, statistik, waktu unggah, serta `imagePostInfo` untuk post foto. Tidak butuh tanda tangan, tidak kena rate limit, dan responsnya di bawah 1 detik.
2. **tikwm.com** sebagai cadangan metadata, dijalankan paralel bila halaman embed gagal dimuat. Layanan ini lambat (~10 detik) dan tidak menyediakan informasi resolusi.

Stream video dari halaman embed membawa watermark TikTok, jadi untuk post video URL-nya diganti dengan `play_addr` dari API mobile TikTok yang bersih. Permintaan itu berjalan paralel dengan pengambilan metadata; bila API mobile sedang membatasi (HTTP 429), dipakai URL `play` dari tikwm yang juga bersih. Gambar post foto dan trek audio tidak pernah berwatermark.

Post yang dipastikan tidak ada (HTTP 400 dari halaman embed) langsung dilaporkan sebagai error tanpa menunggu cadangan.

Untuk profil akun, `internal/tiktok/profile.go` memakai halaman `https://www.tiktok.com/@<username>/?lang=en`. Trailing slash dan parameter `lang` wajib dipakai: tanpa keduanya halaman profil ditolak WAF TikTok (respons 1.462 byte), sedangkan dengan keduanya state `__UNIVERSAL_DATA_FOR_REHYDRATION__` berisi `webapp.user-detail` lengkap — bahasa, tanggal pembuatan akun, bio link, `videoCount`, dan `friendCount` semuanya tersedia. Avatar memakai `avatarLarger` (hingga 900×900) sehingga tidak perlu request tambahan. API mobile profil (`aweme/v1/user/profile/other`) tetap tidak dipakai karena membalas kosong tanpa tanda tangan `X-Argus`. URL pendek (`vt.tiktok.com`/`vm.tiktok.com`) diselesaikan dengan mengikuti redirect, dan ID post diambil dari pola `/video/`, `/v/`, atau `/photo/`.

### Scraper X

Perintah `.x` memakai CLI Node.js [`x-scraper-node`](../x-scraper-node/scraper.js) yang sudah ada, dijalankan lewat `internal/xscraper`. Paket Go hanya menjalankan scraper ke folder sementara, membaca media beserta `metadata.json`, lalu mengirimnya sebagai gambar/video WhatsApp. Karena `os/exec` tidak tersedia di dalam yaegi, pemanggilan Node berada di paket internal, bukan di berkas plugin.

Scraper dicari berurutan pada:

1. `$X_SCRAPER_DIR/scraper.js` (atau `$X_SCRAPER_DIR` bila menunjuk langsung ke berkas).
2. `x-scraper-node/scraper.js` relatif terhadap direktori kerja bot.
3. `../x-scraper-node/scraper.js`.

Executable Node memakai `node` dari `PATH`; set `X_SCRAPER_NODE` bila Node berada di lokasi lain. `.x video <link>` dan `.x image <link>` diteruskan sebagai `--media video|image` ke scraper.

---

## Lisensi

Proyek ini dilisensikan di bawah lisensi [MIT](LICENSE).
